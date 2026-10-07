package journal

import (
	"time"
)

// Recurrence is a coach decision's latest check folded in: its result, observations and
// repeats (only for repeated), when it was recorded and how many checks there were.
type Recurrence struct {
	Result       string
	Observations *int
	Repeats      *int
	CheckedAt    string
	Checks       int
}

// FoldChecks is fold of coach/journal.go: for each coach decision that has checks, keyed by
// its record_id, the latest check in journal order. A change may be checked again and again
// (not_enough_data is not final), and an older repeats goes with a later result. A check
// counts only for a decision above it in the journal and of the same user; a record whose
// detail does not parse is skipped, since the journal checked it when it was appended.
func FoldChecks(recs []Record) map[string]Recurrence {
	decisions := map[string]string{} // record_id → user_id
	out := map[string]Recurrence{}
	for _, r := range recs {
		e, err := CoachEntryFromRecord(r)
		if err != nil {
			continue
		}
		if e.CheckOf == "" {
			decisions[e.ID] = e.UserID
			continue
		}
		if user, ok := decisions[e.CheckOf]; !ok || user != e.UserID || e.Result == nil {
			continue
		}
		prev := out[e.CheckOf]
		out[e.CheckOf] = Recurrence{
			Result: *e.Result, Observations: e.Observations, Repeats: e.Repeats, CheckedAt: e.At, Checks: prev.Checks + 1,
		}
	}
	return out
}

// CycleCounters are the counters of the improvement cycle (docs/specs/deep-review, «Счётчики
// цикла улучшений»).
type CycleCounters struct {
	// Discussed: coach decisions of any kind and decision events.
	Discussed int
	// Implemented: coach decisions applied or test and application events.
	Implemented int
	// Verified: of Implemented, the records with a result recorded before the end of the
	// period.
	Verified int
}

// Counters counts the journal over [from, to) by recorded_at; the caller filters by person.
// They count the journal, not the registry, so orphaned events count too.
//
// Implemented counts records, not proposals: "test, then applied" on one p2: is 2, though the
// proposal's axis shows one application. A record is verified by a result of its own kind
// recorded before to: a coach decision by a coach_check of it, an application by an effect
// event after it in the chain with the same user, proposal_id and proposal_fingerprint. Other
// pairs, an effect event on a coach decision's proposal among them, are not a result.
func Counters(recs []Record, from, to time.Time) CycleCounters {
	type proposal struct{ user, id, fingerprint string }
	var c CycleCounters
	inside := map[int]bool{}             // index of an Implemented record → verified
	coachIdx := map[string]int{}         // coach decision record_id → its index in Implemented
	applications := map[proposal][]int{} // proposal → indexes of its applications in Implemented
	for _, r := range recs {
		at, err := time.Parse(time.RFC3339, r.RecordedAt)
		if err != nil || !at.Before(to) {
			continue // a record from to on counts in no way, a result included
		}
		inPeriod := !at.Before(from)
		switch r.Kind {
		case KindDecision:
			if inPeriod {
				c.Discussed++
			}
		case KindApplication:
			if inPeriod {
				p := proposal{r.UserID, r.ProposalID, r.ProposalFingerprint}
				applications[p] = append(applications[p], c.Implemented)
				inside[c.Implemented] = false
				c.Implemented++
			}
		case KindEffect:
			for _, i := range applications[proposal{r.UserID, r.ProposalID, r.ProposalFingerprint}] {
				inside[i] = true
			}
		case KindCoachDecision, KindCoachCheck:
			e, err := CoachEntryFromRecord(r)
			if err != nil {
				continue
			}
			if e.CheckOf != "" {
				if i, ok := coachIdx[e.CheckOf]; ok {
					inside[i] = true
				}
				continue
			}
			if !inPeriod {
				continue
			}
			c.Discussed++
			if e.Decision == "applied" || e.Decision == "test" {
				coachIdx[e.ID] = c.Implemented
				inside[c.Implemented] = false
				c.Implemented++
			}
		}
	}
	for _, v := range inside {
		if v {
			c.Verified++
		}
	}
	return c
}
