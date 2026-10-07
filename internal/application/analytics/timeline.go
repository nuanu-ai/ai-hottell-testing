package analytics

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
	"git.alva.dev/alva/harness-telemetry/pkg/redact"
)

// The kinds of the objects a timeline event is made from; with the object's index in its slice
// of TimelineInput they name the object whose line Timeline.Line gives.
const (
	RefPrompt     = "prompt"
	RefCall       = "call"
	RefAPI        = "api"
	RefAPIError   = "api_error"
	RefStop       = "stop"
	RefCompaction = "compact"
	RefSubStart   = "sub_start"
	RefSubStop    = "sub_stop"
	RefInterrupt  = "abort"
	RefSkill      = "skill"
	RefPermission = "permission"
)

// The limits of the texts of a timeline event, in runes.
const (
	timelineTextMax  = 240
	timelineShortMax = 140
	timelineNoteMax  = 160
)

// TimelineInput are the parts of one session its timeline lays out.
type TimelineInput struct {
	// Events are the session's hook events, oldest first: its Stops, Interrupts and subagents'
	// starts and stops come from them.
	Events  []telemetry.HookEvent
	Prompts []Prompt
	Calls   []Call
	API     []APIRecord
	// Claude are the native events of Claude Code; its api_error events are shown.
	Claude      []telemetry.ClaudeEvent
	Compactions []telemetry.HookEvent
	Skills      []SkillActivation
	Turns       []*Turn
	// Waits are the questions and the permission windows, as BuildWaits and PermissionWaits give
	// them.
	Waits []Wait
	// Src are the transcript lines the events are built from; empty when unread.
	Src SourceLines
	// memo masks the texts of one build once (HT-482); nil masks every time.
	memo *redact.Memo
}

// TimelineEvent is one event of a session's timeline. Line is its number in the session, 1..N,
// which every evidence cites; Turn the index of the turn open at its time. K is its kind: prompt,
// answer, tool, err, api, wait, compact, abort, skill or agent. X is its text, Note a remark,
// Side a short label beside it and Tool the tool's shown name; each nil when it has none. SrcLine
// is the line of the transcript the event is built from, as a Deep review's L<n> counts it, and
// SrcKind the kind of that file, main or subagent; both nil when the transcript names none
// (HT-410).
type TimelineEvent struct {
	Line int     `json:"line"`
	At   string  `json:"at"`
	Turn int     `json:"turn"`
	K    string  `json:"k"`
	X    *string `json:"x"`
	Note *string `json:"note"`
	Side *string `json:"side"`
	Tool *string `json:"tool"`
	// SrcLine and SrcKind are the event's source line; see the type.
	SrcLine *int    `json:"src_line"`
	SrcKind *string `json:"src_kind"`
}

// Timeline is the timeline of one session.
type Timeline struct {
	Events []TimelineEvent `json:"events"`
	lines  map[timelineRef]int
	// ats are the times of Events, by index.
	ats []time.Time
}

// Line is the line of the object kind (RefCall and the like) at index i of its slice of
// TimelineInput; 0 when the timeline has no such object.
func (tl Timeline) Line(kind string, i int) int {
	return tl.lines[timelineRef{kind, i}]
}

// WaitLine is the line of in.Waits[i] on tl, the timeline of in: its question's call, its
// permission window, or else, for a window Claude's tool_decision stands for, the last event at or
// before it; the first event when none came before; 0 on an empty timeline.
func (tl Timeline) WaitLine(in TimelineInput, i int) int {
	w := in.Waits[i]
	if w.Call != nil {
		for k := range in.Calls {
			if &in.Calls[k] == w.Call || in.Calls[k].ToolUseID == w.Call.ToolUseID && in.Calls[k].At.Equal(w.Call.At) {
				if line := tl.Line(RefCall, k); line > 0 {
					return line
				}
				break
			}
		}
	} else if line := tl.Line(RefPermission, i); line > 0 {
		return line
	}
	if len(tl.ats) == 0 {
		return 0
	}
	k, _ := slices.BinarySearchFunc(tl.ats, w.At, func(a, t time.Time) int {
		if a.After(t) {
			return 1
		}
		return -1
	})
	return max(k, 1)
}

