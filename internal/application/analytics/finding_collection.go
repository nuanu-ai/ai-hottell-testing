package analytics

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// FindingSession is what the detector and collection-health cards read of one session.
type FindingSession struct {
	Key SessionKey
	// Kind is the session's kind (system, automation, user); a card about the work skips a system
	// session.
	Kind string
	// Events are the session's hook events, oldest first, as GroupSessions lays them out; an
	// evidence line is an event's place among them, from 1.
	Events []telemetry.HookEvent
	// Calls are the session's tool calls, BuildCalls's, with ApplyCodexFacts applied to a Codex one.
	Calls []Call
	// LastTurnOpen tells whether nothing closed the session's last turn; LastTurnStart is when it
	// began.
	LastTurnOpen  bool
	LastTurnStart time.Time
	// SSETokens tells whether a codex.sse_event gave the session tokens; RolloutTokens whether the
	// rollout gave a turn of it its tokens.
	SSETokens     bool
	RolloutTokens bool
	// Friction is the session's friction episodes by signal key, which the cards about the work read.
	Friction Friction
	// Timeline is the session's timeline, laid out over these Events and Calls; an evidence line
	// is a line of it, as the dashboard contract promises. Without one the cards lay out a
	// timeline of the events, prompts, calls and compactions themselves.
	Timeline *Timeline
}

// timeline is the session's timeline: Timeline, or else one laid out of what the session holds.
func (s *FindingSession) timeline() *Timeline {
	if s.Timeline == nil {
		tl := BuildTimeline(TimelineInput{
			Events: s.Events, Prompts: BuildPrompts(s.Events), Calls: s.Calls, Compactions: Compactions(s.Events),
		})
		s.Timeline = &tl
	}
	return s.Timeline
}

// callLine is the timeline line of the call c of the session, found by its tool_use_id and
// time; 0 when the session has no such call.
func (s *FindingSession) callLine(c *Call) int {
	i := slices.IndexFunc(s.Calls, func(x Call) bool { return x.ToolUseID == c.ToolUseID && x.At.Equal(c.At) })
	if i < 0 {
		return 0
	}
	return s.timeline().Line(RefCall, i)
}

// compactLine is the timeline line of the compaction k of the session; 0 when it is none of
// Compactions(Events).
func (s *FindingSession) compactLine(k telemetry.HookEvent) int {
	i := slices.IndexFunc(Compactions(s.Events), func(x telemetry.HookEvent) bool {
		return x.Time.Equal(k.Time) && x.Event == k.Event
	})
	if i < 0 {
		return 0
	}
	return s.timeline().Line(RefCompaction, i)
}

// The colleague's thresholds of the collection cards.
const (
	// resultGraceS: a call younger than this at build time may still get its result.
	resultGraceS = 120
	// liveSessionS: while the last event of a session is younger than this and its last turn is
	// open, the calls of that turn may still run.
	liveSessionS = 3600
	// lostMin and lostShare: the card on lost PostToolUse needs as many lost calls and as large a
	// share of the counted ones.
	lostMin   = 3
	lostShare = 0.05
)

// CollectionFindings are the cards about the health of collection, as the colleague's
// collection_findings, over the sessions and the coverage of Codex's native OTel, at now. They
// read every session, whatever its kind.
func CollectionFindings(sessions []FindingSession, coverage []telemetry.CodexCoverage, now time.Time) []Finding {
	var out []Finding
	for _, f := range []*Finding{
		postLostFinding(sessions, now), otelFinding(sessions, coverage), bashExitFinding(sessions),
	} {
		if f != nil {
			out = append(out, *f)
		}
	}
	return out
}

// otelEvidenceMax is how many sessions the OTel and Bash cards show as evidence, the newest.
const otelEvidenceMax = 5

// bashExitMin is how many Bash results without an exit code the Bash card needs.
const bashExitMin = 3

// noExitCode tells whether a Codex Bash result has no exit code: its outcome is done, and the
// session's rollout, which gives the code by call_id, is missing or holds no line of the call.
func noExitCode(c *Call) bool {
	return c.State == StateDone && c.Enrich != telemetry.EnrichFound && c.Enrich != telemetry.EnrichPartial
}

