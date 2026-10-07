package telemetry

import (
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// The facts of a Codex session that its hooks do not carry and its rollout does: the exit code,
// status and duration of a tool call, the tokens, model and duration of a turn. They are a port
// of ai-hottell@c3c8357:hottell/enrich.go to the server. The colleague read the rollout file
// backward from its end and kept the first record he met; the store hands the lines ordered by
// transcript.line, so these reads go forward in one pass and keep the last record instead. Only
// numbers, short status tokens and the model name are copied out of the rollout, never the text
// of a command, an output or a message.

// ToolFact is what the rollout tells about one tool call. A field the rollout does not hold is
// empty: Status "" and nil numbers.
type ToolFact struct {
	// Status is the call's status as item_completed states it, such as "completed", "failed" or
	// "declined"; only a short token of letters, digits, '_' and '-' is kept.
	Status string
	// ExitCode is the exit code of a command.
	ExitCode *int64
	// DurationMs is the call's duration in milliseconds.
	DurationMs *int64
}

// codexTokens are the token counts of one usage record.
type codexTokens struct {
	Input      int64 `json:"input_tokens"`
	Cached     int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_write_input_tokens"`
	Output     int64 `json:"output_tokens"`
	Reasoning  int64 `json:"reasoning_output_tokens"`
	Total      int64 `json:"total_tokens"`
}

// The range policy of the rollout's numbers (HT-371): the rollout comes from the user's machine,
// so a number out of range is left out rather than copied. A duration and a token count are
// never negative and an exit code may be; a number past int64, or a duration whose milliseconds
// would overflow, is no number. A token count below zero counts as none, and a sum of counts
// saturates at the int64 maximum instead of wrapping.

// add adds o's counts to t, each saturating at the int64 maximum; o is cleaned first.
func (t *codexTokens) add(o codexTokens) {
	o = o.clean()
	t.Input = addCount(t.Input, o.Input)
	t.Cached = addCount(t.Cached, o.Cached)
	t.CacheWrite = addCount(t.CacheWrite, o.CacheWrite)
	t.Output = addCount(t.Output, o.Output)
	t.Reasoning = addCount(t.Reasoning, o.Reasoning)
	t.Total = addCount(t.Total, o.Total)
}

// clean returns t with every negative count set to zero.
func (t codexTokens) clean() codexTokens {
	for _, c := range []*int64{&t.Input, &t.Cached, &t.CacheWrite, &t.Output, &t.Reasoning, &t.Total} {
		*c = max(*c, 0)
	}
	return t
}

// addCount adds two counts that are not negative, saturating at the int64 maximum.
func addCount(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// rolloutRecord holds only the fields the facts need; the decoder skips the large ones (outputs
// aside, which the exit code fallback reads) without building strings of them.
type rolloutRecord struct {
	Type    string `json:"type"`
	Payload struct {
		Type          string          `json:"type"`
		TurnID        string          `json:"turn_id"`
		CallID        string          `json:"call_id"`
		Model         string          `json:"model"`
		ResponseID    string          `json:"response_id"`
		DurationMs    any             `json:"duration_ms"`
		StartedAtMs   any             `json:"started_at_ms"`
		CompletedAtMs any             `json:"completed_at_ms"`
		Output        json.RawMessage `json:"output"`
		Item          *struct {
			ID         string `json:"id"`
			Status     any    `json:"status"`
			ExitCode   any    `json:"exit_code"`
			Duration   any    `json:"duration"`
			DurationMs any    `json:"durationMs"`
		} `json:"item"`
		Usage     *codexTokens `json:"usage"`
		TurnUsage *codexTokens `json:"turn_token_usage"`
		Info      *struct {
			Total *codexTokens `json:"total_token_usage"`
			Last  *codexTokens `json:"last_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

// decodeRecord decodes one rollout line; false for a damaged or unfinished line. A field of an
// unexpected type is left empty and the rest of the record kept, as the colleague's reader did.
func decodeRecord(body string) (rolloutRecord, bool) {
	var rec rolloutRecord
	if err := json.Unmarshal([]byte(body), &rec); err != nil {
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) {
			return rolloutRecord{}, false
		}
	}
	return rec, true
}

// callFacts are the two records of one call: item_completed and the call's output. The fields
// item_completed holds outweigh those the output gives.
type callFacts struct {
	item, output *ToolFact
}

func (c *callFacts) fact() ToolFact {
	var f ToolFact
	if c.output != nil {
		f = *c.output
	}
	if c.item != nil {
		if c.item.Status != "" {
			f.Status = c.item.Status
		}
		if c.item.ExitCode != nil {
			f.ExitCode = c.item.ExitCode
		}
		if c.item.DurationMs != nil {
			f.DurationMs = c.item.DurationMs
		}
	}
	return f
}

// CodexToolFacts returns the facts of the tool calls in the lines of a Codex rollout, by call id
// (the tool_use_id of the PostToolUse hook). The lines are read in the order given, which is
// Number's. A call is in the map once the rollout holds its item_completed or its output; a call
// with neither, the rollout's own function_call aside, is not, and a reader answers it as not
// found. When a call id recurs, the records after its latest function_call count. Damaged and
// unfinished lines are skipped.
func CodexToolFacts(lines []TranscriptLine) map[string]ToolFact {
	calls := map[string]*callFacts{}
	call := func(id string) *callFacts {
		c := calls[id]
		if c == nil {
			c = &callFacts{}
			calls[id] = c
		}
		return c
	}
	for _, line := range lines {
		rec, ok := decodeRecord(line.Body)
		if !ok {
			continue
		}
		p := &rec.Payload
		switch {
		case rec.Type == "event_msg" && p.Type == "item_completed":
			if p.Item == nil || p.Item.ID == "" {
				continue
			}
			f := itemCompletedFact(&rec)
			c := call(p.Item.ID)
			c.item = &f
		case rec.Type == "response_item" && (p.Type == "function_call_output" || p.Type == "custom_tool_call_output"):
			if p.CallID == "" {
				continue
			}
			var f ToolFact
			if code, ms, ok := exitFromOutput(p.Output); ok {
				f.ExitCode = &code
				if ms >= 0 {
					f.DurationMs = &ms
				}
			}
			c := call(p.CallID)
			c.output = &f
		case rec.Type == "response_item" && (p.Type == "function_call" || p.Type == "custom_tool_call" || p.Type == "local_shell_call"):
			// The call comes before its item_completed and its output: what was recorded under
			// its id before belongs to an earlier call.
			delete(calls, p.CallID)
		}
	}
	out := make(map[string]ToolFact, len(calls))
	for id, c := range calls {
		out[id] = c.fact()
	}
	return out
}

// itemCompletedFact reads the status, exit code and duration of an item_completed record. The
// duration is the item's Rust Duration, else its durationMs, else the record's completed_at_ms
// less started_at_ms.
func itemCompletedFact(rec *rolloutRecord) ToolFact {
	it := rec.Payload.Item
	f := ToolFact{Status: statusToken(it.Status)}
	if code, ok := wholeNumber(it.ExitCode); ok {
		f.ExitCode = &code
	}
	if ms, ok := durationObjectMs(it.Duration); ok {
		f.DurationMs = &ms
	} else if ms, ok := durationNumber(it.DurationMs); ok {
		f.DurationMs = &ms
	} else if start, ok := durationNumber(rec.Payload.StartedAtMs); ok {
		if end, ok := durationNumber(rec.Payload.CompletedAtMs); ok && end >= start {
			ms := end - start
			f.DurationMs = &ms
		}
	}
	return f
}

// wholeNumber reads a JSON number decoded into any as an integer, cutting a fraction; false for
// a number outside int64.
func wholeNumber(v any) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return floatInt64(x)
	case json.Number:
		n, err := x.Int64()
		return n, err == nil
	}
	return 0, false
}

// floatInt64 converts x to int64, cutting a fraction; false for NaN or a value outside int64,
// whose conversion Go leaves undefined.
func floatInt64(x float64) (int64, bool) {
	// -2^63 converts exactly; 2^63 is the first float64 past the int64 maximum.
	if !(x >= math.MinInt64 && x < -math.MinInt64) {
		return 0, false
	}
	return int64(x), true
}

// durationNumber reads a duration or a time in milliseconds: a whole number that is not negative.
func durationNumber(v any) (int64, bool) {
	n, ok := wholeNumber(v)
	return n, ok && n >= 0
}

// secondsMs converts a duration in seconds to milliseconds; false when it is negative or its
// milliseconds overflow int64.
func secondsMs(sec float64) (int64, bool) {
	if !(sec >= 0) {
		return 0, false
	}
	return floatInt64(sec * 1000)
}

// durationObjectMs reads a Rust Duration {secs, nanos} as milliseconds; false when a part is
// negative or the milliseconds overflow int64.
func durationObjectMs(v any) (int64, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return 0, false
	}
	secs, ok1 := wholeNumber(m["secs"])
	nanos, ok2 := wholeNumber(m["nanos"])
	if !ok1 && !ok2 || secs < 0 || nanos < 0 || secs > (math.MaxInt64-nanos/1_000_000)/1000 {
		return 0, false
	}
	return secs*1000 + nanos/1_000_000, true
}

// statusToken keeps a status only when it is a short identifier, so that no free text of the
// rollout passes as a status.
func statusToken(v any) string {
	s, _ := v.(string)
	if len(s) == 0 || len(s) > 64 {
		return ""
	}
	for _, r := range s {
		if !isAlnum(r) && r != '_' && r != '-' {
			return ""
		}
	}
	return s
}

// isAlnum reports whether r is an ASCII letter or digit.
func isAlnum(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

var (
	reExitCode      = regexp.MustCompile(`(?m)^Exit code: (-?\d+)\s*$`)
	reProcessExited = regexp.MustCompile(`(?m)^Process exited with code (-?\d+)\s*$`)
	reWallTime      = regexp.MustCompile(`(?m)^Wall time:? (\d+(?:\.\d+)?) seconds\s*$`)
)

// outputHeaderMax bounds the head of an output searched for the exit code when the output has no
// "Output:" line.
const outputHeaderMax = 2048

// exitFromOutput reads the exit code of a call from its output, for old rollouts and tools
// without item_completed: the header "Exit code: N" or "Process exited with code N" before the
// "Output:" line, or the JSON {"output":…,"metadata":{"exit_code":N,"duration_seconds":S}}. The
// body of the output is never searched, so that its text is not taken for a code. durationMs is
// -1 when the output gives no duration.
func exitFromOutput(raw json.RawMessage) (code, durationMs int64, ok bool) {
	var v any
	if len(raw) == 0 || json.Unmarshal(raw, &v) != nil {
		return 0, -1, false
	}
	var text string
	switch x := v.(type) {
	case string:
		text = x
	case []any:
		for _, b := range x {
			if m, _ := b.(map[string]any); m != nil {
				if s, _ := m["text"].(string); s != "" {
					text = s
					break
				}
			}
		}
	case map[string]any:
		return exitFromMetadata(x)
	}
	if t := strings.TrimSpace(text); strings.HasPrefix(t, "{") {
		var obj map[string]any
		if json.Unmarshal([]byte(t), &obj) == nil {
			return exitFromMetadata(obj)
		}
	}
	header := text
	if i := strings.Index(header, "\nOutput:"); i >= 0 {
		header = header[:i]
	} else if len(header) > outputHeaderMax {
		header = header[:outputHeaderMax]
	}
	m := reExitCode.FindStringSubmatch(header)
	if m == nil {
		m = reProcessExited.FindStringSubmatch(header)
	}
	if m == nil {
		return 0, -1, false
	}
	code, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, -1, false
	}
	durationMs = -1
	if w := reWallTime.FindStringSubmatch(header); w != nil {
		if sec, err := strconv.ParseFloat(w[1], 64); err == nil {
			if ms, ok := secondsMs(sec); ok {
				durationMs = ms
			}
		}
	}
	return code, durationMs, true
}

// exitFromMetadata reads the exit code and duration of an output of the form
// {"metadata":{"exit_code":N,"duration_seconds":S}}.
func exitFromMetadata(obj map[string]any) (code, durationMs int64, ok bool) {
	meta, _ := obj["metadata"].(map[string]any)
	code, ok = wholeNumber(meta["exit_code"])
	if !ok {
		return 0, -1, false
	}
	durationMs = -1
	if sec, isNum := meta["duration_seconds"].(float64); isNum {
		if ms, ok := secondsMs(sec); ok {
			durationMs = ms
		}
	}
	return code, durationMs, true
}

// TurnFact is what the rollout tells about one turn: the model's responses and their tokens, the
// model and the turn's duration.
type TurnFact struct {
	// Responses counts the model's responses in the turn.
	Responses int64
	// Input, Cached, Output and Reasoning are the turn's tokens: input, cached input, output and
	// reasoning output.
	Input, Cached, Output, Reasoning int64
	// Model is the turn's model; only a short identifier is kept, else it is empty.
	Model string
	// DurationMs is the duration task_complete or turn_aborted states; nil before either.
	DurationMs *int64
	// Complete is set when the rollout holds both the turn's task_started and its end,
	// task_complete or turn_aborted.
	Complete bool
}

// tokenCount is one token_count record of the old format: the session's running total and the
// last response's usage.
type tokenCount struct{ total, last codexTokens }

// turnState gathers the records of one turn as the lines go by.
type turnState struct {
	started, ended bool
	durationMs     *int64
	// modelByID is the model of the latest turn_context naming the turn; modelInside of the
	// latest one without a turn id inside the turn; modelBefore of the latest one without an id
	// between the previous boundary and the turn's task_started. A context without a model
	// changes none of them.
	modelByID, modelInside, modelBefore string
	// New format: token_usage_record with the turn's id, each response once.
	records   int64
	responses map[string]bool
	sum       codexTokens
	// turnUsage is turn_token_usage of the turn's latest record: it is cumulative.
	turnUsage *codexTokens
	// Old format: the token_count records inside the turn, in order, and the total of the last
	// token_count before the turn started.
	counts   []tokenCount
	baseline *codexTokens
}

// tokens returns the turn's responses and tokens. New format: the count of distinct responses
// and turn_token_usage of the latest record, else the sum of the usages. Old format:
// last_token_usage of the token_count records inside the turn, where a record repeating the
// previous total (the baseline before the turn first) is a poll, not a response.
func (t *turnState) tokens() (int64, codexTokens) {
	if t.records > 0 {
		if t.turnUsage != nil {
			return t.records, *t.turnUsage
		}
		return t.records, t.sum
	}
	var sum codexTokens
	var responses int64
	prev := t.baseline
	for _, c := range t.counts {
		if prev != nil && *prev == c.total {
			continue
		}
		responses++
		sum.add(c.last)
		total := c.total
		prev = &total
	}
	return responses, sum
}

func (t *turnState) fact() TurnFact {
	responses, u := t.tokens()
	model := t.modelByID
	if model == "" {
		model = t.modelInside
	}
	if model == "" {
		model = t.modelBefore
	}
	return TurnFact{
		Responses: responses,
		Input:     u.Input, Cached: u.Cached, Output: u.Output, Reasoning: u.Reasoning,
		Model:      modelToken(model),
		DurationMs: t.durationMs,
		Complete:   t.started && t.ended,
	}
}

// CodexTurnFacts returns the facts of the turns in the lines of a Codex rollout, by turn id (the
// turn_id of the Stop hook). The lines are read in the order given, which is Number's. A turn is
// in the map once a record names it: task_started, task_complete, turn_aborted, turn_context or
// token_usage_record; a reader answers a turn that is in the map but not Complete as partial and
// a turn that is not in it as not found. Of repeated records the latest counts. Damaged and
// unfinished lines are skipped.
func CodexTurnFacts(lines []TranscriptLine) map[string]TurnFact {
	turns := map[string]*turnState{}
	turn := func(id string) *turnState {
		t := turns[id]
		if t == nil {
			t = &turnState{responses: map[string]bool{}}
			turns[id] = t
		}
		return t
	}
	// current is the turn whose task_started was the latest boundary; empty after an end.
	var current *turnState
	// modelBefore is the latest turn_context without an id since the latest boundary.
	var modelBefore string
	// lastTotal is the total of the latest token_count, the baseline of the next turn.
	var lastTotal *codexTokens
	for _, line := range lines {
		rec, ok := decodeRecord(line.Body)
		if !ok {
			continue
		}
		p := &rec.Payload
		switch {
		case rec.Type == "token_usage_record":
			if p.TurnID == "" || p.Usage == nil {
				continue
			}
			t := turn(p.TurnID)
			if p.TurnUsage != nil {
				u := p.TurnUsage.clean()
				t.turnUsage = &u
			}
			if p.ResponseID != "" {
				if t.responses[p.ResponseID] {
					continue
				}
				t.responses[p.ResponseID] = true
			}
			t.records++
			t.sum.add(*p.Usage)
		case rec.Type == "turn_context":
			// A context without a model leaves the model of an earlier one; the turn it names
			// still enters the map.
			switch {
			case p.TurnID != "":
				if t := turn(p.TurnID); p.Model != "" {
					t.modelByID = p.Model
				}
			case p.Model == "":
			case current != nil:
				current.modelInside = p.Model
			default:
				modelBefore = p.Model
			}
		case rec.Type == "event_msg" && p.Type == "task_started":
			if p.TurnID == "" {
				current, modelBefore = nil, ""
				continue
			}
			t := turn(p.TurnID)
			t.started = true
			t.modelBefore = modelBefore
			t.counts, t.baseline = nil, lastTotal
			current, modelBefore = t, ""
		case rec.Type == "event_msg" && (p.Type == "task_complete" || p.Type == "turn_aborted"):
			if p.TurnID != "" {
				t := turn(p.TurnID)
				t.ended = true
				if ms, ok := durationNumber(p.DurationMs); ok {
					t.durationMs = &ms
				}
			}
			current, modelBefore = nil, ""
		case rec.Type == "event_msg" && p.Type == "token_count":
			if p.Info == nil || p.Info.Total == nil {
				continue
			}
			c := tokenCount{total: *p.Info.Total}
			if p.Info.Last != nil {
				c.last = *p.Info.Last
			}
			if current != nil {
				current.counts = append(current.counts, c)
			}
			total := c.total
			lastTotal = &total
		}
	}
	out := make(map[string]TurnFact, len(turns))
	for id, t := range turns {
		out[id] = t.fact()
	}
	return out
}

// modelToken keeps a model name only when it is a short identifier, so that no free text of the
// rollout passes as a model.
func modelToken(s string) string {
	if len(s) == 0 || len(s) > 128 {
		return ""
	}
	for _, r := range s {
		if !isAlnum(r) && !strings.ContainsRune("._:/-@", r) {
			return ""
		}
	}
	return s
}

// The enrich statuses of a Codex hook event, as hottell.enrich_status of the colleague's binary
// named them. Reading a file is not the server's, so his "disabled" and "error" do not arise.
const (
	// EnrichFound: the rollout holds the call's item_completed or output, or the turn's
	// task_started and end.
	EnrichFound = "found"
	// EnrichPartial: the rollout names the turn but lacks its task_started or its end.
	EnrichPartial = "partial"
	// EnrichNotFound: the rollout holds nothing about the call or the turn yet.
	EnrichNotFound = "not_found"
	// EnrichNoTranscript: the store holds no rollout of the session.
	EnrichNoTranscript = "no_transcript"
)

// CodexFacts are the facts of one Codex session's rollout, looked up by its hook events.
type CodexFacts struct {
	// Transcript is set when the store holds the session's main rollout.
	Transcript bool
	// Tools are the facts of the tool calls by call id, as CodexToolFacts returns them.
	Tools map[string]ToolFact
	// Turns are the facts of the turns by turn id, as CodexTurnFacts returns them.
	Turns map[string]TurnFact
}

// NewCodexFacts returns the facts of a session whose main rollout has lines.
func NewCodexFacts(lines []TranscriptLine) CodexFacts {
	return CodexFacts{Transcript: true, Tools: CodexToolFacts(lines), Turns: CodexTurnFacts(lines)}
}

// EventFacts are the facts of one hook event: its enrich status and, when the rollout holds
// them, the facts of its call or of its turn.
type EventFacts struct {
	Status string
	// Tool is set for a PostToolUse whose call the rollout holds.
	Tool *ToolFact
	// Turn is set for a Stop whose turn the rollout names.
	Turn *TurnFact
}

// For returns the facts of ev: a Codex PostToolUse by its tool_use_id or a Codex Stop by its
// turn_id. Any other event, or one without that id, has no facts and false.
func (f CodexFacts) For(ev HookEvent) (EventFacts, bool) {
	if ev.Agent != "codex" {
		return EventFacts{}, false
	}
	switch {
	case ev.Event == "PostToolUse" && ev.ToolUseID != "":
		if !f.Transcript {
			return EventFacts{Status: EnrichNoTranscript}, true
		}
		tool, ok := f.Tools[ev.ToolUseID]
		if !ok {
			return EventFacts{Status: EnrichNotFound}, true
		}
		return EventFacts{Status: EnrichFound, Tool: &tool}, true
	case ev.Event == "Stop" && ev.TurnID != "":
		if !f.Transcript {
			return EventFacts{Status: EnrichNoTranscript}, true
		}
		turn, ok := f.Turns[ev.TurnID]
		if !ok {
			return EventFacts{Status: EnrichNotFound}, true
		}
		status := EnrichPartial
		if turn.Complete {
			status = EnrichFound
		}
		return EventFacts{Status: status, Turn: &turn}, true
	}
	return EventFacts{}, false
}
