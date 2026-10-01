package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// TelemetryReport is a private, derived index of an old session. Reconstructed
// transcript events are never presented as events captured by Hooks or OTel.
type TelemetryReport struct {
	Kind           string                     `json:"kind"`
	SchemaVersion  int                        `json:"schema_version"`
	SessionID      string                     `json:"session_id"`
	ThreadID       string                     `json:"thread_id"`
	Agent          string                     `json:"agent"`
	SourceURL      string                     `json:"source_url"`
	Availability   string                     `json:"availability"`
	SourceCoverage map[string]SourceCoverage  `json:"source_coverage"`
	Events         []TelemetryEvent           `json:"events"`
	Metrics        map[string]TelemetryMetric `json:"metrics"`
	Gaps           []string                   `json:"gaps"`
	Period         map[string]string          `json:"period"`
	Source         json.RawMessage            `json:"source"`
}

type SourceCoverage struct {
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type TelemetryEvent struct {
	ID         string          `json:"id"`
	At         *string         `json:"at"`
	Kind       string          `json:"kind"`
	Source     string          `json:"source"`
	Provenance string          `json:"provenance"`
	Evidence   json.RawMessage `json:"evidence"`
	Tool       string          `json:"tool,omitempty"`
}

type TelemetryMetric struct {
	Value      json.RawMessage `json:"value"`
	Unit       string          `json:"unit"`
	Source     string          `json:"source"`
	Provenance string          `json:"provenance"`
	Detail     string          `json:"detail,omitempty"`
}

func (s *server) telemetryPath(id string) string {
	return filepath.Join(s.dataDir, "telemetry", id+".json")
}

func (s *server) telemetryExists(id string) bool {
	if !safeID.MatchString(id) {
		return false
	}
	_, err := os.Stat(s.telemetryPath(id))
	return err == nil
}

func (s *server) telemetry(id string) (TelemetryReport, error) {
	if !safeID.MatchString(id) {
		return TelemetryReport{}, os.ErrNotExist
	}
	f, err := os.Open(s.telemetryPath(id))
	if err != nil {
		return TelemetryReport{}, err
	}
	defer f.Close()
	var report TelemetryReport
	dec := json.NewDecoder(io.LimitReader(f, 16<<20))
	if err := dec.Decode(&report); err != nil {
		return TelemetryReport{}, err
	}
	if report.Kind != "telemetry" || report.SessionID != id || report.ThreadID != id || report.SourceURL != "codex://threads/"+id {
		return TelemetryReport{}, fmt.Errorf("invalid telemetry report identity")
	}
	if report.Availability != "available" && report.Availability != "partial" && report.Availability != "unavailable" {
		return TelemetryReport{}, fmt.Errorf("invalid telemetry availability")
	}
	for _, source := range []string{"hook", "otel", "transcript", "app"} {
		coverage, ok := report.SourceCoverage[source]
		if !ok || coverage.Status == "" || coverage.Detail == "" {
			return TelemetryReport{}, fmt.Errorf("missing source coverage: %s", source)
		}
	}
	for _, event := range report.Events {
		if event.ID == "" || event.Kind == "" || len(event.Evidence) == 0 {
			return TelemetryReport{}, fmt.Errorf("event without identity or evidence")
		}
		if event.Source != "hook" && event.Source != "otel" && event.Source != "transcript" && event.Source != "app" {
			return TelemetryReport{}, fmt.Errorf("invalid event source")
		}
		if event.Provenance != "recorded" && event.Provenance != "reconstructed" {
			return TelemetryReport{}, fmt.Errorf("invalid event provenance")
		}
		if (event.Source == "hook" || event.Source == "otel") && event.Provenance == "reconstructed" {
			return TelemetryReport{}, fmt.Errorf("reconstructed event cannot claim hook or otel source")
		}
	}
	return report, nil
}

func (s *server) telemetryAPI(w http.ResponseWriter, r *http.Request) {
	report, err := s.telemetry(r.PathValue("id"))
	if err != nil {
		serveError(w, err)
		return
	}
	jsonResponse(w, report)
}
