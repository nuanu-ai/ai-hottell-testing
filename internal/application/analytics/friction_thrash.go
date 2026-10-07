package analytics

import "strconv"

// ThrashMin is the fewest failing test runs in a row, with an edit between each two, that make a
// thrash, as the colleague's builder names it.
const ThrashMin = 3

// DetectThrash finds where the agent went round edit → test → failure: at least ThrashMin
// failing test runs (IsTestCmd) in a row with an edit that did not fail between each two. A
// passing run closes the series; a failure with no edit since the previous one breaks it and
// starts a new one. A run that is neither error nor success (a Codex Bash with no exit code) is no
// run. calls are the session's calls in their order, sid its id.
func DetectThrash(sid string, calls []Call) []FrictionEpisode {
	var out []FrictionEpisode
	var streak []int
	flush := func() {
		if len(streak) >= ThrashMin {
			var ep FrictionEpisode
			for k, j := range streak {
				x := calls[j]
				text := "провал " + strconv.Itoa(k+1) + " · " + NormalizeCmd(x.Cmd)
				ep.Evidence = append(ep.Evidence, frictionEvidence(sid, ISO(x.At), text))
			}
			out = append(out, ep)
		}
		streak = nil
	}
	for i, c := range calls {
		if c.Cmd == "" || !IsTestCmd(c.Cmd) || (c.State != StateError && c.State != StateSuccess) {
			continue
		}
		if c.State == StateSuccess {
			flush()
			continue
		}
		if len(streak) > 0 && !editedBetween(calls, streak[len(streak)-1], i) {
			flush()
		}
		streak = append(streak, i)
	}
	flush()
	return out
}
