package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Реальная форма config.toml: чужие таблицы, комментарии, inline-таблицы,
// заголовки с кавычками, [otel] с подтаблицами в конце файла.
const codexRealShape = `model = "gpt-6-astra"
approval_policy = "never"

[mcp_servers.node_repl.env]
CODEX_HOME = "/Users/a/.codex"

[projects."/Users/a/Documents/Страховка на машину"]
trust_level = "trusted"

[hooks.state."/Users/a/.codex/hooks.json:pre_tool_use:0:0"]
trusted_hash = "sha256:2a22"

# --- otel-lab: телеметрия агентов ---
[otel]
environment = "lab"
log_user_prompt = false

[otel.exporter.otlp-http]
endpoint = "https://otel.alva.dev/v1/logs"
protocol = "binary"
headers = { Authorization = "Bearer SECRET" }

[otel.metrics_exporter.otlp-http]
endpoint = "https://otel.alva.dev/v1/metrics"
protocol = "binary"
headers = { Authorization = "Bearer SECRET" }

[otel.trace_exporter.otlp-http]
endpoint = "https://otel.alva.dev/v1/traces"
protocol = "binary"
headers = { Authorization = "Bearer SECRET" }
`

func otelHome(t *testing.T) (claude, codex string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	claude, codex = otelPaths()
	os.MkdirAll(filepath.Dir(claude), 0o755)
	os.MkdirAll(filepath.Dir(codex), 0o755)
	os.MkdirAll(filepath.Join(home, ".config/hottell"), 0o700)
	os.WriteFile(filepath.Join(home, ".config/hottell/token"), []byte("TOK\n"), 0o600)
	return
}

func TestOtelStatusCodexAndClaude(t *testing.T) {
	cp, xp := otelHome(t)
	os.WriteFile(xp, []byte(codexRealShape), 0o600)
	os.WriteFile(cp, []byte(`{"model":"opus","env":{"FOO":"bar","CLAUDE_CODE_ENABLE_TELEMETRY":"1","OTEL_EXPORTER_OTLP_ENDPOINT":"https://otel.alva.dev","OTEL_EXPORTER_OTLP_HEADERS":"Authorization=Bearer SECRET","OTEL_LOG_TOOL_DETAILS":"1"}}`), 0o600)
	var st otelStatusOut
	res := callTool(t, "otel_status", nil, &st)
	if !st.Codex.Enabled || st.Codex.Prompts || st.Codex.LogsEndpoint != "https://otel.alva.dev/v1/logs" || !st.Codex.HeadersSet || st.Codex.Environment != "lab" {
		t.Fatalf("codex: %+v", st.Codex)
	}
	if !st.Claude.Enabled || !st.Claude.ToolDetails || st.Claude.Prompts || !st.Claude.HeadersSet {
		t.Fatalf("claude: %+v", st.Claude)
	}
	if strings.Contains(mustJSON(res), "SECRET") {
		t.Fatal("status выдал секрет")
	}
}

// Codex: смена prompts/endpoint переписывает только [otel], заголовки
// сохраняются, чужое байт в байт; выключение снимает семейство целиком;
// включение с нуля ставит токен hottell.
func TestOtelConfigureCodexSurgery(t *testing.T) {
	_, xp := otelHome(t)
	os.WriteFile(xp, []byte(codexRealShape), 0o600)
	head := codexRealShape[:strings.Index(codexRealShape, "[otel]")]

	var out otelConfigureOut
	callTool(t, "otel_configure", map[string]any{"agent": "codex", "prompts": true, "endpoint": "https://new.example"}, &out)
	text, _ := readTextFile(xp)
	if !strings.HasPrefix(text, head) || !strings.Contains(text, "log_user_prompt = true") ||
		!strings.Contains(text, `endpoint = "https://new.example/v1/metrics"`) || strings.Count(text, "Bearer SECRET") != 3 {
		t.Fatalf("правка:\n%s", text)
	}
	if !out.Status.Codex.Prompts || len(out.Changed) != 1 {
		t.Fatalf("out: %+v", out)
	}
	// повтор — без изменений
	callTool(t, "otel_configure", map[string]any{"agent": "codex", "prompts": true, "endpoint": "https://new.example"}, &out)
	if len(out.Changed) != 0 {
		t.Fatalf("повтор изменил файл: %+v", out.Changed)
	}

	callTool(t, "otel_configure", map[string]any{"agent": "codex", "enabled": false}, &out)
	text, _ = readTextFile(xp)
	if strings.Contains(text, "[otel") || !strings.HasPrefix(text, strings.TrimRight(head, "\n")) || out.Status.Codex.Enabled {
		t.Fatalf("выключение:\n%s", text)
	}

	callTool(t, "otel_configure", map[string]any{"agent": "codex", "enabled": true}, &out)
	text, _ = readTextFile(xp)
	if !strings.Contains(text, `headers = { Authorization = "Bearer TOK" }`) || !out.Status.Codex.Enabled ||
		!strings.Contains(text, `endpoint = "https://otel.alva.dev/v1/logs"`) || !strings.Contains(text, "[hooks.state.") {
		t.Fatalf("включение:\n%s", text)
	}
}

func TestOtelConfigureClaude(t *testing.T) {
	cp, _ := otelHome(t)
	os.WriteFile(cp, []byte(`{"model":"opus","hooks":{"Stop":[]},"env":{"FOO":"bar"}}`), 0o600)
	var out otelConfigureOut
	callTool(t, "otel_configure", map[string]any{"agent": "claude", "enabled": true, "prompts": true}, &out)
	doc, _ := readJSONFile(cp)
	env := envOf(doc)
	if env["FOO"] != "bar" || env["CLAUDE_CODE_ENABLE_TELEMETRY"] != "1" || env["OTEL_EXPORTER_OTLP_HEADERS"] != "Authorization=Bearer TOK" ||
		env["OTEL_LOG_USER_PROMPTS"] != "1" || doc["model"] != "opus" || doc["hooks"] == nil {
		t.Fatalf("включение: %v", doc)
	}
	callTool(t, "otel_configure", map[string]any{"agent": "claude", "endpoint": "https://x.example/", "prompts": false}, &out)
	doc, _ = readJSONFile(cp)
	if envOf(doc)["OTEL_EXPORTER_OTLP_ENDPOINT"] != "https://x.example" || envOf(doc)["OTEL_LOG_USER_PROMPTS"] != nil {
		t.Fatalf("смена: %v", envOf(doc))
	}
	callTool(t, "otel_configure", map[string]any{"agent": "claude", "enabled": false}, &out)
	doc, _ = readJSONFile(cp)
	if len(envOf(doc)) != 1 || envOf(doc)["FOO"] != "bar" || out.Status.Claude.Enabled {
		t.Fatalf("выключение: %v", doc)
	}
	// битый JSON — отказ
	os.WriteFile(cp, []byte("{broken"), 0o600)
	if _, err := configureClaudeOtel(cp, otelConfigureIn{Agent: "claude", Enabled: boolp(true)}, "TOK"); err == nil {
		t.Fatal("битый JSON должен давать отказ")
	}
}
