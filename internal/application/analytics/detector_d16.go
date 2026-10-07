package analytics

import (
	"fmt"
	"slices"
	"strconv"
	"time"
)

// The parameters of D16 in the colleague's analytics/catalogue.yaml.
const (
	// d16MinQuestions is how many turns ending on a question make an episode (a).
	d16MinQuestions = 3
	// d16WaitMin is the shortest stop on such a question, in minutes.
	d16WaitMin = 2.0
	// d16PermissionTotalMin is the permission waiting of a session, in minutes, that makes an
	// episode (b).
	d16PermissionTotalMin = 5.0
	// d16MediumMin is the waiting of a session, in minutes, that raises the card to warn, the
	// catalogue's medium.
	d16MediumMin = 15.0
)

// D16Session is what D16 reads of one session: its question waits (BuildWaits), its permission
// windows (PermissionWaits) and its turns. WaitLines and PermissionLines are the timeline lines of
// Waits and Permissions, by index.
type D16Session struct {
	SID             string
	Waits           []Wait
	Permissions     []Wait
	WaitLines       []int
	PermissionLines []int
	Turns           *Turns
}

// NewD16Session is what D16 reads of the session sid laid out by in, with turns: its question
// waits apart from its permission windows, each with its line on the session's timeline.
func NewD16Session(sid string, in TimelineInput, turns *Turns) D16Session {
	return d16SessionOn(sid, in, turns, BuildTimeline(in))
}

// d16SessionOn is NewD16Session with the lines of tl, the timeline BuildTimeline lays in out.
func d16SessionOn(sid string, in TimelineInput, turns *Turns, tl Timeline) D16Session {
	out := D16Session{SID: sid, Turns: turns}
	for i, w := range in.Waits {
		if w.Call != nil {
			out.Waits = append(out.Waits, w)
			out.WaitLines = append(out.WaitLines, tl.WaitLine(in, i))
		} else {
			out.Permissions = append(out.Permissions, w)
			out.PermissionLines = append(out.PermissionLines, tl.WaitLine(in, i))
		}
	}
	return out
}

// lineOf is lines[i], 0 when lines does not reach i.
func lineOf(lines []int, i int) int {
	if i < len(lines) {
		return lines[i]
	}
	return 0
}

// d16Episode is one flagged episode of a session: the waits it rests on and their minutes.
type d16Episode struct {
	sid   string
	waits []Wait
	lines []int
	min   float64
	label string
}

// d16Questions is episode (a) of a session, or false: d16MinQuestions turns or more stopped on a
// question for d16WaitMin minutes or more. Without a model a turn ends on a question only by a
// question call (BuildWaits); the questions of one turn, those asked in one second among them,
// are one.
func d16Questions(s D16Session) (d16Episode, bool) {
	ep := d16Episode{sid: s.SID, label: "вопрос"}
	var turns []*Turn
	var seconds []time.Time
	for i, w := range s.Waits {
		if w.Call == nil || !w.Stopped || w.Min == nil || *w.Min < d16WaitMin {
			continue
		}
		sec := w.At.Truncate(time.Second)
		if slices.ContainsFunc(seconds, sec.Equal) {
			continue
		}
		seconds = append(seconds, sec)
		ep.waits = append(ep.waits, w)
		ep.lines = append(ep.lines, lineOf(s.WaitLines, i))
		ep.min += *w.Min
		var t *Turn
		if s.Turns != nil {
			t = s.Turns.At(w.At, s.Turns.mainKey(w.Call.PreEvent()))
		}
		if t == nil || !slices.Contains(turns, t) {
			turns = append(turns, t)
		}
	}
	return ep, len(turns) >= d16MinQuestions
}

// d16Permissions is episode (b) of a session, or false: its permission windows of known length
// together d16PermissionTotalMin minutes or more.
func d16Permissions(s D16Session) (d16Episode, bool) {
	ep := d16Episode{sid: s.SID, label: "окно разрешения"}
	for i, w := range s.Permissions {
		if w.Min == nil {
			continue
		}
		ep.waits = append(ep.waits, w)
		ep.lines = append(ep.lines, lineOf(s.PermissionLines, i))
		ep.min += *w.Min
	}
	return ep, ep.min >= d16PermissionTotalMin
}

// D16Findings is the card of D16 «Простой на человеке», or none: the sessions where (a) three
// turns or more stopped two minutes or more on a question to the person, or (b) the permission
// windows took five minutes or more. D16 rests only on measured stops: the colleague's plan
// dropped the question card, which no confirmed stop supported. The card is a habit hypothesis,
// asking the questions together before the work starts; info, the catalogue's low, and warn
// when a session waited d16MediumMin minutes or more. Its impact is the minutes of waiting.
func D16Findings(sessions []D16Session) []Finding {
	var eps []d16Episode
	for _, s := range sessions {
		if ep, ok := d16Questions(s); ok {
			eps = append(eps, ep)
		}
		if ep, ok := d16Permissions(s); ok {
			eps = append(eps, ep)
		}
	}
	if len(eps) == 0 {
		return nil
	}
	return []Finding{d16Finding(eps)}
}

// d16Finding is the card over the flagged episodes.
func d16Finding(eps []d16Episode) Finding {
	var ev []Evidence
	var sids []string
	bySID := map[string]float64{}
	var total float64
	for _, ep := range eps {
		sids = append(sids, ep.sid)
		bySID[ep.sid] = roundTo(bySID[ep.sid]+ep.min, 1)
		total += ep.min
		for k, w := range ep.waits {
			text := ep.label + " · ждал " + strconv.FormatFloat(*w.Min, 'f', 1, 64) + " мин"
			if w.Call != nil {
				text += " · " + ToolSummary(w.Call.Tool, w.Call.Input, waitSummaryMax)
			}
			ev = append(ev, Evidence{SID: ep.sid, At: ISO(w.At), Line: ep.lines[k], Text: Clean(text, frictionTextMax)})
		}
	}
	slices.Sort(sids)
	sids = slices.Compact(sids)
	sev := SevInfo
	for _, m := range bySID {
		if m >= d16MediumMin {
			sev = SevWarn
		}
	}
	what := fmt.Sprintf("Агент стоял, ожидая вас: %s мин в %d сессиях — вопросы по одному в нескольких ходах "+
		"(каждый с ожиданием от %d мин) или решения по разрешениям (от %d мин за сессию).",
		strconv.FormatFloat(roundTo(total, 1), 'f', 1, 64), len(sids), int(d16WaitMin), int(d16PermissionTotalMin))
	f := NewFinding("D16", sev, "Простой на человеке", what, ev, sids)
	f.PatternID = "D16"
	f.Kind = KindHabit
	f.Episodes = len(eps)
	f.Impact = &Impact{Value: strconv.FormatFloat(roundTo(total, 1), 'f', 1, 64) + " мин", Label: "ожидания"}
	f.ImpactBySession = bySID
	f.ImpactUnit = "мин"
	f.Snip = "Кандидат: задавать вопросы пакетом до старта работы."
	f.AlternativeCauses = []string{"Вопросы возникли по ходу работы, и задать их заранее было нельзя."}
	f.Exceptions = []string{"Вопросы, собранные в одном сообщении.", "Пауза после хода без вопроса — человек отошёл."}
	verification := "Ожидание на сессию (waits_per_session_min) снижается."
	f.Verification = &verification
	return f
}
