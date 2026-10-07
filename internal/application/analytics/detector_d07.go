package analytics

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// The parameters of D07 in the colleague's analytics/catalogue.yaml.
const (
	// d07MinErrors is how many environment errors of one class make an episode.
	d07MinErrors = 3
	// d07EarlyCalls and d07EarlyErrors: as many environment errors among the session's first calls
	// make an episode too.
	d07EarlyCalls  = 5
	d07EarlyErrors = 2
)

// repeatPeriod is the catalogue's period_days: episodes in two sessions of one person within it
// raise D07 to high and count toward D24.
const repeatPeriod = 7 * 24 * time.Hour

// StructuralEpisode is one episode of a structural detector of the catalogue in one session: the
// detector's pattern_id, the class it is of, the calls it rests on and the first success after
// them, nil when none came, and the compaction it follows, nil for a detector that reads none.
type StructuralEpisode struct {
	PatternID  string
	Session    SessionKey
	Class      string
	Calls      []Call
	Recovery   *Call
	Compaction *telemetry.HookEvent
}

// callErrorText is the text a failed call's error class is read from: the hook's error, the native
// error, the outcome's note and the head of the output.
func callErrorText(c *Call) string {
	return strings.Join([]string{c.Failure, c.NativeError, c.Note, c.RespHead}, "\n")
}

// DetectD07 finds the episodes of D07 «Каскад ошибок среды» in a session: d07MinErrors environment
// errors of one class (error_class, EnvironmentError), one episode per class, or else
// d07EarlyErrors of them among its first d07EarlyCalls calls. Not counted, as the catalogue
// excludes them: a call of an MCP server (D24's); an error the next call of the same tool recovers
// from; a series of rate_limit errors of one tool with growing pauses that ends in its success.
func DetectD07(s FindingSession) []StructuralEpisode {
	calls := s.Calls
	excluded := d07Excluded(calls)
	var counted []int
	for i := range calls {
		c := &calls[i]
		if c.MCP != "" || c.State != StateError || excluded[i] {
			continue
		}
		if EnvironmentError(ErrorClass(callErrorText(c))) {
			counted = append(counted, i)
		}
	}
	byClass := map[string][]int{}
	var classes []string
	for _, i := range counted {
		class := ErrorClass(callErrorText(&calls[i]))
		if byClass[class] == nil {
			classes = append(classes, class)
		}
		byClass[class] = append(byClass[class], i)
	}
	var eps []StructuralEpisode
	for _, class := range classes {
		if len(byClass[class]) >= d07MinErrors {
			eps = append(eps, d07Episode(s, class, byClass[class]))
		}
	}
	if len(eps) > 0 {
		return eps
	}
	var early []int
	for _, i := range counted {
		if i < d07EarlyCalls {
			early = append(early, i)
		}
	}
	if len(early) >= d07EarlyErrors {
		return []StructuralEpisode{d07Episode(s, ErrorClass(callErrorText(&calls[early[0]])), early)}
	}
	return nil
}

// d07Episode is the episode of the calls at idx, with the first success of the tool of its last
// error after it.
func d07Episode(s FindingSession, class string, idx []int) StructuralEpisode {
	ep := StructuralEpisode{PatternID: "D07", Session: s.Key, Class: class}
	for _, i := range idx {
		ep.Calls = append(ep.Calls, s.Calls[i])
	}
	last := idx[len(idx)-1]
	for j := last + 1; j < len(s.Calls); j++ {
		if d07Tool(&s.Calls[j]) == d07Tool(&s.Calls[last]) && s.Calls[j].State == StateSuccess {
			r := s.Calls[j]
			ep.Recovery = &r
			break
		}
	}
	return ep
}

// d07Excluded marks the errors D07 does not count: one whose next call of the same tool succeeds,
// and each error of a rate_limit series of one tool whose pauses grow and which ends in a success.
func d07Excluded(calls []Call) []bool {
	out := make([]bool, len(calls))
	byTool := map[string][]int{}
	for i := range calls {
		k := d07Tool(&calls[i])
		byTool[k] = append(byTool[k], i)
	}
	for _, idx := range byTool {
		for k := 0; k < len(idx); k++ {
			c := &calls[idx[k]]
			if c.State != StateError {
				continue
			}
			if k+1 < len(idx) && calls[idx[k+1]].State == StateSuccess {
				out[idx[k]] = true
				continue
			}
			if ErrorClass(callErrorText(c)) != "rate_limit" {
				continue
			}
			end := k
			for end+1 < len(idx) && calls[idx[end+1]].State == StateError &&
				ErrorClass(callErrorText(&calls[idx[end+1]])) == "rate_limit" {
				end++
			}
			if end+1 < len(idx) && calls[idx[end+1]].State == StateSuccess && growingPauses(calls, idx[k:end+2]) {
				for _, i := range idx[k : end+1] {
					out[i] = true
				}
			}
			k = end
		}
	}
	return out
}

// d07Tool is the tool a recovery is looked for in: the tool, and for a shell call its command
// as NormalizeCmd groups it, since any command runs through the one shell tool.
func d07Tool(c *Call) string {
	if c.Cmd != "" {
		return c.Tool + ":" + NormalizeCmd(c.Cmd)
	}
	return c.Tool
}

