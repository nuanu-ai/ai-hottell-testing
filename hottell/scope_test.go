package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func feedHook(t *testing.T, cfg Config, payload string) int {
	t.Helper()
	r, w, _ := os.Pipe()
	w.WriteString(payload)
	w.Close()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()
	before := len(spoolList(cfg))
	runHook(cfg, "claude")
	return len(spoolList(cfg)) - before
}

// Хук-режим: фильтр до записи в буфер — маркер вверх от cwd, scope,
// киллсвитч, skip_events, оверрайд send_prompts из маркера.
func TestHookScope(t *testing.T) {
	cfg := testConfig(t)
	cfg.LingerSec = 0
	cfg.Endpoint = "http://127.0.0.1:1" // дрейнер не должен ничего успеть
	root := t.TempDir()
	marked := filepath.Join(root, "work", "proj", "sub")
	plain := filepath.Join(root, "other")
	os.MkdirAll(marked, 0o755)
	os.MkdirAll(plain, 0o755)
	os.WriteFile(filepath.Join(root, "work", markerName), nil, 0o644) // пустой маркер = send

	ev := func(cwd, name string) string {
		return `{"hook_event_name":"` + name + `","session_id":"s","cwd":"` + cwd + `","prompt":"секрет"}`
	}
	if cfg.Scope != "marked" {
		t.Fatalf("scope по умолчанию: %q", cfg.Scope)
	}
	if n := feedHook(t, cfg, ev(marked, "Stop")); n != 1 {
		t.Fatalf("помеченный проект (маркер выше cwd): %d", n)
	}
	if n := feedHook(t, cfg, ev(plain, "Stop")); n != 0 {
		t.Fatalf("непомеченный при scope=marked: %d", n)
	}
	all := cfg
	all.Scope = "all"
	if n := feedHook(t, all, ev(plain, "Stop")); n != 1 {
		t.Fatalf("scope=all: %d", n)
	}

	skip := cfg
	skip.SkipEvents = []string{"PreToolUse"}
	if n := feedHook(t, skip, ev(marked, "PreToolUse")); n != 0 {
		t.Fatalf("skip_events: %d", n)
	}

	t.Setenv(killswitchEnv, "1")
	if n := feedHook(t, all, ev(marked, "Stop")); n != 0 {
		t.Fatalf("киллсвитч: %d", n)
	}
	t.Setenv(killswitchEnv, "")

	// ближайший маркер: send=false в подпроекте перекрывает маркер выше
	os.WriteFile(filepath.Join(root, "work", "proj", markerName), []byte(`{"send": false}`), 0o644)
	if n := feedHook(t, all, ev(marked, "Stop")); n != 0 {
		t.Fatalf("send=false при scope=all: %d", n)
	}
	// send_prompts=false из маркера
	os.WriteFile(filepath.Join(root, "work", "proj", markerName), []byte(`{"send_prompts": false}`), 0o644)
	for _, e := range spoolList(cfg) {
		os.Remove(e.path)
	}
	if n := feedHook(t, cfg, ev(marked, "UserPromptSubmit")); n != 1 {
		t.Fatalf("маркер с send_prompts: %d", n)
	}
	raw, _ := os.ReadFile(spoolList(cfg)[0].path)
	if strings.Contains(string(raw), "секрет") {
		t.Fatal("промпт утёк при send_prompts=false в маркере")
	}
}

func TestProjectScopeTool(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, ".config/hottell"), 0o700)
	os.WriteFile(filepath.Join(home, ".config/hottell/token"), []byte("TOK\n"), 0o600)
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude/settings.json"), []byte(`{"env":{"CLAUDE_CODE_ENABLE_TELEMETRY":"1","OTEL_EXPORTER_OTLP_ENDPOINT":"https://g.example","OTEL_EXPORTER_OTLP_HEADERS":"Authorization=Bearer G"}}`), 0o600)
	proj := filepath.Join(home, "proj")
	os.MkdirAll(filepath.Join(proj, ".claude"), 0o755)
	local := filepath.Join(proj, ".claude/settings.local.json")
	os.WriteFile(local, []byte(`{"permissions":{"allow":["Bash(ls)"]}}`), 0o600)

	var out projectScopeOut
	callTool(t, "project_scope", map[string]any{"action": "status", "path": proj}, &out)
	if out.Hooks.Send || !strings.Contains(out.ClaudeNative, "глобально") || !strings.Contains(out.CodexNative, "Codex") {
		t.Fatalf("status до: %+v", out)
	}

	callTool(t, "project_scope", map[string]any{"action": "on", "path": proj, "prompts": false}, &out)
	doc, _ := readJSONFile(local)
	env := envOf(doc)
	if !out.Hooks.Send || out.Hooks.SendPrompts || env["CLAUDE_CODE_ENABLE_TELEMETRY"] != "1" ||
		env["OTEL_EXPORTER_OTLP_ENDPOINT"] != "https://g.example" || env["OTEL_LOG_USER_PROMPTS"] != "0" || doc["permissions"] == nil {
		t.Fatalf("on: %+v / %v", out, doc)
	}

	callTool(t, "project_scope", map[string]any{"action": "off", "path": proj}, &out)
	doc, _ = readJSONFile(local)
	if out.Hooks.Send || envOf(doc)["CLAUDE_CODE_ENABLE_TELEMETRY"] != "0" || len(envOf(doc)) != 1 ||
		!strings.Contains(out.ClaudeNative, "выключен для проекта") || doc["permissions"] == nil {
		t.Fatalf("off: %+v / %v", out, doc)
	}
}
