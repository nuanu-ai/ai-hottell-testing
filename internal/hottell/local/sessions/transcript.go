package sessions

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
	"sync/atomic"
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
	Seq     int            `json:"seq"`  // the position in the session's stream
	Line    int            `json:"line"` // the file line it comes from (see eachJSONLine)
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

	line int // the file line being parsed; emit gives it to an event that has none
}

func (tr *Transcript) emit(ev Event) {
	ev.Seq = len(tr.Events)
	if ev.Line == 0 {
		ev.Line = tr.line
	}
	tr.Events = append(tr.Events, ev)
	if ev.Model != "" && !slices.Contains(tr.Models, ev.Model) {
		tr.Models = append(tr.Models, ev.Model)
	}
}

// eachJSONLine calls fn for every line that is not blank, numbered as eachLine numbers
// them. Broken lines are fn's business.
func eachJSONLine(r io.Reader, maxLine int, fn func(n int, raw []byte)) error {
	return eachLine(r, maxLine, func(n int, raw []byte) bool {
		if len(raw) > 1 {
			fn(n, raw)
		}
		return true
	})
}

// eachLine calls fn for every line, blank ones included, until fn returns false. A line may
// be megabytes long — transcripts hold such lines — but not longer than maxLine: that one
// stops the walk as sessionTooLarge before it is gathered. n is the line's 1-based number
// over the content read — for a .jsonl.zst the decompressed one — so it is the line an
// editor shows; raw keeps its "\n", which a last line cut short lacks. A read error stops
// the walk.
func eachLine(r io.Reader, maxLine int, fn func(n int, raw []byte) bool) error {
	br := bufio.NewReaderSize(r, 1<<20)
	for n := 1; ; n++ {
		line, err := readLine(br, maxLine)
		if errors.Is(err, errLineTooLong) {
			return &tooLargeError{reason: fmt.Sprintf("line %d is longer than %d bytes", n, maxLine)}
		}
		var tl *tooLargeError
		if errors.As(err, &tl) { // the line it cut is not given to fn
			return tl
		}
		if len(line) > 0 && !fn(n, line) {
			return nil
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
	}
}

var errLineTooLong = errors.New("line too long")

// readLine is bufio's ReadBytes('\n') that gives up on a line past maxLine bytes.
func readLine(br *bufio.Reader, maxLine int) ([]byte, error) {
	var line []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(line)+len(chunk) > maxLine {
			return nil, errLineTooLong
		}
		line = append(line, chunk...)
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, err
		}
	}
}

// The bounds of one read of a transcript: a compressed rollout or a line of megabytes must
// not take hottell-local's memory (HT-359). A read past one of them is refused with an error
// that starts with sessionTooLarge.
const (
	maxDecoded = 512 << 20 // bytes of content one read takes, decompressed for a .jsonl.zst
	maxLine    = 64 << 20  // bytes of one line
	maxWindow  = 64 << 20  // the zstd window a frame may ask for; the decoder allocates it
)

// sessionTooLarge is the code a refused read's error starts with.
const sessionTooLarge = "session_too_large"

// readLimits narrows the bounds of one read; a zero field keeps the package's bound.
type readLimits struct {
	decoded int64
	line    int
}

func (l readLimits) or() readLimits {
	if l.decoded <= 0 {
		l.decoded = maxDecoded
	}
	if l.line <= 0 {
		l.line = maxLine
	}
	return l
}

// tooLargeError is a read refused at one of the bounds; its text starts with sessionTooLarge.
type tooLargeError struct {
	path, reason string
}

func (e *tooLargeError) Error() string {
	if e.path == "" {
		return sessionTooLarge + ": " + e.reason
	}
	return sessionTooLarge + ": " + e.path + ": " + e.reason
}

// asTooLarge returns err as the tooLargeError of path when it is one, so that the code stays
// the first word of the error the tool returns; ok is false for any other error.
func asTooLarge(err error, path string) (*tooLargeError, bool) {
	var tl *tooLargeError
	if !errors.As(err, &tl) {
		return nil, false
	}
	tl.path = path
	return tl, true
}

