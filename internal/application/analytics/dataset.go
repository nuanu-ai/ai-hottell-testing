package analytics

import (
	"context"
	"fmt"
	"maps"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
	"git.alva.dev/alva/harness-telemetry/pkg/redact"
)

// SchemaVersion is the version of the dataset's form.
const SchemaVersion = 1

// VariantLive is the only variant the server builds: the live data of the collector, with no
// Deep review, tests or applied changes.
const VariantLive = "live"

// historyLookback is how far before the window the store is read: a session that has an event
// inside the window is built over its whole recorded history, and its earlier events lie before
// the window. A session older than this is built from the part inside the lookback.
const historyLookback = 7 * 24 * time.Hour

// emptyListMax is how many sessions without a prompt or a call the gaps name.
const emptyListMax = 8

// maxSessionWorkers bounds the sessions of a dataset built at once.
const maxSessionWorkers = 8

// generalGaps are the lines of the gaps every dataset carries: what the live data does not hold.
//
//nolint:gochecknoglobals // constant lines of text
var generalGaps = []string{
	"Живой вариант: события хуков hottell и нативный OTel на сервере, факты из транскриптов — где они отправлены. " +
		"Deep-разборы и тесты сюда не входят: задания, исходы, 13 проверок, коррекции = пусто/null.",
	"line в ленте — порядковый номер события внутри сессии (1..N), а не номер строки транскрипта.",
	"calls — PreToolUse (дубли по tool_use_id отброшены); unknown_results — вызовы без PostToolUse и без tool_result Claude.",
	"Ошибка вызова: tool_result success=false (Claude), «Exit code: N≠0» в начале ответа (apply_patch, exec_command), " +
		"isError=true, error/interrupted в JSON-ответе, код выхода из фактов транскрипта Codex. Без транскрипта у Bash " +
		"в Codex кода выхода нет — такие вызовы «выполнены, исход неизвестен».",
	"Codex: codex.api_request в OTel без conversation.id (это запросы /models), поэтому токены по сессиям берутся из " +
		"фактов транскрипта (итог хода) или из codex.sse_event с conversation.id; стоимость — по условной цене.",
	"Claude Code: токены и cost_usd — из событий api_request (собственная оценка Claude Code, не счёт); вход = input + " +
		"cache_read + cache_creation, кэш = cache_read; токены рассуждений не сообщаются (null).",
	"active_min: Claude — метрика claude_code.active_time.total (type=cli); Codex — сумма ходов от первого события/реплики " +
		"до Stop, ход без Stop — до последнего своего события. user_min — паузы «ответ агента или его остановка на вопросе → " +
		"ваша реплика», каждая ≤ 30 мин (длиннее — отсутствие, 0), у обоих агентов; метрика Claude " +
		"claude_code.active_time.total (type=user) не используется, чтобы сессия, день и период считались одинаково.",
	"Длительность вызова: Claude — duration_ms из хука/tool_result; Codex — из фактов транскрипта, иначе разница времени " +
		"хуков PreToolUse и PostToolUse.",
	"Коммиты и PR у Codex считаются только при признаке успеха в выводе (строка «[ветка sha]» у git commit, ссылка " +
		"…/pull/N у gh pr create); у Claude — метрики commit.count, pull_request.count, lines_of_code.count.",
	"otel_traces не используются: у спанов Codex нет id сессии, длительности инструментов Claude уже есть в tool_result.",
	"Имена инструментов Codex в хуках склеены с пространством имён (collaborationspawn_agent, clockcurr_time, webrun) — " +
		"в интерфейсе показаны как collaboration.spawn_agent и т. п.",
}

// Window is the period a dataset covers, To excluded.
type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Dataset is the live analytics of the sessions of a window: the colleague's dataset.json in our
// form. Done and ChecksCatalog are always empty in the live variant.
type Dataset struct {
	SchemaVersion int       `json:"schema_version"`
	Variant       string    `json:"variant"`
	GeneratedAt   time.Time `json:"generated_at"`
	Window        Window    `json:"window"`
	Filter        Filter    `json:"-"`
	Facets        Facets    `json:"facets"`
	Summary       Summary   `json:"summary"`
	// Sessions are the records of the sessions the dataset shows, oldest first.
	Sessions []Session `json:"sessions"`
	// The aggregates over the sessions the dataset shows.
	Tools         []ToolRow        `json:"tools"`
	MCP           []MCPRow         `json:"mcp"`
	Commands      []CommandRow     `json:"commands"`
	Permissions   []PermissionRow  `json:"permissions"`
	Friction      []FrictionRow    `json:"friction"`
	Skills        Skills           `json:"skills"`
	Pricing       Pricing          `json:"pricing"`
	Gaps          []string         `json:"gaps"`
	Findings      []Finding        `json:"findings"`
	Done          []map[string]any `json:"done"`
	ChecksCatalog []map[string]any `json:"checks_catalog"`
	// UserNames are the names of the people of the dataset, filled in by the Service.
	UserNames map[uuid.UUID]string `json:"-"`
	// Encoded is the one encoding of the dataset its answers share, set by the Service on each
	// build; nil for a dataset built without it.
	Encoded *Encoded `json:"-"`
	// Built are the sessions of the window as they were built, oldest first; the session fields
	// and the aggregates are made from them.
	Built []SessionBuild `json:"-"`
}

