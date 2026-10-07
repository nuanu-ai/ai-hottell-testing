package analytics

import (
	"slices"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// compactWait is how long a PreCompact waits for its PostCompact: one without a PostCompact within
// it is a compaction of its own, whose end the hooks did not record.
const compactWait = 10 * time.Minute

// Compactions returns the compactions of a session's hook events, oldest first: every
// PostCompact, and every PreCompact that no PostCompact follows within compactWait.
func Compactions(events []telemetry.HookEvent) []telemetry.HookEvent {
	var pre, post []telemetry.HookEvent
	for _, ev := range events {
		switch ev.Event {
		case "PreCompact":
			pre = append(pre, ev)
		case "PostCompact":
			post = append(post, ev)
		}
	}
	out := slices.Clone(post)
	for _, p := range pre {
		answered := slices.ContainsFunc(post, func(q telemetry.HookEvent) bool {
			d := q.Time.Sub(p.Time)
			return d >= 0 && d <= compactWait
		})
		if !answered {
			out = append(out, p)
		}
	}
	slices.SortStableFunc(out, func(a, b telemetry.HookEvent) int { return a.Time.Compare(b.Time) })
	return out
}
