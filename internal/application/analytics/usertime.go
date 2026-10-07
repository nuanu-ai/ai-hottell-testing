package analytics

import (
	"slices"
	"sort"
	"time"
)

// AwayMin is the longest pause, in minutes, that counts as the person's time; a longer one is
// the person away.
const AwayMin = 30

// activeTimeMetric is Claude Code's metric of active time, in seconds; its type cli is the agent's
// time.
const activeTimeMetric = "claude_code.active_time.total"

// WaitMinutes is how long the agent stood idle on the person, in minutes to one decimal: the
// union of the [IdleFrom, End] of the stops on questions and of the permission windows whose end
// is known. Nil when there were
// stops and none of them was measured; 0 when there were none.
func WaitMinutes(waits []Wait) *float64 {
	var spans []idleSpan
	stopped := false
	for _, w := range waits {
		if !w.Stopped {
			continue
		}
		stopped = true
		if (w.Call != nil || w.Permission != nil) && !w.End.IsZero() {
			spans = append(spans, idleSpan{Start: w.IdleFrom, End: w.End})
		}
	}
	if len(spans) == 0 {
		if stopped {
			return nil
		}
		zero := 0.0
		return &zero
	}
	slices.SortFunc(spans, func(a, b idleSpan) int {
		if c := a.Start.Compare(b.Start); c != 0 {
			return c
		}
		return a.End.Compare(b.End)
	})
	total := 0.0
	start, end := spans[0].Start, spans[0].End
	for _, s := range spans[1:] {
		if s.Start.After(end) {
			total += Minutes(start, end)
			start, end = s.Start, s.End
			continue
		}
		if s.End.After(end) {
			end = s.End
		}
	}
	total += Minutes(start, end)
	total = roundTo(total, 1)
	return &total
}

// UserGap is one pause of the person: from what they answered to their prompt. Min is its length
// in minutes to one decimal, 0 when it is longer than AwayMin.
type UserGap struct {
	Prompt *Prompt
	Min    float64
}

// UserTime are the person's pauses in a session and how many of them were absences.
type UserTime struct {
	Gaps []UserGap
	Away int
}

// UserGaps returns the person's pauses: from the latest anchor before each prompt, after the
// previous prompt, to the prompt. The anchors are the turns' Stops, the stops on questions and
// permission windows, and the ends of the open turns. The agent's notices are no prompts here.
func UserGaps(prompts []Prompt, turns []*Turn, waits []Wait) UserTime {
	prompts = PersonPrompts(prompts)
	var anchors []time.Time
	for _, t := range turns {
		if !t.Stop.IsZero() {
			anchors = append(anchors, t.Stop)
		}
		if t.State == TurnOpen {
			anchors = append(anchors, t.End)
		}
	}
	for _, w := range waits {
		if w.Stopped {
			anchors = append(anchors, w.At)
		}
	}
	slices.SortFunc(anchors, func(a, b time.Time) int { return a.Compare(b) })

	var ut UserTime
	var prev time.Time
	for i := range prompts {
		p := &prompts[i]
		lo := 0
		if i > 0 {
			lo = sort.Search(len(anchors), func(j int) bool { return anchors[j].After(prev) })
		}
		hi := sort.Search(len(anchors), func(j int) bool { return !anchors[j].Before(p.At) })
		prev = p.At
		if hi <= lo {
			continue
		}
		g := Minutes(anchors[hi-1], p.At)
		if g > AwayMin {
			ut.Away++
			g = 0
		}
		// Each pause is rounded on its own, so that a session, its days and the period add up
		// the same tenths of a minute.
		ut.Gaps = append(ut.Gaps, UserGap{Prompt: p, Min: roundTo(g, 1)})
	}
	return ut
}

// UserMinutes is the person's time in a session, in minutes to one decimal: the sum of the pauses,
// for both agents. Claude Code's claude_code.active_time.total of type user is not used: it has no
// split by day, and the period and the days count the pauses.
func UserMinutes(ut UserTime) float64 {
	total := 0.0
	for _, g := range ut.Gaps {
		total += g.Min
	}
	return roundTo(total, 1)
}

// idleSpan is the idle of one stop, from IdleFrom to End.
type idleSpan struct {
	Start, End time.Time
}
