package analytics

import (
	"sort"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// The states of a turn.
const (
	// TurnComplete: a Stop closed the turn.
	TurnComplete = "task_complete"
	// TurnAborted: an Interrupt closed the turn.
	TurnAborted = "turn_aborted"
	// TurnOpen: nothing closed the turn; it ends on its last event.
	TurnOpen = "open"
)

// Turn is one turn of a session: from the person's prompt to the agent's answer.
type Turn struct {
	// Key is the turn_id of Codex's main thread or the prompt_id of Claude.
	Key string
	// Start is the first event of the turn, or its prompt when that is earlier.
	Start time.Time
	// Last is the latest event of the turn, SessionStart and SessionEnd aside.
	Last time.Time
	// Stop is when a Stop or an Interrupt closed the turn; zero while it is open.
	Stop time.Time
	// State is TurnComplete, TurnAborted or TurnOpen.
	State string
	// Prompt and PromptAt are the turn's first prompt as PromptText reads it; empty without one.
	Prompt   string
	PromptAt time.Time
	// Answer is the agent's last message of the Stop; empty when the Stop carried none.
	Answer string
	// End is Stop, or Last for an open turn; DurMs is the time from Start to End.
	End   time.Time
	DurMs int64
	// Tok are the tokens of the turn's model requests.
	Tok TurnTokens
	// Notice tells that the agent's notice (PromptKindSystem) opened the turn after an earlier
	// turn: the agent's work, not a turn of the person. The first turn of a session is never one.
	Notice bool
}

// PersonTurns are the turns of the person: those not opened by the agent's notice.
func PersonTurns(turns []*Turn) []*Turn {
	var out []*Turn
	for _, t := range turns {
		if !t.Notice {
			out = append(out, t)
		}
	}
	return out
}

// Turns are the turns of a session in the order they start, with the lookup of the turn an event
// belongs to.
type Turns struct {
	List []*Turn
	// API are the session's model requests that count, oldest first: those BuildTurns was given
	// less the codex.sse_event inside a turn whose total the rollout gives.
	API    []APIRecord
	agent  string
	byKey  map[string]*Turn
	starts []time.Time
	// notices are the keys of the agent's notices (PromptKindSystem).
	notices map[string]bool
}

// BuildTurns lays a session's hook events out in turns. A Codex turn is a turn_id of the main
// thread (an event of a subagent carries agent_id and opens none); a Claude turn is a prompt_id.
// Every other event belongs to the turn open at its time. The first Stop of a turn completes it
// with its answer; otherwise its first Interrupt aborts it; otherwise it stays open and ends on
// its last event. The model requests (BuildAPI) join the turn open at their time, extend it and
// add their tokens to it. A turn the agent's notice (PromptKindSystem) opens after another turn
// is marked Notice: it keeps its own span, Stop and tokens, but it is not a turn of the person.
func BuildTurns(agent string, events []telemetry.HookEvent, prompts []Prompt, api []APIRecord) *Turns {
	ts := &Turns{agent: agent, byKey: map[string]*Turn{}, notices: map[string]bool{}}
	// A turn is the notice's when the first prompt of its key is a notice: the person's prompt
	// that opened a turn keeps it the person's, whatever notice comes later under that key.
	seen := map[string]bool{}
	for _, p := range prompts {
		k := ts.promptKey(p)
		if !seen[k] {
			seen[k] = true
			ts.notices[k] = !p.Person()
		}
	}
	ts.open(events)
	ts.API = ts.attachAPI(api)
	ts.close(events, prompts)
	return ts
}

// open creates the turns of the events and extends each to its last event.
func (ts *Turns) open(events []telemetry.HookEvent) {
	for _, ev := range events {
		k := ts.mainKey(ev)
		if k == "" || ts.byKey[k] != nil {
			continue
		}
		t := &Turn{Key: k, Start: ev.Time, Last: ev.Time, State: TurnOpen, Notice: ts.notices[k] && len(ts.List) > 0}
		ts.byKey[k] = t
		ts.List = append(ts.List, t)
		ts.starts = append(ts.starts, ev.Time)
	}
	for _, ev := range events {
		if ev.Event == "SessionStart" || ev.Event == "SessionEnd" {
			continue
		}
		if t := ts.At(ev.Time, ts.mainKey(ev)); t != nil && ev.Time.After(t.Last) {
			t.Last = ev.Time
		}
	}
}

// close applies the Stops, Interrupts and prompts and sets each turn's end and duration.
func (ts *Turns) close(events []telemetry.HookEvent, prompts []Prompt) {
	for _, event := range []string{"Stop", "Interrupt"} {
		for _, ev := range events {
			if ev.Event != event {
				continue
			}
			t := ts.At(ev.Time, ts.mainKey(ev))
			if t == nil || !t.Stop.IsZero() {
				continue
			}
			t.Stop = ev.Time
			if event == "Stop" {
				t.State, t.Answer = TurnComplete, ev.LastAssistantMessage
			} else {
				t.State = TurnAborted
			}
		}
	}
	for _, p := range prompts {
		if !p.Person() {
			continue
		}
		t := ts.At(p.At, ts.promptKey(p))
		if t == nil || !t.PromptAt.IsZero() {
			continue
		}
		t.Prompt, t.PromptAt = p.Text, p.At
		if p.At.Before(t.Start) {
			t.Start = p.At
		}
	}
	for _, t := range ts.List {
		t.End = t.Stop
		if t.End.IsZero() {
			t.End = t.Last
		}
		t.DurMs = t.End.Sub(t.Start).Milliseconds()
	}
}

// At is the turn of key when there is one, else the turn open at at: the latest to start no
// later than it. Nil when at is before every turn.
func (ts *Turns) At(at time.Time, key string) *Turn {
	if t := ts.byKey[key]; key != "" && t != nil {
		return t
	}
	i := sort.Search(len(ts.starts), func(i int) bool { return ts.starts[i].After(at) }) - 1
	if i < 0 {
		return nil
	}
	return ts.List[i]
}

// promptKey is the turn a prompt names: its turn_id for Codex, its prompt_id for Claude.
func (ts *Turns) promptKey(p Prompt) string {
	if ts.agent == "codex" {
		return p.TurnID
	}
	return p.PromptID
}

// mainKey is the turn an event names: the turn_id of a Codex event of the main thread, the
// prompt_id of a Claude event.
func (ts *Turns) mainKey(ev telemetry.HookEvent) string {
	if ts.agent == "codex" {
		if ev.AgentID != "" {
			return ""
		}
		return ev.TurnID
	}
	return ev.PromptID
}

// TurnMinutes is the length of the turns together, in minutes to one decimal: the active time of
// a session without Claude's claude_code.active_time metric.
func TurnMinutes(turns []*Turn) float64 {
	var ms int64
	for _, t := range turns {
		ms += t.DurMs
	}
	return roundTo(float64(ms)/60000, 1)
}
