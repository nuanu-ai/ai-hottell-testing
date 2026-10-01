package codex_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/codex"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/hookcmd"
)

// foreignHooks has the prototype's hooks, which run the binary at its install path, among
// hooks of others and a hottell hook.
const foreignHooks = `{
  "hooks": {
    "PreToolUse": [{"hooks": [{"type": "command", "command": "'` + binary + `' -agent codex -config x", "timeout": 5}]}],
    "Stop": [
      {"hooks": [{"type": "command", "command": "say done"}]},
      {"hooks": [{"type": "command", "command": "$HOME/.local/bin/hottell -agent codex"}]},
      {"hooks": [{"type": "command", "command": "` + binary + ` hook codex >/dev/null 2>&1 || true", "timeout": 10}]}
    ],
    "SessionStart": [{"hooks": [{"type": "command", "command": "logger ` + binary + ` -agent codex"}]}]
  }
}
`

func TestForeignHooks(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(home, hooksName)
	writeBytes(t, path, []byte(foreignHooks))

	found, err := codex.ForeignHooks(home, binary, "/Users/dev")
	want := []hookcmd.Hook{
		{Event: "PreToolUse", Command: "'" + binary + "' -agent codex -config x"},
		{Event: "Stop", Command: "$HOME/.local/bin/hottell -agent codex"},
	}
	if err != nil || !slices.Equal(found, want) {
		t.Fatalf("ForeignHooks() = %+v, %v; want %+v", found, err, want)
	}
	if got := string(readBytes(t, path)); got != foreignHooks {
		t.Errorf("ForeignHooks() wrote the file:\n%s", got)
	}

	n, err := codex.RemoveForeignHooks(home, binary, "/Users/dev")
	if err != nil || n != 2 {
		t.Fatalf("RemoveForeignHooks() = %d, %v; want 2", n, err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(readBytes(t, path), &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.Hooks["PreToolUse"]; ok {
		t.Errorf("PreToolUse is left without hooks")
	}
	var stop []string
	for _, g := range doc.Hooks["Stop"] {
		for _, h := range g.Hooks {
			stop = append(stop, h.Command)
		}
	}
	if want := []string{"say done", binary + " hook codex >/dev/null 2>&1 || true"}; !slices.Equal(stop, want) {
		t.Errorf("Stop hooks %q, want %q", stop, want)
	}
	if len(doc.Hooks["SessionStart"]) != 1 {
		t.Errorf("the hook that only mentions the binary is removed: %+v", doc.Hooks)
	}

	// None is left, so the file is not written again.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := codex.RemoveForeignHooks(home, binary, "/Users/dev"); err != nil || n != 0 {
		t.Errorf("second RemoveForeignHooks() = %d, %v", n, err)
	}
	if after, err := os.Stat(path); err != nil || !os.SameFile(info, after) {
		t.Errorf("second RemoveForeignHooks() rewrote the file")
	}
}

// TestForeignHooksDuplicateEvent: an event that is twice in hooks.json makes the scan fail,
// naming the file and the event, and the file is left as it is.
func TestForeignHooksDuplicateEvent(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(home, hooksName)
	data := `{"hooks": {"Stop": [], "Stop": [{"hooks": [{"type": "command", "command": "` + binary + ` -agent codex"}]}]}}`
	writeBytes(t, path, []byte(data))

	found, err := codex.ForeignHooks(home, binary, "/Users/dev")
	if !errors.Is(err, codex.ErrMalformed) || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), `duplicate key "Stop"`) {
		t.Errorf("ForeignHooks() = %+v, %v; want an error naming %s and the key", found, err, path)
	}
	if n, err := codex.RemoveForeignHooks(home, binary, "/Users/dev"); !errors.Is(err, codex.ErrMalformed) || n != 0 {
		t.Errorf("RemoveForeignHooks() = %d, %v; want %v", n, err, codex.ErrMalformed)
	}
	if got := string(readBytes(t, path)); got != data {
		t.Errorf("the file changed:\n%s", got)
	}
}
