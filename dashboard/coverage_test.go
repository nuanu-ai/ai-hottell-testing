package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoverageRequiresEveryDetectorAndSeparatesUnknownFromClear(t *testing.T) {
	const id = "session-coverage"
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "coverage"), 0700); err != nil {
		t.Fatal(err)
	}
	checks := make([]DetectorCheck, 0, len(detectorCatalogue))
	for _, detector := range detectorCatalogue {
		checks = append(checks, DetectorCheck{ID: detector.ID, Status: "checked_clear", Summary: "Inspected required input", Evidence: []string{"rollout:L1-L10"}})
	}
	checks[6] = DetectorCheck{ID: "D13", Status: "insufficient_data", Summary: "No comparable baseline", MissingData: []string{"task-class baseline"}}
	checks[1] = DetectorCheck{ID: "D03", Status: "not_checked", Summary: "Source calls exist but normalization is incomplete"}
	report := CoverageReport{Kind: "detector_coverage", SchemaVersion: 1, CatalogueVersion: "0.1", SessionID: id, SourceSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Checks: checks}
	path := filepath.Join(dir, "coverage", id+".json")
	write := func() {
		b, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	s := &server{dataDir: dir}
	got, err := s.coverage(id)
	if err != nil || len(got.Checks) != 13 || got.Checks[6].Status != "insufficient_data" || got.Checks[1].Status != "not_checked" {
		t.Fatalf("coverage read: %+v, %v", got, err)
	}
	report.Checks = report.Checks[:12]
	write()
	if _, err := s.coverage(id); err == nil {
		t.Fatal("accepted incomplete detector coverage")
	}
	if _, err := s.coverage("../secret"); !os.IsNotExist(err) {
		t.Fatalf("unsafe session ID accepted: %v", err)
	}
}

func TestCoverageShowsCrossSessionEvidence(t *testing.T) {
	checks := make([]DetectorCheck, 0, len(detectorCatalogue))
	for _, detector := range detectorCatalogue {
		checks = append(checks, DetectorCheck{ID: detector.ID, Status: "not_checked", Summary: "Pending"})
	}
	checks[9].PeerSources = []V2Source{{SessionID: "peer-session", TaskID: "T2", Evidence: []string{"L7"}}}
	var page strings.Builder
	writeCoverageDetail(&page, CoverageReport{SessionID: "local-session", Checks: checks})
	if !strings.Contains(page.String(), `/sessions/peer-session/deep`) || !strings.Contains(page.String(), `T2 · L7`) {
		t.Fatal("cross-session task evidence is missing from coverage detail")
	}
}
