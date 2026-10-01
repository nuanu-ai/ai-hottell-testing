package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Нативный OTel агентов — чтение и правка их собственной конфигурации.
//   Claude Code: блок env в ~/.claude/settings.json — только ключи из
//                claudeOtelKeys, остальное env не трогается.
//   Codex:       семейство [otel] в ~/.codex/config.toml, только глобальный
//                уровень: проектный [otel] Codex игнорирует намеренно.
// Секреты (заголовки с токеном) наружу не отдаются: status сообщает только,
// заданы ли они. Токен для заголовков берётся из токена hottell.

var claudeOtelKeys = []string{
	"CLAUDE_CODE_ENABLE_TELEMETRY",
	"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_EXPORTER_OTLP_HEADERS",
	"OTEL_LOGS_EXPORTER", "OTEL_METRICS_EXPORTER",
	"OTEL_LOGS_EXPORT_INTERVAL", "OTEL_METRIC_EXPORT_INTERVAL",
	"OTEL_LOG_TOOL_DETAILS", "OTEL_LOG_USER_PROMPTS", "OTEL_RESOURCE_ATTRIBUTES",
}

const defaultOtelBase = "https://otel.alva.dev"

type claudeOtelState struct {
	Enabled            bool   `json:"enabled"`
	Endpoint           string `json:"endpoint,omitempty"`
	Protocol           string `json:"protocol,omitempty"`
	HeadersSet         bool   `json:"headers_set"`
	LogsExporter       string `json:"logs_exporter,omitempty"`
	MetricsExporter    string `json:"metrics_exporter,omitempty"`
	LogsIntervalMs     string `json:"logs_interval_ms,omitempty"`
	MetricsIntervalMs  string `json:"metrics_interval_ms,omitempty"`
	Prompts            bool   `json:"prompts"`
	ToolDetails        bool   `json:"tool_details"`
	ResourceAttributes string `json:"resource_attributes,omitempty"`
	Path               string `json:"path"`
}

type codexOtelState struct {
	Enabled         bool   `json:"enabled"`
	Environment     string `json:"environment,omitempty"`
	Prompts         bool   `json:"prompts"`
	LogsEndpoint    string `json:"logs_endpoint,omitempty"`
	MetricsEndpoint string `json:"metrics_endpoint,omitempty"`
	TracesEndpoint  string `json:"traces_endpoint,omitempty"`
	Protocol        string `json:"protocol,omitempty"`
	HeadersSet      bool   `json:"headers_set"`
	Path            string `json:"path"`
	Note            string `json:"note"`
}

type otelStatusOut struct {
	Claude claudeOtelState `json:"claude"`
	Codex  codexOtelState  `json:"codex"`
}

func envOf(doc map[string]any) map[string]any {
	env, _ := doc["env"].(map[string]any)
	return env
}

func claudeOtelStatus(path string) (claudeOtelState, error) {
	st := claudeOtelState{Path: path}
	doc, err := readJSONFile(path)
	if err != nil {
		return st, err
	}
	env := envOf(doc)
	get := func(k string) string { return fmt.Sprint(nz(env[k])) }
	st.Enabled = get("CLAUDE_CODE_ENABLE_TELEMETRY") == "1"
	st.Endpoint = get("OTEL_EXPORTER_OTLP_ENDPOINT")
	st.Protocol = get("OTEL_EXPORTER_OTLP_PROTOCOL")
	st.HeadersSet = get("OTEL_EXPORTER_OTLP_HEADERS") != ""
	st.LogsExporter = get("OTEL_LOGS_EXPORTER")
	st.MetricsExporter = get("OTEL_METRICS_EXPORTER")
	st.LogsIntervalMs = get("OTEL_LOGS_EXPORT_INTERVAL")
	st.MetricsIntervalMs = get("OTEL_METRIC_EXPORT_INTERVAL")
	st.Prompts = get("OTEL_LOG_USER_PROMPTS") == "1"
	st.ToolDetails = get("OTEL_LOG_TOOL_DETAILS") == "1"
	st.ResourceAttributes = get("OTEL_RESOURCE_ATTRIBUTES")
	return st, nil
}

func nz(v any) any {
	if v == nil {
		return ""
	}
	return v
}

const codexOtelNote = "только глобальный ~/.codex/config.toml: проектный [otel] Codex игнорирует"

