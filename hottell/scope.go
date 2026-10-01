package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Что отправлять и откуда. Решение принимается в хук-режиме до записи в
// буфер, по порядку:
//   1. киллсвитч сессии: env HOTTELL_OFF=1 — молчим;
//   2. skip_events из конфига — событие не шлётся;
//   3. маркер проекта: файл .hottell в каталоге проекта или выше по дереву от
//      cwd события; ближайший выигрывает. {"send": false} — явный отказ
//      при любом scope; {"send_prompts": …} переопределяет конфиг;
//   4. scope: marked (по умолчанию) — шлём только из проектов с маркером,
//      all — отовсюду, кроме явного отказа.
// Пустой или нечитаемый маркер значит send=true.

const markerName = ".hottell"
const killswitchEnv = "HOTTELL_OFF"

type projectMarker struct {
	Send        *bool `json:"send,omitempty"`
	SendPrompts *bool `json:"send_prompts,omitempty"`
}

// findMarker — ближайший маркер вверх от dir; путь пуст, если его нет.
func findMarker(dir string) (string, projectMarker) {
	if dir == "" {
		return "", projectMarker{}
	}
	dir = filepath.Clean(dir)
	for {
		p := filepath.Join(dir, markerName)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			var m projectMarker
			if data, err := os.ReadFile(p); err == nil && len(data) > 0 {
				_ = json.Unmarshal(data, &m)
			}
			return p, m
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", projectMarker{}
		}
		dir = parent
	}
}

type scopeDecision struct {
	Send        bool   `json:"send"`
	SendPrompts bool   `json:"send_prompts"`
	Reason      string `json:"reason"`
	Marker      string `json:"marker,omitempty"`
}

func decideScope(cfg Config, event, cwd string) scopeDecision {
	d := scopeDecision{SendPrompts: cfg.SendPrompts}
	if v := os.Getenv(killswitchEnv); v != "" && v != "0" {
		d.Reason = killswitchEnv + " в окружении сессии"
		return d
	}
	for _, s := range cfg.SkipEvents {
		if s == event {
			d.Reason = "событие в skip_events"
			return d
		}
	}
	marker, m := findMarker(cwd)
	d.Marker = marker
	if m.SendPrompts != nil {
		d.SendPrompts = *m.SendPrompts
	}
	switch {
	case marker != "" && m.Send != nil && !*m.Send:
		d.Reason = "маркер проекта: send=false"
	case marker != "":
		d.Send, d.Reason = true, "проект помечен"
	case cfg.Scope == "all":
		d.Send, d.Reason = true, "scope=all"
	default:
		d.Reason = "scope=marked, у проекта нет маркера " + markerName
	}
	return d
}

// ---------- MCP: project_scope ----------

type projectScopeIn struct {
	Action  string `json:"action" jsonschema:"on, off или status"`
	Path    string `json:"path,omitempty" jsonschema:"корень проекта; по умолчанию текущий каталог MCP-сервера (проект сессии)"`
	Prompts *bool  `json:"prompts,omitempty" jsonschema:"при on: слать ли тексты промптов из этого проекта"`
}

type projectScopeOut struct {
	Project      string        `json:"project"`
	Marker       string        `json:"marker,omitempty"`
	Hooks        scopeDecision `json:"hooks"`
	ClaudeNative string        `json:"claude_native"`
	ClaudeLocal  string        `json:"claude_settings_local"`
	CodexNative  string        `json:"codex_native"`
	Changed      []string      `json:"changed"`
}

const codexScopeNote = "Codex: нативный OTel по проектам невозможен (проектный [otel] Codex игнорирует) — только глобально через otel_configure или профили; хуки Codex фильтруются маркером"

