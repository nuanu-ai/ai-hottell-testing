package codex_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/codex"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

// liveEnv set to 1 runs TestLive.
const liveEnv = "HOTTELL_CODEX_LIVE"

// TestLive installs the hooks for a freshly built hottell into a temporary CODEX_HOME and
// runs the real codex exec twice: with the hooks alone, which Codex does not run, and
// with the trust hottell writes, which makes their events reach the queue without
// /hooks. It runs only when HOTTELL_CODEX_LIVE=1; the queue lives in a temporary
// HOTTELL_HOME and ~/.codex is not touched.
//
//nolint:paralleltest // runs the real codex
func TestLive(t *testing.T) {
	if os.Getenv(liveEnv) != "1" {
		t.Skip("set " + liveEnv + "=1 to run the real codex with the hottell hooks")
	}
	codexBin, err := exec.LookPath("codex")
	if err != nil {
		t.Fatalf("codex is not on PATH: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	tmp := t.TempDir()
	binary := filepath.Join(tmp, "bin", "hottell")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "git.alva.dev/alva/harness-telemetry/cmd/hottell")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build hottell: %v\n%s", err, out)
	}
	codexEnv := filepath.Join(tmp, "codex")
	work := filepath.Join(tmp, "work")
	for _, dir := range []string{codexEnv, work} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// The temporary directory lies behind the /var symlink of macOS: the trust keys have
	// to use the resolved path, as Codex does.
	home, err := codex.Home(func(k string) string {
		if k == "CODEX_HOME" {
			return codexEnv
		}
		return os.Getenv(k)
	})
	if err != nil {
		t.Fatal(err)
	}
	version, _ := exec.CommandContext(ctx, codexBin, "--version").Output()
	t.Logf("%s, CODEX_HOME %s", strings.TrimSpace(string(version)), home)

	run := func(stateRoot string) map[string]int {
		cmd := exec.CommandContext(ctx, codexBin, "exec", "--skip-git-repo-check", "-s", "read-only", "Reply with the single word OK.")
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "CODEX_HOME="+codexEnv, state.HomeEnv+"="+stateRoot)
		out, err := cmd.CombinedOutput()
		// Without a login the prompt fails; the hooks before it still run.
		t.Logf("codex exec: %v, %d bytes of output", err, len(out))
		return queuedEvents(t, state.PathsIn(stateRoot).QueueDir())
	}

	if err := codex.EnsureHooks(home, binary); err != nil {
		t.Fatalf("EnsureHooks() = %v", err)
	}
	t.Cleanup(func() {
		if err := codex.Uninstall(home); err != nil {
			t.Errorf("Uninstall() = %v", err)
		}
	})
	untrusted := filepath.Join(tmp, "state-untrusted")
	if queued := run(untrusted); len(queued) > 0 {
		t.Errorf("untrusted hooks ran: %v", queued)
	}

	if err := codex.EnsureTrust(home, binary); err != nil {
		t.Fatalf("EnsureTrust() = %v", err)
	}
	trusted := filepath.Join(tmp, "state-trusted")
	queued := run(trusted)
	t.Logf("queued hook events with trust: %v", queued)
	for _, event := range []string{"SessionStart", "UserPromptSubmit"} {
		if queued[event] == 0 {
			t.Errorf("no %s event in the queue; hook log: %s", event, hookLog(trusted))
		}
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
		for _, event := range codex.Events() {
			if regexp.MustCompile(`"hook_event_name"\s*:\s*"` + event + `"`).Match(data) {
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
