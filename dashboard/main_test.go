package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHookReportUsesOnlyHookServiceAndBindsSessionID(t *testing.T) {
	const id = "session-1"
	requests := 0
	ch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Query().Get("readonly") != "1" {
			t.Error("query is not read-only")
		}
		body, _ := io.ReadAll(r.Body)
		query := string(body)
		if !strings.Contains(query, "ServiceName = 'agent-hooks'") {
			t.Error("hook source filter missing")
		}
		if strings.Contains(query, id) {
			t.Error("session ID interpolated into SQL")
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(query, "GROUP BY session_id") {
			_, _ = w.Write([]byte(`{"data":[{"session_id":"session-1","agent":"codex","first_event":"t1","last_event":"t2","hook_events":2,"prompt_hooks":0,"tool_start_hooks":1,"tool_end_hooks":0,"has_session_start":0,"has_session_end":0}]}`))
		} else {
			if r.URL.Query().Get("param_sid") != id {
				t.Error("session ID parameter missing")
			}
			_, _ = w.Write([]byte(`{"data":[{"at":"t1","name":"agent.hook.PreToolUse","tool":"exec"}]}`))
		}
	}))
	defer ch.Close()
	s := &server{clickhouse: ch.URL, dataDir: t.TempDir(), client: ch.Client()}
	report, err := s.hooks(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || len(report.Events) != 1 || report.Session.HasStart != 0 || report.Kind != "hooks" {
		t.Fatalf("unexpected report: %+v", report)
	}
	if _, err := s.hooks(context.Background(), "../secret"); !os.IsNotExist(err) {
		t.Fatalf("unsafe session ID accepted: %v", err)
	}
}

func TestDeepOnlyReportStaysLocalAndRequiresEvidence(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "deep"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "deep", "session-2.json")
	report := DeepReport{Kind: "deep", SessionID: "session-2", Task: "example", Outcome: "unknown", OutcomeBasis: "no human confirmation", Observations: []Observation{{Pattern: "D05", Finding: "claim without proof", Status: "suspected", Evidence: []string{"turn:3"}}}}
	b, _ := json.Marshal(report)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	s := &server{dataDir: dir}
	got, err := s.deep("session-2")
	if err != nil || got.SessionID != "session-2" {
		t.Fatalf("deep read: %+v, %v", got, err)
	}
	report.Observations[0].Evidence = nil
	b, _ = json.Marshal(report)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.deep("session-2"); err == nil {
		t.Fatal("unsupported observation without evidence")
	}
	if _, err := s.deep(url.PathEscape("../secret")); !os.IsNotExist(err) {
		t.Fatalf("unsafe path accepted: %v", err)
	}
}

func TestRetroTelemetryIsDistinctFromRecordedHooks(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "telemetry"), 0700); err != nil {
		t.Fatal(err)
	}
	id := "session-3"
	report := TelemetryReport{
		Kind: "telemetry", SessionID: id, ThreadID: id, Agent: "codex", SourceURL: "codex://threads/" + id, Availability: "partial",
		SourceCoverage: map[string]SourceCoverage{
			"hook": {Status: "missing", Detail: "no match"}, "otel": {Status: "missing", Detail: "no match"},
			"transcript": {Status: "available", Detail: "local"}, "app": {Status: "available", Detail: "link resolved"},
		},
		Events: []TelemetryEvent{{ID: "L1", Kind: "tool_call", Source: "transcript", Provenance: "reconstructed", Evidence: json.RawMessage(`{"line":1}`)}},
	}
	path := filepath.Join(dir, "telemetry", id+".json")
	write := func() {
		b, _ := json.Marshal(report)
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	s := &server{dataDir: dir}
	if _, err := s.telemetry(id); err != nil {
		t.Fatal(err)
	}
	report.Events[0].Source = "hook"
	write()
	if _, err := s.telemetry(id); err == nil {
		t.Fatal("reconstructed transcript event masqueraded as recorded hook")
	}
}

func TestOTelReportUsesExactSessionIDAndOmitsContent(t *testing.T) {
	const id = "session-otel"
	queries := 0
	now := time.Now().UTC().UnixNano()
	ch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries++
		if r.URL.Query().Get("readonly") != "1" || r.URL.Query().Get("param_sid") != id {
			t.Error("OTel query is not read-only or session bound")
		}
		body, _ := io.ReadAll(r.Body)
		query := string(body)
		if strings.Contains(query, id) || strings.Contains(query, "SELECT Body") {
			t.Error("session ID or raw body exposed in OTel query")
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(query, "toUnixTimestamp64Nano(min(Timestamp))"):
			_, _ = fmt.Fprintf(w, `{"data":[{"events":2,"first_ns":%d,"last_ns":%d,"starts":1,"ends":1}]}`, now-int64(time.Hour), now)
		case strings.Contains(query, "FROM otel.otel_logs"):
			if !strings.Contains(query, "ServiceName != 'agent-hooks'") {
				t.Error("hooks were not excluded")
			}
			if r.URL.Query().Get("param_from") == "" || r.URL.Query().Get("param_until") == "" || !strings.Contains(query, "Timestamp >=") {
				t.Error("OTel logs lack bounded time parameters")
			}
			_, _ = w.Write([]byte(`{"data":[{"service":"node_repl","event_name":"security_check","events":10,"first_event":"t1","last_event":"t2"}]}`))
		case strings.Contains(query, "FROM otel.otel_traces"):
			if !strings.Contains(query, "Timestamp >=") {
				t.Error("traces lack time bounds")
			}
			_, _ = w.Write([]byte(`{"data":[{"count":0}]}`))
		default:
			if !strings.Contains(query, "TimeUnix >=") {
				t.Error("metrics lack time bounds")
			}
			_, _ = w.Write([]byte(`{"data":[{"count":0}]}`))
		}
	}))
	defer ch.Close()
	s := &server{clickhouse: ch.URL, client: ch.Client()}
	report, err := s.otel(context.Background(), id)
	if err != nil || queries != 5 || !report.Queried || report.Coverage != "complete" || report.logCount() != 10 || report.Traces != 0 || report.MetricRecords != 0 {
		t.Fatalf("unexpected OTel report: %+v, queries=%d, err=%v", report, queries, err)
	}
}
