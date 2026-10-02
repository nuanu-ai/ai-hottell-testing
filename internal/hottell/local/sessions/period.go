package sessions

import (
	"errors"
	"fmt"
	"time"
)

// Period is a window over the time of the events themselves, [From, To). since and until
// of List look at the file's mtime instead, and a fresh file can hold month-old replies:
// Codex appends a resumed session to its old rollout.
type Period struct {
	From time.Time
	To   time.Time
}

// ParsePeriod reads from and to like since and until: YYYY-MM-DD, RFC3339 or a duration
// back from now (168h). A date in to is inclusive.
func ParsePeriod(from, to string, now time.Time) (Period, error) {
	f, err := ParseWhen(from, now)
	if err != nil {
		return Period{}, fmt.Errorf("from: %w", err)
	}
	t, err := ParseWhen(to, now)
	if err != nil {
		return Period{}, fmt.Errorf("to: %w", err)
	}
	if to != "" && len(to) == len("2006-01-02") {
		t = t.Add(24 * time.Hour)
	}
	if !f.IsZero() && !t.IsZero() && !f.Before(t) {
		return Period{}, errors.New("from must be earlier than to")
	}
	return Period{From: f, To: t}, nil
}

// Set reports whether the window bounds anything.
func (p Period) Set() bool { return !p.From.IsZero() || !p.To.IsZero() }

// Has reports whether an event at ts is inside the window. With a window set, an event
// without a time is outside: nothing proves it belongs to the period.
func (p Period) Has(ts time.Time) bool {
	if !p.Set() {
		return true
	}
	if ts.IsZero() {
		return false
	}
	return (p.From.IsZero() || !ts.Before(p.From)) && (p.To.IsZero() || ts.Before(p.To))
}

// Overlaps reports whether [a, b] meets the window; unknown ends meet a set window never.
func (p Period) Overlaps(a, b time.Time) bool {
	if !p.Set() {
		return true
	}
	if a.IsZero() || b.IsZero() {
		return false
	}
	return (p.From.IsZero() || !b.Before(p.From)) && (p.To.IsZero() || a.Before(p.To))
}

// inPeriod returns the events inside the window and how many it dropped.
func inPeriod(evs []Event, p Period) ([]Event, int) {
	if !p.Set() {
		return evs, 0
	}
	out := make([]Event, 0, len(evs))
	for _, ev := range evs {
		if p.Has(ev.TS) {
			out = append(out, ev)
		}
	}
	return out, len(evs) - len(out)
}