// timelineRef names an object of TimelineInput by its kind and index.
type timelineRef struct {
	kind string
	i    int
}

// timelineItem is an object of the session placed in time; rank orders the objects of one instant.
type timelineItem struct {
	at   time.Time
	rank int
	ref  timelineRef
}

// The ranks of the objects of one instant, as the colleague's builder orders them: the prompt,
// then calls, compactions and subagents, then skills, then requests, then the answer and aborts.
const (
	rankPrompt = 0
	rankCall   = 2
	rankSkill  = 3
	rankAPI    = 4
	rankStop   = 6
)

// BuildTimeline lays out a session's prompts, calls, permission windows, model requests, API
// errors, Stops, compactions, subagents, aborts and skills in time, ordered by rank within an
// instant, and numbers them 1..N.
func BuildTimeline(in TimelineInput) Timeline {
	items := timelineItems(in)
	slices.SortStableFunc(items, func(a, b timelineItem) int {
		if c := a.at.Compare(b.at); c != 0 {
			return c
		}
		return a.rank - b.rank
	})
	var starts []time.Time
	for _, t := range PersonTurns(in.Turns) {
		starts = append(starts, t.Start)
	}
	waitByCall := map[*Call]*Wait{}
	for i := range in.Waits {
		if w := &in.Waits[i]; w.Call != nil {
			waitByCall[w.Call] = w
		}
	}
	tl := Timeline{lines: map[timelineRef]int{}}
	for _, it := range items {
		line := len(tl.Events) + 1
		tl.lines[it.ref] = line
		ev := TimelineEvent{Line: line, At: ISO(it.at), Turn: turnIndex(starts, it.at)}
		describe(&ev, in, it.ref, waitByCall)
		if src, ok := sourceOf(in, it.ref); ok {
			ev.SrcLine, ev.SrcKind = &src.Line, &src.Kind
		}
		tl.Events = append(tl.Events, ev)
		tl.ats = append(tl.ats, it.at)
	}
	return tl
}

// timelineItems are the objects of in to lay out, in the colleague's order of kinds.
func timelineItems(in TimelineInput) []timelineItem {
	var items []timelineItem
	add := func(at time.Time, rank int, kind string, i int) {
		items = append(items, timelineItem{at: at, rank: rank, ref: timelineRef{kind, i}})
	}
	for i, p := range in.Prompts {
		add(p.At, rankPrompt, RefPrompt, i)
	}
	for i := range in.Calls {
		add(in.Calls[i].At, rankCall, RefCall, i)
	}
	for i, w := range in.Waits {
		if w.Permission != nil {
			add(w.At, rankCall, RefPermission, i)
		}
	}
	for i, a := range in.API {
		add(a.At, rankAPI, RefAPI, i)
	}
	for i, e := range in.Claude {
		if e.Event == "api_error" {
			add(e.Time, rankAPI, RefAPIError, i)
		}
	}
	for i, ev := range in.Events {
		if ev.Event == "Stop" {
			add(ev.Time, rankStop, RefStop, i)
		}
	}
	for i, ev := range in.Compactions {
		add(ev.Time, rankCall, RefCompaction, i)
	}
	for _, kind := range []string{"SubagentStart", "SubagentStop"} {
		for i, ev := range in.Events {
			if ev.Event != kind {
				continue
			}
			ref := RefSubStart
			if kind == "SubagentStop" {
				ref = RefSubStop
			}
			add(ev.Time, rankCall, ref, i)
		}
	}
	for i, ev := range in.Events {
		if ev.Event == "Interrupt" {
			add(ev.Time, rankStop, RefInterrupt, i)
		}
	}
	for i, s := range in.Skills {
		add(s.At, rankSkill, RefSkill, i)
	}
	return items
}

// turnIndex is the index of the turn open at at, 0 before the first.
func turnIndex(starts []time.Time, at time.Time) int {
	i := sort.Search(len(starts), func(i int) bool { return starts[i].After(at) }) - 1
	return max(i, 0)
}

