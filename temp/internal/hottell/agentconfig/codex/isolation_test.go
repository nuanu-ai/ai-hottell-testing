package codex_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/codex"
)

// TestCommandIsolatesTheAgent runs the hook command through a shell, as Codex does,
// with stand-in scripts for the binary: whether the binary is missing, blocks or succeeds,
// the command exits 0 and prints nothing, so the agent never acts on it.
func TestCommandIsolatesTheAgent(t *testing.T) {
	t.Parallel()
	const event = `{"hook_event_name":"PreToolUse","session_id":"s"}`

	for _, tt := range []struct {
		name   string
		script string
	}{
		{name: "binary missing"},
		{name: "binary blocks", script: "#!/bin/sh\necho '{\"decision\":\"block\"}'\necho 'denied' >&2\nexit 2\n"},
		{name: "binary succeeds", script: "#!/bin/sh\ncat > \"$(dirname \"$0\")/stdin\"\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(t.TempDir(), "my bin")
			bin := filepath.Join(dir, "hottell")
			if tt.script != "" {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(bin, []byte(tt.script), 0o700); err != nil { //nolint:gosec // a stand-in binary must be executable
					t.Fatal(err)
				}
			}
			command, err := codex.Command(bin)
			if err != nil {
				t.Fatal(err)
			}

			cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", command)
			cmd.Stdin = strings.NewReader(event)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Errorf("%s: %v, want exit 0", command, err)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Errorf("%s printed stdout %q, stderr %q; want nothing", command, stdout.String(), stderr.String())
			}
			if tt.name == "binary succeeds" {
				if got, err := os.ReadFile(filepath.Join(dir, "stdin")); err != nil || string(got) != event {
					t.Errorf("the binary read %q (%v) from stdin, want the event", got, err)
				}
			}
		})
	}
}
