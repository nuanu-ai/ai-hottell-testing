package claude_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/claude"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

// Environment of TestLive.
const (
	liveEnv = "HOTTELL_CLAUDE_LIVE"
	// liveConfigEnv names the Claude Code configuration directory TestLive installs the
	// hooks into; without it a temporary one is used, where Claude Code is not logged in
	// and still runs the session hooks.
	liveConfigEnv = "HOTTELL_CLAUDE_LIVE_CONFIG_DIR"
)

// TestLive installs the hooks for a freshly built hottell, runs the real claude in print
// mode and checks that its hook events reach the queue, then removes the hooks. It runs
// only when HOTTELL_CLAUDE_LIVE=1; the queue lives in a temporary HOTTELL_HOME.
//
//nolint:paralleltest // runs the real claude
func TestLive(t *testing.T) {
	if os.Getenv(liveEnv) != "1" {
		t.Skip("set " + liveEnv + "=1 to run the real claude with the hottell hooks")
	}
	claudeBin, err := exec.LookPath("claude")
	if err != nil {
		t.Fatalf("claude is not on PATH: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	tmp := t.TempDir()
	binary := filepath.Join(tmp, "bin", "hottell")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "git.alva.dev/alva/harness-telemetry/cmd/hottell")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build hottell: %v\n%s", err, out)
	}

	configDir := os.Getenv(liveConfigEnv)
	if configDir == "" {
		configDir = filepath.Join(tmp, "claude")
	}
	settings := filepath.Join(configDir, "settings.json")
	if err := claude.EnsureHooks(settings, binary); err != nil {
		t.Fatalf("EnsureHooks() = %v", err)
	}
	t.Cleanup(func() {
		if err := claude.RemoveHooks(settings); err != nil {
			t.Errorf("RemoveHooks() = %v", err)
		}
	})

	stateRoot := filepath.Join(tmp, "state")
	work := filepath.Join(tmp, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	version, _ := exec.CommandContext(ctx, claudeBin, "--version").Output()
	t.Logf("claude %s", strings.TrimSpace(string(version)))

	run := exec.CommandContext(ctx, claudeBin, "-p", "Reply with the single word ok.", "--max-turns", "1")
	run.Dir = work
	run.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+configDir, state.HomeEnv+"="+stateRoot)
	out, err := run.CombinedOutput()
	// Not being logged in fails the prompt, not the session hooks.
	t.Logf("claude -p: %v, %d bytes of output", err, len(out))

	queued := queuedEvents(t, state.PathsIn(stateRoot).QueueDir())
	t.Logf("queued hook events: %v", queued)
	if queued["SessionStart"] == 0 {
		t.Errorf("no SessionStart event in the queue; hook log: %s", hookLog(stateRoot))
	}
}

// queuedEvents counts the queued events by hook_event_name without reading more of them.
func queuedEvents(t *testing.T, dir string) map[string]int {
	t.Helper()
	counts := map[string]int{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, event := range append(claude.Events(), "WorktreeCreate", "WorktreeRemove") {
			if bytes.Contains(data, []byte(`"hook_event_name":"`+event+`"`)) {
				counts[event]++
			}
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read queue: %v", err)
	}
	return counts
}

func hookLog(stateRoot string) string {
	data, err := os.ReadFile(filepath.Join(state.PathsIn(stateRoot).Logs, "hook.log"))
	if err != nil {
		return err.Error()
	}
	return string(data)
}
