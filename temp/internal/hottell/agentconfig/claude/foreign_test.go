package claude_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/claude"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/hookcmd"
)

// foreignSettings has the prototype's hooks, which run the binary at its install path,
// among hooks of others and a hottell hook.
const foreignSettings = `{
  "hooks": {
    "SessionStart": [{"hooks": [{"type": "command", "command": "` + binary + ` -agent claude"}]}],
    "Stop": [
      {"hooks": [{"type": "command", "command": "say done"}, {"type": "command", "command": "~/.local/bin/hottell -agent claude -config x"}]},
      {"hooks": [{"type": "command", "command": "` + binary + ` hook claude >/dev/null 2>&1 || true", "timeout": 10}]}
    ],
    "UserPromptSubmit": [{"hooks": [{"type": "command", "command": "echo ` + binary + ` -agent claude"}]}]
  }
}
`

func TestForeignHooks(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(foreignSettings), 0o600); err != nil {
		t.Fatal(err)
	}

	found, err := claude.ForeignHooks(path, binary, "/Users/me")
	want := []hookcmd.Hook{
		{Event: "SessionStart", Command: binary + " -agent claude"},
		{Event: "Stop", Command: "~/.local/bin/hottell -agent claude -config x"},
	}
	if err != nil || !slices.Equal(found, want) {
		t.Fatalf("ForeignHooks() = %+v, %v; want %+v", found, err, want)
	}
	if got := string(readFile(t, path)); got != foreignSettings {
		t.Errorf("ForeignHooks() wrote the file:\n%s", got)
	}

	n, err := claude.RemoveForeignHooks(path, binary, "/Users/me")
	if err != nil || n != 2 {
		t.Fatalf("RemoveForeignHooks() = %d, %v; want 2", n, err)
	}
	got := hooksOf(t, path)
	if _, ok := got["SessionStart"]; ok {
		t.Errorf("SessionStart is left without hooks: %+v", got["SessionStart"])
	}
	var commands []string
	for _, groups := range got {
		for _, g := range groups {
			for _, h := range g.Hooks {
				commands = append(commands, h.Command)
			}
		}
	}
	slices.Sort(commands)
	if want := []string{binary + " hook claude" + silenced, "echo " + binary + " -agent claude", "say done"}; !slices.Equal(commands, want) {
		t.Errorf("hooks left %q, want %q", commands, want)
	}

	// None is left, so the file is not written again.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := claude.RemoveForeignHooks(path, binary, "/Users/me"); err != nil || n != 0 {
		t.Errorf("second RemoveForeignHooks() = %d, %v", n, err)
	}
	if after, err := os.Stat(path); err != nil || !os.SameFile(info, after) {
		t.Errorf("second RemoveForeignHooks() rewrote the file")
	}
	if found, err := claude.ForeignHooks(filepath.Join(t.TempDir(), "missing.json"), binary, "/Users/me"); err != nil || len(found) != 0 {
		t.Errorf("ForeignHooks(missing) = %+v, %v", found, err)
	}
}

// TestForeignHooksDuplicateEvent: an event that is twice in the settings makes the scan
// fail, naming the file and the event.
func TestForeignHooksDuplicateEvent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.json")
	data := `{"hooks": {"Stop": [], "Stop": [{"hooks": [{"type": "command", "command": "` + binary + ` -agent claude"}]}]}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	found, err := claude.ForeignHooks(path, binary, "/Users/me")
	if !errors.Is(err, claude.ErrMalformed) || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), `duplicate key "Stop"`) {
		t.Errorf("ForeignHooks() = %+v, %v; want an error naming %s and the key", found, err, path)
	}
}