// growingPauses tells whether each pause between the calls at idx is longer than the one before.
func growingPauses(calls []Call, idx []int) bool {
	prev := time.Duration(-1)
	for k := 1; k < len(idx); k++ {
		gap := calls[idx[k]].At.Sub(calls[idx[k-1]].At)
		if gap <= prev {
			return false
		}
		prev = gap
	}
	return true
}

// repeatedWithin tells whether two of the instants of one person fall within repeatPeriod of
// each other.
func repeatedWithin(byUser map[uuid.UUID][]time.Time) bool {
	for _, ts := range byUser {
		slices.SortFunc(ts, time.Time.Compare)
		for k := 1; k < len(ts); k++ {
			if ts[k].Sub(ts[k-1]) <= repeatPeriod {
				return true
			}
		}
	}
	return false
}

// D07Finding is the card of D07 over the episodes of the sessions that are not system ones, nil
// without an episode: a hypothesis about the work, kind diagnostic, its candidate change the
// catalogue's action. It is warn, and bad when one person had episodes in two sessions within
// repeatPeriod, the catalogue's high.
func D07Finding(sessions []FindingSession) *Finding { return d07Finding(sessions, 1) }

// d07Finding is D07Finding with the sessions read workers at once.
func d07Finding(sessions []FindingSession, workers int) *Finding {
	episodes := detectEach(sessions, workers, func(s FindingSession) []StructuralEpisode {
		if s.Kind == "system" {
			return nil
		}
		return DetectD07(s)
	})
	var (
		eps     []StructuralEpisode
		ev      []Evidence
		sids    []string
		bySID   = map[string]float64{}
		byUser  = map[uuid.UUID][]time.Time{}
		byClass = map[string]int{}
		classes []string
		costSum int
	)
	for i, s := range sessions {
		found := episodes[i]
		if len(found) == 0 {
			continue
		}
		sid := s.Key.SessionID
		sids = append(sids, sid)
		byUser[s.Key.UserID] = append(byUser[s.Key.UserID], found[0].Calls[0].At)
		for _, ep := range found {
			eps = append(eps, ep)
			if byClass[ep.Class] == 0 {
				classes = append(classes, ep.Class)
			}
			byClass[ep.Class] += len(ep.Calls)
			cost := d07Cost(s, ep)
			bySID[sid] += float64(cost)
			costSum += cost
			for i := range ep.Calls {
				c := &ep.Calls[i]
				ev = append(ev, Evidence{
					SID: sid, Line: s.callLine(c), At: ISO(c.At),
					Text: Clean("ошибка среды "+ep.Class+" · "+c.Name+" · "+c.Note, 160),
				})
			}
			if r := ep.Recovery; r != nil {
				ev = append(ev, Evidence{
					SID: sid, Line: s.callLine(r), At: ISO(r.At),
					Text: Clean("первый успех после серии · "+r.Name, 160),
				})
			}
		}
	}
	if len(eps) == 0 {
		return nil
	}
	slices.SortStableFunc(classes, func(a, b string) int { return cmp.Compare(byClass[b], byClass[a]) })
	parts := make([]string, 0, len(classes))
	for _, c := range classes {
		parts = append(parts, fmt.Sprintf("%s — %d", c, byClass[c]))
	}
	what := fmt.Sprintf("В %d сессиях — %d эпизодов каскада ошибок среды: %s. Ошибки одного класса повторяются, "+
		"а следующий вызов того же инструмента не исправляет их. Вызовы MCP-серверов не считаются (это D24), "+
		"как и сразу исправленная ошибка и обработанный backoff при rate_limit.",
		len(sids), len(eps), strings.Join(parts, ", "))
	sev := SevWarn
	if repeatedWithin(byUser) {
		sev = SevBad
	}
	slices.Sort(sids)
	f := NewFinding("D07", sev, "Каскад ошибок среды", what, ev, sids)
	f.PatternID = "D07"
	f.Episodes = len(eps)
	f.Impact = &Impact{Value: fmt.Sprintf("%d вызовов", costSum), Label: "от первой ошибки среды до успеха"}
	f.ImpactBySession = bySID
	f.ImpactUnit = "вызовов"
	f.Where = "Старт сессии агента: ключи, права и зависимости среды"
	f.Snip = "Кандидат правки — только если разбор подтвердит, что среда не готова к началу работы: " +
		"проверка среды на старте сессии: ключи, права, зависимости."
	f.AlternativeCauses = []string{
		"Среда сломалась один раз, и человек уже починил её.",
		"Ошибку исправил другой инструмент, а не повтор того же.",
	}
	f.Preconditions = []string{
		"Ошибки одного класса среды повторяются в нескольких сессиях, а не однажды.",
		"Причина ошибок — среда (ключи, права, зависимости, сеть), а не сама задача.",
	}
	verification := "Доля ошибок среды среди вызовов (env_error_share) в новых сессиях ниже."
	f.Verification = &verification
	return &f
}

// d07Cost is the catalogue's cost of an episode: the calls from its first error to the first
// success of the same tool, or to the end of the session.
func d07Cost(s FindingSession, ep StructuralEpisode) int {
	first := ep.Calls[0]
	n := 0
	for i := range s.Calls {
		c := &s.Calls[i]
		if c.At.Before(first.At) {
			continue
		}
		n++
		if d07Tool(c) == d07Tool(&first) && c.State == StateSuccess {
			break
		}
	}
	return n
}
