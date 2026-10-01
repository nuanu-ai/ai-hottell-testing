package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/apply"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/launchd"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/mcpclient"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/mcpconfig"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/queue"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/sender"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

// agentFixtures are the agents' configs of testdata/home, with hooks and keys of others
// and no [otel] family.
//
//nolint:gochecknoglobals // a constant list
var agentFixtures = []string{".claude/settings.json", ".codex/hooks.json", ".codex/config.toml"}

// withAgentFixtures copies the agents' configs of testdata/home into the fixture's home.
func (f *installFixture) withAgentFixtures(t *testing.T) {
	t.Helper()
	for _, rel := range agentFixtures {
		data, err := os.ReadFile(filepath.Join("testdata", "home", rel))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.home, rel), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *installFixture) status(t *testing.T, asJSON bool) (code int, rep statusReport, text string) {
	t.Helper()
	rep = collectStatus(t.Context(), f.cfg)
	var out bytes.Buffer
	if asJSON {
		if err := json.NewEncoder(&out).Encode(rep); err != nil {
			t.Fatal(err)
		}
		rep = statusReport{}
		if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
			t.Fatal(err)
		}
	} else {
		printStatus(&out, rep)
	}
	code = exitOK
	if !rep.OK {
		code = exitFailure
	}
	for _, secret := range []string{mcpKey, firstToken} {
		if strings.Contains(out.String(), secret) {
			t.Errorf("the status shows a secret")
		}
	}
	return code, rep, out.String()
}

func (f *installFixture) uninstall(t *testing.T) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = uninstall(t.Context(), f.cfg, &out, &errOut)
	return code, out.String(), errOut.String()
}

