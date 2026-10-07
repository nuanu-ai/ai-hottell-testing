package analytics

import (
	"math"
	"slices"
	"strconv"
	"time"
)

// isoLayout is how analytics writes a time: UTC, milliseconds, a literal Z.
const isoLayout = "2006-01-02T15:04:05.000Z"

// ISO formats t in UTC with milliseconds (truncated); the zero time, an unknown
// moment, is "".
func ISO(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(isoLayout)
}

// Minutes is the length of a..b in minutes, negative when b is before a.
func Minutes(a, b time.Time) float64 {
	return b.Sub(a).Minutes()
}

// round2 rounds v to two decimals the way Python's round(v, 2) does: from the exact
// binary value, ties to even.
func round2(v float64) float64 { return roundTo(v, 2) }

// roundTo rounds v to places decimals the way Python's round(v, places) does: from the exact
// binary value, ties to even.
func roundTo(v float64, places int) float64 {
	r, err := strconv.ParseFloat(strconv.FormatFloat(v, 'f', places, 64), 64)
	if err != nil {
		return v
	}
	return r
}

// Pct is the q-quantile of values by the nearest-rank rule, the element at
// ceil(q·n)−1 of the sorted values, rounded to two decimals. ok is false when values is
// empty. values is not changed.
func Pct(values []float64, q float64) (float64, bool) {
	if len(values) == 0 {
		return 0, false
	}
	sorted := slices.Sorted(slices.Values(values))
	k := int(math.Ceil(q*float64(len(sorted)))) - 1
	k = max(0, min(len(sorted)-1, k))
	return round2(sorted[k]), true
}

// Newest keeps the n newest items in time order. An item whose at is the zero time
// counts as the oldest; among equal times the later input item is kept, since the sort
// is stable. items is not changed.
func Newest[T any](items []T, n int, at func(T) time.Time) []T {
	if n <= 0 {
		return []T{}
	}
	sorted := slices.Clone(items)
	slices.SortStableFunc(sorted, func(a, b T) int { return at(a).Compare(at(b)) })
	if len(sorted) > n {
		sorted = sorted[len(sorted)-n:]
	}
	return sorted
}

// Day is the UTC date of t, 2006-01-02.
func Day(t time.Time) string {
	return DayIn(t, time.UTC)
}

// DayIn is the date of t in the zone loc, 2006-01-02.
func DayIn(t time.Time, loc *time.Location) string {
	return t.In(loc).Format(time.DateOnly)
}

// DayMinutes is the part of an interval that falls on one day.
type DayMinutes struct {
	Date    string
	Minutes float64
}

// SplitByDay splits a..b at UTC midnights. An empty or reversed interval, or one with
// an unknown (zero) end, gives nothing.
func SplitByDay(a, b time.Time) []DayMinutes {
	return SplitByDayIn(a, b, time.UTC)
}

// SplitByDayIn splits a..b at the midnights of the zone loc, as SplitByDay does at UTC ones. A
// day of a daylight saving change is as long as it is there.
func SplitByDayIn(a, b time.Time, loc *time.Location) []DayMinutes {
	if a.IsZero() || b.IsZero() || !b.After(a) {
		return nil
	}
	var out []DayMinutes
	for x := a.In(loc); x.Before(b); {
		next := nextDayStart(x, loc)
		y := next
		if b.Before(next) {
			y = b.In(loc)
		}
		out = append(out, DayMinutes{Date: DayIn(x, loc), Minutes: y.Sub(x).Minutes()})
		x = y
	}
	return out
}

// nextDayStart is the first instant after t that falls on a later day of loc: its midnight, or,
// in a zone that skips midnight for daylight saving (America/Santiago), the first instant after
// the gap, where time.Date would answer an instant before t and stall a loop over the days.
func nextDayStart(t time.Time, loc *time.Location) time.Time {
	day := DayIn(t, loc)
	if next := time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc); next.After(t) &&
		DayIn(next, loc) != day && DayIn(next.Add(-time.Second), loc) == day {
		return next
	}
	// The zones change their offsets on whole seconds: search them, lo on t's day, hi after it.
	lo, hi := t.Unix(), t.Unix()+2*24*3600
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		if DayIn(time.Unix(mid, 0), loc) == day {
			lo = mid
		} else {
			hi = mid
		}
	}
	return time.Unix(hi, 0).In(loc)
}

// TimeSpan is one labelled interval.
type TimeSpan struct {
	From, To time.Time
	Label    string
}

// MergedSpan is a run of TimeSpans whose gaps were shorter than the merge gap, with
// their labels in start order.
type MergedSpan struct {
	From, To time.Time
	Labels   []string
}

// MergeSegments sorts spans by start and merges a span into the previous run when it
// starts less than gap after the run's end; the run then ends at the later end.
func MergeSegments(spans []TimeSpan, gap time.Duration) []MergedSpan {
	sorted := slices.Clone(spans)
	slices.SortStableFunc(sorted, func(a, b TimeSpan) int { return a.From.Compare(b.From) })
	var out []MergedSpan
	for _, s := range sorted {
		if n := len(out); n > 0 && s.From.Sub(out[n-1].To) < gap {
			if s.To.After(out[n-1].To) {
				out[n-1].To = s.To
			}
			out[n-1].Labels = append(out[n-1].Labels, s.Label)
			continue
		}
		out = append(out, MergedSpan{From: s.From, To: s.To, Labels: []string{s.Label}})
	}
	return out
}