// bashExitFinding is the card «Получать код выхода Bash Codex из транскрипта», part (c) of the
// colleague's collection_findings narrowed to our collection: the hook's tool_response of a Codex
// Bash carries no exit code, the session's rollout gives it, so a result lacks it only where the
// rollout is missing, as when the person's settings keep transcripts from being sent, or holds no
// line of the call. It needs bashExitMin such results; its evidence is each session's last one.
func bashExitFinding(sessions []FindingSession) *Finding {
	var bash int
	var per tally
	var ev []Evidence
	for i := range sessions {
		s := &sessions[i]
		if s.Key.Agent != "codex" {
			continue
		}
		var last *Call
		for j := range s.Calls {
			c := &s.Calls[j]
			if c.Tool != "Bash" || !c.HasPost() {
				continue
			}
			bash++
			if noExitCode(c) {
				per.add(s.Key.SessionID)
				last = c
			}
		}
		if last != nil {
			ev = append(ev, Evidence{
				SID: s.Key.SessionID, Line: s.callLine(last), At: ISO(last.At),
				Text: "PostToolUse Bash без кода выхода, в транскрипте сессии записи вызова нет",
			})
		}
	}
	n := 0
	for _, k := range per.keys {
		n += per.n[k]
	}
	if n < bashExitMin {
		return nil
	}
	what := fmt.Sprintf("У %d из %d результатов Bash в сессиях Codex нет кода выхода: в хуке PostToolUse его нет, "+
		"а транскрипта сессии нет или в нём нет записи вызова (например, отправка транскриптов Codex выключена в "+
		"настройках hottell). Ошибки этих команд поэтому не попадают в errors, retry и thrash; такие вызовы считаются "+
		"выполненными с неизвестным исходом.", n, bash)
	f := NewFinding("codex-bash-exit", SevWarn, "Получать код выхода Bash Codex из транскрипта", what,
		Newest(ev, otelEvidenceMax, evidenceAt), per.sortedKeys())
	f.Impact = &Impact{Value: fmt.Sprintf("%d результатов", n), Label: "без кода выхода"}
	f.ImpactBySession = per.bySession()
	f.ImpactUnit = "результатов"
	f.Where = "настройки hottell: источник transcripts для Codex"
	f.Snip = "Диагностика: проверить в настройках hottell, что источник transcripts для Codex включён, и что строки " +
		"rollout сессии дошли до сервера; код выхода сервер берёт из транскрипта по call_id."
	f.Readiness = ReadinessNeedsSpec
	f.Scope = ScopeCollection
	f.AlternativeCauses = []string{
		"Отправка транскриптов Codex выключена в настройках hottell.",
		"Транскрипт сессии ещё не дошёл до сервера или в нём нет записи вызова.",
	}
	verification := "У результатов Bash в новых сессиях Codex виден код выхода."
	f.Verification = &verification
	return &f
}

// otelFinding is the card «Связать нативный OTel Codex с сессиями», part (b) of the colleague's
// collection_findings with his fixes "count a session once" and "the time is the session's last
// event": the Codex sessions without tokens tied to them, neither from a codex.sse_event nor from
// the rollout, while Codex's native OTel sent events. Its text counts those events with a
// conversation.id and without, and codex.api_request by endpoint.
func otelFinding(sessions []FindingSession, coverage []telemetry.CodexCoverage) *Finding {
	var apiTotal, apiLinked, linkedEvents, allEvents uint64
	var endpoints tally
	for _, r := range coverage {
		allEvents += r.Events
		if r.Linked {
			linkedEvents += r.Events
		}
		if r.Event != "codex.api_request" {
			continue
		}
		apiTotal += r.Events
		if r.Linked {
			apiLinked += r.Events
		}
		ep := r.Endpoint
		if ep == "" {
			ep = "—"
		}
		endpoints.addN(Clean(ep, 80), int(r.Events)) //nolint:gosec // an event count fits an int
	}
	var all, linked, covered int
	var missing []*FindingSession
	for i := range sessions {
		s := &sessions[i]
		if s.Key.Agent != "codex" {
			continue
		}
		all++
		if s.SSETokens {
			linked++
		}
		switch {
		case s.RolloutTokens:
			covered++
		case !s.SSETokens:
			missing = append(missing, s)
		}
	}
	if len(missing) == 0 || allEvents == 0 {
		return nil
	}
	ep := endpoints.top(3)
	if ep == "" {
		ep = "—"
	}
	what := fmt.Sprintf("Нативный OTel Codex: %d событий, из них с conversation.id — %d. "+
		"codex.api_request — %d (с conversation.id — %d; endpoint: %s), токенов в них нет. "+
		"Токены с привязкой к сессии (codex.sse_event response.completed) есть только у %d из %d сессий Codex",
		allEvents, linkedEvents, apiTotal, apiLinked, ep, linked, all)
	if covered > 0 {
		what += fmt.Sprintf("; ещё у %d токены за ход взяты из журнала сессии (rollout)", covered)
	}
	what += fmt.Sprintf(". У остальных %d токены, число ответов модели и стоимость здесь null.", len(missing))
	ev := make([]Evidence, 0, len(missing))
	sids := make([]string, 0, len(missing))
	bySID := make(map[string]float64, len(missing))
	for _, s := range missing {
		sid := s.Key.SessionID
		sids = append(sids, sid)
		bySID[sid] = 1
		e := Evidence{SID: sid, Line: 1, Text: "у этой сессии в OTel нет токенов с conversation.id"}
		if tl := s.timeline(); len(tl.Events) > 0 {
			last := tl.Events[len(tl.Events)-1]
			e.Line, e.At = last.Line, last.At
		} else if last, ok := lastEvent(s); ok {
			e.At = ISO(last.Time)
		}
		ev = append(ev, e)
	}
	f := NewFinding("codex-otel-unlinked", SevWarn, "Связать нативный OTel Codex с сессиями", what,
		Newest(ev, otelEvidenceMax, evidenceAt), sids)
	f.Impact = &Impact{Value: fmt.Sprintf("%d сессий", len(missing)), Label: "без токенов и стоимости"}
	f.ImpactBySession = bySID
	f.ImpactUnit = "сессий"
	f.Where = "~/.codex/config.toml → [otel], [otel.exporter.otlp-http]; что hottell ждёт от нативного OTel Codex — " +
		"docs/specs/hottell-contract/native-otel.md"
	f.Snip = "Диагностика: в одной короткой сессии Codex Desktop проверить, какие события codex.* приходят с " +
		"conversation.id и есть ли codex.sse_event с kind=response.completed от codex-app-server; сравнить с " +
		"контрактом native-otel.md и документацией Codex по OTel."
	f.Readiness = ReadinessNeedsSpec
	f.Scope = ScopeCollection
	f.AlternativeCauses = []string{
		"codex-app-server (десктоп) не отправляет в OTel события с токенами ответа модели.",
		"События с токенами приходят без conversation.id.",
		"Экспорт логов включён только у части процессов Codex.",
	}
	verification := "У новой сессии Codex есть события с её conversation.id и токенами ответа."
	f.Verification = &verification
	return &f
}

