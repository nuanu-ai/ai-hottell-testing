package sessions_test

import (
	"strings"
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/sessions"
)

func TestStatsSessionAndPeriod(t *testing.T) {
	t.Parallel()
	roots, now := writeRoots(t), time.Now()
	st, _, err := sessions.StatsOf(roots, sessions.StatsIn{ID: "c1", Filter: sessions.Filter{Agent: "claude"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if st.ToolCalls != 1 || st.ToolErrors != 1 || st.Tools[0].Errors != 1 || st.Turns != 1 || st.Tokens.Output != 70 || st.Coverage != nil {
		t.Fatalf("stats c1: %+v", st)
	}
	st, _, err = sessions.StatsOf(roots, sessions.StatsIn{ID: "c1", Filter: sessions.Filter{Agent: "claude"}, From: "2026-09-01T00:00:00Z"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if c := st.Coverage; c == nil || c.SessionsInPeriod != 0 || c.EventsOutOfPeriod != 9 || st.Events != 0 {
		t.Fatalf("stats c1 outside the window: %+v %+v", st, c)
	}
	st, note, err := sessions.StatsOf(roots, sessions.StatsIn{Filter: sessions.Filter{Agent: "codex"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if st.Sessions != 2 || st.Tokens.Input != 38994+10672 || st.TokensByModel["gpt-6-astra"] == nil || len(st.PerSession) != 2 || note != "" {
		t.Fatalf("stats period: %+v %q", st, note)
	}
}

func TestStatsPeriodCoverageAndPages(t *testing.T) {
	t.Parallel()
	roots, now := writeRoots(t), time.Now()
	st, _, err := sessions.StatsOf(roots, sessions.StatsIn{From: "2026-09-23T00:00:00Z", To: "2026-09-24T00:00:00Z"}, now)
	if err != nil {
		t.Fatal(err)
	}
	c := st.Coverage
	// By mtime 3 files match; only 019e-codex-1 has events in the window.
	if c == nil || c.SessionsFound != 3 || c.SessionsRead != 3 || c.SessionsInPeriod != 1 || c.EventsOutOfPeriod == 0 || c.NextOffset != 0 {
		t.Fatalf("coverage: %+v", c)
	}
	if st.Sessions != 1 || len(st.PerSession) != 1 || st.PerSession[0].ID != "019e-codex-1" || c.EventsInPeriod != st.Events {
		t.Fatalf("stats: %+v", st.PerSession)
	}
	p1, note, _ := sessions.StatsOf(roots, sessions.StatsIn{Filter: sessions.Filter{Limit: 1}}, now)
	if p1.Coverage.SessionsRead != 1 || p1.Coverage.NextOffset != 1 || !strings.Contains(note, "offset=1") {
		t.Fatalf("page 1: %+v %q", p1.Coverage, note)
	}
	p3, _, _ := sessions.StatsOf(roots, sessions.StatsIn{Filter: sessions.Filter{Limit: 1, Offset: 2}}, now)
	if p3.Coverage.SessionsRead != 1 || p3.Coverage.NextOffset != 0 {
		t.Fatalf("last page: %+v", p3.Coverage)
	}
}