// describe fills the kind and texts of ev from the object ref names.
func describe(ev *TimelineEvent, in TimelineInput, ref timelineRef, waitByCall map[*Call]*Wait) {
	switch ref.kind {
	case RefPrompt:
		p := in.Prompts[ref.i]
		if !p.Person() {
			ev.K, ev.X = "agent", text(NoticeLabel(p.Raw))
			return
		}
		ev.K, ev.X, ev.Side = "prompt", text(orDash(cleanWith(in.memo, p.Text, timelineTextMax))), text("вы")
		if p.Kind == PromptKindReply {
			ev.Note = text("ответ на вопрос агента")
		}
	case RefCall:
		describeCall(ev, &in.Calls[ref.i], waitByCall, in.memo)
	case RefPermission:
		w := in.Waits[ref.i]
		ev.K, ev.Tool = "wait", text(DisplayTool(w.Permission.Tool))
		ev.X = text(cleanWith(in.memo, "разрешение · "+toolSummary(in.memo, w.Permission.Tool, w.Permission.ToolInput, timelineShortMax), timelineShortMax))
		ev.Side = text("ждал разрешения, длина неизвестна")
		if w.Min != nil {
			ev.Side = text(fmt.Sprintf("ждал разрешения %.1f мин", *w.Min))
		}
	case RefAPI:
		describeAPI(ev, in.API[ref.i])
	case RefAPIError:
		e := in.Claude[ref.i]
		ev.K, ev.Tool = "err", text("api")
		ev.X = text(cleanWith(in.memo, fmt.Sprintf("ошибка API · %s %s", e.StatusCode, e.Error), timelineShortMax))
	default:
		describeHook(ev, in, ref)
	}
}

// describeHook fills ev from a hook event: a Stop, a compaction, a subagent's start or stop, an
// abort, or a skill.
func describeHook(ev *TimelineEvent, in TimelineInput, ref timelineRef) {
	switch ref.kind {
	case RefStop:
		ev.K, ev.X = "answer", text(orDash(cleanWith(in.memo, in.Events[ref.i].LastAssistantMessage, timelineTextMax)))
	case RefCompaction:
		trigger := in.Compactions[ref.i].Trigger
		if trigger == "" {
			trigger = "триггер не указан"
		}
		ev.K, ev.X = "compact", text(fmt.Sprintf("сжатие контекста (%s)", trigger))
	case RefSubStart, RefSubStop:
		e := in.Events[ref.i]
		who := e.AgentType
		if who == "" {
			who = e.AgentID
		}
		ev.K, ev.X = "agent", text(cleanWith(in.memo, "субагент запущен · "+who, timelineShortMax))
		if ref.kind == RefSubStop {
			ev.X = text(cleanWith(in.memo, "субагент завершён · "+who, timelineShortMax))
			ev.Note = text(cleanWith(in.memo, e.LastAssistantMessage, timelineNoteMax))
		}
	case RefInterrupt:
		reason := in.Events[ref.i].Reason
		if reason == "" {
			reason = "Interrupt"
		}
		ev.K, ev.X = "abort", text(cleanWith(in.memo, "ход прерван · "+reason, timelineShortMax))
	case RefSkill:
		s := in.Skills[ref.i]
		ev.K, ev.Tool = "skill", text(DisplayTool(s.Tool))
		ev.X = text(cleanWith(in.memo, "skill · "+s.Name, timelineShortMax))
		if s.Path != "" {
			ev.Note = text(cleanWith(in.memo, s.Path, timelineNoteMax))
		}
	}
}

