package analytics

import (
	"sort"
	"strings"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// StopMinS is the shortest idle, in seconds, that counts as the agent stopping on a question: an
// answer sooner than that was not waited for (a refusal, an automatic answer).
const StopMinS = 5

// IsQuestionTool tells the tools by which the agent asks the person: Codex's request_user_input
// and request_user_input_async, Claude's AskUserQuestion.
func IsQuestionTool(tool string) bool {
	switch tool {
	case "request_user_input", "request_user_input_async", "AskUserQuestion":
		return true
	}
	return false
}

// Wait is one question of the agent to the person, or one permission window, and whether the
// agent stopped on it.
type Wait struct {
	// Call is the question's call; nil for a permission window.
	Call *Call
	// Permission is the PermissionRequest hook that opened a permission window, Decision the
	// tool_decision of Claude that stands for one without it; both nil for a question.
	Permission *telemetry.HookEvent
	Decision   *telemetry.ClaudeEvent
	// At is when the question was asked: its PreToolUse.
	At time.Time
	// IdleFrom is when the calls of the main agent that were running at the question ended: the
	// idle starts there, not at the question.
	IdleFrom time.Time
	// Reply is the person's next prompt after the question; nil when none came.
	Reply *Prompt
	// End is when the wait ended: for a blocking question its result or the reply, whichever is
	// first; for an async question the reply. Zero while unknown.
	End time.Time
	// Stopped tells that the agent stood idle on the question: no new call of the main agent
	// from IdleFrom to End, at least StopMinS long, and no call of the same turn with an unknown
	// end running.
	Stopped bool
	// Failed marks a blocking question whose result was an error: it was never asked.
	Failed bool
	// Short marks a wait shorter than StopMinS.
	Short bool
	// Min is the idle from IdleFrom to End in minutes; nil while End is unknown.
	Min *float64
}

// BuildWaits returns the questions of a session's calls, in the order of the calls. A question
// known only by its Post has no time and is no wait.
//
// A blocking question holds the agent until its result, which often carries the answer without a
// new prompt, so it ends at its result or the next prompt, whichever is first; an async one does
// not hold the agent and ends at the next prompt. The idle starts when the calls of the main agent
// already running at the question end. A call of the main agent whose end is unknown forbids a
// stop only when it is of the question's turn: a new turn starts with the person's prompt, and a
// call of an earlier turn without a Post is a lost result, not work. A failed or short question is
// no stop; a question without an end is a stop when the main agent started nothing after it.
func BuildWaits(calls []Call, prompts []Prompt, turns *Turns) []Wait {
	var mainCalls []*Call
	var mainAt []time.Time
	for i := range calls {
		c := &calls[i]
		if c.AgentID != "" {
			continue
		}
		mainAt = append(mainAt, c.At)
		if !IsQuestionTool(c.Tool) {
			mainCalls = append(mainCalls, c)
		}
	}
	sort.Slice(mainAt, func(i, j int) bool { return mainAt[i].Before(mainAt[j]) })

	var waits []Wait
	for i := range calls {
		c := &calls[i]
		if !IsQuestionTool(c.Tool) || c.NoPre {
			continue
		}
		waits = append(waits, buildWait(c, prompts, turns, mainCalls, mainAt))
	}
	return waits
}

// buildWait is the wait of the question c.
func buildWait(c *Call, prompts []Prompt, turns *Turns, mainCalls []*Call, mainAt []time.Time) Wait {
	blocking := !strings.HasSuffix(c.Tool, "_async")
	w := Wait{Call: c, At: c.At, Failed: blocking && c.State == StateError}
	for i := sort.Search(len(prompts), func(i int) bool { return prompts[i].At.After(c.At) }); i < len(prompts); i++ {
		if prompts[i].Person() {
			w.Reply = &prompts[i]
			break
		}
	}
	if w.Reply != nil && !w.Failed {
		w.End = w.Reply.At
	}
	if blocking && !w.Failed && c.HasPost() && (w.End.IsZero() || c.PostAt.Before(w.End)) {
		w.End = c.PostAt
	}

	turn := turns.At(c.At, turns.mainKey(c.PreEvent()))
	w.IdleFrom = c.At
	unknown := false
	for _, x := range mainCalls {
		if x.At.After(c.At) {
			continue
		}
		end, ok := callEnd(x)
		switch {
		case !ok:
			unknown = unknown || turns.At(x.At, turns.mainKey(x.PreEvent())) == turn
		case end.After(w.IdleFrom):
			w.IdleFrom = end
		}
	}

	next := sort.Search(len(mainAt), func(i int) bool { return mainAt[i].After(c.At) })
	w.Short = !w.End.IsZero() && w.End.Sub(w.IdleFrom).Seconds() < StopMinS
	switch {
	case w.Failed || w.Short || unknown:
		w.Stopped = false
	case w.End.IsZero():
		w.Stopped = next >= len(mainAt)
	default:
		w.Stopped = next >= len(mainAt) || !mainAt[next].Before(w.End)
	}
	if !w.End.IsZero() {
		m := Minutes(w.IdleFrom, w.End)
		w.Min = &m
	}
	return w
}

// callEnd is when the call ended: its Post, else its start plus the duration Claude's
// tool_result gives; false when unknown.
func callEnd(c *Call) (time.Time, bool) {
	if c.HasPost() {
		return c.PostAt, true
	}
	if c.DurationS != nil {
		return c.At.Add(time.Duration(*c.DurationS * float64(time.Second))), true
	}
	return time.Time{}, false
}

// PermissionWaits returns the permission windows of a session, oldest first. A PermissionRequest
// hook opens a window that the next event of the main agent after it closes (permissionEnds); a
// subagent's event does not, and without one the window has no length. Claude's tool_decision
// whose source is the person is the same window when one is open at its time; without a
// PermissionRequest it is a window of its own, of unknown length.
func PermissionWaits(events []telemetry.HookEvent, native []telemetry.ClaudeEvent) []Wait {
	var waits []Wait
	for i := range events {
		ev := &events[i]
		if ev.Event != "PermissionRequest" || ev.AgentID != "" {
			continue
		}
		w := Wait{Permission: ev, At: ev.Time, IdleFrom: ev.Time, Stopped: true}
		for _, next := range events[i+1:] {
			if next.AgentID == "" && permissionEnds(next.Event) && next.Time.After(ev.Time) {
				w.End = next.Time
				m := Minutes(w.At, w.End)
				w.Min = &m
				break
			}
		}
		waits = append(waits, w)
	}
	opened := len(waits)
	for i := range native {
		e := &native[i]
		if e.Event != "tool_decision" || e.Source != "user" || insideWindow(waits[:opened], e.Time) {
			continue
		}
		waits = append(waits, Wait{Decision: e, At: e.Time, IdleFrom: e.Time, Stopped: true})
	}
	return waits
}

// permissionEnds tells the hook events that show the agent moved on after a permission window:
// a tool call, a denial, the person's prompt, or the turn's or the session's end.
func permissionEnds(event string) bool {
	switch event {
	case "PreToolUse", "PostToolUse", "PostToolUseFailure", "PermissionDenied", "UserPromptSubmit",
		"Stop", "Interrupt", "PreCompact", "SessionEnd":
		return true
	}
	return false
}

// insideWindow tells whether at falls in one of the permission windows: from its start to its
// end, or after its start when its end is unknown.
func insideWindow(windows []Wait, at time.Time) bool {
	for _, w := range windows {
		if !at.Before(w.At) && (w.End.IsZero() || !at.After(w.End)) {
			return true
		}
	}
	return false
}
