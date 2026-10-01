package apply_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/claude"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/codex"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/apply"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/backup"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

const (
	serviceURL = "https://telemetry.example.test"
	token      = "ht_col_0123456789abcdef"
	newToken   = "ht_col_fedcba9876543210"
)

// env is a fixture home: HOME with the agents' directories copied from testdata/home,
// and the hottell state beside it.
type env struct {
	home    string
	applier apply.Applier
}

func (e env) claudeSettings() string { return filepath.Join(e.home, ".claude", "settings.json") }
func (e env) codexConfig() string    { return codex.ConfigFile(e.applier.CodexHome) }
func (e env) codexHooks() string     { return codex.HooksFile(e.applier.CodexHome) }

// setup copies the fixture directories of the named agents into a fresh HOME.
func setup(t *testing.T, agents ...string) env {
	t.Helper()
	home := t.TempDir()
	for _, agent := range agents {
		src := filepath.Join("testdata", "home", agent)
		dst := filepath.Join(home, agent)
		if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
			t.Fatal(err)
		}
	}
	return env{
		home: home,
		applier: apply.Applier{
			ClaudeDir:  filepath.Join(home, ".claude"),
			CodexHome:  filepath.Join(home, ".codex"),
			BinaryPath: filepath.Join(home, ".local", "bin", "hottell"),
			Paths:      state.PathsIn(filepath.Join(home, "hottell-state")),
			Resource:   claude.Resource{Host: "mac", Version: "0.1.0"},
		},
	}
}

func creds(collectorToken string) state.Credentials {
	return state.Credentials{IngestURL: serviceURL, CollectorToken: collectorToken}
}

func run(t *testing.T, e env, settings policy.Settings, c state.Credentials) apply.Result {
	t.Helper()
	result, err := e.applier.Apply(settings, c)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	saved, ok, err := apply.ReadResult(e.applier.Paths)
	if err != nil || !ok {
		t.Fatalf("ReadResult: %v, saved %t", err, ok)
	}
	if saved.Version != result.Version || len(saved.Agents) != len(result.Agents) {
		t.Fatalf("saved result %+v, want %+v", saved, result)
	}
	for agent, want := range result.Agents {
		if got := saved.Agents[agent]; got.Status != want.Status || len(got.Steps) != len(want.Steps) {
			t.Fatalf("saved %s %+v, want %+v", agent, got, want)
		}
	}
	return result
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func contains(t *testing.T, path string, wants ...string) {
	t.Helper()
	data := read(t, path)
	for _, want := range wants {
		if !strings.Contains(data, want) {
			t.Errorf("%s lacks %q:\n%s", path, want, data)
		}
	}
}

func lacks(t *testing.T, path string, unwanted ...string) {
	t.Helper()
	data := read(t, path)
	for _, s := range unwanted {
		if strings.Contains(data, s) {
			t.Errorf("%s holds %q:\n%s", path, s, data)
		}
	}
}

func wantStatus(t *testing.T, result apply.Result, agent policy.Agent, status string, steps ...string) {
	t.Helper()
	got := result.Agents[agent]
	if got.Status != status {
		t.Errorf("%s status %q, want %q; steps %+v", agent, got.Status, status, got.Steps)
	}
	if len(got.Steps) != len(steps) {
		t.Fatalf("%s steps %+v, want statuses %v", agent, got.Steps, steps)
	}
	for i, step := range got.Steps {
		if step.Status != steps[i] {
			t.Errorf("%s step %s status %q (%s), want %q", agent, step.Step, step.Status, step.Error, steps[i])
		}
	}
}

func TestApplyFirstInstall(t *testing.T) {
	t.Parallel()
	e := setup(t, ".claude", ".codex")

	result := run(t, e, policy.Settings{Version: 1}, creds(token))

	wantStatus(t, result, policy.Claude, apply.StatusOK, apply.StatusOK, apply.StatusOK)
	wantStatus(t, result, policy.Codex, apply.StatusOK, apply.StatusOK, apply.StatusOK, apply.StatusOK)
	if result.Version != 1 || result.Err() != nil {
		t.Errorf("version %d, err %v", result.Version, result.Err())
	}

	hook := "/.local/bin/hottell hook"
	contains(t, e.claudeSettings(),
		`"model": "opus"`, `"FOO": "bar"`, "~/bin/guard.sh", hook+" claude",
		`"OTEL_EXPORTER_OTLP_HEADERS": "Authorization=Bearer `+token+`"`,
		`"CLAUDE_CODE_ENABLE_TELEMETRY": "1"`)
	contains(t, e.codexHooks(), "say done", hook+" codex")
	contains(t, e.codexConfig(),
		`model = "gpt-5"`, `trust_level = "trusted"`, "trusted_hash",
		"[otel.exporter.otlp-http]", `"Bearer `+token+`"`)
}

// TestApplyIdempotent applies the same settings twice: the second call writes no file,
// neither of the agents nor of the state.
func TestApplyIdempotent(t *testing.T) {
	t.Parallel()
	e := setup(t, ".claude", ".codex")
	settings := policy.Settings{Version: 3}
	run(t, e, settings, creds(token))

	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	files := []string{e.home, e.applier.Paths.Root}
	before := map[string]string{}
	for _, root := range files {
		if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			before[path] = read(t, path)
			return os.Chtimes(path, old, old)
		}); err != nil {
			t.Fatal(err)
		}
	}
	if len(before) < 6 {
		t.Fatalf("only %d files after the first call: %v", len(before), before)
	}

	run(t, e, settings, creds(token))

	for path, data := range before {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(old) {
			t.Errorf("%s written again: mtime %v", path, info.ModTime())
		}
		if got := read(t, path); got != data {
			t.Errorf("%s changed:\n%s\nwas:\n%s", path, got, data)
		}
	}
}