func projectScope(in projectScopeIn) (projectScopeOut, error) {
	root := in.Path
	if root == "" {
		root, _ = os.Getwd()
	}
	root = expandHome(root)
	abs, err := filepath.Abs(root)
	if err != nil {
		return projectScopeOut{}, err
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return projectScopeOut{}, fmt.Errorf("%s: не каталог", abs)
	}
	out := projectScopeOut{Project: abs, CodexNative: codexScopeNote, Changed: []string{},
		ClaudeLocal: filepath.Join(abs, ".claude", "settings.local.json")}
	markerPath := filepath.Join(abs, markerName)

	switch in.Action {
	case "on", "off":
		send := in.Action == "on"
		m := projectMarker{Send: &send}
		if send {
			m.SendPrompts = in.Prompts
		}
		data, _ := json.MarshalIndent(m, "", "  ")
		if err := writeTextFile(markerPath, string(data)+"\n"); err != nil {
			return out, err
		}
		out.Changed = append(out.Changed, markerPath)
		ch, err := setClaudeLocalTelemetry(out.ClaudeLocal, send, in.Prompts)
		if err != nil {
			return out, err
		}
		if ch {
			out.Changed = append(out.Changed, out.ClaudeLocal)
		}
	case "status":
	default:
		return out, fmt.Errorf("action: on, off или status")
	}

	cfg := loadConfig(defaultConfigPath)
	out.Hooks = decideScope(cfg, "", abs)
	out.Marker = out.Hooks.Marker
	out.ClaudeNative = claudeNativeState(out.ClaudeLocal)
	return out, nil
}

// setClaudeLocalTelemetry — нативный поток Claude для одного проекта через
// env в .claude/settings.local.json (он сильнее пользовательского уровня).
// on: включатель и те же OTel-ключи, что в глобальных настройках (или
// умолчания с токеном hottell); off: CLAUDE_CODE_ENABLE_TELEMETRY=0,
// перекрывает глобальное включение.
func setClaudeLocalTelemetry(path string, on bool, prompts *bool) (bool, error) {
	doc, err := readJSONFile(path)
	if err != nil {
		return false, err
	}
	env := envOf(doc)
	if env == nil {
		env = map[string]any{}
	}
	before := mustJSON(env)
	if on {
		global, _ := readJSONFile(expandHome("~/.claude/settings.json"))
		genv := envOf(global)
		if fmt.Sprint(nz(genv["CLAUDE_CODE_ENABLE_TELEMETRY"])) != "1" {
			// глобально выключено — ключи экспорта из умолчаний otel_configure
			genv = map[string]any{}
			if err := applyClaudeOtelDefaults(genv, otelToken()); err != nil {
				return false, err
			}
		}
		for _, k := range claudeOtelKeys {
			if v, ok := genv[k]; ok {
				env[k] = v
			}
		}
		env["CLAUDE_CODE_ENABLE_TELEMETRY"] = "1"
		if prompts != nil {
			if *prompts {
				env["OTEL_LOG_USER_PROMPTS"] = "1"
			} else {
				env["OTEL_LOG_USER_PROMPTS"] = "0"
			}
		}
	} else {
		for _, k := range claudeOtelKeys {
			delete(env, k)
		}
		env["CLAUDE_CODE_ENABLE_TELEMETRY"] = "0"
	}
	if mustJSON(env) == before {
		return false, nil
	}
	doc["env"] = env
	return true, writeJSONFile(path, doc)
}

func claudeNativeState(localPath string) string {
	local, _ := readJSONFile(localPath)
	if v := fmt.Sprint(nz(envOf(local)["CLAUDE_CODE_ENABLE_TELEMETRY"])); v != "" {
		if v == "1" {
			return "включён для проекта (.claude/settings.local.json)"
		}
		return "выключен для проекта (.claude/settings.local.json)"
	}
	global, _ := readJSONFile(expandHome("~/.claude/settings.json"))
	if fmt.Sprint(nz(envOf(global)["CLAUDE_CODE_ENABLE_TELEMETRY"])) == "1" {
		return "наследуется: включён глобально"
	}
	return "наследуется: выключен глобально"
}

func boolp(b bool) *bool { return &b }

func registerScopeTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "project_scope",
		Description: "Включить (on) или выключить (off) отправку телеметрии для проекта одной командой: маркер .hottell для хуков и env в .claude/settings.local.json для нативного потока Claude Code. status — текущее решение. Codex по-проектно только хуками."},
		func(_ context.Context, _ *mcp.CallToolRequest, in projectScopeIn) (*mcp.CallToolResult, projectScopeOut, error) {
			out, err := projectScope(in)
			return nil, out, err
		})
}
