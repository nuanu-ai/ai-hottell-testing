package analytics

import "strconv"

// waitSummaryMax is how long the question's summary in a wait episode's evidence is.
const waitSummaryMax = 120

// WaitFriction returns the wait episodes of the session sid: one per question the agent stopped
// on (a permission window is none), «агент стоял N мин до ответа» or, without an end, «агент
// остановился, ответа не было», with the question.
func WaitFriction(sid string, waits []Wait) []FrictionEpisode {
	var out []FrictionEpisode
	for _, w := range waits {
		if w.Call == nil || !w.Stopped {
			continue
		}
		tail := "агент остановился, ответа не было"
		if w.Min != nil {
			tail = "агент стоял " + strconv.FormatFloat(*w.Min, 'f', 1, 64) + " мин до ответа"
		}
		text := tail + " · " + ToolSummary(w.Call.Tool, w.Call.Input, waitSummaryMax)
		out = append(out, FrictionEpisode{Evidence: []Evidence{frictionEvidence(sid, ISO(w.Call.At), text)}})
	}
	return out
}