func mustInstall(t *testing.T, f *installFixture) {
	t.Helper()
	if code, stdout, stderr := f.install(t); code != exitOK {
		t.Fatalf("install exit code %d\n%s\n%s", code, stdout, stderr)
	}
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func TestUninstallRestoresTheAgentsConfigs(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	f.withAgentFixtures(t)
	before := make(map[string]string)
	for _, rel := range agentFixtures {
		before[rel] = f.read(t, rel)
	}
	servers := f.claudeServers(t)
	mustInstall(t, f)
	for _, rel := range agentFixtures {
		if f.read(t, rel) == before[rel] {
			t.Fatalf("install left %s unchanged", rel)
		}
	}
	f.checkMCPLocal(t, servers["hottell"])

	code, stdout, stderr := f.uninstall(t)
	if code != exitOK {
		t.Fatalf("uninstall exit code %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, rel := range agentFixtures {
		if got := f.read(t, rel); got != before[rel] {
			t.Errorf("%s after uninstall:\n%s\nwant the fixture byte for byte:\n%s", rel, got, before[rel])
		}
	}
	// hottell-local goes; the MCP server hottell is the user's, not hottell's.
	if got := f.claudeServers(t); !reflect.DeepEqual(got, servers) {
		t.Errorf("mcpServers of .claude.json after uninstall = %+v, want %+v", got, servers)
	}
	agent := launchd.Agent{Home: f.home}
	for _, gone := range []string{
		f.cfg.daemon.binary, agent.PlistPath(), agent.LogDir(), f.cfg.daemon.paths.Logs,
		f.cfg.daemon.paths.StateFile(), f.cfg.daemon.paths.QueueDir(),
	} {
		if exists(t, gone) {
			t.Errorf("%s is left after uninstall", gone)
		}
	}
	if got := f.launchd.runs(); !slices.Equal(got, []string{"bootout", "bootstrap", "bootout"}) {
		t.Errorf("launchctl calls %q, want the daemon booted out", got)
	}
	for _, want := range []string{"launchd     ok", "claude      ok", "codex       ok", "state       ok", "binary      ok"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}

	// A repeated run finds everything gone and changes nothing.
	code, stdout, stderr = f.uninstall(t)
	if code != exitOK {
		t.Fatalf("second uninstall exit code %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, rel := range agentFixtures {
		if got := f.read(t, rel); got != before[rel] {
			t.Errorf("%s changed on the second uninstall:\n%s", rel, got)
		}
	}
	if code, rep, text := f.status(t, false); code != exitFailure || rep.Daemon.Loaded {
		t.Errorf("status after uninstall exit code %d, daemon %+v:\n%s", code, rep.Daemon, text)
	}
}

func TestUninstallWithoutAgents(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	for _, dir := range []string{".claude", ".codex"} {
		if err := os.Remove(filepath.Join(f.home, dir)); err != nil {
			t.Fatal(err)
		}
	}
	code, stdout, stderr := f.uninstall(t)
	if code != exitOK {
		t.Fatalf("uninstall exit code %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, dir := range []string{".claude", ".codex"} {
		if exists(t, filepath.Join(f.home, dir)) {
			t.Errorf("uninstall created %s", dir)
		}
	}
}

func TestUninstallKeepsTheStateWhenAnAgentFails(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	f.withAgentFixtures(t)
	mustInstall(t, f)
	// The daemon would rewrite the broken config with its next application.
	f.launchd.freeze()
	// A config hottell cannot parse is not written, and the step fails.
	broken := filepath.Join(f.home, ".codex", "hooks.json")
	if err := os.WriteFile(broken, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := f.uninstall(t)
	if code != exitFailure {
		t.Fatalf("uninstall exit code %d, want %d\n%s\n%s", code, exitFailure, stdout, stderr)
	}
	if !strings.Contains(stdout, "codex       failed") || !strings.Contains(stdout, "claude      ok") {
		t.Errorf("stdout:\n%s", stdout)
	}
	for _, kept := range []string{f.cfg.daemon.paths.Root, f.cfg.daemon.binary} {
		if !exists(t, kept) {
			t.Errorf("%s removed although Codex is not clean", kept)
		}
	}
	if got := f.read(t, ".claude/settings.json"); got != f.fixture(t, ".claude/settings.json") {
		t.Errorf("Claude Code not restored although only Codex failed:\n%s", got)
	}
	// The outcome of the last application no longer holds: status must not show the
	// removed Claude Code hooks as installed.
	if _, rep, _ := f.status(t, true); rep.Agents[policy.Claude].Status != statusUnknown {
		t.Errorf("claude = %+v after a partial uninstall, want %q", rep.Agents[policy.Claude], statusUnknown)
	}
}

func (f *installFixture) fixture(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "home", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestUninstallStopsWhenLaunchdFails(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	f.withAgentFixtures(t)
	mustInstall(t, f)
	installed := f.read(t, ".claude/settings.json")
	f.cfg.launchd = failingLaunchd{}

	if code, stdout, stderr := f.uninstall(t); code != exitFailure || !strings.Contains(stdout, "launchd     failed") {
		t.Fatalf("uninstall exit code %d, want %d\n%s\n%s", code, exitFailure, stdout, stderr)
	}
	// The running daemon would apply the settings again, so the agents are left alone.
	if got := f.read(t, ".claude/settings.json"); got != installed {
		t.Errorf("the agents were changed while the daemon runs")
	}
}

// TestRemoveStateWhileHooksWriteIntoTheQueue removes the state while running agent sessions
// write a burst of events into the queue, longer than one removal takes.
func TestRemoveStateWhileHooksWriteIntoTheQueue(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	keep := filepath.Join(root, "backup")
	queueDir := filepath.Join(root, "queue")
	for _, dir := range []string{keep, queueDir} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Once the queue is gone, the writes fail.
		write := func(i int) { _ = os.WriteFile(filepath.Join(queueDir, fmt.Sprintf("event-%d", i)), nil, 0o600) }
		write(0)
		close(started)
		end := time.Now().Add(2 * removeStatePause)
		for i := 1; time.Now().Before(end); i++ {
			write(i)
		}
	}()
	<-started

	err := removeState(root, keep)
	<-done
	if err != nil {
		t.Fatalf("removeState: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "backup" {
		t.Errorf("left in the state: %v, want only backup", entries)
	}
}

type failingLaunchd struct{}

func (failingLaunchd) Run(_ context.Context, _ ...string) ([]byte, int, error) {
	return []byte("Operation not permitted"), 1, nil
}

func TestStatusNothingInstalled(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, false)
	code, rep, _ := f.status(t, true)
	if code != exitFailure || rep.OK {
		t.Fatalf("exit code %d, ok %v; want a failure", code, rep.OK)
	}
	if rep.MCP.Error == nil || *rep.MCP.Error != mcpclient.ReasonNoServer {
		t.Errorf("mcp = %+v, want no_server", rep.MCP)
	}
	if rep.Token.Present || rep.Settings.Cached || rep.Daemon.Loaded {
		t.Errorf("token %+v, settings %+v, daemon %+v; want none", rep.Token, rep.Settings, rep.Daemon)
	}
	for _, agent := range []policy.Agent{policy.Claude, policy.Codex} {
		if got := rep.Agents[agent].Status; got != statusUnknown {
			t.Errorf("%s status %q, want %q", agent, got, statusUnknown)
		}
	}
	if rep.Version != currentVersion() {
		t.Errorf("version %q, want %q", rep.Version, currentVersion())
	}

	_, _, text := f.status(t, false)
	for _, want := range []string{"mcp         no_server", "token       missing", "daemon      not loaded", "Problems:"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
}

func TestStatusInstalled(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	mustInstall(t, f)

	code, rep, _ := f.status(t, true)
	if code != exitOK {
		t.Fatalf("exit code %d, problems %q", code, rep.Problems)
	}
	if rep.MCP.Agent != "claude" || rep.MCP.Error != nil || rep.MCP.LastSuccess.IsZero() {
		t.Errorf("mcp = %+v", rep.MCP)
	}
	if !rep.Token.Present || rep.Token.IngestURL != f.intake.URL || rep.Token.LastSent.IsZero() || rep.Token.LastError != nil {
		t.Errorf("token = %+v", rep.Token)
	}
	if rep.Settings != (settingsStatus{Cached: true, Version: 3, Applied: 3}) {
		t.Errorf("settings = %+v", rep.Settings)
	}
	wantAgents := map[policy.Agent]agentStatus{
		policy.Claude: {Status: apply.StatusOK, Hooks: apply.StatusOK, OTel: apply.StatusOK},
		policy.Codex:  {Status: apply.StatusOK, Hooks: apply.StatusOK, Trust: apply.StatusOK, OTel: apply.StatusOK},
	}
	for agent, want := range wantAgents {
		if got := rep.Agents[agent]; !agentStatusEqual(got, want) {
			t.Errorf("%s = %+v, want %+v", agent, got, want)
		}
	}
	if rep.Queue.Queued != 0 || rep.Queue.Rejected != 0 {
		t.Errorf("queue = %+v, want empty", rep.Queue)
	}
	if !rep.Daemon.Loaded || rep.Daemon.PID != 4242 {
		t.Errorf("daemon = %+v, want pid 4242", rep.Daemon)
	}
	if len(rep.Problems) != 0 {
		t.Errorf("problems %q", rep.Problems)
	}

	_, _, text := f.status(t, false)
	for _, want := range []string{
		"mcp         ok            claude config", "token       ok", "settings    ok            version 3, applied 3",
		"claude      ok            hooks ok, otel ok", "codex       ok            hooks ok, trust ok, otel ok",
		"queue       0 records", "daemon      running       pid 4242", "Everything works.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
}

func agentStatusEqual(a, b agentStatus) bool {
	return a.Status == b.Status && a.Hooks == b.Hooks && a.Trust == b.Trust && a.OTel == b.OTel &&
		slices.Equal(a.Errors, b.Errors)
}

// TestStatusProblems breaks one thing after install at a time: status reports it and
// exits 1.
func TestStatusProblems(t *testing.T) {
	t.Parallel()

	writeState := func(name string, v any) func(*testing.T, *installFixture) {
		return func(t *testing.T, f *installFixture) {
			t.Helper()
			writeJSON(t, filepath.Join(f.cfg.daemon.paths.Root, name), v)
		}
	}
	// mcpFails keeps what the client saw of the server and adds a failure.
	mcpFails := func(reason mcpclient.Reason, detail string) func(*testing.T, *installFixture) {
		return func(t *testing.T, f *installFixture) {
			t.Helper()
			st, err := mcpclient.ReadStatus(f.cfg.daemon.paths.MCPFile())
			if err != nil || st.Source.Path == "" {
				t.Fatalf("MCP status after install %+v, %v", st, err)
			}
			st.Problem = &mcpclient.Problem{At: time.Now(), Reason: reason, Detail: detail}
			writeJSON(t, f.cfg.daemon.paths.MCPFile(), st)
		}
	}
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	tests := map[string]struct {
		breaks func(*testing.T, *installFixture)
		check  func(*testing.T, statusReport)
		want   string
	}{
		"MCP key revoked": {
			breaks: mcpFails(mcpclient.ReasonUnauthorized, "401"),
			check: func(t *testing.T, rep statusReport) {
				t.Helper()
				if rep.MCP.Error == nil || *rep.MCP.Error != mcpclient.ReasonUnauthorized {
					t.Errorf("mcp = %+v", rep.MCP)
				}
			},
			want: "mcp         unauthorized  ключ MCP отклонён сервером (401)",
		},
		"service unreachable": {
			breaks: mcpFails(mcpclient.ReasonUnreachable, "connection refused"),
			want:   "mcp         unreachable   сервис недоступен по сети: connection refused",
		},
		"MCP failure of another config": {
			breaks: writeState("mcp.json", mcpclient.Status{
				Source:  mcpconfig.Source{Agent: mcpconfig.Codex, Path: "/elsewhere/config.toml"},
				Problem: &mcpclient.Problem{At: at, Reason: mcpclient.ReasonUnauthorized, Detail: "401"},
			}),
			check: func(t *testing.T, rep statusReport) {
				t.Helper()
				if rep.MCP.Error != nil || rep.MCP.Agent != mcpconfig.Claude {
					t.Errorf("mcp = %+v, want the Claude Code config without the failure of the other", rep.MCP)
				}
			},
			want: "mcp         no answer     claude config",
		},
		"collector token refused": {
			breaks: writeState("sender.json", sender.Status{Unauthorized: "401 Unauthorized"}),
			check: func(t *testing.T, rep statusReport) {
				t.Helper()
				if rep.Token.Unauthorized == "" {
					t.Errorf("token = %+v", rep.Token)
				}
			},
			want: "token       refused       401 Unauthorized",
		},
		"send failed": {
			breaks: writeState("sender.json", sender.Status{LastError: &sender.Failure{At: at, Reason: "connection refused"}}),
			check: func(t *testing.T, rep statusReport) {
				t.Helper()
				if rep.Token.LastError == nil || rep.Token.LastError.Reason != "connection refused" {
					t.Errorf("token = %+v", rep.Token)
				}
			},
			want: "token       send failed   connection refused",
		},
		"Codex trust failed": {
			breaks: writeState("apply.json", apply.Result{Version: 3, Agents: map[policy.Agent]apply.AgentResult{
				policy.Claude: {Status: apply.StatusNotInstalled},
				policy.Codex: {Status: apply.StatusFailed, Steps: []apply.StepResult{
					{Step: apply.StepHooks, Status: apply.StatusOK},
					{Step: apply.StepTrust, Status: apply.StatusFailed, Error: "unsupported layout"},
					{Step: apply.StepOTel, Status: apply.StatusOK},
				}},
			}}),
			check: func(t *testing.T, rep statusReport) {
				t.Helper()
				if got := rep.Agents[policy.Codex]; got.Trust != apply.StatusFailed || got.Hooks != apply.StatusOK {
					t.Errorf("codex = %+v", got)
				}
			},
			want: "codex       failed        hooks ok, trust failed, otel ok: trust failed: unsupported layout",
		},
		"settings not applied yet": {
			breaks: writeState("apply.json", apply.Result{Version: 2, Agents: map[policy.Agent]apply.AgentResult{
				policy.Claude: {Status: apply.StatusOK}, policy.Codex: {Status: apply.StatusOK},
			}}),
			want: "settings: version 3 is cached, version 2 is applied",
		},
		"daemon not loaded": {
			breaks: func(t *testing.T, f *installFixture) {
				t.Helper()
				f.launchd.mu.Lock()
				f.launchd.frozen = false
				f.launchd.mu.Unlock()
			},
			want: "daemon      not loaded",
		},
		"no MCP server": {
			breaks: func(t *testing.T, f *installFixture) {
				t.Helper()
				if err := os.Remove(filepath.Join(f.home, ".claude.json")); err != nil {
					t.Fatal(err)
				}
			},
			want: "mcp         no_server     MCP-сервер hottell не найден",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newInstallFixture(t, true)
			mustInstall(t, f)
			// The daemon would overwrite the status files it keeps.
			f.launchd.freeze()
			tc.breaks(t, f)

			code, rep, _ := f.status(t, true)
			if code != exitFailure || len(rep.Problems) == 0 {
				t.Errorf("exit code %d, problems %q; want a failure", code, rep.Problems)
			}
			if tc.check != nil {
				tc.check(t, rep)
			}
			if _, _, text := f.status(t, false); !strings.Contains(text, tc.want) {
				t.Errorf("text lacks %q:\n%s", tc.want, text)
			}
		})
	}
}

// TestStatusQueue counts the queued and the rejected records, which are no problem.
func TestStatusQueue(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	mustInstall(t, f)
	f.launchd.freeze()
	if _, err := queue.Open(f.cfg.daemon.paths, 0, nil).Put([]byte(`{}`), []byte("0123456789")); err != nil {
		t.Fatal(err)
	}
	rejected := filepath.Join(f.cfg.daemon.paths.RejectedDir(), "0000000000000000001-0123456789abcdef")
	if err := os.WriteFile(rejected, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, rep, _ := f.status(t, true)
	if code != exitOK {
		t.Fatalf("exit code %d, problems %q", code, rep.Problems)
	}
	if rep.Queue.Queued != 1 || rep.Queue.QueuedBytes == 0 || rep.Queue.Rejected != 1 || rep.Queue.RejectedBytes != 10 {
		t.Errorf("queue = %+v", rep.Queue)
	}
	if _, _, text := f.status(t, false); !strings.Contains(text, "queue       1 records") ||
		!strings.Contains(text, "rejected 1 records, 10 bytes") {
		t.Errorf("text:\n%s", text)
	}
}

func TestRunStatusUsage(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"status", "--yaml"}, {"status", "--json", "extra"}, {"uninstall", "extra"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, nil, &stdout, &stderr, lookup(nil)); code != exitUsage {
			t.Errorf("%q: exit code %d, want %d", args, code, exitUsage)
		}
	}
}

// TestRunStatusJSON runs hottell status --json on a home without HOME set in the
// environment, which fails before launchctl is run.
func TestRunStatusJSON(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	env := map[string]string{state.HomeEnv: t.TempDir()}
	if code := run([]string{"status", "--json"}, nil, &stdout, &stderr, lookup(env)); code != exitFailure {
		t.Errorf("exit code %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stderr.String(), "HOME is not set") {
		t.Errorf("stderr = %q", stderr.String())
	}
}