func TestApplyDenialChange(t *testing.T) {
	t.Parallel()
	e := setup(t, ".claude", ".codex")
	run(t, e, policy.Settings{Version: 1}, creds(token))

	off := false
	denied := policy.Settings{Version: 2}
	denied.Agents.Claude.NativeContent.Denied = []string{"prompts"}
	denied.Agents.Codex.Enabled = &off
	result := run(t, e, denied, creds(token))

	wantStatus(t, result, policy.Claude, apply.StatusOK, apply.StatusOK, apply.StatusOK)
	wantStatus(t, result, policy.Codex, apply.StatusOK, apply.StatusOK, apply.StatusOK, apply.StatusOK)
	if result.Version != 2 {
		t.Errorf("version %d, want 2", result.Version)
	}
	contains(t, e.claudeSettings(), `"OTEL_LOG_USER_PROMPTS": "0"`, `"OTEL_LOG_ASSISTANT_RESPONSES": "1"`)
	// A denied agent keeps its hooks, which drop its events, and loses its native OTel.
	contains(t, e.codexHooks(), "/.local/bin/hottell hook codex")
	contains(t, e.codexConfig(), "trusted_hash", `exporter = "none"`, `trace_exporter = "none"`, `metrics_exporter = "none"`)
	lacks(t, e.codexConfig(), "otlp-http", token)

	// Lifting the denials brings the native OTel back.
	result = run(t, e, policy.Settings{Version: 3}, creds(token))
	wantStatus(t, result, policy.Codex, apply.StatusOK, apply.StatusOK, apply.StatusOK, apply.StatusOK)
	contains(t, e.claudeSettings(), `"OTEL_LOG_USER_PROMPTS": "1"`)
	contains(t, e.codexConfig(), "[otel.exporter.otlp-http]", `"Bearer `+token+`"`)
}

func TestApplyTokenChange(t *testing.T) {
	t.Parallel()
	e := setup(t, ".claude", ".codex")
	settings := policy.Settings{Version: 1}
	run(t, e, settings, creds(token))

	result := run(t, e, settings, creds(newToken))

	wantStatus(t, result, policy.Claude, apply.StatusOK, apply.StatusOK, apply.StatusOK)
	wantStatus(t, result, policy.Codex, apply.StatusOK, apply.StatusOK, apply.StatusOK, apply.StatusOK)
	contains(t, e.claudeSettings(), "Authorization=Bearer "+newToken)
	contains(t, e.codexConfig(), `"Bearer `+newToken+`"`)
	for _, path := range []string{e.claudeSettings(), e.codexConfig(), e.applier.Paths.ClaudeOTelFile()} {
		lacks(t, path, token)
	}
}

func TestApplyAgentNotInstalled(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		installed, missing string
		agent, other       policy.Agent
		steps              []string
	}{
		{".claude", ".codex", policy.Claude, policy.Codex, []string{apply.StatusOK, apply.StatusOK}},
		{".codex", ".claude", policy.Codex, policy.Claude, []string{apply.StatusOK, apply.StatusOK, apply.StatusOK}},
	} {
		t.Run("only "+tc.installed, func(t *testing.T) {
			t.Parallel()
			e := setup(t, tc.installed)

			result := run(t, e, policy.Settings{Version: 1}, creds(token))

			wantStatus(t, result, tc.agent, apply.StatusOK, tc.steps...)
			wantStatus(t, result, tc.other, apply.StatusNotInstalled)
			if result.Err() != nil {
				t.Errorf("Err: %v", result.Err())
			}
			if _, err := os.Stat(filepath.Join(e.home, tc.missing)); !os.IsNotExist(err) {
				t.Errorf("%s was created: %v", tc.missing, err)
			}
		})
	}
}

