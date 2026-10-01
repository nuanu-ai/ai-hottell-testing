package sessions

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"github.com/klauspost/compress/zstd"
)

// The transcripts of both agents come down to one stream of events. Kind is one of
// user_message, assistant_message, reasoning, tool_call, tool_result, system, attachment,
// token_usage, turn_start, turn_end, compact. Service records that mean nothing for the
// analysis (file snapshots, queues, duplicate item_completed) stay out of the stream and
// are counted in Skipped.

// TokenUsage is the tokens of one model response or a sum of them.
type TokenUsage struct {
	Input      int64 `json:"input"`       // the whole input, cache included
	CacheRead  int64 `json:"cache_read"`  // of it, read from the cache
	CacheWrite int64 `json:"cache_write"` // of it, written to the cache
	Output     int64 `json:"output"`
	Reasoning  int64 `json:"reasoning,omitempty"` // Codex: the part of output spent on reasoning
}

// Add adds o to t.
func (t *TokenUsage) Add(o TokenUsage) {
	t.Input += o.Input
	t.CacheRead += o.CacheRead
	t.CacheWrite += o.CacheWrite
	t.Output += o.Output
	t.Reasoning += o.Reasoning
}

// Event is one event of a session's stream.
type Event struct {
	Seq     int            `json:"seq"` // the position in the session's stream
	TS      time.Time      `json:"ts"`
	Kind    string         `json:"kind"`
	Role    string         `json:"role,omitempty"`
	Tool    string         `json:"tool,omitempty"`
	CallID  string         `json:"call_id,omitempty"`
	Text    string         `json:"text,omitempty"`
	IsError bool           `json:"is_error,omitempty"`
	Model   string         `json:"model,omitempty"`
	Turn    string         `json:"turn,omitempty"`
	Tokens  *TokenUsage    `json:"tokens,omitempty"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

// Transcript is a parsed session: its metadata and its events.
type Transcript struct {
	Agent     string   `json:"agent"`
	ID        string   `json:"id"`
	Cwd       string   `json:"cwd,omitempty"`
	GitBranch string   `json:"git_branch,omitempty"`
	Version   string   `json:"version,omitempty"` // the agent's version
	Title     string   `json:"title,omitempty"`
	Models    []string `json:"models,omitempty"`
	Events    []Event  `json:"-"`
	Skipped   int      `json:"skipped_records"`
	Broken    int      `json:"broken_lines"`
}

func (tr *Transcript) emit(ev Event) {
	ev.Seq = len(tr.Events)
	tr.Events = append(tr.Events, ev)
	if ev.Model != "" && !slices.Contains(tr.Models, ev.Model) {
		tr.Models = append(tr.Models, ev.Model)
	}
}

// eachJSONLine calls fn for every line, with no limit on the line length (transcripts
// hold lines of megabytes). Broken lines are fn's business; a read error stops the walk.
func eachJSONLine(r io.Reader, fn func(raw []byte)) error {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 1 {
			fn(line)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
	}
}

// ParseTS reads an RFC3339 time; anything else is the zero time.
func ParseTS(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// open returns the lines of the transcript, unpacking a compressed Codex rollout.
func open(si Info) (io.ReadCloser, error) {
	f, err := os.Open(si.Path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", si.Path, err)
	}
	if !si.Compressed {
		return f, nil
	}
	z, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1))
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("unpack %s: %w", si.Path, err)
	}
	return zstdFile{Decoder: z, f: f}, nil
}

// zstdFile closes both the decoder and the file under it.
type zstdFile struct {
	*zstd.Decoder
	f *os.File
}

// Close releases the decoder and closes the file.
func (z zstdFile) Close() error {
	z.Decoder.Close()
	return z.f.Close()
}

// Parse reads the session's transcript with the parser of its agent.
func Parse(si Info) (*Transcript, error) {
	rc, err := open(si)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var tr *Transcript
	if si.Agent == "codex" {
		tr, err = parseCodex(rc)
	} else {
		tr, err = parseClaude(rc)
	}
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", si.Path, err)
	}
	tr.Agent, tr.ID = si.Agent, si.ID
	return tr, nil
}

// blockText is the text of content: a string or an array of {type,text} blocks.
func blockText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		out := ""
		for _, b := range x {
			bm, _ := b.(map[string]any)
			if s, ok := bm["text"].(string); ok {
				if out != "" {
					out += "\n"
				}
				out += s
			}
		}
		return out
	}
	return ""
}

func jsonString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

func num(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case json.Number:
		n, _ := x.Int64()
		return n
	}
	return 0
}

func str0(v any) string {
	s, _ := v.(string)
	return s
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// ---------- Claude Code ----------

func parseClaude(r io.Reader) (*Transcript, error) {
	tr := &Transcript{}
	seenUsage := map[string]bool{} // one API message is split into records per block, the usage repeats
	err := eachJSONLine(r, func(raw []byte) {
		var rec map[string]any
		if json.Unmarshal(raw, &rec) != nil {
			tr.Broken++
			return
		}
		typ, _ := rec["type"].(string)
		ts := ParseTS(str0(rec["timestamp"]))
		if tr.Cwd == "" {
			tr.Cwd = str0(rec["cwd"])
		}
		if tr.GitBranch == "" {
			tr.GitBranch = str0(rec["gitBranch"])
		}
		if tr.Version == "" {
			tr.Version = str0(rec["version"])
		}
		msg, _ := rec["message"].(map[string]any)
		switch typ {
		case "user":
			content := msg["content"]
			if s, ok := content.(string); ok {
				kind := "user_message"
				if b, _ := rec["isMeta"].(bool); b {
					kind = "system"
				}
				tr.emit(Event{TS: ts, Kind: kind, Role: "user", Text: s})
				return
			}
			blocks, _ := content.([]any)
			for _, b := range blocks {
				bm, _ := b.(map[string]any)
				switch str0(bm["type"]) {
				case "tool_result":
					isErr, _ := bm["is_error"].(bool)
					tr.emit(Event{
						TS: ts, Kind: "tool_result", CallID: str0(bm["tool_use_id"]),
						Text: blockText(bm["content"]), IsError: isErr,
					})
				case "text":
					tr.emit(Event{TS: ts, Kind: "user_message", Role: "user", Text: str0(bm["text"])})
				default:
					tr.emit(Event{TS: ts, Kind: "user_message", Role: "user", Text: "[" + str0(bm["type"]) + "]"})
				}
			}
		case "assistant":
			model := str0(msg["model"])
			blocks, _ := msg["content"].([]any)
			for _, b := range blocks {
				bm, _ := b.(map[string]any)
				switch str0(bm["type"]) {
				case "text":
					tr.emit(Event{TS: ts, Kind: "assistant_message", Role: "assistant", Model: model, Text: str0(bm["text"])})
				case "thinking":
					tr.emit(Event{TS: ts, Kind: "reasoning", Model: model, Text: str0(bm["thinking"])})
				case "tool_use":
					tr.emit(Event{
						TS: ts, Kind: "tool_call", Model: model, Tool: str0(bm["name"]),
						CallID: str0(bm["id"]), Text: jsonString(bm["input"]),
					})
				}
			}
			id := str0(msg["id"])
			if u, ok := msg["usage"].(map[string]any); ok && id != "" && !seenUsage[id] {
				seenUsage[id] = true
				cr, cw := num(u["cache_read_input_tokens"]), num(u["cache_creation_input_tokens"])
				tr.emit(Event{TS: ts, Kind: "token_usage", Model: model, Tokens: &TokenUsage{
					Input: num(u["input_tokens"]) + cr + cw, CacheRead: cr, CacheWrite: cw, Output: num(u["output_tokens"]),
				}})
			}
		case "system":
			tr.emit(Event{
				TS: ts, Kind: "system", Text: firstNonEmpty(str0(rec["content"]), str0(rec["subtype"])),
				Attrs: map[string]any{"subtype": rec["subtype"]},
			})
		case "attachment":
			att, _ := rec["attachment"].(map[string]any)
			tr.emit(Event{
				TS: ts, Kind: "attachment", Text: str0(att["type"]),
				Attrs: map[string]any{"type": att["type"], "hook": att["hookName"]},
			})
		case "ai-title":
			tr.Title = str0(rec["aiTitle"])
			tr.Skipped++
		case "summary":
			if tr.Title == "" {
				tr.Title = str0(rec["summary"])
			}
			tr.Skipped++
		default:
			tr.Skipped++
		}
	})
	return tr, err
}

// ---------- Codex ----------

func parseCodex(r io.Reader) (*Transcript, error) {
	tr := &Transcript{}
	model, turn := "", ""
	// New rollouts write token_usage_record for every model response, old ones only
	// event_msg/token_count. The first is taken if there is any, else the second.
	var fromRecords, fromCounts []Event
	err := eachJSONLine(r, func(raw []byte) {
		var rec struct {
			Timestamp string         `json:"timestamp"`
			Type      string         `json:"type"`
			Payload   map[string]any `json:"payload"`
		}
		if json.Unmarshal(raw, &rec) != nil {
			tr.Broken++
			return
		}
		ts, p := ParseTS(rec.Timestamp), rec.Payload
		switch rec.Type {
		case "session_meta":
			tr.Cwd = firstNonEmpty(tr.Cwd, str0(p["cwd"]))
			tr.Version = firstNonEmpty(tr.Version, str0(p["cli_version"]))
			if g, ok := p["git"].(map[string]any); ok {
				tr.GitBranch = firstNonEmpty(tr.GitBranch, str0(g["branch"]))
			}
			tr.Skipped++
		case "turn_context":
			model = firstNonEmpty(str0(p["model"]), model)
			turn = firstNonEmpty(str0(p["turn_id"]), turn)
			if tr.Cwd == "" {
				tr.Cwd = str0(p["cwd"])
			}
			tr.Skipped++
		case "token_usage_record":
			u, _ := p["usage"].(map[string]any)
			fromRecords = append(fromRecords, Event{
				TS: ts, Kind: "token_usage", Model: model,
				Turn: firstNonEmpty(str0(p["turn_id"]), turn), Tokens: codexUsage(u),
			})
		case "compacted":
			tr.emit(Event{TS: ts, Kind: "compact", Turn: turn})
		case "event_msg":
			switch str0(p["type"]) {
			case "task_started":
				turn = firstNonEmpty(str0(p["turn_id"]), turn)
				tr.emit(Event{TS: ts, Kind: "turn_start", Turn: turn})
			case "task_complete", "turn_aborted":
				attrs := map[string]any{"duration_ms": p["duration_ms"]}
				if r := str0(p["reason"]); r != "" {
					attrs["aborted"] = r
				}
				tr.emit(Event{TS: ts, Kind: "turn_end", Turn: firstNonEmpty(str0(p["turn_id"]), turn), Attrs: attrs})
			case "token_count":
				info, _ := p["info"].(map[string]any)
				if last, ok := info["last_token_usage"].(map[string]any); ok {
					fromCounts = append(fromCounts, Event{TS: ts, Kind: "token_usage", Model: model, Turn: turn, Tokens: codexUsage(last)})
				}
			default:
				tr.Skipped++
			}
		case "response_item":
			codexItem(tr, ts, p, model, turn)
		default:
			tr.Skipped++
		}
	})
	if err != nil {
		return tr, err
	}
	usage := fromRecords
	if len(usage) == 0 {
		usage = fromCounts
	}
	// The usage goes among the other events by time.
	merged := make([]Event, 0, len(tr.Events)+len(usage))
	i, j := 0, 0
	for i < len(tr.Events) || j < len(usage) {
		if j >= len(usage) || (i < len(tr.Events) && !usage[j].TS.Before(tr.Events[i].TS)) {
			merged = append(merged, tr.Events[i])
			i++
		} else {
			merged = append(merged, usage[j])
			j++
		}
	}
	tr.Events = nil
	for _, ev := range merged {
		tr.emit(ev)
	}
	return tr, nil
}

func codexUsage(u map[string]any) *TokenUsage {
	return &TokenUsage{
		Input: num(u["input_tokens"]), CacheRead: num(u["cached_input_tokens"]),
		CacheWrite: num(u["cache_write_input_tokens"]), Output: num(u["output_tokens"]),
		Reasoning: num(u["reasoning_output_tokens"]),
	}
}

func codexItem(tr *Transcript, ts time.Time, p map[string]any, model, turn string) {
	switch str0(p["type"]) {
	case "message":
		role := str0(p["role"])
		kind := "user_message"
		switch role {
		case "assistant":
			kind = "assistant_message"
		case "developer", "system":
			kind = "system"
		}
		ev := Event{TS: ts, Kind: kind, Role: role, Text: blockText(p["content"]), Turn: turn}
		if kind == "assistant_message" {
			ev.Model = model
		}
		tr.emit(ev)
	case "reasoning":
		txt := blockText(p["summary"])
		if txt == "" {
			txt = blockText(p["content"])
		}
		tr.emit(Event{TS: ts, Kind: "reasoning", Model: model, Turn: turn, Text: txt})
	case "function_call":
		tr.emit(Event{
			TS: ts, Kind: "tool_call", Model: model, Turn: turn, Tool: str0(p["name"]),
			CallID: str0(p["call_id"]), Text: str0(p["arguments"]),
		})
	case "custom_tool_call":
		tr.emit(Event{
			TS: ts, Kind: "tool_call", Model: model, Turn: turn, Tool: str0(p["name"]),
			CallID: str0(p["call_id"]), Text: str0(p["input"]),
		})
	case "function_call_output", "custom_tool_call_output":
		out := p["output"]
		txt := blockText(out)
		if txt == "" {
			txt = jsonString(out)
		}
		tr.emit(Event{TS: ts, Kind: "tool_result", Turn: turn, CallID: str0(p["call_id"]), Text: txt})
	case "local_shell_call", "web_search_call", "tool_search_call", "image_generation_call":
		tr.emit(Event{
			TS: ts, Kind: "tool_call", Model: model, Turn: turn, Tool: str0(p["type"]),
			CallID: firstNonEmpty(str0(p["call_id"]), str0(p["id"])), Text: jsonString(p["action"]),
		})
	default:
		tr.Skipped++
	}
}