func codexOtelStatus(path string) (codexOtelState, error) {
	st := codexOtelState{Path: path, Note: codexOtelNote}
	text, err := readTextFile(path)
	if err != nil {
		return st, err
	}
	fam := tomlFamily(text, "otel")
	if fam == "" {
		return st, nil
	}
	st.Environment, _ = tomlValue(fam, "otel", "environment")
	p, _ := tomlValue(fam, "otel", "log_user_prompt")
	st.Prompts = p == "true"
	st.LogsEndpoint, _ = tomlValue(fam, "otel.exporter.otlp-http", "endpoint")
	st.MetricsEndpoint, _ = tomlValue(fam, "otel.metrics_exporter.otlp-http", "endpoint")
	st.TracesEndpoint, _ = tomlValue(fam, "otel.trace_exporter.otlp-http", "endpoint")
	st.Protocol, _ = tomlValue(fam, "otel.exporter.otlp-http", "protocol")
	h, _ := tomlValue(fam, "otel.exporter.otlp-http", "headers")
	st.HeadersSet = h != ""
	st.Enabled = st.LogsEndpoint != "" || st.MetricsEndpoint != "" || st.TracesEndpoint != ""
	return st, nil
}

type otelConfigureIn struct {
	Agent    string `json:"agent" jsonschema:"claude, codex или both"`
	Enabled  *bool  `json:"enabled,omitempty" jsonschema:"включить (true) или выключить (false); не задано — не менять"`
	Endpoint string `json:"endpoint,omitempty" jsonschema:"базовый URL приёмника без /v1/..., например https://otel.alva.dev"`
	Prompts  *bool  `json:"prompts,omitempty" jsonschema:"слать ли тексты промптов"`
}

type otelConfigureOut struct {
	Changed []string      `json:"changed"`
	Status  otelStatusOut `json:"status"`
}

// otelToken — bearer-токен приёмника: тот же, что у hottell.
func otelToken() string {
	return loadConfig(defaultConfigPath).token()
}

func configureClaudeOtel(path string, in otelConfigureIn, token string) (bool, error) {
	doc, err := readJSONFile(path)
	if err != nil {
		return false, err
	}
	env := envOf(doc)
	if env == nil {
		env = map[string]any{}
	}
	before := mustJSON(env)
	enabled := fmt.Sprint(nz(env["CLAUDE_CODE_ENABLE_TELEMETRY"])) == "1"
	if in.Enabled != nil && !*in.Enabled {
		for _, k := range claudeOtelKeys {
			delete(env, k)
		}
		enabled = false
	} else if in.Enabled != nil && *in.Enabled {
		if err := applyClaudeOtelDefaults(env, token); err != nil {
			return false, err
		}
		enabled = true
	}
	if enabled {
		if in.Endpoint != "" {
			env["OTEL_EXPORTER_OTLP_ENDPOINT"] = strings.TrimRight(in.Endpoint, "/")
		}
		if in.Prompts != nil {
			if *in.Prompts {
				env["OTEL_LOG_USER_PROMPTS"] = "1"
			} else {
				delete(env, "OTEL_LOG_USER_PROMPTS")
			}
		}
	} else if in.Endpoint != "" || in.Prompts != nil {
		return false, fmt.Errorf("телеметрия Claude Code выключена: задай enabled=true вместе с endpoint/prompts")
	}
	if mustJSON(env) == before {
		return false, nil
	}
	if len(env) == 0 {
		delete(doc, "env")
	} else {
		doc["env"] = env
	}
	return true, writeJSONFile(path, doc)
}

// applyClaudeOtelDefaults — включатель и недостающие ключи экспорта;
// заданные значения не перетираются.
func applyClaudeOtelDefaults(env map[string]any, token string) error {
	defaults := map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": defaultOtelBase,
		"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
		"OTEL_LOGS_EXPORTER":          "otlp",
		"OTEL_METRICS_EXPORTER":       "otlp",
		"OTEL_LOGS_EXPORT_INTERVAL":   "5000",
		"OTEL_METRIC_EXPORT_INTERVAL": "30000",
		"OTEL_LOG_TOOL_DETAILS":       "1",
	}
	env["CLAUDE_CODE_ENABLE_TELEMETRY"] = "1"
	for k, v := range defaults {
		if fmt.Sprint(nz(env[k])) == "" {
			env[k] = v
		}
	}
	if fmt.Sprint(nz(env["OTEL_EXPORTER_OTLP_HEADERS"])) == "" {
		if token == "" {
			return fmt.Errorf("нет токена hottell (~/.config/hottell/token) для заголовка Authorization")
		}
		env["OTEL_EXPORTER_OTLP_HEADERS"] = "Authorization=Bearer " + token
	}
	if fmt.Sprint(nz(env["OTEL_RESOURCE_ATTRIBUTES"])) == "" {
		env["OTEL_RESOURCE_ATTRIBUTES"] = "host.name=" + loadConfig(defaultConfigPath).HostName + ",deployment.environment=lab"
	}
	return nil
}