// SessionBuild is one session with every part the rules build of it.
type SessionBuild struct {
	Key SessionKey
	// Events are the session's hook events, oldest first.
	Events []telemetry.HookEvent
	// Claude and Metrics are the native events and data points of a Claude session, SSE the
	// codex.sse_event of a Codex one.
	Claude  []telemetry.ClaudeEvent
	Metrics []telemetry.ClaudeMetric
	SSE     []telemetry.CodexSSE
	// Facts are the facts of a Codex session's rollout; zero without one.
	Facts telemetry.CodexFacts
	Calls []Call
	// DupPre counts the repeated PreToolUse dropped.
	DupPre  int
	Prompts []Prompt
	Turns   *Turns
	// BuiltAPI are the requests BuildAPI returned and the transcript gave, before the turns
	// replaced any; Turns.API are those that count.
	BuiltAPI   []APIRecord
	Waits      []Wait
	Partial    Partial
	Kind       SessionKind
	KindReason string
	Cwd        string
	// Project is the last folder of Cwd.
	Project     string
	Skills      []SkillActivation
	Compactions []telemetry.HookEvent
	// Start is the earliest of the session's hook events, requests and native events.
	Start time.Time
	// Built is the session as BuildSession built it, with its record.
	Built *BuiltSession
	// Src are the transcript lines its timeline events are built from: of a Codex session from
	// its rollout's fact lines, of a Claude one only when its timeline is asked for.
	Src SourceLines
}

// D16Session is what D16 reads of the session: its question waits apart from its permission
// windows. Its lines are of the timeline BuildSession built from the same input, which laying it
// out again for every session made a sequential step of the build (HT-534).
func (s *SessionBuild) D16Session() D16Session {
	if s.Built == nil {
		return NewD16Session(s.Key.SessionID, s.TimelineInput(), s.Turns)
	}
	return d16SessionOn(s.Key.SessionID, s.TimelineInput(), s.Turns, s.Built.Timeline)
}

// TimelineInput is what the session's timeline lays out.
func (s *SessionBuild) TimelineInput() TimelineInput {
	return TimelineInput{
		Events: s.Events, Prompts: s.Prompts, Calls: s.Calls, API: s.Turns.API, Claude: s.Claude,
		Compactions: s.Compactions, Skills: s.Skills, Turns: s.Turns.List, Waits: s.Waits, Src: s.Src,
		memo: s.memo(),
	}
}

// memo is the memo of the build that built the session; nil masks every time.
func (s *SessionBuild) memo() *redact.Memo {
	if s.Built == nil {
		return nil
	}
	return s.Built.memo
}

// Timeline is the session's timeline as GET /api/analytics/sessions/{id} gives it, laid out
// from the timeline BuildSession built.
func (s *SessionBuild) Timeline() SessionTimeline {
	return SessionTimelineOf(s.Key.SessionID, s.Start, s.TimelineInput(), s.Built.Timeline)
}

// cachedTimeline is the timeline of a session a dataset built: a Claude session's source lines,
// which the dataset does not read, are read now and laid on a copy of its events; the build is
// shared by the requests and stays as it is.
func (s *SessionBuild) cachedTimeline(ctx context.Context, src Source) (SessionTimeline, error) {
	in := s.TimelineInput()
	tl := s.Built.Timeline
	if s.Key.Agent == "claude" {
		lines, err := readClaudeSource(ctx, src, s.Key, s.Events[0].Time)
		if err != nil {
			return SessionTimeline{}, err
		}
		in.Src = lines
		tl.Events = slices.Clone(tl.Events)
		for ref, line := range tl.lines {
			if l, ok := sourceOf(in, ref); ok {
				tl.Events[line-1].SrcLine, tl.Events[line-1].SrcKind = &l.Line, &l.Kind
			}
		}
	}
	return SessionTimelineOf(s.Key.SessionID, s.Start, in, tl), nil
}

// FindingSession is what the cards read of the session; its timeline is the one BuildSession
// built.
func (s *SessionBuild) FindingSession() FindingSession {
	fs := FindingSession{Key: s.Key, Kind: string(s.Kind), Events: s.Events, Calls: s.Calls, Timeline: &s.Built.Timeline}
	if n := len(s.Turns.List); n > 0 {
		last := s.Turns.List[n-1]
		fs.LastTurnOpen, fs.LastTurnStart = last.State == TurnOpen, last.Start
	}
	fs.SSETokens = slices.ContainsFunc(s.BuiltAPI, func(a APIRecord) bool { return a.Src == SrcCodexSSE })
	fs.RolloutTokens = slices.ContainsFunc(s.Turns.API, func(a APIRecord) bool { return a.Src == SrcCodexRollout })
	if s.Built != nil {
		fs.Friction = s.Built.Friction
	}
	return fs
}

