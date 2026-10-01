package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeliveryReportSeparatesQueueLossAndCollectorReceipt(t *testing.T) {
	root := t.TempDir()
	spool := filepath.Join(root, "spool")
	if err := os.MkdirAll(filepath.Join(spool, ".state"), 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.json")
	config, _ := json.Marshal(map[string]string{"spool_dir": spool})
	if err := os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"1.json": "target", "2.json": "other"} {
		queued, _ := json.Marshal(map[string]any{"ts": int64(1234), "payload": map[string]string{"session_id": id, "prompt": "private prompt"}})
		if err := os.WriteFile(filepath.Join(spool, name), queued, 0600); err != nil {
			t.Fatal(err)
		}
	}
	s := &server{collectorConfig: configPath}
	report, err := s.delivery("target")
	if err != nil || !report.QueueAvailable || report.QueuedEvents != 1 || report.DiagnosticsAvailable {
		t.Fatalf("queue without diagnostics: %+v, %v", report, err)
	}
	state := `{"schema_version":1,"updated_at":"2026-09-30T03:00:00Z","sessions":{"target":{"dropped":2,"last_dropped_at":"2026-09-30T01:00:00Z","last_send_failure_at":"2026-09-30T02:00:00Z","last_collector_accept_at":"2026-09-30T03:00:00Z"}},"unattributed_dropped":1}`
	if err := os.WriteFile(filepath.Join(spool, ".state", "delivery.json"), []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	report, err = s.delivery("target")
	if err != nil || !report.DiagnosticsAvailable || report.DroppedEvents != 2 || report.UnattributedDropped != 1 || report.LastSendFailureAt == "" {
		t.Fatalf("diagnostics omitted: %+v, %v", report, err)
	}
	var html strings.Builder
	writeDeliveryReport(&html, report)
	if !strings.Contains(html.String(), "2.") || !strings.Contains(html.String(), "Это ещё не доказательство записи в ClickHouse") {
		t.Fatalf("page overstates receipt or hides loss: %s", html.String())
	}
	req := httptest.NewRequest("GET", "/api/reports/delivery/target", nil)
	req.SetPathValue("id", "target")
	w := httptest.NewRecorder()
	s.deliveryAPI(w, req)
	if w.Code != 200 || strings.Contains(w.Body.String(), "private prompt") || !strings.Contains(w.Body.String(), `"queued_events":1`) {
		t.Fatalf("private queue data or count wrong: %d %s", w.Code, w.Body.String())
	}
}

func TestDeliveryReportDoesNotClaimHealthyWhenSpoolUnavailable(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	config, _ := json.Marshal(map[string]string{"spool_dir": filepath.Join(root, "missing-spool")})
	if err := os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	s := &server{collectorConfig: configPath}
	report, err := s.delivery("target")
	if err != nil || report.QueueAvailable || report.DiagnosticsAvailable || report.hasEvidence() {
		t.Fatalf("missing queue presented as observed: %+v, %v", report, err)
	}
	if _, err := s.delivery("../target"); !os.IsNotExist(err) {
		t.Fatalf("unsafe ID accepted: %v", err)
	}
}
