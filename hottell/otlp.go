package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// Событие в спуле: конверт, который хук-режим пишет, а дрейнер превращает
// в OTLP log record.
type event struct {
	TS      int64          `json:"ts"` // unix nano
	Agent   string         `json:"agent"`
	Event   string         `json:"event"`
	Payload map[string]any `json:"payload"`
}

type kv struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

func str(s string) map[string]string { return map[string]string{"stringValue": s} }

func truncate(s string, max int) string {
	if max > 0 && len(s) > max {
		return s[:max] + fmt.Sprintf("…[обрезано, всего %d байт]", len(s))
	}
	return s
}

// attrValue — скаляры остаются собой, объекты и массивы уходят JSON-строкой.
func attrValue(v any, maxBytes int) any {
	switch x := v.(type) {
	case string:
		return str(truncate(x, maxBytes))
	case bool:
		return map[string]bool{"boolValue": x}
	case int64: // атрибуты обогащения до записи в спул
		return map[string]string{"intValue": strconv.FormatInt(x, 10)}
	case int:
		return map[string]string{"intValue": strconv.Itoa(x)}
	case float64:
		if x == float64(int64(x)) {
			return map[string]string{"intValue": strconv.FormatInt(int64(x), 10)}
		}
		return map[string]float64{"doubleValue": x}
	case nil:
		return str("")
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return str("")
		}
		return str(truncate(string(raw), maxBytes))
	}
}

// buildLogRecord — плоский маппинг: каждое верхнеуровневое поле payload —
// атрибут с тем же именем, Body — agent.hook.<событие>.
func buildLogRecord(cfg Config, ev event) map[string]any {
	attrs := []kv{{Key: "agent", Value: str(ev.Agent)}}
	keys := make([]string, 0, len(ev.Payload))
	for k := range ev.Payload {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		attrs = append(attrs, kv{Key: k, Value: attrValue(ev.Payload[k], cfg.MaxFieldBytes)})
	}
	return map[string]any{
		"timeUnixNano":   strconv.FormatInt(ev.TS, 10),
		"severityNumber": 9,
		"severityText":   "INFO",
		"body":           str("agent.hook." + ev.Event),
		"attributes":     attrs,
	}
}

func buildOTLP(cfg Config, events []event) []byte {
	records := make([]map[string]any, 0, len(events))
	for _, ev := range events {
		records = append(records, buildLogRecord(cfg, ev))
	}
	return otlpPayload(cfg, cfg.ServiceName, records)
}

// otlpPayload — один ресурс, один scope, готовые log records.
func otlpPayload(cfg Config, service string, records []map[string]any) []byte {
	payload := map[string]any{
		"resourceLogs": []map[string]any{{
			"resource": map[string]any{"attributes": []kv{
				{Key: "service.name", Value: str(service)},
				{Key: "host.name", Value: str(cfg.HostName)},
				{Key: "deployment.environment", Value: str(cfg.Environment)},
			}},
			"scopeLogs": []map[string]any{{
				"scope":      map[string]any{"name": "hottell", "version": versionString()},
				"logRecords": records,
			}},
		}},
	}
	data, _ := json.Marshal(payload)
	return data
}

func sendOTLP(cfg Config, body []byte) error {
	req, err := http.NewRequest(http.MethodPost, cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if tok := cfg.token(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := &http.Client{Timeout: time.Duration(cfg.SendTimeoutSec) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("отправка: HTTP %d", resp.StatusCode)
	}
	return nil
}
