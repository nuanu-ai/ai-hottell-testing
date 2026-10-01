package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOTelUnknownTimeRangeDoesNotReportZero(t *testing.T) {
	queries := 0
	ch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries++
		_, _ = w.Write([]byte(`{"data":[{"events":0,"first_ns":0,"last_ns":0,"starts":0,"ends":0}]}`))
	}))
	defer ch.Close()
	s := &server{clickhouse: ch.URL, dataDir: t.TempDir(), client: ch.Client()}
	_, err := s.otel(context.Background(), "session-without-bounds")
	if !errors.Is(err, errOTelWindowUnknown) || queries != 1 {
		t.Fatalf("unknown source window must not run count queries: queries=%d err=%v", queries, err)
	}
}

func TestOTelOldSessionDoesNotClaimCompleteCoverage(t *testing.T) {
	old := time.Now().UTC().Add(-100 * 24 * time.Hour).UnixNano()
	queries := 0
	ch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries++
		_, _ = fmt.Fprintf(w, `{"data":[{"events":2,"first_ns":%d,"last_ns":%d,"starts":1,"ends":1}]}`, old, old)
	}))
	defer ch.Close()
	s := &server{clickhouse: ch.URL, dataDir: t.TempDir(), client: ch.Client()}
	report, err := s.otel(context.Background(), "old-session")
	if err != nil || queries != 1 || report.Coverage != "partial" || report.Queried {
		t.Fatalf("expired history presented as checked: report=%+v queries=%d err=%v", report, queries, err)
	}
	var html strings.Builder
	writeOTelReport(&html, report)
	if !strings.Contains(html.String(), "не проверено") || strings.Contains(html.String(), "0 логов") {
		t.Fatalf("expired history page implies a zero count: %s", html.String())
	}
}

func TestOTelLongSessionLimitsQueriesToRetainedWindow(t *testing.T) {
	now := time.Now().UTC()
	first := now.Add(-100 * 24 * time.Hour).UnixNano()
	last := now.UnixNano()
	queries := 0
	ch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries++
		query, _ := io.ReadAll(r.Body)
		if queries == 1 {
			_, _ = fmt.Fprintf(w, `{"data":[{"events":2,"first_ns":%d,"last_ns":%d,"starts":1,"ends":1}]}`, first, last)
			return
		}
		from, err := time.Parse("2006-01-02 15:04:05", r.URL.Query().Get("param_from"))
		if err != nil || from.Before(now.Add(-31*24*time.Hour)) || r.URL.Query().Get("param_until") == "" {
			t.Errorf("query not bounded to retained period: %s", r.URL.RawQuery)
		}
		if strings.Contains(string(query), "FROM otel.otel_logs") {
			_, _ = w.Write([]byte(`{"data":[]}`))
		} else {
			_, _ = w.Write([]byte(`{"data":[{"count":0}]}`))
		}
	}))
	defer ch.Close()
	s := &server{clickhouse: ch.URL, dataDir: t.TempDir(), client: ch.Client()}
	report, err := s.otel(context.Background(), "long-session")
	if err != nil || queries != 5 || !report.Queried || report.Coverage != "partial" {
		t.Fatalf("long session coverage incorrect: report=%+v queries=%d err=%v", report, queries, err)
	}
	if findings := technicalFindings(nil, nil, false, &report, true); len(findings) != 0 {
		t.Fatalf("partial zero generated technical findings: %+v", findings)
	}
}

func TestOTelUsesLocalTranscriptPeriodWhenHooksAbsent(t *testing.T) {
	const id = "transcript-only"
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "telemetry"), 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	fixture := fmt.Sprintf(`{"kind":"telemetry","schema_version":1,"session_id":%q,"thread_id":%q,"source_url":%q,"availability":"available","source_coverage":{"hook":{"status":"missing","detail":"x"},"otel":{"status":"missing","detail":"x"},"transcript":{"status":"available","detail":"x"},"app":{"status":"available","detail":"x"}},"period":{"first_recorded_at":%q,"last_recorded_at":%q}}`, id, id, "codex://threads/"+id, now, now)
	if err := os.WriteFile(filepath.Join(dir, "telemetry", id+".json"), []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	queries := 0
	ch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries++
		if queries == 1 {
			_, _ = w.Write([]byte(`{"data":[{"events":0,"first_ns":0,"last_ns":0,"starts":0,"ends":0}]}`))
		} else if queries == 2 {
			_, _ = w.Write([]byte(`{"data":[]}`))
		} else {
			_, _ = w.Write([]byte(`{"data":[{"count":0}]}`))
		}
	}))
	defer ch.Close()
	s := &server{clickhouse: ch.URL, dataDir: dir, client: ch.Client()}
	report, err := s.otel(context.Background(), id)
	if err != nil || queries != 5 || report.Coverage != "complete" || !report.Queried {
		t.Fatalf("transcript period was not used: report=%+v queries=%d err=%v", report, queries, err)
	}
}
