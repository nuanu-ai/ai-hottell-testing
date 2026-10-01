package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// callTool — вызов тула через настоящий MCP-клиент поверх in-memory транспорта;
// out получает structuredContent.
func callTool(t *testing.T, name string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	if _, err := newMCPServer().Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s: ошибка тула: %v", name, res.Content)
	}
	if out != nil {
		raw, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s: structuredContent: %v (%s)", name, err, raw)
		}
	}
	return res
}

func TestMCPPing(t *testing.T) {
	var out pingOut
	callTool(t, "ping", nil, &out)
	if out.Pong != "pong" || out.Version == "" {
		t.Fatalf("ping: %+v", out)
	}
}

const codexSample = `model = "gpt"

[mcp_servers.gitea]
command = "/x/gitea-mcp"

[mcp_servers.gitea.env]
GITEA_ACCESS_TOKEN = "secret"

[projects."/Users/a/Документы # не комментарий"]
trust_level = "trusted"

# --- otel-lab: комментарий к блоку ---
[otel]
environment = "lab"
log_user_prompt = false

[otel.exporter.otlp-http]
endpoint = "https://otel.example/v1/logs"
headers = { Authorization = "Bearer x" }

[tui]
theme = "dark"
`

// Регистрация MCP: чужие серверы, комментарии и порядок целы; повтор — без
// изменений; снятие возвращает файл байт в байт.
func TestMCPRegisterSurgical(t *testing.T) {
	dir := t.TempDir()
	p := paths{claudeJSON: filepath.Join(dir, ".claude.json"), codexConfig: filepath.Join(dir, "config.toml")}
	claudeSample := `{"numStartups": 12345678901234567, "mcpServers": {"linear": {"type": "http", "url": "https://x"}}}`
	os.WriteFile(p.claudeJSON, []byte(claudeSample), 0o600)
	os.WriteFile(p.codexConfig, []byte(codexSample), 0o600)

	for i := 0; i < 2; i++ {
		if err := registerMCP(p, "/bin/hottell"); err != nil {
			t.Fatal(err)
		}
	}
	text, _ := readTextFile(p.codexConfig)
	if strings.Count(text, "[mcp_servers.hottell]") != 1 || !strings.HasPrefix(text, codexSample) {
		t.Fatalf("config.toml испорчен:\n%s", text)
	}
	claude, codex := mcpRegistered(p)
	if !claude || !codex {
		t.Fatalf("регистрация: claude=%v codex=%v", claude, codex)
	}
	raw, _ := os.ReadFile(p.claudeJSON)
	if !strings.Contains(string(raw), "12345678901234567") || !strings.Contains(string(raw), `"linear"`) {
		t.Fatalf(".claude.json испорчен: %s", raw)
	}

	if err := unregisterMCP(p); err != nil {
		t.Fatal(err)
	}
	text, _ = readTextFile(p.codexConfig)
	if strings.TrimRight(text, "\n") != strings.TrimRight(codexSample, "\n") {
		t.Fatalf("после снятия не исходный файл:\n%q", text)
	}
	if claude, codex = mcpRegistered(p); claude || codex {
		t.Fatal("после снятия запись осталась")
	}
}

func TestTOMLFamily(t *testing.T) {
	fam := tomlFamily(codexSample, "otel")
	if !strings.HasPrefix(fam, "[otel]") || !strings.Contains(fam, "otlp-http") || strings.Contains(fam, "[tui]") {
		t.Fatalf("семейство otel: %q", fam)
	}
	if v, _ := tomlValue(fam, "otel.exporter.otlp-http", "endpoint"); v != "https://otel.example/v1/logs" {
		t.Fatalf("endpoint: %q", v)
	}
	if v, _ := tomlValue(fam, "otel", "log_user_prompt"); v != "false" {
		t.Fatalf("log_user_prompt: %q", v)
	}
	// комментарий перед [otel] принадлежит ему и не уходит с предыдущим блоком
	g := tomlFamily(codexSample, `projects."/Users/a/Документы # не комментарий"`)
	if strings.Contains(g, "otel-lab") || !strings.Contains(g, "trusted") {
		t.Fatalf("семейство projects: %q", g)
	}
	out, ok := tomlRemoveFamily(codexSample, "otel")
	if !ok || strings.Contains(out, "otlp-http") || !strings.Contains(out, "[tui]") || !strings.Contains(out, "GITEA") {
		t.Fatalf("удаление otel: %q", out)
	}
	// замена на месте
	rep := tomlSetFamily(codexSample, "otel", "[otel]\nenvironment = \"prod\"\n")
	if !strings.Contains(rep, "environment = \"prod\"\n\n[tui]") || strings.Contains(rep, "otlp-http") {
		t.Fatalf("замена otel: %q", rep)
	}
}