// nativeKey is the person and the session id a native record belongs to: two people can send
// one session id, and their records must not mix (HT-425).
type nativeKey struct {
	user uuid.UUID
	sid  string
}

// nativeData are the native records of the read period, laid out by person and session id.
type nativeData struct {
	claude   map[nativeKey][]telemetry.ClaudeEvent
	metrics  map[nativeKey][]telemetry.ClaudeMetric
	sse      map[nativeKey][]telemetry.CodexSSE
	coverage []telemetry.CodexCoverage
	// claudeUsage are the Claude transcript usage lines read for the whole period, by session
	// id; nil when a session's lines are read alone.
	claudeUsage map[string][]claudeUsage
	// codexFacts are the Codex transcript files and facts read for the whole period; nil
	// when a session's are read alone.
	codexFacts *codexPeriodFacts
	// skills are the latest skill snapshots of the sessions of the build, read for them all at
	// once, by session; nil when a session's is read alone.
	skills map[SessionKey]telemetry.SessionSkillSnapshot
}

// Build builds the dataset of f at the instant now (BuildDataset), and the timeline of each
// session it shows (Timelines).
func Build(ctx context.Context, src Source, f Filter, now time.Time) (Dataset, map[SessionKey]SessionTimeline, error) {
	ds, err := BuildDataset(ctx, src, f, now)
	if err != nil {
		return Dataset{}, nil, err
	}
	return ds, Timelines(ds), nil
}

// Timelines are the timelines of the sessions ds shows, as GET /api/analytics/sessions/{id}
// gives them.
func Timelines(ds Dataset) map[SessionKey]SessionTimeline {
	timelines := make(map[SessionKey]SessionTimeline, len(ds.Built))
	for i := range ds.Built {
		timelines[ds.Built[i].Key] = ds.Built[i].Timeline()
	}
	return timelines
}

// BuildDataset builds the dataset of f at the instant now.
// The store is read over [From − historyLookback, To) of the filter's window; a session of the
// window is one with a hook event inside it. The facets are over the sessions of the window; the
// gaps and the cards about the collection over those that pass the agent, project and person of
// the filter, every kind included; the sessions and the pricing over those the filter keeps. An error of the store fails the whole build: a dataset short of a part would
// show wrong numbers.
func BuildDataset(ctx context.Context, src Source, f Filter, now time.Time) (Dataset, error) {
	return buildDataset(ctx, src, f, now, redact.NewMemo(), sessionWorkers())
}

// sessionWorkers is how many sessions of a dataset are built at once: one per processor, at most
// maxSessionWorkers (HT-493).
func sessionWorkers() int {
	return min(runtime.GOMAXPROCS(0), maxSessionWorkers)
}

// buildDataset is BuildDataset, masking each text once through memo, nil masking every time, and
// building workers sessions at once.
func buildDataset(
	ctx context.Context, src Source, f Filter, now time.Time, memo *redact.Memo, workers int,
) (Dataset, error) {
	if err := f.Validate(); err != nil {
		return Dataset{}, err
	}
	p, err := buildPeriod(ctx, src, f.Window(now), now, memo, workers)
	if err != nil {
		return Dataset{}, err
	}
	return p.dataset(f), nil
}

// periodBuild is what the dataset of every filter over one window rests on (HT-527): each session
// of the window built, of every person, agent, project and kind, and what the gaps and the cards
// need of the read. A dataset of a filter is cut from it (dataset), so the costly part is built
// once per window; it keeps the keys of the read, not its events.
type periodBuild struct {
	win Window
	now time.Time
	// sessions are the sessions of the window, oldest first.
	sessions []SessionBuild
	// empty are the empty sessions of the window; dropped counts the dropped events of the window.
	empty   []SessionKey
	dropped map[string]int
	// coverage is the Codex coverage of the read period; claudeGap names the Claude sessions with
	// native events and no hooks, "" when none.
	coverage  []telemetry.CodexCoverage
	claudeGap string
	// workers is how many detectors a dataset cut from the build runs at once (HT-534).
	workers int
	// lastAt is the time of the last kept hook event of each session of the read, empty ones
	// included; droppedAt the times of the dropped events inside win, by the reason; claudeNative
	// the Claude sessions with native events in the read. A shorter window is cut from them (cut).
	lastAt       map[SessionKey]time.Time
	droppedAt    map[string][]time.Time
	claudeNative map[nativeKey]claudeNativeTimes
}

// claudeNativeTimes are what claudeWithoutHooksGap needs of one Claude session's native events:
// the time of its last one and those of its api_request events.
type claudeNativeTimes struct {
	last     time.Time
	requests []time.Time
}

