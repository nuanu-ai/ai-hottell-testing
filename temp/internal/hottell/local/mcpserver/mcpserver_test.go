package mcpserver_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/mcpserver"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/sessions"
)

func connect(t *testing.T, cfg mcpserver.Config) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	if _, err := mcpserver.New(cfg).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// config is a server over an empty home with an existing, empty data directory: the
// journal creates only its coach/ inside it.
func config(t *testing.T) mcpserver.Config {
	t.Helper()
	home := t.TempDir()
	data := filepath.Join(home, "data")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	return mcpserver.Config{
		Roots:   sessions.Roots{ClaudeProjects: filepath.Join(home, ".claude", "projects"), CodexHome: filepath.Join(home, ".codex")},
		DataDir: data, Version: "test",
		Now: func() time.Time { return time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) },
	}
}

func TestTools(t *testing.T) {
	t.Parallel()
	cs := connect(t, config(t))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"coach_journal", "findings", "session_read", "session_stats", "sessions_list"}) {
		t.Fatalf("tools: %v", names)
	}
}

func TestJournalAndFindingsOverMCP(t *testing.T) {
	t.Parallel()
	cs := connect(t, config(t))
	ctx := context.Background()
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %+v", name, err, res)
		}
		var out map[string]any
		raw, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(raw, &out)
		return out
	}
	if f := call("findings", map[string]any{"since": "168h"}); len(f["sources"].([]any)) != 3 {
		t.Fatalf("findings: %+v", f)
	}
	entry := map[string]any{
		"topic_key": "a-b", "agent": "claude", "topic": "т", "decision": "declined", "layer": nil,
		"evidence": []map[string]any{{"session": "s", "quote": "q"}},
	}
	call("coach_journal", map[string]any{"op": "append", "entry": entry})
	if r := call("coach_journal", map[string]any{"op": "read"}); r["count"] != float64(1) {
		t.Fatalf("journal: %+v", r)
	}
}

// withClaudeSessions writes two one-reply Claude Code sessions under cfg's roots.
func withClaudeSessions(t *testing.T, cfg mcpserver.Config) {
	t.Helper()
	for _, id := range []string{"one", "two"} {
		line := `{"type":"user","message":{"role":"user","content":"реплика"},"timestamp":"2026-09-30T08:00:00Z","sessionId":"` + id + `"}` + "\n"
		p := filepath.Join(cfg.Roots.ClaudeProjects, "-w-"+id, id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestSessionsOverMCP: the session tools pass the SDK's input and output schemas, and a
// partial aggregate keeps the data in the text content next to the note.
func TestSessionsOverMCP(t *testing.T) {
	t.Parallel()
	cfg := config(t)
	withClaudeSessions(t, cfg)
	cs := connect(t, cfg)
	ctx := context.Background()
	call := func(name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %+v", name, err, res)
		}
		return res
	}
	structured := func(res *mcp.CallToolResult) map[string]any {
		t.Helper()
		var out map[string]any
		raw, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if list := structured(call("sessions_list", map[string]any{"agent": "claude"})); list["total"] != float64(2) {
		t.Fatalf("sessions_list: %+v", list)
	}
	if page := structured(call("session_read", map[string]any{"id": "one", "from": "2026-09-30"})); page["matched_events"] != float64(1) {
		t.Fatalf("session_read: %+v", page)
	}
	res := call("session_stats", map[string]any{"limit": 1})
	if st := structured(res); st["sessions"] != float64(1) {
		t.Fatalf("session_stats: %+v", st)
	}
	if len(res.Content) != 2 {
		t.Fatalf("session_stats content: %d parts, want the note and the stats", len(res.Content))
	}
	var st map[string]any
	if err := json.Unmarshal([]byte(res.Content[1].(*mcp.TextContent).Text), &st); err != nil || st["sessions"] != float64(1) {
		t.Fatalf("session_stats text: %v %+v", err, st)
	}
}
