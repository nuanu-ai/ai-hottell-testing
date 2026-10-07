package analytics

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// The parameters of D03 in the colleague's analytics/catalogue.yaml.
const (
	// d03MinRepeats is how many equal calls without progress make an episode.
	d03MinRepeats = 3
	// d03WindowCalls is how many calls, the first included, the repeats are looked for in.
	d03WindowCalls = 10
)

// The classes of a D03 episode: every repeat failed with one error signature, or every one
// gave one result.
const (
	D03SameError  = "same_error"
	D03SameResult = "same_result"
)

var (
	// d03PathRe and d03NumberRe are what an error signature drops: paths and numbers.
	d03PathRe   = regexp.MustCompile(`(?:[\w.~-]*/)+[\w.-]*`)
	d03NumberRe = regexp.MustCompile(`\d+`)
)

// d03Key is what makes two calls the same call over the same object, the catalogue's
// input_exact_hash: a shell command with its blanks collapsed, as the case D03-P1 canonicalises
// it, else the tool and its whole input.
func d03Key(c *Call) string {
	if c.Cmd != "" {
		return c.Tool + "$" + strings.Join(strings.Fields(c.Cmd), " ")
	}
	return c.InputHash
}

// d03ErrorSignature is the signature of a failed call's error: its most telling line
// (errorLine) without paths and numbers, lower case.
func d03ErrorSignature(c *Call) string {
	line := errorLine(callErrorText(c), 200)
	line = d03PathRe.ReplaceAllString(line, "<p>")
	line = d03NumberRe.ReplaceAllString(line, "<n>")
	return strings.ToLower(line)
}

// d03Outcome is what a repeat must share with the first one: its class and the error signature
// or the result hash; ok is false for a call D03 does not count: an error of the environment
// (D07's and D24's), and a call whose result is unknown.
func d03Outcome(c *Call) (class, sig string, ok bool) {
	switch c.State {
	case StateError:
		if EnvironmentError(ErrorClass(callErrorText(c))) {
			return "", "", false
		}
		return D03SameError, d03ErrorSignature(c), true
	case StateSuccess, StateDone:
		if c.RespHash == "" {
			return "", "", false
		}
		return D03SameResult, c.RespHash, true
	}
	return "", "", false
}

// DetectD03 finds the episodes of D03 «Повтор без прогресса» in a session: d03MinRepeats calls
// or more with one d03Key within d03WindowCalls calls, all with one error signature or one
// result, and no progress between two neighbours: a successful edit or a reply of yours. A call
// that waits (IsWait) is no repeat; an error of the environment is D07's or D24's. A call takes
// part in one episode at most.
func DetectD03(s FindingSession) []StructuralEpisode {
	calls := s.Calls
	used := make([]bool, len(calls))
	// Each call's key and outcome are taken once: within the window of every earlier call they
	// were taken again, up to d03WindowCalls times, and the regular expressions of the outcome
	// made D03 most of the time of the findings (HT-493).
	type d03Call struct {
		key, class, sig string
		ok              bool
	}
	memo := make([]d03Call, len(calls))
	for i := range calls {
		if c := &calls[i]; !c.IsWait {
			m := &memo[i]
			m.key = d03Key(c)
			m.class, m.sig, m.ok = d03Outcome(c)
		}
	}
	var eps []StructuralEpisode
	for i := range calls {
		first := &memo[i]
		if used[i] || calls[i].IsWait || !first.ok {
			continue
		}
		run := []int{i}
		for j := i + 1; j < min(i+d03WindowCalls, len(calls)); j++ {
			c := &memo[j]
			if used[j] || calls[j].IsWait || c.key != first.key {
				continue
			}
			if !c.ok || c.class != first.class || c.sig != first.sig || d03Progress(s, run[len(run)-1], j) {
				break
			}
			run = append(run, j)
		}
		if len(run) < d03MinRepeats {
			continue
		}
		ep := StructuralEpisode{PatternID: "D03", Session: s.Key, Class: first.class}
		for _, j := range run {
			used[j] = true
			ep.Calls = append(ep.Calls, calls[j])
		}
		eps = append(eps, ep)
	}
	return eps
}