// describeCall fills ev from a tool call: a question, a call of an agent, or another call.
func describeCall(ev *TimelineEvent, c *Call, waitByCall map[*Call]*Wait, memo *redact.Memo) {
	ev.Tool, ev.X = text(c.Name), text(toolSummary(memo, c.Tool, c.Input, timelineShortMax))
	if c.DurationS != nil {
		ev.Side = text(FmtDur(*c.DurationS))
	}
	switch {
	case IsQuestionTool(c.Tool):
		ev.K, ev.Side = "wait", text(questionSide(waitByCall[c]))
	case c.Tool == "Agent" || c.Tool == "SendMessage" || strings.HasPrefix(c.Name, "collaboration."):
		ev.K = "agent"
	case c.State == StateError:
		ev.K = "err"
	default:
		ev.K = "tool"
	}
	note := ""
	if c.State == StateError || c.State == StateUnknown {
		note = c.Note
	}
	if c.AgentID != "" {
		who := c.AgentType
		if who == "" {
			who = string([]rune(c.AgentID)[:min(8, len([]rune(c.AgentID)))])
		}
		sub := "субагент " + who
		if note != "" {
			sub += " · " + note
		}
		note = cleanWith(memo, sub, timelineNoteMax)
	}
	ev.Note = text(note)
}

// questionSide is the label of a question's event: what came of the wait w, nil for a question
// known only by its Post.
func questionSide(w *Wait) string {
	switch {
	case w == nil:
		return "вопрос без PreToolUse"
	case w.Failed:
		return "вопрос не прошёл"
	case w.Short && w.IdleFrom.Equal(w.At):
		return "ответ сразу"
	case !w.Stopped:
		return "агент продолжал работу"
	case w.Min != nil:
		return fmt.Sprintf("ждал %.1f мин", *w.Min)
	}
	return "без ответа"
}

// describeAPI fills ev from a model request.
func describeAPI(ev *TimelineEvent, a APIRecord) {
	ratio := 0.0
	if a.Input != 0 {
		ratio = float64(a.Cached) / float64(a.Input) * 100
	}
	model := a.Model
	if model == "" {
		model = "модель?"
	}
	input := a.Input
	ev.K = "api"
	ev.X = text(fmt.Sprintf("%s · вход %s (кэш %.0f%%)", model, FmtKtok(&input), ratio))
	note := fmt.Sprintf("выход %d", a.Output)
	switch a.Src {
	case SrcCodexSSE:
		note += " · codex.sse_event"
	case SrcCodexRollout:
		note += fmt.Sprintf(" · %d отв. за ход · из журнала сессии", a.Responses)
	}
	ev.Note = text(note)
	if a.Cost != nil {
		ev.Side = text(FmtUSD(*a.Cost))
	}
}

