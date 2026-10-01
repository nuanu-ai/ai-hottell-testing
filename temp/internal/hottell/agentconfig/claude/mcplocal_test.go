package claude_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/claude"
)

// claudeJSON is the part of ~/.claude.json the tests look at.
type claudeJSON struct {
	NumStartups int                       `json:"numStartups"`
	Servers     map[string]map[string]any `json:"mcpServers"`
}

// readClaudeJSON parses the file at path.
func readClaudeJSON(t *testing.T, path string) claudeJSON {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc claudeJSON
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("%v:\n%s", err, b)
	}
	return doc
}

// stat returns the file info of path.
func stat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestMCPLocal(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".claude.json")
	orig := `{"numStartups":3,"mcpServers":{"hottell":{"type":"http","url":"http://localhost:8080/mcp"}},"projects":{}}`
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := claude.EnsureMCPLocal(path, "hottell-local", "/u/.local/bin/hottell"); err != nil {
		t.Fatal(err)
	}
	doc := readClaudeJSON(t, path)
	want := map[string]any{"type": "stdio", "command": "/u/.local/bin/hottell", "args": []any{"mcp-local"}, "env": map[string]any{}}
	if doc.NumStartups != 3 || doc.Servers["hottell"]["type"] != "http" || !reflect.DeepEqual(doc.Servers["hottell-local"], want) {
		t.Fatalf("after Ensure: %+v", doc)
	}
	before := stat(t, path)
	if err := claude.EnsureMCPLocal(path, "hottell-local", "/u/.local/bin/hottell"); err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, stat(t, path)) {
		t.Fatal("a repeated Ensure without a change wrote the file")
	}
	if err := claude.RemoveMCPLocal(path, "hottell-local"); err != nil {
		t.Fatal(err)
	}
	doc = readClaudeJSON(t, path)
	if _, ok := doc.Servers["hottell-local"]; ok || doc.Servers["hottell"] == nil {
		t.Fatalf("Remove must take out only hottell-local: %+v", doc.Servers)
	}
}

// TestMCPLocalKeepsUserKeys: hottell owns only type, command and args of the entry; env
// and any other key of the user's stay, and a changed binary path changes only command.
func TestMCPLocalKeepsUserKeys(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".claude.json")
	orig := `{"mcpServers":{"hottell-local":{"type":"stdio","command":"/old/hottell","args":["mcp-local"],` +
		`"env":{"HOTTELL_DATA_DIR":"/data"},"timeout":5000}}}`
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := claude.EnsureMCPLocal(path, "hottell-local", "/u/.local/bin/hottell"); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"type": "stdio", "command": "/u/.local/bin/hottell", "args": []any{"mcp-local"},
		"env": map[string]any{"HOTTELL_DATA_DIR": "/data"}, "timeout": float64(5000),
	}
	if got := readClaudeJSON(t, path).Servers["hottell-local"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("after Ensure: %+v\nwant %+v", got, want)
	}
	before := stat(t, path)
	if err := claude.EnsureMCPLocal(path, "hottell-local", "/u/.local/bin/hottell"); err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, stat(t, path)) {
		t.Fatal("a repeated Ensure without a change wrote the file")
	}
}

// TestMCPLocalCompletesEntry: an entry without type or args, or without env, gets ours and
// is not given an env.
func TestMCPLocalCompletesEntry(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"hottell-local":{"command":"/u/.local/bin/hottell","args":["serve"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := claude.EnsureMCPLocal(path, "hottell-local", "/u/.local/bin/hottell"); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"type": "stdio", "command": "/u/.local/bin/hottell", "args": []any{"mcp-local"}}
	if got := readClaudeJSON(t, path).Servers["hottell-local"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("after Ensure: %+v\nwant %+v", got, want)
	}
}

// TestMCPLocalRefusesEntryOfAnotherKind: an entry that is not an object is not replaced.
func TestMCPLocalRefusesEntryOfAnotherKind(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".claude.json")
	orig := `{"mcpServers":{"hottell-local":"x"}}`
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := claude.EnsureMCPLocal(path, "hottell-local", "/u/.local/bin/hottell"); !errors.Is(err, claude.ErrMalformed) {
		t.Fatalf("Ensure = %v, want ErrMalformed", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != orig {
		t.Fatalf("Ensure changed the file: %s", b)
	}
}