// d03Progress tells whether there was progress between calls[from] and calls[to]: an edit
// between them that did not fail, or a prompt of yours.
func d03Progress(s FindingSession, from, to int) bool {
	if editedBetween(s.Calls, from, to) {
		return true
	}
	lo, hi := s.Calls[from].At, s.Calls[to].At
	return slices.ContainsFunc(s.Events, func(ev telemetry.HookEvent) bool {
		return ev.Event == "UserPromptSubmit" && ev.Time.After(lo) && ev.Time.Before(hi) && !IsNotice(ev.Prompt)
	})
}

// D03Finding is the card of D03 over the episodes of the sessions that are not system ones, nil
// without an episode: a hypothesis, kind hook, its candidate change the catalogue's action, a
// PreToolUse hint to the agent. It is warn, the catalogue's medium, and bad when an episode has
// twice d03MinRepeats calls. Its impact is the repeats past the first of each episode.
func D03Finding(sessions []FindingSession) *Finding { return d03Finding(sessions, 1) }

// d03Finding is D03Finding with the sessions read workers at once.
func d03Finding(sessions []FindingSession, workers int) *Finding {
	episodes := detectEach(sessions, workers, func(s FindingSession) []StructuralEpisode {
		if s.Kind == "system" {
			return nil
		}
		return DetectD03(s)
	})
	var (
		ev      []Evidence
		sids    []string
		bySID   = map[string]float64{}
		n       int
		repeats int
		sev     = SevWarn
	)
	for i, s := range sessions {
		found := episodes[i]
		if len(found) == 0 {
			continue
		}
		sid := s.Key.SessionID
		sids = append(sids, sid)
		for _, ep := range found {
			n++
			repeats += len(ep.Calls) - 1
			bySID[sid] += float64(len(ep.Calls) - 1)
			if len(ep.Calls) >= 2*d03MinRepeats {
				sev = SevBad
			}
			for k := range ep.Calls {
				c := &ep.Calls[k]
				outcome := "тот же результат"
				if ep.Class == D03SameError {
					outcome = errorNote(*c)
				}
				ev = append(ev, Evidence{
					SID: sid, Line: s.callLine(c), At: ISO(c.At),
					Text: Clean(fmt.Sprintf("повтор %d/%d · %s · %s", k+1, len(ep.Calls),
						ToolSummary(c.Tool, c.Input, 80), outcome), 160),
				})
			}
		}
	}
	if n == 0 {
		return nil
	}
	slices.Sort(sids)
	what := fmt.Sprintf("В %d сессиях — %d эпизодов повтора без прогресса: агент повторял тот же вызов над тем же "+
		"объектом и получал ту же ошибку или тот же результат, а между повторами не было ни успешной правки, ни вашей "+
		"реплики. Лишних повторов — %d. Опрос статуса и ошибки среды не считаются (это D07 и D24).", len(sids), n, repeats)
	f := NewFinding("D03", sev, "Повтор без прогресса", what, ev, sids)
	f.PatternID = "D03"
	f.Kind = KindHook
	f.Episodes = n
	f.Impact = &Impact{Value: fmt.Sprintf("%d вызовов", repeats), Label: "повторов без прогресса"}
	f.ImpactBySession = bySID
	f.ImpactUnit = "вызовов"
	f.Where = "Хук PreToolUse агента"
	f.Snip = "Кандидат правки — только если разбор подтвердит, что агент застревал: хук PreToolUse при повторе " +
		"без прогресса — подсказка агенту «опиши, что меняешь, или доложи»."
	f.AlternativeCauses = []string{
		"Агент ждал внешнего изменения (сборки, деплоя), а не повторял попытку вслепую.",
		"Повтор проверял нестабильный результат, и это было оправдано.",
	}
	f.Preconditions = []string{
		"Открыть эпизоды: менялось ли что-то между повторами, чего хуки не видят.",
		"Убедиться, что повторы — не опрос состояния, который хуки не распознали как ожидание.",
	}
	verification := "Доля повторов без прогресса (repeat_no_progress_share) в новых сессиях ниже."
	f.Verification = &verification
	return &f
}
