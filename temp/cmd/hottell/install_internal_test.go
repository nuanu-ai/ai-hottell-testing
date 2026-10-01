package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/apply"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/backup"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/launchd"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/mcpclient/mcptest"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

// fakeLaunchd stands for launchctl: bootstrap runs the daemon in this process with the
// installed configuration, bootout stops it, print shows it running as pid 4242.
type fakeLaunchd struct {
	t   *testing.T
	cfg daemonConfig

	mu    sync.Mutex
	calls []string
	stop  context.CancelFunc
	done  chan error
	// frozen: the daemon is stopped, but print still shows it running, so that nothing
	// changes the state a test has written.
	frozen bool
	// bootoutFails: bootout fails and changes nothing, as when launchctl cannot stop the agent.
	bootoutFails bool
}

func (l *fakeLaunchd) Run(ctx context.Context, args ...string) ([]byte, int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, args[0])
	switch args[0] {
	case "bootout":
		if l.bootoutFails {
			return []byte("Boot-out failed: 5: Input/output error"), 5, nil
		}
		l.frozen = false
		if l.stop == nil {
			return nil, 3, nil // No such process
		}
		l.stopLocked()
	case "print":
		if l.stop == nil && !l.frozen {
			return []byte("Could not find service"), 113, nil
		}
		return []byte("gui/501/" + launchd.Label + " = {\n\tstate = running\n\tpid = 4242\n}\n"), 0, nil
	case "bootstrap":
		if want := launchd.Label + ".plist"; filepath.Base(args[2]) != want {
			l.t.Errorf("bootstrap %s, want the plist %s", args[2], want)
		}
		// The daemon outlives the launchctl call, as under launchd.
		ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		l.stop, l.done = cancel, make(chan error, 1)
		go func() { l.done <- serve(ctx, l.cfg) }()
	}
	return nil, 0, nil
}

func (l *fakeLaunchd) stopLocked() {
	l.stop()
	if err := <-l.done; err != nil {
		l.t.Errorf("serve: %v", err)
	}
	l.stop, l.done = nil, nil
}

func (l *fakeLaunchd) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stop != nil {
		l.stopLocked()
	}
}

// freeze stops the daemon and keeps it loaded and running for print.
func (l *fakeLaunchd) freeze() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stop != nil {
		l.stopLocked()
	}
	l.frozen = true
}

// failBootout makes every later bootout fail.
func (l *fakeLaunchd) failBootout() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.bootoutFails = true
}

func (l *fakeLaunchd) runs() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.calls)
}

// installFixture is a temporary home with both agents installed, the test MCP server
// in the Claude Code config, the test intake and a stand-in for the downloaded binary.
type installFixture struct {
	home    string
	cfg     installConfig
	intake  *intake
	launchd *fakeLaunchd
}

func newInstallFixture(t *testing.T, withMCP bool) *installFixture {
	t.Helper()
	home := t.TempDir()
	for _, dir := range []string{".claude", ".codex", "download"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f := &installFixture{home: home, intake: newIntake(t, acceptAll)}
	mcp := mcptest.New(mcpKey, firstToken, f.intake.URL, `{"version":3}`)
	t.Cleanup(mcp.Close)
	if withMCP {
		writeJSON(t, filepath.Join(home, ".claude.json"), map[string]any{"mcpServers": map[string]any{"hottell": map[string]any{
			"type": "http", "url": mcp.URL, "headers": map[string]string{"Authorization": "Bearer " + mcpKey},
		}}})
	}
	self := filepath.Join(home, "download", "hottell")
	if err := os.WriteFile(self, []byte("hottell binary v1"), 0o700); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{"HOME": home, state.HomeEnv: filepath.Join(home, "state")}
	cfg, err := installConfigFrom(lookup(env))
	if err != nil {
		t.Fatal(err)
	}
	cfg.self = self
	cfg.daemon.grace = daemonWait
	f.launchd = &fakeLaunchd{t: t, cfg: cfg.daemon}
	t.Cleanup(f.launchd.close)
	cfg.launchd = f.launchd
	f.cfg = cfg
	return f
}

func (f *installFixture) install(t *testing.T) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = install(t.Context(), f.cfg, &out, &errOut)
	return code, out.String(), errOut.String()
}