// buildPeriod reads the store over [win.From − historyLookback, win.To) and builds every session
// of win.
func buildPeriod(
	ctx context.Context, src Source, win Window, now time.Time, memo *redact.Memo, workers int,
) (*periodBuild, error) {
	period := telemetry.Filter{From: win.From.Add(-historyLookback), To: win.To}
	// The transcripts of the period are read beside the hook events and the native records, each
	// a query of its own that ClickHouse answers at once: in turn, the reads were most of a cold
	// build (HT-493). The hook events and the native records stay one after the other, so the two
	// never sit in memory together (HT-525); the transcripts come back reduced and small.
	var (
		all         Grouped
		inWindow    map[SessionKey]bool
		dropped     map[string]int
		lastAt      map[SessionKey]time.Time
		droppedAt   map[string][]time.Time
		native      nativeData
		claudeUsage map[string][]claudeUsage
		codexFacts  *codexPeriodFacts
	)
	err := inParallel(ctx,
		func(ctx context.Context) error {
			var err error
			if all, inWindow, dropped, lastAt, droppedAt, err = readHooks(ctx, src, period, win); err != nil {
				return err
			}
			native, err = readNative(ctx, src, period)
			return err
		},
		func(ctx context.Context) error {
			var err error
			claudeUsage, err = readClaudeUsage(ctx, src, period)
			return err
		},
		func(ctx context.Context) error {
			var err error
			codexFacts, err = readCodexFacts(ctx, src, period)
			return err
		},
	)
	if err != nil {
		return nil, err
	}
	native.claudeUsage, native.codexFacts = claudeUsage, codexFacts

	inside := func(k SessionKey) bool { return inWindow[k] }
	var keys []SessionKey
	for key := range all.Sessions {
		if inside(key) {
			keys = append(keys, key)
		}
	}
	slices.SortFunc(keys, func(a, b SessionKey) int {
		if c := all.Sessions[a][0].Time.Compare(all.Sessions[b][0].Time); c != 0 {
			return c
		}
		return strings.Compare(a.SessionID, b.SessionID)
	})
	empty := slices.DeleteFunc(slices.Clone(all.Empty), func(k SessionKey) bool { return !inside(k) })
	if native.skills, err = readSkillSnapshots(ctx, src, period, keys); err != nil {
		return nil, err
	}

	sessions, err := buildSessions(ctx, src, keys, all.Sessions, native, memo, workers)
	if err != nil {
		return nil, err
	}
	hooked := append(slices.Collect(maps.Keys(all.Sessions)), all.Empty...)
	claudeGap, _ := claudeWithoutHooksGap(hooked, native.claude)
	claudeNative := make(map[nativeKey]claudeNativeTimes, len(native.claude))
	for k, evs := range native.claude {
		var t claudeNativeTimes
		for _, e := range evs {
			if e.Time.After(t.last) {
				t.last = e.Time
			}
			if e.Event == "api_request" {
				t.requests = append(t.requests, e.Time)
			}
		}
		claudeNative[k] = t
	}
	return &periodBuild{
		win: win, now: now, sessions: sessions, empty: empty, dropped: dropped,
		coverage: native.coverage, claudeGap: claudeGap, workers: workers,
		lastAt: lastAt, droppedAt: droppedAt, claudeNative: claudeNative,
	}, nil
}

// cut is the build of the shorter window win, which ends where p's does, cut from p (HT-536):
// the sessions with a kept hook event inside win, in p's order, and the empty ones, the dropped
// events and the Claude sessions without hooks of win and of its read. The Codex coverage of its
// read is read anew, a count. A session that began before win's read keeps the events p read of
// it before that.
func (p *periodBuild) cut(ctx context.Context, src Source, win Window) (*periodBuild, error) {
	if win == p.win {
		return p, nil
	}
	readFrom := win.From.Add(-historyLookback)
	inside := func(k SessionKey) bool { return !p.lastAt[k].Before(win.From) }
	c := &periodBuild{win: win, now: p.now, workers: p.workers, dropped: map[string]int{}}
	for i := range p.sessions {
		if inside(p.sessions[i].Key) {
			c.sessions = append(c.sessions, p.sessions[i])
		}
	}
	for _, k := range p.empty {
		if inside(k) {
			c.empty = append(c.empty, k)
		}
	}
	for reason, times := range p.droppedAt {
		for _, t := range times {
			if !t.Before(win.From) {
				c.dropped[reason]++
			}
		}
	}
	claude := map[nativeKey][]telemetry.ClaudeEvent{}
	for k, t := range p.claudeNative {
		if t.last.Before(readFrom) {
			continue
		}
		evs := []telemetry.ClaudeEvent{}
		for _, at := range t.requests {
			if !at.Before(readFrom) {
				evs = append(evs, telemetry.ClaudeEvent{Time: at, Event: "api_request"})
			}
		}
		claude[k] = evs
	}
	var hooked []SessionKey
	for k, at := range p.lastAt {
		if !at.Before(readFrom) {
			hooked = append(hooked, k)
		}
	}
	if gap, ok := claudeWithoutHooksGap(hooked, claude); ok {
		c.claudeGap = gap
	}
	coverage, err := src.CodexCoverage(ctx, telemetry.Filter{From: readFrom, To: win.To})
	if err != nil {
		return nil, fmt.Errorf("read codex coverage: %w", err)
	}
	c.coverage = coverage
	return c, nil
}

