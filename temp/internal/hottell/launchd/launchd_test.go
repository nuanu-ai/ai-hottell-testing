package launchd_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/launchd"
)

// reply is what the fake launchctl answers to one call.
type reply struct {
	out  string
	code int
	err  error
}

// fakeRunner records launchctl calls and answers them with replies in order; once
// they run out it answers with exit code 0.
type fakeRunner struct {
	mu      sync.Mutex
	replies []reply
	calls   [][]string
}

func (f *fakeRunner) Run(_ context.Context, args ...string) ([]byte, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, args)
	if len(f.replies) == 0 {
		return nil, 0, nil
	}
	r := f.replies[0]
	f.replies = f.replies[1:]
	return []byte(r.out), r.code, r.err
}

func newAgent(t *testing.T, replies ...reply) (launchd.Agent, *fakeRunner) {
	t.Helper()

	runner := &fakeRunner{replies: replies}
	return launchd.Agent{
		Home:   t.TempDir(),
		Binary: "/Users/tester/.local/bin/hottell",
		UID:    501,
		Runner: runner,
	}, runner
}

func TestPlistMatchesGolden(t *testing.T) {
	t.Parallel()

	agent := launchd.Agent{Home: "/Users/tester", Binary: "/Users/tester/.local/bin/hottell"}
	want, err := os.ReadFile(filepath.Join("testdata", launchd.Label+".plist"))
	if err != nil {
		t.Fatal(err)
	}
	if got := agent.Plist(); !bytes.Equal(got, want) {
		t.Errorf("plist differs from golden file:\n%s", got)
	}
}

func TestPlistEscapesPaths(t *testing.T) {
	t.Parallel()

	agent := launchd.Agent{Home: "/Users/a&b", Binary: "/Users/a&b/<bin>/hottell"}
	got := string(agent.Plist())
	for _, want := range []string{
		"<string>/Users/a&amp;b/&lt;bin&gt;/hottell</string>",
		"<string>/Users/a&amp;b/Library/Logs/hottell/stdout.log</string>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plist lacks %s:\n%s", want, got)
		}
	}
}

func TestPlistIsValidPropertyList(t *testing.T) {
	t.Parallel()

	plutil, err := exec.LookPath("plutil")
	if err != nil {
		t.Skip("plutil is not available")
	}
	path := filepath.Join(t.TempDir(), "agent.plist")
	agent := launchd.Agent{Home: "/Users/a&b", Binary: "/Users/a&b/<bin>/hottell"}
	if err := os.WriteFile(path, agent.Plist(), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), plutil, "-lint", path).CombinedOutput(); err != nil {
		t.Errorf("plutil -lint: %v: %s", err, out)
	}
}

func TestInstall(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		bootout reply
	}{
		"not loaded":           {bootout: reply{out: "Boot-out failed: 3: No such process", code: 3}},
		"not loaded, old code": {bootout: reply{out: "Could not find specified service", code: 113}},
		"loaded":               {bootout: reply{}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			agent, runner := newAgent(t, tc.bootout)
			if err := agent.Install(t.Context()); err != nil {
				t.Fatalf("Install() = %v", err)
			}

			want := [][]string{
				{"bootout", "gui/501/dev.alva.hottell"},
				{"bootstrap", "gui/501", agent.PlistPath()},
			}
			if !reflect.DeepEqual(runner.calls, want) {
				t.Errorf("calls = %q, want %q", runner.calls, want)
			}
			got, err := os.ReadFile(agent.PlistPath())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, agent.Plist()) {
				t.Errorf("written plist differs from Plist()")
			}
			if info, err := os.Stat(agent.LogDir()); err != nil || !info.IsDir() {
				t.Errorf("log dir %s not created: %v", agent.LogDir(), err)
			}
		})
	}
}

func TestInstallAgainReplacesPlistAndRestarts(t *testing.T) {
	t.Parallel()

	agent, runner := newAgent(t)
	if err := agent.Install(t.Context()); err != nil {
		t.Fatal(err)
	}
	agent.Binary = "/opt/new/hottell"
	if err := agent.Install(t.Context()); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(agent.PlistPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("<string>/opt/new/hottell</string>")) {
		t.Errorf("plist does not run the new binary:\n%s", got)
	}
	verbs := make([]string, 0, len(runner.calls))
	for _, call := range runner.calls {
		verbs = append(verbs, call[0])
	}
	if want := []string{"bootout", "bootstrap", "bootout", "bootstrap"}; !reflect.DeepEqual(verbs, want) {
		t.Errorf("verbs = %q, want %q", verbs, want)
	}
	entries, err := os.ReadDir(filepath.Dir(agent.PlistPath()))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("LaunchAgents holds %d entries, want only the plist", len(entries))
	}
}

func TestInstallRetriesBootstrapWhileOldInstanceGoesAway(t *testing.T) {
	t.Parallel()

	agent, runner := newAgent(t, reply{}, reply{out: "Bootstrap failed: 5: Input/output error", code: 5})
	if err := agent.Install(t.Context()); err != nil {
		t.Fatalf("Install() = %v", err)
	}
	if len(runner.calls) != 3 {
		t.Errorf("calls = %q, want bootout and two bootstraps", runner.calls)
	}
}

