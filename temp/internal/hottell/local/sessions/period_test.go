package sessions_test

import (
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/sessions"
)

func TestParsePeriod(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	p, err := sessions.ParsePeriod("168h", "", now)
	if err != nil || !p.From.Equal(now.Add(-168*time.Hour)) || !p.To.IsZero() {
		t.Fatalf("168h: %+v %v", p, err)
	}
	p, err = sessions.ParsePeriod("", "2026-09-23", now) // a date in to is inclusive, in local time
	want, _ := time.ParseInLocation("2006-01-02", "2026-09-24", time.Local)
	if err != nil || !p.To.Equal(want) {
		t.Fatalf("to: %+v %v", p, err)
	}
	if _, err := sessions.ParsePeriod("2026-09-02T00:00:00Z", "2026-09-01T00:00:00Z", now); err == nil {
		t.Fatal("from after to is an error")
	}
	if p, _ := sessions.ParsePeriod("", "", now); p.Set() || !p.Has(time.Time{}) {
		t.Fatal("an empty window lets everything through")
	}
	p, _ = sessions.ParsePeriod("2026-09-01T00:00:00Z", "2026-09-02T00:00:00Z", now)
	in, edge := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	if !p.Has(in) || p.Has(edge) || p.Has(time.Time{}) {
		t.Fatal("window [from, to): from is inside, to is outside, no time is outside")
	}
	if !p.Overlaps(in.Add(-time.Hour), in.Add(time.Hour)) || p.Overlaps(edge, edge.Add(time.Hour)) {
		t.Fatal("a span meeting the window")
	}
}