// dataset is the dataset of f cut from the build of its window: the facets over every session of
// the window; the gaps and the cards about the collection over those that pass the agent, project
// and person of f, every kind included; the sessions, the aggregates and the pricing over those f
// keeps. It costs no read and no session build.
func (p *periodBuild) dataset(f Filter) Dataset {
	win, sessions := p.win, p.sessions
	ds := Dataset{
		SchemaVersion: SchemaVersion, Variant: VariantLive, GeneratedAt: p.now.UTC(), Window: win,
		Filter: f.withDefaults(), Facets: buildFacets(sessions),
		Done: []map[string]any{}, ChecksCatalog: []map[string]any{}, Sessions: []Session{},
	}
	var scope []SessionBuild
	var findingSessions []FindingSession
	for i := range sessions {
		if f.inScope(&sessions[i]) {
			scope = append(scope, sessions[i])
			findingSessions = append(findingSessions, sessions[i].FindingSession())
		}
	}
	var apis [][]APIRecord
	var shown []*BuiltSession
	for i := range scope {
		s := &scope[i]
		if !f.keeps(s) {
			continue
		}
		ds.Built = append(ds.Built, *s)
		ds.Sessions = append(ds.Sessions, s.Built.Session)
		shown = append(shown, s.Built)
		apis = append(apis, s.Turns.API)
	}
	ds.Pricing = BuildPricing(apis...)
	ds.Tools, ds.MCP = AggregateTools(shown), AggregateMCP(shown)
	ds.Commands, ds.Permissions = AggregateCommands(shown), AggregatePermissions(shown)
	ds.Skills = AggregateSkills(shown)
	ds.Friction = AggregateFriction(shown)
	var d16 []D16Session
	for i := range scope {
		if scope[i].Kind != SessionKindSystem {
			d16 = append(d16, scope[i].D16Session())
		}
	}
	ds.Findings = BuildFindings(FindingsInput{
		Sessions: findingSessions, Coverage: p.coverage, Now: win.To, D16: d16, Workers: p.workers,
	})
	claudeGap := p.claudeGap
	if f.Agent == "codex" {
		claudeGap = ""
	}
	ds.Gaps = datasetGaps(scope, p.dropped, p.empty, claudeGap)
	if line, ok := SkillsGap(ds.Skills); ok {
		ds.Gaps = dedupe(append(ds.Gaps, line))
	}
	signals := 0
	for _, r := range ds.Friction {
		if r.Count != nil {
			signals += *r.Count
		}
	}
	ds.Summary = BuildSummary(ds.Built, ds.Findings, signals, win)
	return ds
}

// readHooks reads the hook events of the period and lays them out by session (GroupSessions),
// with the sessions of win and what it drops (windowKeys), in one pass over the stream of the
// read (streamHooks), so the period's events never sit in a slice beside their layout (HT-533).
// The layout alone is held while the native records are read (HT-525).
func readHooks(
	ctx context.Context, src Source, period telemetry.Filter, win Window,
) (Grouped, map[SessionKey]bool, map[string]int, map[SessionKey]time.Time, map[string][]time.Time, error) {
	keys, dropped := map[SessionKey]bool{}, map[string]int{}
	lastAt, droppedAt := map[SessionKey]time.Time{}, map[string][]time.Time{}
	g, err := streamHooks(ctx, src, period, func(ev *telemetry.HookEvent) {
		windowKey(ev, win, keys, dropped)
		if reason := dropReason(*ev); reason != "" {
			if !ev.Time.Before(win.From) && ev.Time.Before(win.To) {
				droppedAt[reason] = append(droppedAt[reason], ev.Time)
			}
			return
		}
		k := SessionKey{UserID: ev.UserID, Agent: ev.Agent, SessionID: ev.SessionID}
		if ev.Time.After(lastAt[k]) || lastAt[k].IsZero() {
			lastAt[k] = ev.Time
		}
	})
	if err != nil {
		return Grouped{}, nil, nil, nil, nil, err
	}
	return g, keys, dropped, lastAt, droppedAt, nil
}

// streamHooks lays out the hook events of the period as GroupSessions does, as HookEventSeq
// yields them, each event seen by each first: a slice of the whole read beside the layout was the
// period's events held twice (HT-533).
func streamHooks(
	ctx context.Context, src Source, period telemetry.Filter, each func(*telemetry.HookEvent),
) (Grouped, error) {
	var l layout
	for ev, err := range src.HookEventSeq(ctx, period) {
		if err != nil {
			return Grouped{}, fmt.Errorf("read hook events: %w", err)
		}
		each(&ev)
		l.add(ev)
	}
	return l.grouped(), nil
}

