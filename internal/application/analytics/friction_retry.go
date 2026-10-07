package analytics

import "strconv"

// The thresholds of the retry signal, as the colleague's builder names them.
const (
	// RetryWindow is how many calls, the first included, a retry is looked for in.
	RetryWindow = 10
	// RetryMin is the fewest equal failing calls that make a retry.
	RetryMin = 3
)

// DetectRetry finds where the agent repeated one call that kept failing: the same tool with the
// same whole input (InputHash) at least RetryMin times within RetryWindow calls, every one an
// error, and no edit that did not fail between the first and the last. A call that waits on something else
// (IsWait) is no attempt. A call takes part in one episode at most. calls are the session's
// calls in their order, sid its id.
func DetectRetry(sid string, calls []Call) []FrictionEpisode {
	var out []FrictionEpisode
	used := map[int]bool{}
	for i, c := range calls {
		if used[i] || c.IsWait || c.State != StateError {
			continue
		}
		var same []int
		for j := i; j < min(i+RetryWindow, len(calls)); j++ {
			if calls[j].InputHash == c.InputHash {
				same = append(same, j)
			}
		}
		if len(same) < RetryMin || !allFailed(calls, same) {
			continue
		}
		if editedBetween(calls, same[0], same[len(same)-1]) {
			continue
		}
		var ep FrictionEpisode
		for k, j := range same {
			used[j] = true
			x := calls[j]
			text := "повтор " + strconv.Itoa(k+1) + "/" + strconv.Itoa(len(same)) + " · " +
				ToolSummary(x.Tool, x.Input, 80) + " · " + errorNote(x)
			ep.Evidence = append(ep.Evidence, frictionEvidence(sid, ISO(x.At), text))
		}
		out = append(out, ep)
	}
	return out
}

// errorNote is the note of a failed call, "ошибка" when it has none.
func errorNote(c Call) string {
	if c.Note == "" {
		return "ошибка"
	}
	return c.Note
}

// allFailed tells whether every call of idx is an error.
func allFailed(calls []Call, idx []int) bool {
	for _, j := range idx {
		if calls[j].State != StateError {
			return false
		}
	}
	return true
}

// editedBetween tells whether a call that changes files started strictly between calls[from] and
// calls[to] and did not fail; calls are in the order of their start, so only the calls between
// the two are looked at. Unlike the colleague's builder, a failed edit is no edit: three failing
// equal patches are a retry (HT-263).
func editedBetween(calls []Call, from, to int) bool {
	lo, hi := calls[from].At, calls[to].At
	for _, x := range calls[from+1 : to] {
		if x.IsEdit && x.State != StateError && x.At.After(lo) && x.At.Before(hi) {
			return true
		}
	}
	return false
}
