package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	cfg := defaultConfig()
	cfg.SpoolDir = t.TempDir()
	cfg.TokenFile = filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(cfg.TokenFile, []byte("test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.HostName = "test-host"
	return cfg
}

func attrMap(t *testing.T, attrs []any) map[string]any {
	t.Helper()
	out := map[string]any{}
	for _, a := range attrs {
		pair := a.(map[string]any)
		out[pair["key"].(string)] = pair["value"]
	}
	return out
}

// Полный путь: событие -> спул -> дрейнер шлёт OTLP с bearer-токеном -> спул пуст.
func TestEndToEnd(t *testing.T) {
	cfg := testConfig(t)
	var got []byte
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		got = buf
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	cfg.Endpoint = srv.URL
	cfg.LingerSec = 0

	payload := map[string]any{
		"hook_event_name": "PostToolUse",
		"session_id":      "s-1",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "ls"},
	}
	raw, _ := json.Marshal(event{TS: time.Now().UnixNano(), Agent: "codex", Event: "PostToolUse", Payload: payload})
	if err := spoolWrite(cfg, raw); err != nil {
		t.Fatal(err)
	}

	runDrain(cfg)

	if auth != "Bearer test-token" {
		t.Fatalf("токен не дошёл: %q", auth)
	}
	var otlp map[string]any
	if err := json.Unmarshal(got, &otlp); err != nil {
		t.Fatalf("тело не JSON: %v", err)
	}
	rl := otlp["resourceLogs"].([]any)[0].(map[string]any)
	res := attrMap(t, rl["resource"].(map[string]any)["attributes"].([]any))
	if v := res["service.name"].(map[string]any)["stringValue"]; v != "agent-hooks" {
		t.Fatalf("service.name: %v", v)
	}
	rec := rl["scopeLogs"].([]any)[0].(map[string]any)["logRecords"].([]any)[0].(map[string]any)
	if body := rec["body"].(map[string]any)["stringValue"]; body != "agent.hook.PostToolUse" {
		t.Fatalf("body: %v", body)
	}
	attrs := attrMap(t, rec["attributes"].([]any))
	if v := attrs["agent"].(map[string]any)["stringValue"]; v != "codex" {
		t.Fatalf("agent: %v", v)
	}
	// объект ушёл JSON-строкой
	ti := attrs["tool_input"].(map[string]any)["stringValue"].(string)
	if !strings.Contains(ti, `"command":"ls"`) {
		t.Fatalf("tool_input: %v", ti)
	}
	if left := spoolList(cfg); len(left) != 0 {
		t.Fatalf("спул не очищен: %d", len(left))
	}
	state, err := loadDeliveryState(cfg)
	if err != nil || state.Sessions["s-1"].LastAcceptedAt == "" {
		t.Fatalf("collector acceptance not recorded: %+v, %v", state, err)
	}
}

// Недоступный коллектор: события лежат в буфере, ничего не потеряно.
func TestUnavailableKeepsBuffer(t *testing.T) {
	cfg := testConfig(t)
	cfg.Endpoint = "http://127.0.0.1:1" // connection refused
	cfg.LingerSec = 0
	cfg.BackoffMaxSec = 0

	raw, _ := json.Marshal(event{TS: 1, Agent: "claude", Event: "Stop", Payload: map[string]any{"session_id": "session-error"}})
	if err := spoolWrite(cfg, raw); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { runDrain(cfg); close(done) }()
	time.Sleep(300 * time.Millisecond)
	if len(spoolList(cfg)) != 1 {
		t.Fatal("событие пропало при недоступном коллекторе")
	}
	state, err := loadDeliveryState(cfg)
	if err != nil || state.Sessions["session-error"].LastSendFailure == "" {
		t.Fatalf("send failure not recorded: %+v, %v", state, err)
	}
	// дрейнер ретраит — не даём тесту зависнуть, забираем у него работу
	for _, e := range spoolList(cfg) {
		_ = os.Remove(e.path)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("дрейнер не завершился на пустом спуле")
	}
}

func TestCorruptQueuedEventIsCountedAsUnattributedLoss(t *testing.T) {
	cfg := testConfig(t)
	cfg.LingerSec = 0
	if err := os.WriteFile(filepath.Join(cfg.SpoolDir, "0001-broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	runDrain(cfg)
	state, err := loadDeliveryState(cfg)
	if err != nil || state.Unattributed != 1 || len(spoolList(cfg)) != 0 {
		t.Fatalf("corrupt event loss not recorded: %+v, %v", state, err)
	}
}

// Переполнение буфера: дроп старых, свежие остаются.
func TestOverflowDropsOldest(t *testing.T) {
	cfg := testConfig(t)
	cfg.BufferMaxEvents = 3
	for i := 0; i < 5; i++ {
		raw, _ := json.Marshal(event{TS: int64(i), Agent: "codex", Event: fmt.Sprintf("E%d", i), Payload: map[string]any{"session_id": "s-drop"}})
		if err := spoolWrite(cfg, raw); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // разные таймстампы в именах
	}
	if n := spoolEnforce(cfg); n != 2 {
		t.Fatalf("выброшено %d, ожидалось 2", n)
	}
	state, err := loadDeliveryState(cfg)
	if err != nil || state.Sessions["s-drop"].Dropped != 2 {
		t.Fatalf("session overflow not recorded: %+v, %v", state, err)
	}
	left := spoolList(cfg)
	if len(left) != 3 {
		t.Fatalf("осталось %d", len(left))
	}
	var ev event
	raw, _ := os.ReadFile(left[0].path)
	_ = json.Unmarshal(raw, &ev)
	if ev.Event != "E2" {
		t.Fatalf("дропнуты не старые: первым лежит %s", ev.Event)
	}
}

// send_prompts=false: текст промпта не покидает машину.
func TestPromptRedaction(t *testing.T) {
	cfg := testConfig(t)
	cfg.SendPrompts = false
	cfg.Scope = "all"

	r, w, _ := os.Pipe()
	payload := `{"hook_event_name":"UserPromptSubmit","session_id":"s","prompt":"секретный план"}`
	_, _ = w.WriteString(payload)
	w.Close()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	runHook(cfg, "codex")

	entries := spoolList(cfg)
	if len(entries) != 1 {
		t.Fatalf("в спуле %d событий", len(entries))
	}
	raw, _ := os.ReadFile(entries[0].path)
	if strings.Contains(string(raw), "секретный план") {
		t.Fatal("промпт утёк при send_prompts=false")
	}
	if !strings.Contains(string(raw), "UserPromptSubmit") {
		t.Fatal("событие потеряло имя")
	}
}

// Обрезка длинных полей.
func TestTruncation(t *testing.T) {
	cfg := testConfig(t)
	cfg.MaxFieldBytes = 10
	ev := event{TS: 1, Agent: "codex", Event: "PostToolUse",
		Payload: map[string]any{"tool_response": strings.Repeat("x", 100)}}
	rec := buildLogRecord(cfg, ev)
	attrs := attrMap(t, toAny(t, rec["attributes"]))
	got := attrs["tool_response"].(map[string]any)["stringValue"].(string)
	if !strings.HasPrefix(got, "xxxxxxxxxx…") || !strings.Contains(got, "100 байт") {
		t.Fatalf("обрезка: %q", got)
	}
}

func toAny(t *testing.T, v any) []any {
	t.Helper()
	raw, _ := json.Marshal(v)
	var out []any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