// startedUnderHooks tells whether a SessionStart of source startup or resume came at or before the
// session's first call, the colleague's _started_under_hooks: before it the hooks were not
// installed and a missing PostToolUse says nothing.
func startedUnderHooks(s *FindingSession) bool {
	for _, ev := range s.Events {
		if ev.Event != "SessionStart" || (ev.Source != "startup" && ev.Source != "resume") {
			continue
		}
		if len(s.Calls) == 0 || !ev.Time.After(s.Calls[0].At) {
			return true
		}
	}
	return false
}

// lastEvent is the session's last hook event; false without one.
func lastEvent(s *FindingSession) (telemetry.HookEvent, bool) {
	if len(s.Events) == 0 {
		return telemetry.HookEvent{}, false
	}
	return s.Events[len(s.Events)-1], true
}

// tally counts keys and keeps the order they first came in, as Python's Counter.
type tally struct {
	keys []string
	n    map[string]int
}

func (t *tally) add(k string) { t.addN(k, 1) }

func (t *tally) addN(k string, n int) {
	if t.n == nil {
		t.n = map[string]int{}
	}
	if _, ok := t.n[k]; !ok {
		t.keys = append(t.keys, k)
	}
	t.n[k] += n
}

// top is its k most common keys with their counts, most first, ties in the order they came:
// Counter.most_common.
func (t *tally) top(k int) string {
	keys := slices.Clone(t.keys)
	slices.SortStableFunc(keys, func(a, b string) int { return cmp.Compare(t.n[b], t.n[a]) })
	parts := make([]string, 0, k)
	for _, key := range keys[:min(k, len(keys))] {
		parts = append(parts, fmt.Sprintf("%s — %d", key, t.n[key]))
	}
	return strings.Join(parts, ", ")
}

// bySession is the tally as the impact of a card per session.
func (t *tally) bySession() map[string]float64 {
	out := make(map[string]float64, len(t.n))
	for k, n := range t.n {
		out[k] = float64(n)
	}
	return out
}

// sortedKeys are its keys in order.
func (t *tally) sortedKeys() []string {
	keys := slices.Clone(t.keys)
	slices.Sort(keys)
	return keys
}

// lostCall is a call of a Codex session that no PostToolUse closed; atEnd marks one within
// resultGraceS of the session's last call.
type lostCall struct {
	s     *FindingSession
	c     *Call
	atEnd bool
}

