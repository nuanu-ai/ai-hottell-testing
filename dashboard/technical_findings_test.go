package main

import "testing"

func TestTechnicalFindingsRespectSourceAndOutcomeBoundary(t *testing.T) {
	retro := &TelemetryReport{SourceCoverage: map[string]SourceCoverage{
		"hook": {Status: "missing"}, "otel": {Status: "missing"}, "transcript": {Status: "available"},
	}}
	hook := &Session{Events: 12, ToolStarts: 4, ToolEnds: 1, HasStart: 0, HasEnd: 0}
	otel := &OTelReport{Queried: true, Coverage: "complete", LogGroups: []OTelLogGroup{{Events: 2}}}
	got := technicalFindings(retro, hook, true, otel, true)
	if len(got) != 6 {
		t.Fatalf("expected six bounded findings, got %+v", got)
	}
	for _, finding := range got {
		if finding.Finding == "" || finding.Evidence == "" || finding.Meaning == "" {
			t.Fatalf("finding lacks evidence or meaning: %+v", finding)
		}
	}
	if got[0].Code != "historical-capture-gap" || got[1].Code != "hook-recorded" || got[2].Code != "hook-boundary-gap" || got[3].Code != "hook-tool-end-gap" || got[4].Code != "otel-logs-recorded" || got[5].Code != "otel-trace-metric-gap" {
		t.Fatalf("unexpected source findings: %+v", got)
	}
	if got := technicalFindings(nil, nil, false, nil, false); len(got) != 0 {
		t.Fatalf("unavailable sources produced claims: %+v", got)
	}
	otel.Coverage = "partial"
	if got := technicalFindings(nil, nil, false, otel, true); len(got) != 1 || got[0].Code != "otel-logs-recorded" {
		t.Fatalf("partial OTel coverage produced a false zero: %+v", got)
	}
	otel.Queried = false
	if got := technicalFindings(nil, nil, false, otel, true); len(got) != 0 {
		t.Fatalf("unqueried OTel produced findings: %+v", got)
	}
	if got := technicalFindings(nil, &Session{Events: 2, HasStart: 1, HasEnd: 1, ToolStarts: 1, ToolEnds: 1}, true,
		&OTelReport{Traces: 1, MetricRecords: 1}, true); len(got) != 1 || got[0].Code != "hook-recorded" {
		t.Fatalf("complete technical evidence produced a gap: %+v", got)
	}
}
