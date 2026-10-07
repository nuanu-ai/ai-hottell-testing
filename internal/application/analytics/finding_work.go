package analytics

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// The colleague's thresholds of the cards about the work.
const (
	// compactMany is how many compactions make a session a long one; compactCloseMin how close,
	// in minutes, two compactions in a row make it one too.
	compactMany     = 3
	compactCloseMin = 10.0
	// rereadCardMin is how many reread episodes make the card that proposes to check the
	// instructions; rereadCardTop how many files its text names.
	rereadCardMin = 3
	rereadCardTop = 4
	// genericCardMin is how many episodes of a signal make its card «Разобрать эпизоды…».
	genericCardMin = 3
)

// handoffCandidate is the candidate change of the compaction card, the colleague's HANDOFF_CANDIDATE.
const handoffCandidate = "Кандидат правки — только если разбор подтвердит, что после сжатия терялось нужное:\n" +
	"1. Когда задача закончена или было 2–3 сжатия, попросить агента записать состояние в docs/handoff.md: " +
	"что сделано, что дальше, какие файлы и команды.\n" +
	"2. Продолжить в новой сессии с репликой «продолжи по docs/handoff.md»."

// rereadCandidate is the candidate change of the reread card, the colleague's REREAD_CANDIDATE.
const rereadCandidate = "Кандидат правки — только если в AGENTS.md/CLAUDE.md действительно есть требование " +
	"перечитывать этот файл: «файл целиком — один раз за сессию и после сжатия контекста; перед решением — " +
	"только нужный раздел»."

// WorkFindings are the cards about the person's work, the colleague's work_findings: hypotheses,
// each with what to check before the change and how to measure its use. They read the sessions
// that are not system ones. detected are the cards of the structural detectors: a signal one of
// them covers gets no card «Разобрать эпизоды…» of its own (genericCoveredBy).
func WorkFindings(sessions []FindingSession, detected []Finding) []Finding {
	users := make([]FindingSession, 0, len(sessions))
	for _, s := range sessions {
		if s.Kind != "system" {
			users = append(users, s)
		}
	}
	var out []Finding
	for _, f := range []*Finding{compactWorkFinding(users), rereadWorkFinding(users)} {
		if f != nil {
			out = append(out, *f)
		}
	}
	return append(out, genericWorkFindings(users, detected)...)
}

// FindingsInput is what the cards of the dashboard are built from.
type FindingsInput struct {
	// Sessions are all the sessions of the period, system ones too.
	Sessions []FindingSession
	// Coverage is the coverage of Codex's native OTel; Now the build time.
	Coverage []telemetry.CodexCoverage
	Now      time.Time
	// D16 are the waits of the sessions that are not system ones, which D16 reads.
	D16 []D16Session
	// Workers is how many sessions a detector reads at once, and the detectors then run beside
	// each other; below 2 everything runs one after another.
	Workers int
}

// BuildFindings are the cards of the dashboard, the colleague's build_findings with the
// structural detectors: the health of collection over all the sessions, then the cards of the
// detectors D03, D07, D12, D16 and D24 and the cards about the work over the sessions that are
// not system ones.
func BuildFindings(in FindingsInput) []Finding {
	users := make([]FindingSession, 0, len(in.Sessions))
	for _, s := range in.Sessions {
		if s.Kind != "system" {
			users = append(users, s)
		}
	}
	// The detectors read the sessions and write nothing of them, so they run beside each other and
	// D03 and D07, most of their time, over several sessions at once: one after another they were
	// most of the sequential tail of a cold build (HT-534). Each writes only its own result, and
	// the cards are put in their order after all of them end.
	workers := max(in.Workers, 1)
	var (
		collection, d16, d24 []Finding
		d03, d07, d12        *Finding
	)
	inTurn(workers > 1,
		func() { collection = CollectionFindings(in.Sessions, in.Coverage, in.Now) },
		func() { d03 = d03Finding(users, workers) },
		func() { d07 = d07Finding(users, workers) },
		func() { d12 = D12Finding(users) },
		func() { d16 = D16Findings(in.D16) },
		func() { d24 = D24Findings(users) },
	)
	var detected []Finding
	for _, f := range []*Finding{d03, d07, d12} {
		if f != nil {
			detected = append(detected, *f)
		}
	}
	detected = append(detected, d16...)
	detected = append(detected, d24...)
	collection = append(collection, detected...)
	return append(collection, WorkFindings(users, detected)...)
}