// lostLabel names a lost call: its MCP server, else its tool.
func lostLabel(c *Call) string {
	if c.MCP != "" {
		return Clean("MCP "+c.MCP, 80)
	}
	return Clean(c.Name, 80)
}

// postLostFinding is the card «Codex не присылает PostToolUse», part (a) of the colleague's
// collection_findings with his fixes T7, D and #18: over the Codex sessions started under hooks,
// the calls with a PreToolUse and no result, less those younger than resultGraceS and those of the
// open last turn of a session still live (its last event younger than liveSessionS). It needs
// lostMin of them and lostShare of the counted calls; its evidence is each session's newest lost
// call.
func postLostFinding(sessions []FindingSession, now time.Time) *Finding {
	var (
		lost         []lostCall
		total, codex int
		under        int
	)
	for i := range sessions {
		s := &sessions[i]
		if s.Key.Agent != "codex" || len(s.Calls) == 0 {
			continue
		}
		codex++
		if !startedUnderHooks(s) {
			continue
		}
		under++
		end := s.Calls[len(s.Calls)-1].At
		last, _ := lastEvent(s)
		running := s.LastTurnOpen && now.Sub(last.Time).Seconds() < liveSessionS
		for j := range s.Calls {
			c := &s.Calls[j]
			if c.NoPre || now.Sub(c.At).Seconds() < resultGraceS {
				continue
			}
			if running && !c.At.Before(s.LastTurnStart) {
				continue
			}
			total++
			if c.State == StateUnknown {
				lost = append(lost, lostCall{s: s, c: c, atEnd: end.Sub(c.At).Seconds() < resultGraceS})
			}
		}
	}
	if len(lost) < lostMin || float64(len(lost))/float64(total) < lostShare {
		return nil
	}
	var labels, per tally
	atEnd := 0
	for _, l := range lost {
		labels.add(lostLabel(l.c))
		per.add(l.s.Key.SessionID)
		if l.atEnd {
			atEnd++
		}
	}
	what := fmt.Sprintf("В %d сессиях Codex, начатых при работающих хуках, у %d из %d вызовов нет PostToolUse (%.0f %%), "+
		"из них в последние 2 минуты своей сессии — %d (возможен обрыв хода). Чаще всего: %s. "+
		"Сессии, начатые до установки хуков (%d), не считаются — они в «Ограничениях данных».",
		len(per.keys), len(lost), total, float64(len(lost))/float64(total)*100, atEnd, labels.top(4), codex-under)
	newestFirst := slices.Clone(lost)
	slices.SortStableFunc(newestFirst, func(a, b lostCall) int { return b.c.At.Compare(a.c.At) })
	var ev []Evidence
	seen := map[string]bool{}
	for _, l := range newestFirst {
		sid := l.s.Key.SessionID
		if seen[sid] || len(ev) >= MaxEvidence {
			continue
		}
		seen[sid] = true
		ev = append(ev, Evidence{
			SID: sid, Line: l.s.callLine(l.c), At: ISO(l.c.At), Text: "нет PostToolUse · " + lostLabel(l.c),
		})
	}
	f := NewFinding("codex-post-lost", SevWarn, "Найти, почему Codex не присылает PostToolUse для части вызовов",
		what, ev, per.sortedKeys())
	f.Impact = &Impact{Value: fmt.Sprintf("%d вызовов", len(lost)), Label: "без результата"}
	f.ImpactBySession = per.bySession()
	f.ImpactUnit = "вызовов"
	f.Where = "Codex → хук PostToolUse; ~/.codex/hooks.json — как его пишет установщик hottell " +
		"(internal/hottell/agentconfig/codex/hooks.go)"
	f.Snip = "Диагностика: в новой сессии Codex вызвать инструмент из списка выше и сравнить в otel_logs " +
		"countIf(Body='agent.hook.PreToolUse') и countIf(Body='agent.hook.PostToolUse') по его tool_use_id. " +
		"Post нет — проверить, вызывает ли Codex PostToolUse для такого инструмента. Post есть в debug-логе hottell, " +
		"но нет в ClickHouse — искать потерю в спуле и отправке hottell или на приёме сервера " +
		"(internal/delivery/http/ingest)."
	f.Readiness = ReadinessNeedsSpec
	f.Scope = ScopeCollection
	f.AlternativeCauses = []string{
		"Codex не вызывает PostToolUse для части инструментов (MCP-коннекторы и т. п.).",
		"Вызов оборван: ход прерван или окно закрыто до результата.",
		"Потеря событий в спуле или отправке hottell либо на приёме сервера.",
	}
	verification := "В новых сессиях доля вызовов без PostToolUse — меньше 5 %."
	f.Verification = &verification
	return &f
}