func TestInstallFails(t *testing.T) {
	t.Parallel()

	errNoLaunchctl := errors.New("no launchctl")
	tests := map[string]struct {
		replies []reply
		want    string
	}{
		"bootout fails": {
			replies: []reply{{out: "Boot-out failed: 1: Operation not permitted", code: 1}},
			want:    "launchctl bootout: exit code 1: Boot-out failed: 1: Operation not permitted",
		},
		"bootstrap fails": {
			replies: []reply{{}, {out: "Bootstrap failed: 22: Invalid argument", code: 22}},
			want:    "launchctl bootstrap: exit code 22: Bootstrap failed: 22: Invalid argument",
		},
		"launchctl cannot run": {
			replies: []reply{{err: errNoLaunchctl}},
			want:    "no launchctl",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			agent, _ := newAgent(t, tc.replies...)
			err := agent.Install(t.Context())
			if err == nil || err.Error() != tc.want {
				t.Errorf("Install() = %v, want %s", err, tc.want)
			}
		})
	}
}

func TestUninstall(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		installed bool
		bootout   reply
	}{
		"installed":     {installed: true},
		"not installed": {bootout: reply{out: "Boot-out failed: 3: No such process", code: 3}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			agent, runner := newAgent(t)
			if tc.installed {
				if err := agent.Install(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			runner.calls, runner.replies = nil, []reply{tc.bootout}

			if err := agent.Uninstall(t.Context()); err != nil {
				t.Fatalf("Uninstall() = %v", err)
			}
			if want := [][]string{{"bootout", "gui/501/dev.alva.hottell"}}; !reflect.DeepEqual(runner.calls, want) {
				t.Errorf("calls = %q, want %q", runner.calls, want)
			}
			if _, err := os.Stat(agent.PlistPath()); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("plist still present: %v", err)
			}
		})
	}
}

func TestUninstallFailsWhenBootoutFails(t *testing.T) {
	t.Parallel()

	agent, _ := newAgent(t, reply{}, reply{}, reply{out: "Boot-out failed: 1: Operation not permitted", code: 1})
	if err := agent.Install(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := agent.Uninstall(t.Context()); err == nil {
		t.Fatal("Uninstall() = nil, want error")
	}
	if _, err := os.Stat(agent.PlistPath()); err != nil {
		t.Errorf("plist removed although the agent was not unloaded: %v", err)
	}
}

func TestStatus(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		reply reply
		want  launchd.Status
	}{
		"running, never exited": {
			reply: reply{out: printOutput("state = running", "pid = 4242", "last exit code = (never exited)")},
			want:  launchd.Status{Loaded: true, PID: 4242},
		},
		"restarted after a kill": {
			reply: reply{out: printOutput("state = running", "pid = 4343", "last exit code = 0")},
			want:  launchd.Status{Loaded: true, PID: 4343, Exited: true},
		},
		"waiting after a failure": {
			reply: reply{out: printOutput("state = not running", "last exit code = 78: EX_CONFIG")},
			want:  launchd.Status{Loaded: true, Exited: true, LastExitCode: 78},
		},
		"not loaded": {
			reply: reply{out: `Bad request.\nCould not find service "dev.alva.hottell" in domain for user gui: 501`, code: 113},
			want:  launchd.Status{},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			agent, runner := newAgent(t, tc.reply)
			got, err := agent.Status(t.Context())
			if err != nil {
				t.Fatalf("Status() = %v", err)
			}
			if got != tc.want {
				t.Errorf("Status() = %+v, want %+v", got, tc.want)
			}
			if want := [][]string{{"print", "gui/501/dev.alva.hottell"}}; !reflect.DeepEqual(runner.calls, want) {
				t.Errorf("calls = %q, want %q", runner.calls, want)
			}
		})
	}
}

func TestStatusFails(t *testing.T) {
	t.Parallel()

	tests := map[string]reply{
		"launchctl fails": {out: "Operation not permitted", code: 1},
		"bad pid":         {out: printOutput("pid = many")},
		"bad exit code":   {out: printOutput("last exit code = lots")},
	}

	for name, r := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			agent, _ := newAgent(t, r)
			if _, err := agent.Status(t.Context()); err == nil {
				t.Error("Status() = nil error, want error")
			}
		})
	}
}

// printOutput builds launchctl print output with the given top-level fields and a
// nested dictionary whose pid and exit code must be ignored.
func printOutput(fields ...string) string {
	var b strings.Builder
	b.WriteString("gui/501/dev.alva.hottell = {\n\tactive count = 1\n\tpath = /Users/tester/Library/LaunchAgents/dev.alva.hottell.plist\n")
	for _, f := range fields {
		b.WriteString("\t" + f + "\n")
	}
	b.WriteString("\n\tspawn = {\n\t\tpid = 1\n\t\tlast exit code = 9\n\t}\n}\n")
	return b.String()
}

func TestExecRunner(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "launchctl")
	body := "#!/bin/sh\necho \"args: $*\"\nexit 3\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil { //nolint:gosec // an executable test script
		t.Fatal(err)
	}

	out, code, err := launchd.ExecRunner{Path: script}.Run(t.Context(), "bootout", "gui/501/dev.alva.hottell")
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if code != 3 {
		t.Errorf("code = %d, want 3", code)
	}
	if got, want := string(out), "args: bootout gui/501/dev.alva.hottell\n"; got != want {
		t.Errorf("out = %q, want %q", got, want)
	}

	if _, _, err := (launchd.ExecRunner{Path: filepath.Join(t.TempDir(), "missing")}).Run(t.Context()); err == nil {
		t.Error("Run() of a missing binary = nil error, want error")
	}
}