// inTurn runs every one of fns and waits for them all: at once when together, else one after
// another.
func inTurn(together bool, fns ...func()) {
	if !together {
		for _, fn := range fns {
			fn()
		}
		return
	}
	var wg sync.WaitGroup
	for _, fn := range fns {
		wg.Go(fn)
	}
	wg.Wait()
}

// detectEach is detect of every one of sessions, in their order, workers sessions at once.
func detectEach(sessions []FindingSession, workers int, detect func(FindingSession) []StructuralEpisode) [][]StructuralEpisode {
	found := make([][]StructuralEpisode, len(sessions))
	next := atomic.Int64{}
	work := func() {
		for i := int(next.Add(1)) - 1; i < len(sessions); i = int(next.Add(1)) - 1 {
			found[i] = detect(sessions[i])
		}
	}
	fns := make([]func(), min(max(workers, 1), max(len(sessions), 1)))
	for k := range fns {
		fns[k] = work
	}
	inTurn(len(fns) > 1, fns...)
	return found
}

// longSession is a session with many or close compactions, and the gaps of its close ones in
// minutes.
type longSession struct {
	s       FindingSession
	compact []timeEvent
	close   []float64
}

// timeEvent is a compaction of a session: its time, trigger and line.
type timeEvent struct {
	at   time.Time
	trig string
	line int
}

// compactWorkFinding is the card (a) on compactions in long sessions, nil without such a
// session: compactMany compactions or more, or two in a row within compactCloseMin minutes. The
// number of compactions alone does not prove that details were lost, so the card is a hypothesis
// whose verification is not «fewer compactions per session», which splitting the work gives.
func compactWorkFinding(users []FindingSession) *Finding {
	var longs []longSession
	for _, s := range users {
		var ks []timeEvent
		for _, k := range Compactions(s.Events) {
			ks = append(ks, timeEvent{at: k.Time, trig: k.Trigger, line: s.compactLine(k)})
		}
		var closeGaps []float64
		for i := 1; i < len(ks); i++ {
			if m := Minutes(ks[i-1].at, ks[i].at); m <= compactCloseMin {
				closeGaps = append(closeGaps, m)
			}
		}
		if len(ks) >= compactMany || len(closeGaps) > 0 {
			longs = append(longs, longSession{s: s, compact: ks, close: closeGaps})
		}
	}
	if len(longs) == 0 {
		return nil
	}
	n := 0
	top := longs[0]
	var (
		ev    []Evidence
		sids  []string
		bySID = map[string]float64{}
	)
	for _, l := range longs {
		n += len(l.compact)
		if len(l.compact) > len(top.compact) {
			top = l
		}
		sid := l.s.Key.SessionID
		sids = append(sids, sid)
		bySID[sid] = float64(len(l.compact))
		for k, c := range l.compact {
			trig := c.trig
			if trig == "" {
				trig = "триггер не указан"
			}
			ev = append(ev, Evidence{
				SID: sid, Line: c.line, At: ISO(c.at),
				Text: fmt.Sprintf("сжатие %d/%d (%s)", k+1, len(l.compact), trig),
			})
		}
	}
	slices.Sort(sids)
	start, end := findingSessionSpan(top.s)
	subs := map[string]bool{}
	for i := range top.s.Calls {
		if a := top.s.Calls[i].AgentID; a != "" {
			subs[a] = true
		}
	}
	what := fmt.Sprintf("Сессий с тремя и более сжатиями контекста или сжатиями подряд — %d, сжатий в них — %d. "+
		"Больше всего у %s: %.1f ч, вызовов — %d, субагентов — %d, сжатий — %d",
		len(longs), n, ShortID(top.s.Key.SessionID), Minutes(start, end)/60, len(top.s.Calls), len(subs), len(top.compact))
	if len(top.close) > 0 {
		what += fmt.Sprintf(", два сжатия с разницей %.0f мин", slices.Min(top.close))
	}
	what += ". Мешало ли это работе, по событиям не видно — нужен разбор эпизодов."
	f := NewFinding("work-long-session", SevWarn, "Проверить, мешает ли сжатие контекста в длинных сессиях", what, ev, sids)
	f.Impact = &Impact{Value: fmt.Sprintf("%d сжатий", n), Label: "в длинных сессиях"}
	f.ImpactBySession = bySID
	f.ImpactUnit = "сжатий"
	f.Where = "Привычка работы с длинными задачами"
	f.Snip = handoffCandidate
	f.Kind = KindHabit
	f.PatternID = FrictionCompact
	f.Episodes = n
	f.Preconditions = []string{
		"Открыть эпизоды после сжатий: перечитывал ли агент уже прочитанное, переспрашивал ли, повторял ли сделанное.",
		"Найти сопоставимые задачи без сжатий и сравнить исход и стоимость.",
	}
	f.AlternativeCauses = []string{
		"Задача одна и большая — сжатия неизбежны и не мешают.",
		"Сжатие вызывают большие ответы инструментов, а не длина работы.",
	}
	verification := "На сопоставимых задачах после правки исход не хуже, стоимость не выше, повторов после сжатия меньше. " +
		"Одно уменьшение числа сжатий пользой не считается: его даёт простое дробление работы."
	f.Verification = &verification
	return &f
}