func (f *installFixture) read(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.home, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// testEvents counts the test events the intake took with the collector token.
func (f *installFixture) testEvents() int {
	f.intake.mu.Lock()
	defer f.intake.mu.Unlock()
	n := 0
	for _, r := range f.intake.requests {
		if r.token == firstToken && strings.Contains(r.body, testEventName) {
			n++
		}
	}
	return n
}

// claudeServers returns mcpServers of the fixture's ~/.claude.json.
func (f *installFixture) claudeServers(t *testing.T) map[string]map[string]any {
	t.Helper()
	var doc struct {
		Servers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(f.read(t, ".claude.json")), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Servers
}

// mcpLocalTable is the table of hottell-local in config.toml for the installed binary.
func (f *installFixture) mcpLocalTable() string {
	return "[mcp_servers.hottell-local]\ncommand = \"" + f.cfg.daemon.binary + "\"\nargs = [\"mcp-local\"]\n"
}

// checkMCPLocal checks that both agents start hottell-local from the installed binary and
// that the service's server hottell is as it was.
func (f *installFixture) checkMCPLocal(t *testing.T, service map[string]any) {
	t.Helper()
	servers := f.claudeServers(t)
	local := servers["hottell-local"]
	if local["type"] != "stdio" || local["command"] != f.cfg.daemon.binary || !reflect.DeepEqual(local["args"], []any{"mcp-local"}) {
		t.Errorf("hottell-local in .claude.json = %+v", local)
	}
	if !reflect.DeepEqual(servers["hottell"], service) {
		t.Errorf("hottell in .claude.json = %+v, want %+v", servers["hottell"], service)
	}
	if config := f.read(t, ".codex/config.toml"); !strings.Contains(config, f.mcpLocalTable()) {
		t.Errorf("Codex config lacks hottell-local:\n%s", config)
	}
}

func TestInstall(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	service := f.claudeServers(t)["hottell"]
	code, stdout, stderr := f.install(t)
	if code != exitOK {
		t.Fatalf("exit code %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	binary := filepath.Join(f.home, ".local", "bin", "hottell")
	if got := f.read(t, ".local/bin/hottell"); got != "hottell binary v1" {
		t.Errorf("installed binary = %q", got)
	}
	if info, err := os.Stat(binary); err != nil || info.Mode().Perm() != binaryPerm {
		t.Errorf("installed binary mode: %v %v", info, err)
	}

	creds, err := f.cfg.daemon.paths.ReadCredentials()
	if err != nil || creds.CollectorToken != firstToken || creds.IngestURL != f.intake.URL {
		t.Errorf("credentials = %+v, %v", creds, err)
	}
	if cache, err := f.cfg.daemon.paths.ReadSettings(); err != nil || cache.Version != 3 {
		t.Errorf("settings cache version %d, %v; want 3", cache.Version, err)
	}

	// Both agents' hooks run the installed binary, and Codex trusts them.
	claudeSettings := f.read(t, ".claude/settings.json")
	for _, want := range []string{binary + " hook claude", "Authorization=Bearer " + firstToken} {
		if !strings.Contains(claudeSettings, want) {
			t.Errorf("Claude Code settings lack %q:\n%s", want, claudeSettings)
		}
	}
	if hooks := f.read(t, ".codex/hooks.json"); !strings.Contains(hooks, binary+" hook codex") {
		t.Errorf("Codex hooks do not run the installed binary:\n%s", hooks)
	}
	if config := f.read(t, ".codex/config.toml"); !strings.Contains(config, "trusted_hash") || !strings.Contains(config, "[otel") {
		t.Errorf("Codex config lacks the trust or OTel:\n%s", config)
	}

	if plist := f.read(t, "Library/LaunchAgents/"+launchd.Label+".plist"); !strings.Contains(plist, "<string>"+binary+"</string>") {
		t.Errorf("the plist does not run the installed binary:\n%s", plist)
	}
	if got := f.launchd.runs(); !slices.Equal(got, []string{"bootout", "bootstrap"}) {
		t.Errorf("launchctl calls %q, want bootout, bootstrap", got)
	}

	if n := f.testEvents(); n != 1 {
		t.Errorf("the intake took %d test events, want 1", n)
	}
	f.checkMCPLocal(t, service)
	for _, want := range []string{
		"binary      ok", "mcp         ok", "token       ok", "settings    ok            version 3",
		"mcp-local   ok            hottell-local in Claude Code and Codex",
		"claude      ok", "codex       ok", "launchd     ok", "test event  ok",
		"Claude Code: start new sessions", "Codex: restart it",
		// The fixture moves the state with HOTTELL_HOME, which launchd would not pass.
		"environment warning       " + state.HomeEnv,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "/hooks") {
		t.Errorf("stdout asks for /hooks although the trust is recorded:\n%s", stdout)
	}
	for _, secret := range []string{mcpKey, firstToken} {
		if strings.Contains(stdout+stderr, secret) {
			t.Errorf("the output shows a secret")
		}
	}
}

func TestInstallAgainReinstalls(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	if code, stdout, stderr := f.install(t); code != exitOK {
		t.Fatalf("first install exit code %d\n%s\n%s", code, stdout, stderr)
	}
	agentFiles := []string{".claude/settings.json", ".codex/hooks.json", ".codex/config.toml", ".claude.json"}
	before := make(map[string]string)
	for _, rel := range agentFiles {
		before[rel] = f.read(t, rel)
	}

	// A newer download replaces the installed binary.
	if err := os.WriteFile(f.cfg.self, []byte("hottell binary v2"), 0o700); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr := f.install(t); code != exitOK {
		t.Fatalf("second install exit code %d\n%s\n%s", code, stdout, stderr)
	}
	if got := f.read(t, ".local/bin/hottell"); got != "hottell binary v2" {
		t.Errorf("installed binary = %q, want the new one", got)
	}
	for _, rel := range agentFiles {
		if got := f.read(t, rel); got != before[rel] {
			t.Errorf("%s changed on the second install:\n%s\nwant:\n%s", rel, got, before[rel])
		}
	}
	if got := f.launchd.runs(); !slices.Equal(got, []string{"bootout", "bootstrap", "bootout", "bootstrap"}) {
		t.Errorf("launchctl calls %q, want the daemon restarted", got)
	}
	if n := f.testEvents(); n != 2 {
		t.Errorf("the intake took %d test events, want one per install", n)
	}

	// Run from the installed binary, install leaves it in place.
	f.cfg.self = f.cfg.daemon.binary
	if code, stdout, stderr := f.install(t); code != exitOK {
		t.Fatalf("install from the installed binary exit code %d\n%s\n%s", code, stdout, stderr)
	}
	if got := f.read(t, ".local/bin/hottell"); got != "hottell binary v2" {
		t.Errorf("installed binary = %q", got)
	}
}

func TestInstallWithoutMCPServer(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, false)
	code, stdout, stderr := f.install(t)
	if code != exitFailure {
		t.Fatalf("exit code %d, want %d\n%s\n%s", code, exitFailure, stdout, stderr)
	}
	if !strings.Contains(stderr, "«Подключение»") {
		t.Errorf("stderr does not send the user to the page «Подключение»:\n%s", stderr)
	}
	if calls := f.launchd.runs(); len(calls) != 0 {
		t.Errorf("launchctl called: %q", calls)
	}
	if _, err := os.Stat(filepath.Join(f.home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("the agents were configured without the MCP server: %v", err)
	}
}

// TestInstallMCPLocalOnlyForInstalledAgents: hottell-local goes only into the agents that
// are installed; install creates no config of an agent that is not.
func TestInstallMCPLocalOnlyForInstalledAgents(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	if err := os.Remove(filepath.Join(f.home, ".codex")); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := f.install(t)
	if code != exitOK {
		t.Fatalf("exit code %d\n%s\n%s", code, stdout, stderr)
	}
	if exists(t, filepath.Join(f.home, ".codex")) {
		t.Errorf("install created the Codex home")
	}
	if local := f.claudeServers(t)["hottell-local"]; local["command"] != f.cfg.daemon.binary {
		t.Errorf("hottell-local in .claude.json = %+v", local)
	}
	if !strings.Contains(stdout, "mcp-local   ok            hottell-local in Claude Code\n") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// TestInstallMCPLocalFailureDoesNotStop: a config where hottell-local cannot be registered
// fails the step and the exit code, but the installation goes on: sending works without
// the coach.
func TestInstallMCPLocalFailureDoesNotStop(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	inline := "[mcp_servers]\nhottell-local = { command = \"x\" }\n"
	if err := os.WriteFile(filepath.Join(f.home, ".codex", "config.toml"), []byte(inline), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := f.install(t)
	if code != exitFailure {
		t.Fatalf("exit code %d, want %d\n%s\n%s", code, exitFailure, stdout, stderr)
	}
	for _, want := range []string{"mcp-local   failed", "defined outside its own table", "codex       ok", "launchd     ok", "test event  ok"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if local := f.claudeServers(t)["hottell-local"]; local["command"] != f.cfg.daemon.binary {
		t.Errorf("hottell-local in .claude.json = %+v, want it registered despite Codex", local)
	}
	if config := f.read(t, ".codex/config.toml"); !strings.HasPrefix(config, inline) {
		t.Errorf("the inline definition was rewritten:\n%s", config)
	}
}

// TestInstallMCPLocalAgentDirNotADirectory: an agent's config path that is a file fails
// the step with the reason instead of passing for an agent that is not installed, and the
// other agent still gets hottell-local.
func TestInstallMCPLocalAgentDirNotADirectory(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	if err := f.cfg.daemon.paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	claudeDir := filepath.Join(f.home, ".claude")
	if err := os.Remove(claudeDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	r := &report{out: &out}
	ensureMCPLocal(f.cfg.daemon, r)
	if !r.anyFailed || !strings.Contains(out.String(), "mcp-local   failed") || !strings.Contains(out.String(), claudeDir+" is not a directory") {
		t.Errorf("the step does not fail on a file in place of the Claude Code directory:\n%s", out.String())
	}
	if config := f.read(t, ".codex/config.toml"); !strings.Contains(config, f.mcpLocalTable()) {
		t.Errorf("Codex config lacks hottell-local:\n%s", config)
	}
}

// TestOverridesWarning: hottell-local is named only when the agents' locations are moved,
// since the state it does not read.
func TestOverridesWarning(t *testing.T) {
	t.Parallel()

	const local = "hottell-local uses them as well unless they are set in the env of its entry"
	if got := overridesWarning([]string{state.HomeEnv}); strings.Contains(got, local) {
		t.Errorf("HOTTELL_HOME alone: %q names hottell-local", got)
	}
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		if got := overridesWarning([]string{state.HomeEnv, name}); !strings.Contains(got, local) {
			t.Errorf("%s: %q does not name hottell-local", name, got)
		}
	}
}

func TestRunInstallUsage(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if code := run([]string{"install", "extra"}, nil, &stdout, &stderr, lookup(nil)); code != exitUsage {
		t.Errorf("exit code %d, want %d", code, exitUsage)
	}
}

func TestInstallManualSteps(t *testing.T) {
	t.Parallel()

	codexWithTrust := func(trust string) apply.Result {
		return apply.Result{Agents: map[policy.Agent]apply.AgentResult{
			policy.Claude: {Status: apply.StatusNotInstalled},
			policy.Codex: {Status: apply.StatusFailed, Steps: []apply.StepResult{
				{Step: apply.StepHooks, Status: apply.StatusOK},
				{Step: apply.StepTrust, Status: trust},
				{Step: apply.StepOTel, Status: apply.StatusOK},
			}},
		}}
	}
	for _, tc := range []struct {
		trust     string
		wantHooks bool
	}{
		{apply.StatusOK, false},
		{apply.StatusFailed, true},
		{apply.StatusSkipped, true},
	} {
		var out bytes.Buffer
		(&report{out: &out}).manualSteps(codexWithTrust(tc.trust))
		if got := strings.Contains(out.String(), "run /hooks in Codex"); got != tc.wantHooks {
			t.Errorf("trust %s: asks for /hooks %v, want %v:\n%s", tc.trust, got, tc.wantHooks, out.String())
		}
		if strings.Contains(out.String(), "Claude Code") {
			t.Errorf("trust %s: steps for Claude Code, which is not installed:\n%s", tc.trust, out.String())
		}
	}
}

// binaryMark stands for the install path of the binary in testdata/foreign.
const binaryMark = "{{BINARY}}"

// foreignFiles are the agents' configs of testdata/foreign: the prototype's hooks, which
// run the binary at the install path, among hooks of others.
//
//nolint:gochecknoglobals // a constant list
var foreignFiles = []string{".claude/settings.json", ".codex/hooks.json"}

// foreignCommands are the prototype's hooks of testdata/foreign, as written into the home.
func (f *installFixture) foreignCommands() []string {
	b := f.cfg.daemon.binary
	return []string{b + " -agent claude", b + " -agent claude -config x", b + " -agent codex", b + " -agent codex -config x"}
}

// withForeignHooks copies the configs of testdata/foreign into the fixture's home with the
// install path of the binary in place of binaryMark, and returns them as written.
func (f *installFixture) withForeignHooks(t *testing.T) map[string]string {
	t.Helper()
	written := make(map[string]string)
	for _, rel := range foreignFiles {
		data, err := os.ReadFile(filepath.Join("testdata", "foreign", rel))
		if err != nil {
			t.Fatal(err)
		}
		data = bytes.ReplaceAll(data, []byte(binaryMark), []byte(f.cfg.daemon.binary))
		if err := os.WriteFile(filepath.Join(f.home, rel), data, 0o600); err != nil {
			t.Fatal(err)
		}
		written[rel] = string(data)
	}
	return written
}

// TestInstallStopsAtForeignHooks: hooks of others that run the binary at the install path
// stop the installation before it changes anything, and are listed with the flag that
// replaces them.
func TestInstallStopsAtForeignHooks(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	before := f.withForeignHooks(t)
	code, stdout, stderr := f.install(t)
	if code != exitFailure {
		t.Fatalf("exit code %d, want %d\n%s\n%s", code, exitFailure, stdout, stderr)
	}

	for rel, want := range before {
		if got := f.read(t, rel); got != want {
			t.Errorf("%s changed:\n%s", rel, got)
		}
	}
	for _, path := range []string{f.cfg.daemon.binary, f.cfg.daemon.paths.BackupDir()} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s exists: %v", path, err)
		}
	}
	if calls := f.launchd.runs(); len(calls) != 0 {
		t.Errorf("launchctl called: %q", calls)
	}

	claude, codex := filepath.Join(f.home, ".claude", "settings.json"), filepath.Join(f.home, ".codex", "hooks.json")
	b := f.cfg.daemon.binary
	for _, want := range []string{
		"foreign hooks failed",
		claude + " SessionStart: " + b + " -agent claude\n",
		claude + " PreToolUse: " + b + " -agent claude\n",
		claude + " Stop: " + b + " -agent claude -config x\n",
		codex + " PreToolUse: " + b + " -agent codex -config x\n",
		codex + " Stop: " + b + " -agent codex\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	for _, other := range []string{"echo " + b, "ORCA_PANE_KEY", "guard.sh", "say done"} {
		if strings.Contains(stdout, other) {
			t.Errorf("stdout lists the hook %q, which does not run the binary first:\n%s", other, stdout)
		}
	}
	if !strings.Contains(stderr, "--replace-foreign-hooks") {
		t.Errorf("stderr does not name the flag that replaces them:\n%s", stderr)
	}
}

// TestInstallReplacesForeignHooks: with the flag install backs up the configs, removes
// exactly the foreign hooks and installs its own.
func TestInstallReplacesForeignHooks(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	before := f.withForeignHooks(t)
	f.cfg.replaceForeign = true
	code, stdout, stderr := f.install(t)
	if code != exitOK {
		t.Fatalf("exit code %d\n%s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "foreign hooks removed 5") {
		t.Errorf("stdout lacks the count of removed hooks:\n%s", stdout)
	}

	claudeSettings, codexHooks := f.read(t, ".claude/settings.json"), f.read(t, ".codex/hooks.json")
	b := f.cfg.daemon.binary
	for _, command := range f.foreignCommands() {
		for _, text := range []string{claudeSettings, codexHooks} {
			if strings.Contains(text, `"`+command+`"`) {
				t.Errorf("the foreign hook %q is left:\n%s", command, text)
			}
		}
	}
	for _, want := range []string{b + " hook claude", "~/bin/guard.sh", "say done", "echo " + b + " -agent claude", "ORCA_PANE_KEY", `"model": "opus"`} {
		if !strings.Contains(claudeSettings, want) {
			t.Errorf("Claude Code settings lack %q:\n%s", want, claudeSettings)
		}
	}
	for _, want := range []string{b + " hook codex", "say done", "ORCA_PANE_KEY"} {
		if !strings.Contains(codexHooks, want) {
			t.Errorf("Codex hooks lack %q:\n%s", want, codexHooks)
		}
	}

	// The install set holds the configs as they were before the removal.
	sets := slices.DeleteFunc(f.sets(t), func(s backup.Set) bool { return s.Reason != backup.ReasonInstall })
	if len(sets) != 1 {
		t.Fatalf("install sets %+v, want one", sets)
	}
	dir := filepath.Join(f.cfg.daemon.paths.BackupDir(), sets[0].Name)
	checked := 0
	for _, file := range sets[0].Files {
		rel, err := filepath.Rel(f.home, file.Path)
		if err != nil {
			t.Fatal(err)
		}
		want, ok := before[rel]
		if !ok {
			continue
		}
		checked++
		if got, err := os.ReadFile(filepath.Join(dir, file.Name)); err != nil || string(got) != want {
			t.Errorf("copy of %s = %q, %v; want the config before install", rel, got, err)
		}
	}
	if checked != len(before) {
		t.Errorf("the install set holds %d of the %d configs with foreign hooks: %+v", checked, len(before), sets[0].Files)
	}

	// Nothing foreign is left, so the next install goes through without the flag.
	f.cfg.replaceForeign = false
	if code, stdout, stderr := f.install(t); code != exitOK || strings.Contains(stdout, "foreign hooks") {
		t.Errorf("second install exit code %d\n%s\n%s", code, stdout, stderr)
	}
}

func TestRunInstallFlags(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"install", "--replace-foreign-hooks", "extra"}, {"install", "--force"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, nil, &stdout, &stderr, lookup(nil)); code != exitUsage {
			t.Errorf("%q: exit code %d, want %d", args, code, exitUsage)
		}
	}
	// Without HOME the flag passes the usage check and install stops at the configuration.
	var stdout, stderr bytes.Buffer
	if code := run([]string{"install", "--replace-foreign-hooks"}, nil, &stdout, &stderr, lookup(nil)); code != exitFailure {
		t.Errorf("install --replace-foreign-hooks: exit code %d, want %d\n%s", code, exitFailure, stderr.String())
	}
}

// duplicateKeys is a Claude Code settings file with a hook of others on the install path
// that the scan cannot read: Claude Code keeps the last of duplicate keys, an edit the first.
const duplicateKeys = `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "{{BINARY}} -agent claude"}]}]}, "hooks": {}}`

// duplicateEvent is a Codex hooks file with a hook of others on the install path under an
// event that is there twice, which the scan cannot read either.
const duplicateEvent = `{"hooks": {"Stop": [], "Stop": [{"hooks": [{"type": "command", "command": "{{BINARY}} -agent codex"}]}]}}`

// TestInstallStopsAtUnreadableConfig: a config the scan for foreign hooks cannot read stops
// the installation before it changes anything, with the flag too.
func TestInstallStopsAtUnreadableConfig(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		rel, data, key string
	}{
		{rel: ".claude/settings.json", data: duplicateKeys, key: "hooks"},
		{rel: ".codex/hooks.json", data: duplicateEvent, key: "Stop"},
	} {
		for _, replace := range []bool{false, true} {
			f := newInstallFixture(t, true)
			before := f.withForeignHooks(t)
			config := filepath.Join(f.home, tc.rel)
			data := strings.ReplaceAll(tc.data, binaryMark, f.cfg.daemon.binary)
			if err := os.WriteFile(config, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			before[tc.rel] = data
			f.cfg.replaceForeign = replace

			code, stdout, stderr := f.install(t)
			if code != exitFailure {
				t.Fatalf("%s, flag %v: exit code %d, want %d\n%s\n%s", tc.rel, replace, code, exitFailure, stdout, stderr)
			}
			for rel, want := range before {
				if got := f.read(t, rel); got != want {
					t.Errorf("%s, flag %v: %s changed:\n%s", tc.rel, replace, rel, got)
				}
			}
			for _, path := range []string{f.cfg.daemon.binary, f.cfg.daemon.paths.BackupDir()} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("%s, flag %v: %s exists: %v", tc.rel, replace, path, err)
				}
			}
			if calls := f.launchd.runs(); len(calls) != 0 {
				t.Errorf("%s, flag %v: launchctl called: %q", tc.rel, replace, calls)
			}
			for _, want := range []string{"foreign hooks failed", config, `duplicate key "` + tc.key + `"`} {
				if !strings.Contains(stdout, want) {
					t.Errorf("%s, flag %v: stdout lacks %q:\n%s", tc.rel, replace, want, stdout)
				}
			}
			if !strings.Contains(stderr, "nothing is changed") {
				t.Errorf("%s, flag %v: stderr does not say that nothing is changed:\n%s", tc.rel, replace, stderr)
			}
		}
	}
}

// oldBinary is what an earlier installation left at the install path.
const oldBinary = "hottell prototype"

// TestInstallReplaceKeepsBinaryOnFailure: with the flag, the binary is replaced only after
// the foreign hooks are gone; a step that fails before leaves the old binary as it was.
func TestInstallReplaceKeepsBinaryOnFailure(t *testing.T) {
	t.Parallel()

	withOldBinary := func(t *testing.T, f *installFixture) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(f.cfg.daemon.binary), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.cfg.daemon.binary, []byte(oldBinary), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("before the removal", func(t *testing.T) {
		t.Parallel()

		// Without the MCP server install stops before the backup and the removal.
		f := newInstallFixture(t, false)
		withOldBinary(t, f)
		before := f.withForeignHooks(t)
		f.cfg.replaceForeign = true
		code, stdout, stderr := f.install(t)
		if code != exitFailure {
			t.Fatalf("exit code %d, want %d\n%s\n%s", code, exitFailure, stdout, stderr)
		}
		for rel, want := range before {
			if got := f.read(t, rel); got != want {
				t.Errorf("%s changed:\n%s", rel, got)
			}
		}
		if got := f.read(t, ".local/bin/hottell"); got != oldBinary {
			t.Errorf("binary = %q, want the old one", got)
		}
	})

	t.Run("after the removal", func(t *testing.T) {
		t.Parallel()

		// The Codex home cannot be written: the removal from Claude Code goes through, the
		// one from Codex fails.
		f := newInstallFixture(t, true)
		withOldBinary(t, f)
		f.withForeignHooks(t)
		f.cfg.replaceForeign = true
		codexDir := filepath.Join(f.home, ".codex")
		if err := os.Chmod(codexDir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(codexDir, 0o700) })

		code, stdout, stderr := f.install(t)
		if code != exitFailure {
			t.Fatalf("exit code %d, want %d\n%s\n%s", code, exitFailure, stdout, stderr)
		}
		if !strings.Contains(stdout, "foreign hooks failed") {
			t.Errorf("stdout lacks the failed removal:\n%s", stdout)
		}
		if strings.Contains(f.read(t, ".claude/settings.json"), f.cfg.daemon.binary+" -agent claude\"") {
			t.Errorf("the removal from Claude Code did not happen, the test does not reach the binary step")
		}
		if got := f.read(t, ".local/bin/hottell"); got != oldBinary {
			t.Errorf("binary = %q, want the old one", got)
		}
		if calls := f.launchd.runs(); len(calls) != 0 {
			t.Errorf("launchctl called: %q", calls)
		}
	})
}