// windowKey adds ev to the sessions with a hook event inside win, keys, or to the events inside
// win that GroupSessions drops, counted by the reason, dropped: what grouping the events of the
// window would give, without a copy of them.
func windowKey(ev *telemetry.HookEvent, win Window, keys map[SessionKey]bool, dropped map[string]int) {
	if ev.Time.Before(win.From) || !ev.Time.Before(win.To) {
		return
	}
	if reason := dropReason(*ev); reason != "" {
		dropped[reason]++
		return
	}
	keys[SessionKey{UserID: ev.UserID, Agent: ev.Agent, SessionID: ev.SessionID}] = true
}

// buildSessions builds the sessions of keys, workers at once, each into its place, so the order
// of keys stays. The first error cancels the context of the others: the sessions not yet begun do
// not begin, the reads in flight end on the cancellation, and that first error is the result.
func buildSessions(
	ctx context.Context, src Source, keys []SessionKey, events map[SessionKey][]telemetry.HookEvent, nd nativeData,
	memo *redact.Memo, workers int,
) ([]SessionBuild, error) {
	sessions := make([]SessionBuild, len(keys))
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	slots := make(chan struct{}, max(workers, 1))
	var wg sync.WaitGroup
	for i, key := range keys {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Go(func() {
			defer func() { <-slots }()
			s, err := buildSession(ctx, src, key, events[key], nd, false, memo)
			if err != nil {
				cancel(err)
				return
			}
			sessions[i] = s
		})
	}
	wg.Wait()
	if err := context.Cause(ctx); err != nil {
		return nil, err //nolint:wrapcheck // buildSession's errors carry their context; else the caller's own
	}
	return sessions, nil
}

// inParallel runs every one of fns at once and waits for them all. The first error cancels the
// context of the others and is the result.
func inParallel(ctx context.Context, fns ...func(context.Context) error) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var wg sync.WaitGroup
	for _, fn := range fns {
		wg.Go(func() {
			if err := fn(ctx); err != nil {
				cancel(err)
			}
		})
	}
	wg.Wait()
	return context.Cause(ctx) //nolint:wrapcheck // the errors of fns carry their context; else the caller's own
}

// readNative reads the native records of the period.
func readNative(ctx context.Context, src Source, period telemetry.Filter) (nativeData, error) {
	nd := nativeData{
		claude: map[nativeKey][]telemetry.ClaudeEvent{}, metrics: map[nativeKey][]telemetry.ClaudeMetric{},
		sse: map[nativeKey][]telemetry.CodexSSE{},
	}
	claude, err := src.ClaudeEvents(ctx, period)
	if err != nil {
		return nd, fmt.Errorf("read claude events: %w", err)
	}
	for _, e := range claude {
		k := nativeKey{e.UserID, e.SessionID}
		nd.claude[k] = append(nd.claude[k], e)
	}
	metrics, err := src.ClaudeMetrics(ctx, period)
	if err != nil {
		return nd, fmt.Errorf("read claude metrics: %w", err)
	}
	for _, m := range metrics {
		k := nativeKey{m.UserID, m.SessionID}
		nd.metrics[k] = append(nd.metrics[k], m)
	}
	sse, err := src.CodexSSE(ctx, period)
	if err != nil {
		return nd, fmt.Errorf("read codex sse: %w", err)
	}
	for _, e := range sse {
		k := nativeKey{e.UserID, e.SessionID}
		nd.sse[k] = append(nd.sse[k], e)
	}
	if nd.coverage, err = src.CodexCoverage(ctx, period); err != nil {
		return nd, fmt.Errorf("read codex coverage: %w", err)
	}
	return nd, nil
}