func codexOtelBlock(env string, prompts bool, base, headers string) string {
	base = strings.TrimRight(base, "/")
	var b strings.Builder
	fmt.Fprintf(&b, "[otel]\nenvironment = %s\nlog_user_prompt = %t\n", tomlQuote(env), prompts)
	for _, e := range []struct{ table, path string }{
		{"otel.exporter.otlp-http", "/v1/logs"},
		{"otel.metrics_exporter.otlp-http", "/v1/metrics"},
		{"otel.trace_exporter.otlp-http", "/v1/traces"},
	} {
		fmt.Fprintf(&b, "\n[%s]\nendpoint = %s\nprotocol = \"binary\"\n", e.table, tomlQuote(base+e.path))
		if headers != "" {
			fmt.Fprintf(&b, "headers = %s\n", headers)
		}
	}
	return b.String()
}

func configureCodexOtel(path string, in otelConfigureIn, token string) (bool, error) {
	text, err := readTextFile(path)
	if err != nil {
		return false, err
	}
	cur, _ := codexOtelStatus(path)
	if in.Enabled != nil && !*in.Enabled {
		out, ok := tomlRemoveFamily(text, "otel")
		if !ok {
			return false, nil
		}
		return true, writeTextFile(path, out)
	}
	if !cur.Enabled && (in.Enabled == nil || !*in.Enabled) {
		if in.Endpoint != "" || in.Prompts != nil {
			return false, fmt.Errorf("телеметрия Codex выключена: задай enabled=true вместе с endpoint/prompts")
		}
		return false, nil
	}
	fam := tomlFamily(text, "otel")
	env := firstNonEmpty(cur.Environment, "lab")
	prompts := cur.Prompts
	if in.Prompts != nil {
		prompts = *in.Prompts
	}
	base := strings.TrimSuffix(cur.LogsEndpoint, "/v1/logs")
	if in.Endpoint != "" {
		base = in.Endpoint
	}
	if base == "" {
		base = defaultOtelBase
	}
	headers, _ := tomlValue(fam, "otel.exporter.otlp-http", "headers")
	if headers == "" {
		if token == "" {
			return false, fmt.Errorf("нет токена hottell (~/.config/hottell/token) для заголовка Authorization")
		}
		headers = "{ Authorization = " + tomlQuote("Bearer "+token) + " }"
	}
	block := codexOtelBlock(env, prompts, base, headers)
	if strings.TrimSpace(fam) == strings.TrimSpace(block) {
		return false, nil
	}
	return true, writeTextFile(path, tomlSetFamily(text, "otel", block))
}

// otelPaths — места конфигов; подменяются в тестах через HOME.
func otelPaths() (claude, codex string) {
	return expandHome("~/.claude/settings.json"), expandHome("~/.codex/config.toml")
}

func otelStatusBoth() (otelStatusOut, error) {
	cp, xp := otelPaths()
	var out otelStatusOut
	var err error
	if out.Claude, err = claudeOtelStatus(cp); err != nil {
		return out, err
	}
	out.Codex, err = codexOtelStatus(xp)
	return out, err
}

func registerOtelTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "otel_status",
		Description: "Текущая нативная OTel-конфигурация Claude Code (env в ~/.claude/settings.json) и Codex ([otel] в ~/.codex/config.toml): вкл/выкл, endpoint, протокол, интервалы, prompts, resource-атрибуты. Только чтение; секреты не показываются."},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, otelStatusOut, error) {
			out, err := otelStatusBoth()
			return nil, out, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "otel_configure",
		Description: "Включить/выключить нативный OTel агента или поменять endpoint и отправку промптов. Правит только свои ключи, без бэкапов, отказ на битом конфиге. Заголовок авторизации — токен hottell. Действует на новые сессии агента."},
		func(_ context.Context, _ *mcp.CallToolRequest, in otelConfigureIn) (*mcp.CallToolResult, otelConfigureOut, error) {
			out := otelConfigureOut{Changed: []string{}}
			cp, xp := otelPaths()
			tok := otelToken()
			agents := map[string]bool{"claude": in.Agent == "claude" || in.Agent == "both", "codex": in.Agent == "codex" || in.Agent == "both"}
			if !agents["claude"] && !agents["codex"] {
				return nil, out, fmt.Errorf("agent: claude, codex или both")
			}
			if agents["claude"] {
				ch, err := configureClaudeOtel(cp, in, tok)
				if err != nil {
					return nil, out, fmt.Errorf("claude: %w", err)
				}
				if ch {
					out.Changed = append(out.Changed, cp)
				}
			}
			if agents["codex"] {
				ch, err := configureCodexOtel(xp, in, tok)
				if err != nil {
					return nil, out, fmt.Errorf("codex: %w", err)
				}
				if ch {
					out.Changed = append(out.Changed, xp)
				}
			}
			st, err := otelStatusBoth()
			out.Status = st
			return nil, out, err
		})
}

func mustJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
