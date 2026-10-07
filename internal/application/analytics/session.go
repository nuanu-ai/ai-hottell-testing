package analytics

import (
	"path"
	"slices"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
	"git.alva.dev/alva/harness-telemetry/pkg/redact"
)

// The fixed values of a session's record while the rules behind them are not on the server.
const (
	// OutcomeUnknown is a session's outcome: nothing on the server judges it.
	OutcomeUnknown = "unknown"
	// TranscriptNotUsed is sources.transcript: the session's record is not read from its
	// transcript.
	TranscriptNotUsed = "not_used"
)

// sessionTextMax is the most characters of a session's first prompt and title.
const sessionTextMax = 200

// SessionInput is what one session is built from: its hook events and what the stores hold of
// it. The caller reads them for the session's key.
type SessionInput struct {
	Key SessionKey
	// UserName and Host are the person's name and the machine; the service fills them.
	UserName, Host string
	// Events are the session's hook events, oldest first (GroupSessions).
	Events []telemetry.HookEvent
	// Claude and Metrics are Claude Code's native events and metric points of the session.
	Claude  []telemetry.ClaudeEvent
	Metrics []telemetry.ClaudeMetric
	// SSE are the codex.sse_event linked to the session by conversation.id.
	SSE []telemetry.CodexSSE
	// Facts are the facts of the session's Codex rollout; the zero value without one.
	Facts telemetry.CodexFacts
	// TranscriptAPI are the requests read from a Claude transcript (ClaudeTranscriptAPI), nil
	// when the native OpenTelemetry has them.
	TranscriptAPI []APIRecord
	// Snapshot is the session's latest skill snapshot (the SkillSnapshot reader of
	// internal/application/telemetry), nil without one.
	Snapshot *telemetry.SkillSnapshot
	// Src are the transcript lines the timeline's events cite; empty when unread.
	Src SourceLines
	// memo masks the texts of one build once (HT-482); nil masks every time.
	memo *redact.Memo
}

// BuiltSession is one session built: its record and what the aggregates, the friction rules and
// the timeline read of it.
type BuiltSession struct {
	Key    SessionKey
	Events []telemetry.HookEvent
	Claude []telemetry.ClaudeEvent
	// Metrics are Claude Code's metric points of the session.
	Metrics []telemetry.ClaudeMetric
	Calls   Calls
	Prompts []Prompt
	Turns   *Turns
	// API are the session's model requests that count (Turns.API).
	API         []APIRecord
	Waits       []Wait
	Compactions []telemetry.HookEvent
	Skills      []SkillActivation
	// Snapshot is the session's skill snapshot, nil without one.
	Snapshot *telemetry.SkillSnapshot
	// Timeline numbers the session's events; evidence cites its lines.
	Timeline Timeline
	// Friction are the session's friction episodes by signal (SessionFriction).
	Friction Friction
	Cwd      string
	// Project is the session's project (SessionProject).
	Project  string
	Partial  Partial
	Work     Work
	UserTime UserTime
	// Start and End are the earliest and the latest of the hook events, the requests and the
	// native events.
	Start, End time.Time
	// Session is the record of the contract.
	Session Session
	// memo masks the texts of the build that built it; nil masks every time.
	memo *redact.Memo
}

// SessionTok are a session's or a day's tokens. Reasoning is null when no request reported it.
type SessionTok struct {
	Input     int64  `json:"input"`
	Cached    int64  `json:"cached"`
	Output    int64  `json:"output"`
	Reasoning *int64 `json:"reasoning"`
}

// SessionSources tell what the record of a session rests on.
type SessionSources struct {
	// Transcript is TranscriptNotUsed.
	Transcript string `json:"transcript"`
	// Hooks is recorded, or partial when only the tail of the session is recorded.
	Hooks string `json:"hooks"`
	// OTel is recorded or missing for Claude, linked or unlinked for Codex (OTelSource).
	OTel string `json:"otel"`
	// TranscriptFacts is recorded when the Codex rollout gave facts (TranscriptFactsSource).
	TranscriptFacts string `json:"transcript_facts"`
	// HookEvents counts the session's hook events by name.
	HookEvents map[string]int `json:"hook_events"`
}

