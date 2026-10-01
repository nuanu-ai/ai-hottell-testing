package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DeliveryReport is local collector diagnostics, not a claim that an event
// reached ClickHouse. It never returns queued event bodies or credentials.
type DeliveryReport struct {
	Kind                  string `json:"kind"`
	SessionID             string `json:"session_id"`
	QueueAvailable        bool   `json:"queue_available"`
	QueuedEvents          int    `json:"queued_events"`
	QueuedBytes           int64  `json:"queued_bytes"`
	OldestQueuedAt        string `json:"oldest_queued_at,omitempty"`
	DiagnosticsAvailable  bool   `json:"diagnostics_available"`
	DiagnosticsUpdatedAt  string `json:"diagnostics_updated_at,omitempty"`
	DroppedEvents         uint64 `json:"dropped_events"`
	LastDroppedAt         string `json:"last_dropped_at,omitempty"`
	LastSendFailureAt     string `json:"last_send_failure_at,omitempty"`
	LastCollectorAcceptAt string `json:"last_collector_accept_at,omitempty"`
	UnattributedDropped   uint64 `json:"unattributed_dropped"`
	UnreadableQueued      int    `json:"unreadable_queued"`
}

type collectorConfig struct {
	SpoolDir string `json:"spool_dir"`
}

func expandCollectorHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func (s *server) collectorSpoolDir() (string, error) {
	if s.collectorConfig == "" {
		return "", errors.New("collector config is not set")
	}
	config := collectorConfig{SpoolDir: "~/.local/state/hottell/spool"}
	raw, err := os.ReadFile(expandCollectorHome(s.collectorConfig))
	if err == nil {
		if err := json.Unmarshal(raw, &config); err != nil {
			return "", fmt.Errorf("collector config: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if config.SpoolDir == "" {
		return "", errors.New("collector spool is not configured")
	}
	return expandCollectorHome(config.SpoolDir), nil
}

func (s *server) delivery(id string) (DeliveryReport, error) {
	if !safeID.MatchString(id) {
		return DeliveryReport{}, os.ErrNotExist
	}
	report := DeliveryReport{Kind: "collector_delivery", SessionID: id}
	spool, err := s.collectorSpoolDir()
	if err != nil {
		return report, nil
	}
	entries, err := os.ReadDir(spool)
	if err != nil {
		return report, nil
	}
	report.QueueAvailable = true
	var oldest time.Time
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		f, err := os.Open(filepath.Join(spool, entry.Name()))
		if err != nil {
			report.UnreadableQueued++
			continue
		}
		var ev struct {
			TS      int64 `json:"ts"`
			Payload struct {
				SessionID string `json:"session_id"`
			} `json:"payload"`
		}
		err = json.NewDecoder(io.LimitReader(f, 21<<20)).Decode(&ev)
		_ = f.Close()
		if err != nil {
			report.UnreadableQueued++
			continue
		}
		if ev.Payload.SessionID != id {
			continue
		}
		report.QueuedEvents++
		if info, err := entry.Info(); err == nil {
			report.QueuedBytes += info.Size()
		}
		if ev.TS > 0 {
			at := time.Unix(0, ev.TS).UTC()
			if oldest.IsZero() || at.Before(oldest) {
				oldest = at
			}
		}
	}
	if !oldest.IsZero() {
		report.OldestQueuedAt = oldest.Format(time.RFC3339Nano)
	}
	raw, err := os.ReadFile(filepath.Join(spool, ".state", "delivery.json"))
	if err != nil {
		return report, nil
	}
	var state struct {
		SchemaVersion int    `json:"schema_version"`
		UpdatedAt     string `json:"updated_at"`
		Sessions      map[string]struct {
			Dropped         uint64 `json:"dropped"`
			LastDroppedAt   string `json:"last_dropped_at"`
			LastSendFailure string `json:"last_send_failure_at"`
			LastAcceptedAt  string `json:"last_collector_accept_at"`
		} `json:"sessions"`
		Unattributed uint64 `json:"unattributed_dropped"`
	}
	if json.Unmarshal(raw, &state) != nil || state.SchemaVersion != 1 || state.Sessions == nil || state.UpdatedAt == "" {
		return report, nil
	}
	report.DiagnosticsAvailable = true
	report.DiagnosticsUpdatedAt = state.UpdatedAt
	item := state.Sessions[id]
	report.DroppedEvents = item.Dropped
	report.LastDroppedAt = item.LastDroppedAt
	report.LastSendFailureAt = item.LastSendFailure
	report.LastCollectorAcceptAt = item.LastAcceptedAt
	report.UnattributedDropped = state.Unattributed
	return report, nil
}

func (r DeliveryReport) hasEvidence() bool {
	return r.QueuedEvents > 0 || r.DroppedEvents > 0 || r.LastSendFailureAt != "" || r.LastCollectorAcceptAt != ""
}

func (s *server) deliveryAPI(w http.ResponseWriter, r *http.Request) {
	report, err := s.delivery(r.PathValue("id"))
	if err != nil {
		serveError(w, err)
		return
	}
	jsonResponse(w, report)
}