// boundedReader reads at most limit bytes of content and refuses the next one; a zstd frame
// over the decoder's bounds is refused the same way. A request that ends stops it.
type boundedReader struct {
	ctx   context.Context //nolint:containedctx // the request's, for the reads of one call
	r     io.Reader
	limit int64
	left  int64
}

func (b *boundedReader) Read(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if b.left <= 0 {
		var probe [1]byte
		n, err := b.r.Read(probe[:])
		if n > 0 {
			return 0, &tooLargeError{reason: fmt.Sprintf("more than %d bytes of content", b.limit)}
		}
		return 0, mapZstd(err)
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.r.Read(p)
	b.left -= int64(n)
	return n, mapZstd(err)
}

// mapZstd turns the decoder's refusal of a frame over its bounds into sessionTooLarge.
func mapZstd(err error) error {
	if errors.Is(err, zstd.ErrWindowSizeExceeded) || errors.Is(err, zstd.ErrDecoderSizeExceeded) {
		return &tooLargeError{reason: fmt.Sprintf("a zstd window over %d bytes", maxWindow)}
	}
	return err
}

// ParseTS reads an RFC3339 time; anything else is the zero time.
func ParseTS(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// transcriptFile is an open transcript: its content, decompressed for a .jsonl.zst and
// bounded by the limits of one read, and what closes it.
type transcriptFile struct {
	io.Reader
	maxLine int
	bounded *boundedReader
	close   func() error
	// keep, when set, moves the release of the read's share from Close to the func it returns.
	keep func() func()
}

// keepShare makes Close leave the read's share of the budget held and returns what releases it.
func (t transcriptFile) keepShare() func() {
	if t.keep == nil {
		return func() {}
	}
	return t.keep()
}

// Close releases the decoder, if any, closes the file and returns the read's share of the
// budget.
func (t transcriptFile) Close() error { return t.close() }

// decoded is how many bytes of content have been read, decompressed for a .jsonl.zst.
func (t transcriptFile) decoded() int64 { return t.bounded.limit - t.bounded.left }

// open returns the lines of the transcript, unpacking a compressed Codex rollout, within the
// bounds of one read and for as long as ctx lasts. Of a session found by List or Find it opens
// only the file that was judged then: a path that now leads to another file — replaced, or a
// link turned elsewhere — is a session that does not exist, so what is read is what Allow
// was asked about (HT-367). The file is opened without waiting, so a named pipe in its place
// is refused at once rather than holding the call (HT-382). The read first takes its share of
// the process's budget (HT-381), waiting while parallel reads hold it, and gives it back on
// Close.
func open(ctx context.Context, si Info) (transcriptFile, error) {
	return openUpTo(ctx, si, maxDecoded)
}

// openUpTo is open for a read that stops after limit bytes: it takes no more of the budget.
func openUpTo(ctx context.Context, si Info, limit int64) (transcriptFile, error) {
	w := readWeight(si, limit)
	if err := reads.acquire(ctx, w); err != nil {
		return transcriptFile{}, err
	}
	t, err := openFile(ctx, si)
	if err != nil {
		reads.release(w)
		return transcriptFile{}, err
	}
	var once sync.Once
	var kept atomic.Bool
	release := func() { once.Do(func() { reads.release(w) }) }
	closeFile := t.close
	t.close = func() error {
		err := closeFile()
		if !kept.Load() {
			release()
		}
		return err
	}
	t.keep = func() func() {
		kept.Store(true)
		return release
	}
	return t, nil
}

// openFile opens the transcript of si; open takes the budget around it.
func openFile(ctx context.Context, si Info) (transcriptFile, error) {
	lim := si.limits.or()
	f, err := os.OpenFile(si.Path, openFlags, 0)
	if err != nil {
		return transcriptFile{}, fmt.Errorf("open %s: %w", si.Path, err)
	}
	// What was opened is checked on the descriptor itself: a regular file, and the one that
	// was judged when there was a judgement.
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() || (si.file != nil && !os.SameFile(st, si.file)) {
		_ = f.Close()
		return transcriptFile{}, fmt.Errorf("session %q not found", si.ID)
	}
	if !si.Compressed {
		b := &boundedReader{ctx: ctx, r: f, limit: lim.decoded, left: lim.decoded}
		return transcriptFile{Reader: b, bounded: b, maxLine: lim.line, close: f.Close}, nil
	}
	z, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxWindow(maxWindow), zstd.WithDecoderMaxMemory(maxWindow))
	if err != nil {
		_ = f.Close()
		return transcriptFile{}, fmt.Errorf("unpack %s: %w", si.Path, err)
	}
	b := &boundedReader{ctx: ctx, r: z, limit: lim.decoded, left: lim.decoded}
	return transcriptFile{
		Reader: b, bounded: b, maxLine: lim.line,
		close: func() error { z.Close(); return f.Close() },
	}, nil
}

// Parse reads the session's transcript with the parser of its agent. A transcript past the
// bounds of one read is refused with an error that starts with session_too_large; ctx ending
// stops the reading.
func Parse(ctx context.Context, si Info) (*Transcript, error) {
	tr, _, release, err := parse(ctx, si)
	release()
	return tr, err
}

// parse is Parse that also says how many bytes of content it read, decompressed, and keeps the
// read's share of the budget until release is called: the parsed events take the memory the
// share stands for while the caller works through them (HT-381). release is never nil and may be
// called more than once.
func parse(ctx context.Context, si Info) (*Transcript, int64, func(), error) {
	rc, err := open(ctx, si)
	if err != nil {
		return nil, 0, func() {}, err
	}
	share := rc.keepShare()
	defer rc.Close()
	var tr *Transcript
	if si.Agent == "codex" {
		tr, err = parseCodex(rc, rc.maxLine)
	} else {
		tr, err = parseClaude(rc, rc.maxLine)
	}
	if err != nil {
		share()
		if tl, ok := asTooLarge(err, si.Path); ok {
			return nil, rc.decoded(), func() {}, tl
		}
		return nil, rc.decoded(), func() {}, fmt.Errorf("parse %s: %w", si.Path, err)
	}
	tr.Agent, tr.ID = si.Agent, si.ID
	return tr, rc.decoded(), share, nil
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

func parseClaude(r io.Reader, maxLine int) (*Transcript, error) {
	tr := &Transcript{}
	seenUsage := map[string]bool{} // one API message is split into records per block, the usage repeats
	err := eachJSONLine(r, maxLine, func(n int, raw []byte) {
		tr.line = n
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

func parseCodex(r io.Reader, maxLine int) (*Transcript, error) {
	tr := &Transcript{}
	model, turn := "", ""
	// New rollouts write token_usage_record for every model response, old ones only
	// event_msg/token_count. The first is taken for a turn that has any, else the second:
	// an old session continued in a new Codex holds both formats, a turn each.
	var fromRecords, fromCounts []Event
	calls := &codexCalls{}
	// prevTotal is total_token_usage of the latest token_count: a token_count repeating it is
	// a poll, not a response, as CodexTurnFacts on the server reads it.
	var prevTotal *codexTotal
	err := eachJSONLine(r, maxLine, func(n int, raw []byte) {
		tr.line = n
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
				TS: ts, Line: n, Kind: "token_usage", Model: model,
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
				if t, ok := info["total_token_usage"].(map[string]any); ok {
					total := codexTotal{*codexUsage(t), num(t["total_tokens"])}
					if prevTotal != nil && *prevTotal == total {
						tr.Skipped++
						return
					}
					prevTotal = &total
				}
				if last, ok := info["last_token_usage"].(map[string]any); ok {
					fromCounts = append(fromCounts, Event{TS: ts, Line: n, Kind: "token_usage", Model: model, Turn: turn, Tokens: codexUsage(last)})
				}
			case "item_completed":
				calls.completed(tr, p)
				tr.Skipped++
			default:
				tr.Skipped++
			}
		case "response_item":
			codexItem(tr, ts, p, model, turn, calls)
		default:
			tr.Skipped++
		}
	})
	if err != nil {
		return tr, err
	}
	recordTurns := make(map[string]bool, len(fromRecords))
	for _, ev := range fromRecords {
		recordTurns[ev.Turn] = true
	}
	usage := fromRecords
	for _, ev := range fromCounts {
		if !recordTurns[ev.Turn] {
			usage = append(usage, ev)
		}
	}
	// Both lists are in line order; together they go back to it for the merge by time.
	slices.SortStableFunc(usage, func(a, b Event) int { return cmp.Compare(a.Line, b.Line) })
	// The usage goes among the other events by time; each event keeps its own line.
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

// codexTotal is a token_count's total_token_usage, compared with the previous one.
type codexTotal struct {
	usage TokenUsage
	total int64
}

func codexUsage(u map[string]any) *TokenUsage {
	return &TokenUsage{
		Input: num(u["input_tokens"]), CacheRead: num(u["cached_input_tokens"]),
		CacheWrite: num(u["cache_write_input_tokens"]), Output: num(u["output_tokens"]),
		Reasoning: num(u["reasoning_output_tokens"]),
	}
}

// codexCalls joins a call's item_completed to its output, which come in either order. It
// holds, by call id, the failure an item_completed reported before the output and the index
// of the output already emitted; a new call under the same id clears both.
type codexCalls struct {
	failed map[string]bool
	result map[string]int
}

// call forgets what an earlier call under id left.
func (c *codexCalls) call(id string) {
	delete(c.failed, id)
	delete(c.result, id)
}

// output marks the tool_result about to be emitted as an error if the call failed.
func (c *codexCalls) output(tr *Transcript, ev *Event) {
	ev.IsError = c.failed[ev.CallID]
	if c.result == nil {
		c.result = map[string]int{}
	}
	c.result[ev.CallID] = len(tr.Events)
}

// completed reads item_completed: the call failed when its status is "failed" or its exit
// code is not zero.
func (c *codexCalls) completed(tr *Transcript, p map[string]any) {
	it, _ := p["item"].(map[string]any)
	id := str0(it["id"])
	if id == "" {
		return
	}
	code, hasCode := it["exit_code"].(float64)
	failed := str0(it["status"]) == "failed" || hasCode && code != 0
	if i, ok := c.result[id]; ok {
		tr.Events[i].IsError = failed
		return
	}
	if c.failed == nil {
		c.failed = map[string]bool{}
	}
	c.failed[id] = failed
}

func codexItem(tr *Transcript, ts time.Time, p map[string]any, model, turn string, calls *codexCalls) {
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
		calls.call(str0(p["call_id"]))
		tr.emit(Event{
			TS: ts, Kind: "tool_call", Model: model, Turn: turn, Tool: str0(p["name"]),
			CallID: str0(p["call_id"]), Text: str0(p["arguments"]),
		})
	case "custom_tool_call":
		calls.call(str0(p["call_id"]))
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
		ev := Event{TS: ts, Kind: "tool_result", Turn: turn, CallID: str0(p["call_id"]), Text: txt}
		calls.output(tr, &ev)
		tr.emit(ev)
	case "local_shell_call", "web_search_call", "tool_search_call", "image_generation_call":
		id := firstNonEmpty(str0(p["call_id"]), str0(p["id"]))
		calls.call(id)
		tr.emit(Event{
			TS: ts, Kind: "tool_call", Model: model, Turn: turn, Tool: str0(p["type"]),
			CallID: id, Text: jsonString(p["action"]),
		})
	default:
		tr.Skipped++
	}
}
