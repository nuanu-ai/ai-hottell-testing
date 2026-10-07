package analytics

import (
	"fmt"
	"slices"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// d12AfterTurns is the catalogue's after_turns of D12: in how many turns after a compaction a
// repeated error counts; the turn the compaction fell in is the first.
const d12AfterTurns = 5

// d12Fixed is an error fixed before a compaction: the failed call, the first success of its
// tool (d07Tool) after it and the error's signature.
type d12Fixed struct {
	err, fix Call
	sig      string
}

// DetectD12a finds the episodes of the structural part (a) of D12 «Агент забыл сказанное» in a
// session: within d12AfterTurns turns after a compaction a call fails with an error signature
// (d03ErrorSignature) that a call of the same tool had before the compaction and that a later
// success of that tool fixed before it. One episode per compaction, tool and signature: the
// error, its fix and the first repeat. Part (b), questions asked again and instructions broken,
// is the deep review's.
func DetectD12a(s FindingSession) []StructuralEpisode {
	var eps []StructuralEpisode
	for _, k := range Compactions(s.Events) {
		fixed := d12FixedBefore(s.Calls, k.Time)
		seen := map[string]bool{}
		for i := range s.Calls {
			c := &s.Calls[i]
			if !c.At.After(k.Time) || c.State != StateError || d12TurnsBetween(s.Events, k.Time, c.At) >= d12AfterTurns {
				continue
			}
			sig, tool := d03ErrorSignature(c), d07Tool(c)
			idx := slices.IndexFunc(fixed, func(f d12Fixed) bool { return f.sig == sig && d07Tool(&f.err) == tool })
			if idx < 0 || seen[tool+"\x00"+sig] {
				continue
			}
			seen[tool+"\x00"+sig] = true
			comp := k
			eps = append(eps, StructuralEpisode{
				PatternID: "D12", Session: s.Key, Class: "after_compaction",
				Calls: []Call{fixed[idx].err, fixed[idx].fix, *c}, Compaction: &comp,
			})
		}
	}
	return eps
}

// d12FixedBefore are the errors of the calls before t that a later success of the same tool
// before t fixed, newest error first, so an episode cites the latest fix.
func d12FixedBefore(calls []Call, t time.Time) []d12Fixed {
	var out []d12Fixed
	for i := range calls {
		e := &calls[i]
		if !e.At.Before(t) || e.State != StateError {
			continue
		}
		for j := i + 1; j < len(calls) && calls[j].At.Before(t); j++ {
			if calls[j].State == StateSuccess && d07Tool(&calls[j]) == d07Tool(e) {
				out = append(out, d12Fixed{err: *e, fix: calls[j], sig: d03ErrorSignature(e)})
				break
			}
		}
	}
	slices.Reverse(out)
	return out
}

// d12TurnsBetween is how many turns started after from and up to at: the prompts of yours
// between them.
func d12TurnsBetween(events []telemetry.HookEvent, from, at time.Time) int {
	n := 0
	for i := range events {
		ev := &events[i]
		if ev.Event == "UserPromptSubmit" && ev.Time.After(from) && !ev.Time.After(at) && !IsNotice(ev.Prompt) {
			n++
		}
	}
	return n
}

// D12Finding is the card of D12 (a) over the episodes of the sessions that are not system ones,
// nil without an episode: a hypothesis, kind habit, whose «what to check before the change» is
// to open the episode after the compaction. It is warn, the catalogue's medium, and bad when a
// session has episodes after two compactions or more. Its impact is the episodes.
func D12Finding(sessions []FindingSession) *Finding {
	var (
		ev    []Evidence
		sids  []string
		bySID = map[string]float64{}
		n     int
		sev   = SevWarn
	)
	for _, s := range sessions {
		if s.Kind == "system" {
			continue
		}
		found := DetectD12a(s)
		if len(found) == 0 {
			continue
		}
		sid := s.Key.SessionID
		sids = append(sids, sid)
		comps := map[time.Time]bool{}
		for _, ep := range found {
			n++
			bySID[sid]++
			k := ep.Compaction
			comps[k.Time] = true
			e, fix, again := &ep.Calls[0], &ep.Calls[1], &ep.Calls[2]
			ev = append(ev,
				Evidence{
					SID: sid, Line: s.callLine(e), At: ISO(e.At),
					Text: Clean("ошибка до сжатия · "+e.Name+" · "+errorNote(*e), 160),
				},
				Evidence{
					SID: sid, Line: s.callLine(fix), At: ISO(fix.At),
					Text: Clean("исправлено · "+fix.Name, 160),
				},
				Evidence{SID: sid, Line: s.compactLine(*k), At: ISO(k.Time), Text: "сжатие контекста"},
				Evidence{
					SID: sid, Line: s.callLine(again), At: ISO(again.At),
					Text: Clean("та же ошибка после сжатия · "+again.Name+" · "+errorNote(*again), 160),
				},
			)
		}
		if len(comps) >= 2 {
			sev = SevBad
		}
	}
	if n == 0 {
		return nil
	}
	slices.Sort(sids)
	what := fmt.Sprintf("В %d сессиях — %d эпизодов, где ошибка, исправленная до сжатия контекста, вернулась "+
		"в пределах %d ходов после него: тот же инструмент, та же строка ошибки без чисел и путей. Похоже, "+
		"после сжатия агент потерял, как исправлял её.", len(sids), n, d12AfterTurns)
	f := NewFinding("D12a", sev, "Ошибка вернулась после сжатия контекста", what, ev, sids)
	f.PatternID = "D12"
	f.Kind = KindHabit
	f.Episodes = n
	f.Impact = &Impact{Value: fmt.Sprintf("%d эпизодов", n), Label: "повтора исправленной ошибки"}
	f.ImpactBySession = bySID
	f.ImpactUnit = "эпизодов"
	f.Where = "Привычка работы с длинными задачами"
	f.Snip = "Кандидат правки — только если разбор подтвердит, что после сжатия терялось исправление: одна задача — " +
		"одна сессия; важные указания и найденные исправления — в файл задачи или AGENTS.md; объёмные выводы — в субагента."
	f.AlternativeCauses = []string{
		"Ошибку вернула новая правка, а не забытое исправление.",
		"Исправление до сжатия было временным, и ошибка вернулась бы и без него.",
	}
	f.Preconditions = []string{
		"Открыть эпизод после сжатия: повторил ли агент то, что уже было исправлено, и знал ли он об исправлении.",
		"Сравнить правку до сжатия и после: не отменил ли агент своё же исправление.",
	}
	verification := "Повторов исправленных ошибок после сжатия (forgotten_repeats) в новых сессиях меньше."
	f.Verification = &verification
	return &f
}