// text is s as an optional text: nil when empty.
func text(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// orDash is s, or "—" when it is empty.
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// The limits of a turn's texts in a SessionTimeline, in runes.
const (
	turnPromptMax = 200
	turnAnswerMax = 300
)

// SessionTimeline is the timeline of one session as GET /api/analytics/sessions/{id} gives it:
// its events, the cost curve Cum (minutes from the start, cost so far in USD), the context curve
// Ctx (minutes from the start, the request's input in thousands of tokens), the calls by tool and
// the turns.
type SessionTimeline struct {
	ID     string          `json:"id"`
	Events []TimelineEvent `json:"events"`
	Cum    [][2]float64    `json:"cum"`
	Ctx    [][2]float64    `json:"ctx"`
	Tools  ToolCounts      `json:"tools"`
	Turns  []TimelineTurn  `json:"turns"`
}

// TimelineTurn is one turn of a SessionTimeline. End is its Stop, nil while it is open;
// PromptLine the line of its prompt, nil without one; Tok its tokens, nil when the session has
// no model request.
type TimelineTurn struct {
	Turn       int              `json:"turn"`
	TurnID     string           `json:"turn_id"`
	Start      string           `json:"start"`
	End        *string          `json:"end"`
	DurMs      int64            `json:"dur_ms"`
	State      string           `json:"state"`
	PromptLine *int             `json:"prompt_line"`
	Prompt     *string          `json:"prompt"`
	Answer     *string          `json:"answer"`
	Tok        *TimelineTurnTok `json:"tok"`
}

// TimelineTurnTok are the tokens of a turn's requests.
type TimelineTurnTok struct {
	Input  int64 `json:"input"`
	Cached int64 `json:"cached"`
	Output int64 `json:"output"`
}

// ToolCount is the number of calls of one tool, by its shown name.
type ToolCount struct {
	Name  string
	Count int
}

// ToolCounts are the calls by tool, the commonest first and a tie in the order the tools first
// appear. In JSON it is an object in that order.
type ToolCounts []ToolCount

// Count is the number of calls of the tool name.
func (tc ToolCounts) Count(name string) int {
	for _, c := range tc {
		if c.Name == name {
			return c.Count
		}
	}
	return 0
}

// MarshalJSON writes the counts as one object, in their order.
func (tc ToolCounts) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, c := range tc {
		if i > 0 {
			b.WriteByte(',')
		}
		name, err := json.Marshal(c.Name)
		if err != nil {
			return nil, err
		}
		b.Write(name)
		fmt.Fprintf(&b, ":%d", c.Count)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// BuildSessionTimeline builds the SessionTimeline of the session sid that started at start.
func BuildSessionTimeline(sid string, start time.Time, in TimelineInput) SessionTimeline {
	return SessionTimelineOf(sid, start, in, BuildTimeline(in))
}

// SessionTimelineOf is the SessionTimeline of the session sid that started at start, laid out
// from tl, the timeline BuildTimeline built of in.
func SessionTimelineOf(sid string, start time.Time, in TimelineInput, tl Timeline) SessionTimeline {
	st := SessionTimeline{
		ID: sid, Events: tl.Events, Cum: [][2]float64{}, Ctx: [][2]float64{},
		Tools: toolCounts(in.Calls), Turns: []TimelineTurn{},
	}
	if st.Events == nil {
		st.Events = []TimelineEvent{}
	}
	total := 0.0
	for _, a := range in.API {
		mins := round2(Minutes(start, a.At))
		if a.Cost != nil {
			if len(st.Cum) == 0 {
				st.Cum = append(st.Cum, [2]float64{0, 0})
			}
			total += *a.Cost
			st.Cum = append(st.Cum, [2]float64{mins, roundTo(total, 4)})
		}
		st.Ctx = append(st.Ctx, [2]float64{mins, roundTo(float64(a.Input)/1000, 1)})
	}
	for _, t := range in.Turns {
		if t.Notice && len(st.Turns) > 0 {
			// The agent's notice opens no turn of the feed: its work joins the turn before it, its
			// length added without the idle between them, its end and state the latest.
			last := &st.Turns[len(st.Turns)-1]
			last.DurMs += t.DurMs
			last.End, last.State = text(ISO(t.Stop)), t.State
			if last.Tok != nil {
				last.Tok.Input += t.Tok.Input
				last.Tok.Cached += t.Tok.Cached
				last.Tok.Output += t.Tok.Output
			}
			continue
		}
		turn := TimelineTurn{
			Turn: len(st.Turns), TurnID: t.Key, Start: ISO(t.Start), End: text(ISO(t.Stop)), DurMs: t.DurMs, State: t.State,
			Prompt: text(cleanWith(in.memo, t.Prompt, turnPromptMax)), Answer: text(cleanWith(in.memo, t.Answer, turnAnswerMax)),
		}
		if !t.PromptAt.IsZero() {
			if j := slices.IndexFunc(in.Prompts, func(p Prompt) bool { return p.At.Equal(t.PromptAt) }); j >= 0 {
				line := tl.Line(RefPrompt, j)
				turn.PromptLine = &line
			}
		}
		if len(in.API) > 0 {
			turn.Tok = &TimelineTurnTok{Input: t.Tok.Input, Cached: t.Tok.Cached, Output: t.Tok.Output}
		}
		st.Turns = append(st.Turns, turn)
	}
	return st
}

// toolCounts counts the calls by their shown name.
func toolCounts(calls []Call) ToolCounts {
	tc := ToolCounts{}
	at := map[string]int{}
	for _, c := range calls {
		if i, ok := at[c.Name]; ok {
			tc[i].Count++
			continue
		}
		at[c.Name] = len(tc)
		tc = append(tc, ToolCount{Name: c.Name, Count: 1})
	}
	slices.SortStableFunc(tc, func(a, b ToolCount) int { return b.Count - a.Count })
	return tc
}