// Session is the record of one session in the dataset; the key is (user_id, agent, id).
type Session struct {
	ID             string      `json:"id"`
	Short          string      `json:"short"`
	Agent          string      `json:"agent"`
	UserID         uuid.UUID   `json:"user_id"`
	UserName       string      `json:"user_name"`
	Host           string      `json:"host"`
	Kind           SessionKind `json:"kind"`
	KindReason     *string     `json:"kind_reason"`
	Records        int         `json:"records"`
	Project        string      `json:"project"`
	Cwd            string      `json:"cwd"`
	Branch         *string     `json:"branch"`
	Start          string      `json:"start"`
	End            string      `json:"end"`
	WallMin        float64     `json:"wall_min"`
	ActiveMin      float64     `json:"active_min"`
	UserMin        float64     `json:"user_min"`
	Turns          int         `json:"turns"`
	Prompts        int         `json:"prompts"`
	Corrections    *int        `json:"corrections"`
	Reqs           *int64      `json:"reqs"`
	Model          *string     `json:"model"`
	Tok            *SessionTok `json:"tok"`
	CacheHit       *float64    `json:"cache_hit"`
	CostUSD        *float64    `json:"cost_usd"`
	CostBasis      *string     `json:"cost_basis"`
	Calls          int         `json:"calls"`
	Errors         int         `json:"errors"`
	UnknownResults int         `json:"unknown_results"`
	OutcomeKnown   int         `json:"outcome_known"`
	Commits        int         `json:"commits"`
	PRs            int         `json:"prs"`
	Added          *int64      `json:"added"`
	Removed        *int64      `json:"removed"`
	First          *string     `json:"first"`
	Title          *string     `json:"title"`
	// Flags are the keys of the friction signals the session has episodes of (Friction.Flags).
	Flags       []string       `json:"flags"`
	Compactions int            `json:"compactions"`
	Aborted     int            `json:"aborted"`
	Waits       int            `json:"waits"`
	WaitMin     *float64       `json:"wait_min"`
	Outcome     string         `json:"outcome"`
	Tasks       []any          `json:"tasks"`
	Checks      []any          `json:"checks"`
	Deep        any            `json:"deep"`
	Sources     SessionSources `json:"sources"`
	Daily       []SessionDay   `json:"daily"`
}

// BuildSession builds one session from its input, as the colleague's build_session and
// session_fields do: the calls with the facts of the Codex rollout, the prompts, the model
// requests and turns, the waits, the person's time, the work and the record. Its events are
// oldest first.
func BuildSession(in SessionInput) *BuiltSession {
	agent, events := in.Key.Agent, in.Events
	b := &BuiltSession{
		Key: in.Key, Events: events, Claude: in.Claude, Metrics: in.Metrics, Snapshot: in.Snapshot, memo: in.memo,
	}
	b.Calls = BuildCalls(agent, events, in.Claude)
	ApplyCodexFacts(b.Calls.Calls, in.Facts)
	calls := b.Calls.Calls
	b.Prompts = BuildPrompts(events)
	built := BuildAPI(agent, events, in.Claude, in.SSE, in.Facts)
	if len(in.TranscriptAPI) > 0 {
		built = append(slices.Clone(built), in.TranscriptAPI...)
		slices.SortStableFunc(built, func(a, c APIRecord) int { return a.At.Compare(c.At) })
	}
	b.Turns = BuildTurns(agent, events, b.Prompts, built)
	b.API = b.Turns.API
	b.Waits = append(BuildWaits(calls, b.Prompts, b.Turns), PermissionWaits(events, in.Claude)...)
	b.Compactions = Compactions(events)
	b.Cwd = SessionCwd(events)
	b.Project = SessionProject(events, b.Cwd)
	b.Skills = SessionSkills(calls, b.Cwd)
	b.Partial = PartialOf(in.Key.SessionID, events, in.firstRecord())
	b.Work = SessionWork(agent, calls, in.Metrics, b.Partial.Partial)
	b.UserTime = UserGaps(b.Prompts, b.Turns.List, b.Waits)
	b.Start, b.End = sessionSpan(events, b.API, in.Claude)
	b.Timeline = BuildTimeline(TimelineInput{
		Events: events, Prompts: b.Prompts, Calls: calls, API: b.API, Claude: in.Claude,
		Compactions: b.Compactions, Skills: b.Skills, Turns: b.Turns.List, Waits: b.Waits, Src: in.Src,
		memo: in.memo,
	})
	b.Friction = SessionFriction(b)
	b.Session = b.record(in, built)
	// The objects of the inputs were read, by the skills and the friction, and are let go: they
	// were a tenth of what a dataset holds for as long as it lives (HT-533).
	for i := range calls {
		calls[i].Args = nil
	}
	return b
}