// findingSessionSpan is the time of the first and the last hook event of a session; zero times without one.
func findingSessionSpan(s FindingSession) (start, end time.Time) {
	if len(s.Events) == 0 {
		return time.Time{}, time.Time{}
	}
	return s.Events[0].Time, s.Events[len(s.Events)-1].Time
}

// sessionEpisode is a friction episode of a session.
type sessionEpisode struct {
	sid string
	ep  FrictionEpisode
}

// episodesOf are the episodes of the signal key in the sessions, in their order.
func episodesOf(users []FindingSession, key string) []sessionEpisode {
	var out []sessionEpisode
	for _, s := range users {
		for _, ep := range s.Friction[key] {
			out = append(out, sessionEpisode{sid: s.Key.SessionID, ep: ep})
		}
	}
	return out
}

// lastEvidence is the evidence of each episode's last event, the newest MaxEvidence of them.
func lastEvidence(occ []sessionEpisode) []Evidence {
	var ev []Evidence
	for _, o := range occ {
		if n := len(o.ep.Evidence); n > 0 {
			ev = append(ev, o.ep.Evidence[n-1])
		}
	}
	return ev
}

// shortPath is a file as the reread card names it, folder/name, so README.md files of different
// folders stay apart, as the colleague's _short_path.
func shortPath(p string) string {
	dir := path.Base(path.Dir(p))
	if dir == "." || dir == "/" || dir == "" {
		return path.Base(p)
	}
	return dir + "/" + path.Base(p)
}

// rereadFile is what the reread card counts of one file: its extra reads and its sessions.
type rereadFile struct {
	path     string
	extra    int
	sessions map[string]bool
}