// buildSession builds one session from its hook events, its native records and its transcript:
// BuildSession over what the stores hold of it, and the parts the dataset reads beside.
func buildSession(
	ctx context.Context, src Source, key SessionKey, events []telemetry.HookEvent, nd nativeData, claudeSrc bool,
	memo *redact.Memo,
) (SessionBuild, error) {
	in := SessionInput{Key: key, Events: events, memo: memo}
	if key.Agent == "claude" {
		nk := nativeKey{key.UserID, key.SessionID}
		in.Claude, in.Metrics = nd.claude[nk], nd.metrics[nk]
		// The events are oldest first; the transcript is read from the first on.
		fromTranscript, err := sessionTranscriptAPI(ctx, src, key, in.Claude, events[0].Time, nd.claudeUsage)
		if err != nil {
			return SessionBuild{}, err
		}
		in.TranscriptAPI = fromTranscript
		// The source lines of Claude cost a read of their own, so only the timeline reads them.
		if claudeSrc {
			if in.Src, err = readClaudeSource(ctx, src, key, events[0].Time); err != nil {
				return SessionBuild{}, err
			}
		}
	} else {
		in.SSE = nd.sse[nativeKey{key.UserID, key.SessionID}]
		facts, lines, err := sessionCodexFacts(ctx, src, key, nd.codexFacts)
		if err != nil {
			return SessionBuild{}, err
		}
		in.Facts, in.Src = facts, lines
	}
	snap, ok, err := sessionSkillSnapshot(ctx, src, key, nd.skills)
	if err != nil {
		return SessionBuild{}, err
	}
	if ok {
		in.Snapshot = &snap
	}
	b := BuildSession(in)
	s := SessionBuild{
		Key: key, Events: events, Claude: in.Claude, Metrics: in.Metrics, SSE: in.SSE, Facts: in.Facts,
		Calls: b.Calls.Calls, DupPre: b.Calls.DupPre, Prompts: b.Prompts, Turns: b.Turns, Waits: b.Waits,
		Partial: b.Partial, Kind: b.Session.Kind, Cwd: b.Cwd, Project: b.Session.Project, Skills: b.Skills,
		Compactions: b.Compactions, Start: b.Start, Built: b, Src: in.Src,
	}
	if b.Session.KindReason != nil {
		s.KindReason = *b.Session.KindReason
	}
	s.BuiltAPI = BuildAPI(key.Agent, events, in.Claude, in.SSE, in.Facts)
	if len(in.TranscriptAPI) > 0 {
		s.BuiltAPI = append(s.BuiltAPI, in.TranscriptAPI...)
		slices.SortStableFunc(s.BuiltAPI, func(a, c APIRecord) int { return a.At.Compare(c.At) })
	}
	return s, nil
}

