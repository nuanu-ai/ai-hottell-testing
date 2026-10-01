package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// The drainer is the only writer. This file contains delivery diagnostics,
// never event bodies, prompts or credentials. An HTTP 2xx only means that the
// collector accepted the request; ClickHouse receipt is checked separately.
type deliveryState struct {
	SchemaVersion int                             `json:"schema_version"`
	UpdatedAt     string                          `json:"updated_at"`
	Sessions      map[string]sessionDeliveryState `json:"sessions"`
	Unattributed  uint64                          `json:"unattributed_dropped"`
}

type sessionDeliveryState struct {
	Dropped         uint64 `json:"dropped"`
	LastDroppedAt   string `json:"last_dropped_at,omitempty"`
	LastSendFailure string `json:"last_send_failure_at,omitempty"`
	LastAcceptedAt  string `json:"last_collector_accept_at,omitempty"`
}

func deliveryStatePath(cfg Config) string {
	return filepath.Join(cfg.SpoolDir, ".state", "delivery.json")
}

func eventSessionID(ev event) string {
	id, _ := ev.Payload["session_id"].(string)
	return id
}

func loadDeliveryState(cfg Config) (deliveryState, error) {
	state := deliveryState{SchemaVersion: 1, Sessions: map[string]sessionDeliveryState{}}
	data, err := os.ReadFile(deliveryStatePath(cfg))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return deliveryState{}, err
	}
	if err := json.Unmarshal(data, &state); err != nil || state.SchemaVersion != 1 || state.Sessions == nil {
		return deliveryState{}, errors.New("invalid delivery state")
	}
	return state, nil
}

func updateDeliveryState(cfg Config, events []event, outcome string) {
	state, err := loadDeliveryState(cfg)
	if err != nil {
		cfg.debugf("delivery state: %v", err)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, ev := range events {
		id := eventSessionID(ev)
		if id == "" {
			if outcome == "dropped" {
				state.Unattributed++
			}
			continue
		}
		item := state.Sessions[id]
		switch outcome {
		case "dropped":
			item.Dropped++
			item.LastDroppedAt = now
		case "send_failure":
			item.LastSendFailure = now
		case "collector_accept":
			item.LastAcceptedAt = now
		}
		state.Sessions[id] = item
	}
	state.UpdatedAt = now
	path := deliveryStatePath(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		cfg.debugf("delivery state: %v", err)
		return
	}
	data, err := json.Marshal(state)
	if err != nil {
		cfg.debugf("delivery state: %v", err)
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".delivery-*")
	if err != nil {
		cfg.debugf("delivery state: %v", err)
		return
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(data)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		cfg.debugf("delivery state: %v", err)
	}
}