// TestApplyWriteFailure makes the Codex home read-only: Codex fails, Claude Code is
// still brought in line, and the failures are saved for hottell status.
func TestApplyWriteFailure(t *testing.T) {
	t.Parallel()
	e := setup(t, ".claude", ".codex")
	codexBefore := read(t, e.codexConfig())
	if err := os.Chmod(e.applier.CodexHome, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(e.applier.CodexHome, 0o700) })

	result := run(t, e, policy.Settings{Version: 1}, creds(token))

	wantStatus(t, result, policy.Claude, apply.StatusOK, apply.StatusOK, apply.StatusOK)
	wantStatus(t, result, policy.Codex, apply.StatusFailed, apply.StatusFailed, apply.StatusSkipped, apply.StatusFailed)
	if err := result.Err(); err == nil || !strings.Contains(err.Error(), "codex hooks") || strings.Contains(err.Error(), "claude") {
		t.Errorf("Err: %v", err)
	}
	contains(t, e.claudeSettings(), "/.local/bin/hottell hook claude", "Authorization=Bearer "+token)
	if got := read(t, e.codexConfig()); got != codexBefore {
		t.Errorf("config.toml changed:\n%s", got)
	}

	saved, _, err := apply.ReadResult(e.applier.Paths)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range saved.Agents[policy.Codex].Steps {
		if step.Status == apply.StatusFailed && step.Error == "" {
			t.Errorf("saved step %s failed without an error", step.Step)
		}
	}

	// Once the directory is writable again, the next call repairs Codex.
	if err := os.Chmod(e.applier.CodexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	result = run(t, e, policy.Settings{Version: 1}, creds(token))
	wantStatus(t, result, policy.Codex, apply.StatusOK, apply.StatusOK, apply.StatusOK, apply.StatusOK)
}

func TestReadResultNone(t *testing.T) {
	t.Parallel()
	_, ok, err := apply.ReadResult(state.PathsIn(t.TempDir()))
	if ok || err != nil {
		t.Errorf("ReadResult: saved %t, err %v", ok, err)
	}
}

// TestApplyBacksUpAForeignChange: the daemon applies again and again; only a change of an
// agent's config that hottell did not write itself is copied into a new set first.
func TestApplyBacksUpAForeignChange(t *testing.T) {
	t.Parallel()
	e := setup(t, ".claude", ".codex")
	store := &backup.Store{
		Dir: e.applier.Paths.BackupDir(),
		Files: []backup.File{
			{Name: "claude-settings.json", Path: e.claudeSettings()},
			{Name: "codex-config.toml", Path: e.codexConfig()},
			{Name: "codex-hooks.json", Path: e.codexHooks()},
		},
	}
	e.applier.Backup = store
	if _, err := store.Take(backup.ReasonInstall); err != nil {
		t.Fatal(err)
	}
	countSets := func() int {
		t.Helper()
		sets, err := store.Sets()
		if err != nil {
			t.Fatal(err)
		}
		return len(sets)
	}

	run(t, e, policy.Settings{Version: 1}, creds(token))
	run(t, e, policy.Settings{Version: 1}, creds(token))
	// A new version hottell writes itself is no foreign change either.
	run(t, e, policy.Settings{Version: 2}, creds(newToken))
	if n := countSets(); n != 1 {
		t.Fatalf("%d sets after applies without a foreign change, want only the install set", n)
	}

	edited := strings.Replace(read(t, e.claudeSettings()), `"model": "opus"`, `"model": "sonnet"`, 1)
	if err := os.WriteFile(e.claudeSettings(), []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, e, policy.Settings{Version: 2}, creds(newToken))
	sets, err := store.Sets()
	if err != nil {
		t.Fatal(err)
	}
	if len(sets) != 2 || sets[0].Reason != backup.ReasonApply {
		t.Fatalf("sets %+v, want a new apply set", sets)
	}
	if got := read(t, filepath.Join(store.Dir, sets[0].Name, "claude-settings.json")); got != edited {
		t.Errorf("the apply set holds %q, want the foreign edit", got)
	}

	run(t, e, policy.Settings{Version: 2}, creds(newToken))
	if n := countSets(); n != 2 {
		t.Errorf("%d sets after one more apply, want 2", n)
	}
}