// readSkillSnapshots reads the latest skill snapshot of each session of keys in at most two reads
// (HT-461). The first reads every snapshot stored in the period from TranscriptSlack before it:
// a snapshot is sent when a session starts. A session it has none of may be older than the
// lookback, or resumed with its newer snapshot lost, so the second read takes the snapshots of
// all those sessions by id with no period, as the read of one session does.
func readSkillSnapshots(
	ctx context.Context, src Source, period telemetry.Filter, keys []SessionKey,
) (map[SessionKey]telemetry.SessionSkillSnapshot, error) {
	snaps, err := src.SkillSnapshotsInPeriod(ctx, telemetry.Filter{
		UserID: period.UserID, From: period.From.Add(-TranscriptSlack), To: period.To,
	})
	if err != nil {
		return nil, fmt.Errorf("read the skill snapshots: %w", err)
	}
	out := make(map[SessionKey]telemetry.SessionSkillSnapshot, len(keys))
	for _, s := range snaps {
		out[SessionKey{UserID: s.UserID, Agent: s.Agent, SessionID: s.SessionID}] = s
	}
	unsettled := map[SessionKey]bool{}
	var ids []string
	for _, k := range keys {
		if _, ok := out[k]; ok {
			continue
		}
		unsettled[k] = true
		if !slices.Contains(ids, k.SessionID) {
			ids = append(ids, k.SessionID)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	older, err := src.SkillSnapshotsOfSessions(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read the skill snapshots: %w", err)
	}
	for _, s := range older {
		if k := (SessionKey{UserID: s.UserID, Agent: s.Agent, SessionID: s.SessionID}); unsettled[k] {
			out[k] = s
		}
	}
	return out, nil
}

// sessionSkillSnapshot is the latest skill snapshot of the session key from snaps, the snapshots
// read for the sessions of the build, or, when snaps is nil, from the session's own read; false
// when there is none. A damaged snapshot fails the session that has it, as its own read does.
func sessionSkillSnapshot(
	ctx context.Context, src Source, key SessionKey, snaps map[SessionKey]telemetry.SessionSkillSnapshot,
) (telemetry.SkillSnapshot, bool, error) {
	if snaps != nil {
		s, ok := snaps[key]
		if ok && s.Err != nil {
			return telemetry.SkillSnapshot{}, false, fmt.Errorf("read the skill snapshot: %w", s.Err)
		}
		return s.Snapshot, ok, nil
	}
	snap, ok, err := src.SkillSnapshot(ctx, key.UserID, key.Agent, key.SessionID)
	if err != nil {
		return telemetry.SkillSnapshot{}, false, fmt.Errorf("read the skill snapshot: %w", err)
	}
	return snap, ok, nil
}

// codexFacts reads the facts of a Codex session's main rollout, and the lines they are read from:
// of several main files, which the store holds only when a rollout's name changed, the one with
// the most lines. A session without a rollout has zero facts, which answer no_transcript.
func codexFacts(ctx context.Context, src Source, key SessionKey) (telemetry.CodexFacts, telemetry.Lines, error) {
	files, err := src.TranscriptFiles(ctx, key.UserID, key.Agent, key.SessionID)
	if err != nil {
		return telemetry.CodexFacts{}, nil, fmt.Errorf("list codex transcript files: %w", err)
	}
	main, ok := mainRollout(files)
	if !ok {
		return telemetry.CodexFacts{}, nil, nil
	}
	lines, err := src.CodexFactLines(ctx, telemetry.TranscriptKey{
		UserID: main.UserID, Agent: key.Agent, SessionID: key.SessionID, File: main.Name,
	})
	if err != nil {
		return telemetry.CodexFacts{}, nil, fmt.Errorf("read codex fact lines: %w", err)
	}
	return telemetry.NewCodexFacts(lines), lines, nil
}

// datasetGaps are the gaps of the dataset: the general lines, what was dropped, claudeGap (the
// Claude sessions with native events and no hooks, "" for none), and per session of built what
// its data lacks; a line that repeats is kept once, in its first place.
func datasetGaps(built []SessionBuild, dropped map[string]int, empty []SessionKey, claudeGap string) []string {
	gaps := slices.Clone(generalGaps)
	if len(dropped) > 0 || len(empty) > 0 {
		var parts []string
		for _, reason := range []string{DropNoAgent, DropNoSessionID, DropHottellCheck, DropNoTime} {
			if n := dropped[reason]; n > 0 {
				parts = append(parts, reason+": "+strconv.Itoa(n)+" событий")
			}
		}
		if len(empty) > 0 {
			var ids []string
			for _, k := range empty[:min(len(empty), emptyListMax)] {
				ids = append(ids, ShortID(k.SessionID))
			}
			parts = append(parts, strconv.Itoa(len(empty))+" сессий без реплик и вызовов ("+strings.Join(ids, ", ")+")")
		}
		gaps = append(gaps, "Отброшено: "+strings.Join(parts, "; ")+".")
	}
	if claudeGap != "" {
		gaps = append(gaps, claudeGap)
	}
	for i := range built {
		gaps = append(gaps, sessionGaps(&built[i])...)
	}
	return dedupe(gaps)
}

// claudeWithoutHooksGap names the Claude session ids that have native events and no hook event;
// keys are the sessions with a hook event in the whole read period: one a filter hides, or one
// before the window read through the lookback, still has its hooks.
func claudeWithoutHooksGap(keys []SessionKey, claude map[nativeKey][]telemetry.ClaudeEvent) (string, bool) {
	hooked := map[nativeKey]bool{}
	for _, k := range keys {
		if k.Agent == "claude" {
			hooked[nativeKey{k.UserID, k.SessionID}] = true
		}
	}
	sessions, requests := 0, 0
	for k, evs := range claude {
		if k.sid == "" || hooked[k] {
			continue
		}
		sessions++
		for _, e := range evs {
			if e.Event == "api_request" {
				requests++
			}
		}
	}
	if sessions == 0 {
		return "", false
	}
	return fmt.Sprintf("Claude Code OTel: %d session.id без событий хуков (%d api_request) — не показаны.",
		sessions, requests), true
}

// sessionGaps are the lines of the gaps one session adds.
func sessionGaps(s *SessionBuild) []string {
	short := ShortID(s.Key.SessionID)
	var gaps []string
	if line, ok := PartialGap(s.Key.SessionID, s.Partial); ok {
		gaps = append(gaps, line)
	}
	unknown, done := 0, 0
	for _, c := range s.Calls {
		switch c.State {
		case StateUnknown:
			unknown++
		case StateDone:
			done++
		}
	}
	if unknown > 0 {
		gaps = append(gaps, fmt.Sprintf("%s: %d из %d вызовов без PostToolUse — результат и длительность неизвестны.",
			short, unknown, len(s.Calls)))
	}
	if done > 0 {
		gaps = append(gaps, fmt.Sprintf("%s: у %d вызовов результат записан без признака успеха/ошибки (Bash у Codex и т. п.).",
			short, done))
	}
	if s.DupPre > 0 {
		gaps = append(gaps, fmt.Sprintf("%s: %d повторных PreToolUse с тем же tool_use_id отброшены.", short, s.DupPre))
	}
	if s.Key.Agent == "codex" && OTelSource(s.Key.Agent, s.BuiltAPI, nil) == "linked" {
		sse := 0
		for _, a := range s.BuiltAPI {
			if a.Src == SrcCodexSSE {
				sse++
			}
		}
		gaps = append(gaps, fmt.Sprintf("%s: токены — из %d событий codex.sse_event с conversation.id; полнота не "+
			"проверяется, стоимость — по условной цене API.", short, sse))
	}
	if line, ok := ClaudeTranscriptGap(s.Turns.API); ok {
		gaps = append(gaps, line)
	}
	open := 0
	for _, t := range s.Turns.List {
		if t.State == TurnOpen {
			open++
		}
	}
	if open > 0 {
		gaps = append(gaps, fmt.Sprintf("%s: %d ход(ов) без Stop — длительность до их последнего события.", short, open))
	}
	return gaps
}

// dedupe keeps the first of each line, in order.
func dedupe(lines []string) []string {
	seen := make(map[string]bool, len(lines))
	out := lines[:0:0]
	for _, l := range lines {
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}