// record is the Session of b; built are the requests BuildAPI returned.
func (b *BuiltSession) record(in SessionInput, built []APIRecord) Session {
	sid, agent := b.Key.SessionID, b.Key.Agent
	counts := CountCalls(b.Calls.Calls)
	kind, reason := ClassifySession(b.Prompts, b.Cwd, CodexExec(in.SSE))
	s := Session{
		ID: sid, Short: ShortID(sid), Agent: agent, UserID: b.Key.UserID, UserName: in.UserName, Host: in.Host,
		Kind: kind, KindReason: optString(reason), Records: len(b.Events),
		Project: b.Project, Cwd: b.Cwd,
		Start: ISO(b.Start), End: ISO(b.End),
		ActiveMin: ActiveMinutes(b.Turns.List, in.Metrics), UserMin: UserMinutes(b.UserTime),
		Turns: len(PersonTurns(b.Turns.List)), Prompts: len(PersonPrompts(b.Prompts)),
		Model: optString(SessionModel(b.API, b.Events)),
		Calls: counts.Calls, Errors: counts.Errors, UnknownResults: counts.Unknown, OutcomeKnown: counts.OutcomeKnown,
		Commits: b.Work.Commits, PRs: b.Work.PRs, Added: b.Work.Added, Removed: b.Work.Removed,
		Flags: b.Friction.Flags(), Compactions: len(b.Compactions), Aborted: countEvents(b.Events, "Interrupt"),
		Waits: len(b.Waits), WaitMin: WaitMinutes(b.Waits),
		Outcome: OutcomeUnknown, Tasks: []any{}, Checks: []any{},
		Sources: SessionSources{
			Transcript: TranscriptNotUsed, Hooks: b.Partial.HooksSource(), OTel: OTelSource(agent, built, in.Metrics),
			TranscriptFacts: TranscriptFactsSource(b.Calls.Calls, b.API), HookEvents: hookEventCounts(b.Events),
		},
	}
	if !b.Start.IsZero() {
		s.WallMin = roundTo(Minutes(b.Start, b.End), 1)
	}
	if s.Turns == 0 {
		s.Turns = countEvents(b.Events, "Stop")
	}
	if tok, reqs := SessionTokens(b.API); tok != nil {
		s.Tok = &SessionTok{Input: tok.Input, Cached: tok.Cached, Output: tok.Output, Reasoning: tok.Reasoning}
		s.Reqs = &reqs
		if tok.Input > 0 {
			hit := roundTo(float64(tok.Cached)/float64(tok.Input), 3)
			s.CacheHit = &hit
		}
	}
	s.CostUSD = SessionCost(b.API)
	s.CostBasis = optString(CostBasis(b.API, s.CostUSD))
	if first := firstPrompt(b.Prompts); first != nil {
		s.First = optString(cleanWith(b.memo, first.Text, sessionTextMax))
	}
	s.Title = s.First
	for i := len(b.Prompts) - 1; i >= 0; i-- {
		if b.Prompts[i].Title != "" {
			s.Title = optString(cleanWith(b.memo, b.Prompts[i].Title, sessionTextMax))
			break
		}
	}
	s.Daily = b.daily(time.UTC)
	return s
}

// sessionSpan is the earliest and the latest of the hook events, the requests and the native
// events; zero without events.
func sessionSpan(events []telemetry.HookEvent, api []APIRecord, claude []telemetry.ClaudeEvent) (start, end time.Time) {
	if len(events) == 0 {
		return time.Time{}, time.Time{}
	}
	times := make([]time.Time, 0, len(events)+len(api)+len(claude))
	for _, e := range events {
		times = append(times, e.Time)
	}
	for _, a := range api {
		times = append(times, a.At)
	}
	for _, e := range claude {
		times = append(times, e.Time)
	}
	cmp := func(a, c time.Time) int { return a.Compare(c) }
	return slices.MinFunc(times, cmp), slices.MaxFunc(times, cmp)
}

// firstPrompt is the first typed prompt, nil without one.
func firstPrompt(prompts []Prompt) *Prompt {
	for i := range prompts {
		if prompts[i].Kind == PromptKindPrompt {
			return &prompts[i]
		}
	}
	return nil
}

// projectName is the last element of the working folder, "" without one.
func projectName(cwd string) string {
	if cwd == "" {
		return ""
	}
	return path.Base(cwd)
}

// countEvents counts the hook events named event.
func countEvents(events []telemetry.HookEvent, event string) int {
	n := 0
	for _, e := range events {
		if e.Event == event {
			n++
		}
	}
	return n
}

// hookEventCounts counts the hook events by name.
func hookEventCounts(events []telemetry.HookEvent) map[string]int {
	counts := map[string]int{}
	for _, e := range events {
		counts[e.Event]++
	}
	return counts
}

// optString is s, nil when it is empty.
func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// firstRecord is the session's first record outside the hooks: its first native event, metric
// point or request read from its transcript; zero without one.
func (in SessionInput) firstRecord() time.Time {
	var first time.Time
	take := func(at time.Time) {
		if !at.IsZero() && (first.IsZero() || at.Before(first)) {
			first = at
		}
	}
	for _, e := range in.Claude {
		take(e.Time)
	}
	for _, m := range in.Metrics {
		take(m.Time)
	}
	for _, a := range in.TranscriptAPI {
		take(a.At)
	}
	return first
}