// rereadWorkFinding is the card (b) on reread files, nil below rereadCardMin episodes: whether
// an instruction makes the agent read files whole again. Its impact is the extra reads; its text
// names the rereadCardTop files with the most of them.
func rereadWorkFinding(users []FindingSession) *Finding {
	rr := episodesOf(users, FrictionReread)
	if len(rr) < rereadCardMin {
		return nil
	}
	var files []*rereadFile
	byPath := map[string]*rereadFile{}
	per := map[string]float64{}
	var sids []string
	var bytes int64
	for _, o := range rr {
		f := byPath[o.ep.Path]
		if f == nil {
			f = &rereadFile{path: o.ep.Path, sessions: map[string]bool{}}
			byPath[o.ep.Path] = f
			files = append(files, f)
		}
		f.extra += o.ep.Extra
		f.sessions[o.sid] = true
		if _, seen := per[o.sid]; !seen {
			sids = append(sids, o.sid)
		}
		per[o.sid] += float64(o.ep.Extra)
		bytes += o.ep.Bytes
	}
	slices.SortStableFunc(files, func(a, b *rereadFile) int { return cmp.Compare(b.extra, a.extra) })
	top := make([]string, 0, rereadCardTop)
	for _, f := range files[:min(rereadCardTop, len(files))] {
		top = append(top, fmt.Sprintf("%s — %d в %d сес.", shortPath(f.path), f.extra, len(f.sessions)))
	}
	extra := 0.0
	for _, v := range per {
		extra += v
	}
	what := fmt.Sprintf("Один агент перечитывал то же место файла и получал тот же ответ, без сжатия и правки между "+
		"чтениями: эпизодов — %d, сессий — %d, лишних чтений — %.0f (≈ %.0f тыс. токенов). Чаще всего: %s.",
		len(rr), len(per), extra, float64(bytes)/4/1000, strings.Join(top, "; "))
	slices.Sort(sids)
	f := NewFinding("work-reread", SevWarn, "Проверить, требует ли инструкция перечитывать файлы целиком", what,
		lastEvidence(rr), sids)
	f.Impact = &Impact{Value: fmt.Sprintf("%.0f чтений", extra), Label: "лишних"}
	f.ImpactBySession = per
	f.ImpactUnit = "чтений"
	f.Where = "AGENTS.md / CLAUDE.md проектов этих сессий"
	f.Snip = rereadCandidate
	f.Kind = KindProjectRule
	f.PatternID = FrictionReread
	f.Episodes = len(rr)
	f.Preconditions = []string{
		"Найти в инструкциях требование перечитывать эти файлы.",
		"Открыть эпизоды: нужен ли был файл целиком или хватило бы раздела.",
	}
	f.AlternativeCauses = []string{
		"Агент забыл содержимое после длинной цепочки вызовов — инструкция ни при чём.",
		"Проверка перед важным шагом — повторное чтение оправдано.",
	}
	verification := "На сопоставимых задачах после правки лишних чтений этих файлов вдвое меньше, исход не хуже."
	f.Verification = &verification
	return &f
}

// genericKeys are the signals that get a card «Разобрать эпизоды…», in its order.
func genericKeys() []string {
	return []string{FrictionRetry, FrictionThrash, FrictionMCPFail, FrictionColdCache, FrictionWait}
}

// genericCoveredBy is the structural detector whose card speaks of the signal, so that one topic
// does not get two cards: D03 of retry, D24 of mcpfail, D16 of wait; "" for none.
func genericCoveredBy(key string) string {
	switch key {
	case FrictionRetry:
		return "D03"
	case FrictionMCPFail:
		return "D24"
	case FrictionWait:
		return "D16"
	}
	return ""
}

// genericWorkFindings are the cards (c) «Разобрать эпизоды…»: one per signal of genericKeys with
// genericCardMin episodes or more, unless a card of detected covers it. Each is a habit
// hypothesis with two preconditions; its evidence is the last event of each episode.
func genericWorkFindings(users []FindingSession, detected []Finding) []Finding {
	covered := map[string]bool{}
	for _, f := range detected {
		covered[f.PatternID] = true
	}
	meta := map[string]FrictionSignal{}
	for _, m := range FrictionMeta() {
		meta[m.Key] = m
	}
	var out []Finding
	for _, key := range genericKeys() {
		occ := episodesOf(users, key)
		if len(occ) < genericCardMin || covered[genericCoveredBy(key)] {
			continue
		}
		m := meta[key]
		per := map[string]float64{}
		var sids []string
		for _, o := range occ {
			if _, seen := per[o.sid]; !seen {
				sids = append(sids, o.sid)
			}
			per[o.sid]++
		}
		slices.Sort(sids)
		what := fmt.Sprintf("Детектор «%s» сработал %d раз в %d сессиях. Правило: %s", m.Name, len(occ), len(per), m.How)
		f := NewFinding("friction-"+key, m.Sev, "Разобрать эпизоды «"+strings.ToLower(m.Name)+"»", what,
			lastEvidence(occ), sids)
		f.Impact = &Impact{Value: fmt.Sprintf("%d эпизодов", len(occ)), Label: "за окно данных"}
		f.ImpactBySession = per
		f.ImpactUnit = "эпизодов"
		f.Snip = "Открыть эпизоды по ссылкам и решить, повторяется ли причина; правку описывать только после разбора."
		f.Preconditions = []string{
			"Открыть эпизоды по ссылкам и проверить, повторяется ли одна и та же причина.",
			"Убедиться, что эпизоды не объясняются неполным сбором данных (блок «Здоровье сбора данных»).",
		}
		f.PatternID = key
		f.Kind = KindHabit
		f.Episodes = len(occ)
		out = append(out, f)
	}
	return out
}
