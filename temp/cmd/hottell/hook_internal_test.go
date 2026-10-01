package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/hook"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/queue"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

const testHome = "/Users/u"

const denySettings = `{"version": 3,
	"agents": {
		"claude": {"hook_events": {"denied": ["UserPromptSubmit"]}, "hook_fields": {"denied": ["prompt"]}},
		"codex": {"sources": {"hooks": false}}
	},
	"folders": {"denied": ["~/work/**"]}}`

func claudeEvent(name, cwd string) string {
	return `{"session_id":"s1","hook_event_name":"` + name + `","cwd":"` + cwd + `","prompt":"secret","tool_use_id":"t1"}`
}

// hookRun runs hottell hook with stdin and settings in a fresh state root and returns
// the queued records.
func hookRun(t *testing.T, settings string, args []string, stdin string, env map[string]string) []queue.Record {
	t.Helper()

	return invokeRun(t, settings, append([]string{"hook"}, args...), stdin, env)
}

// invokeRun runs hottell with the full args, stdin and settings in a fresh state root,
// checks that it exits 0 silently and returns the queued records.
func invokeRun(t *testing.T, settings string, args []string, stdin string, env map[string]string) []queue.Record {
	t.Helper()

	root := t.TempDir()
	paths := state.PathsIn(root)
	if settings != "" {
		if err := os.WriteFile(paths.SettingsFile(), []byte(settings), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	full := map[string]string{"HOME": testHome, state.HomeEnv: root}
	for k, v := range env {
		full[k] = v
	}

	var stdout, stderr bytes.Buffer
	if code := run(args, strings.NewReader(stdin), &stdout, &stderr, lookup(full)); code != exitOK {
		t.Errorf("exit code = %d, want %d", code, exitOK)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}

	records, err := queue.Open(paths, 0, nil).Next(1 << 30)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func TestHook(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		settings string
		args     []string
		stdin    string
		env      map[string]string
		// want is the queued payload; empty means nothing is queued.
		want string
	}{
		"allowed event, no settings cached": {
			args: []string{"claude"}, stdin: claudeEvent("PostToolUse", "/Users/u/src"),
			want: claudeEvent("PostToolUse", "/Users/u/src"),
		},
		"denied field is cut": {
			settings: denySettings, args: []string{"claude"}, stdin: claudeEvent("PostToolUse", "/Users/u/src"),
			want: `{"cwd":"/Users/u/src","hook_event_name":"PostToolUse","session_id":"s1","tool_use_id":"t1"}`,
		},
		"denied event": {
			settings: denySettings, args: []string{"claude"}, stdin: claudeEvent("UserPromptSubmit", "/Users/u/src"),
		},
		"denied folder": {
			settings: denySettings, args: []string{"claude"}, stdin: claudeEvent("PostToolUse", "/Users/u/work/x"),
		},
		"denied hooks source": {
			settings: denySettings, args: []string{"codex"}, stdin: claudeEvent("PostToolUse", "/Users/u/src"),
		},
		"broken JSON is sent unchanged": {
			settings: denySettings, args: []string{"claude"}, stdin: `{"session_id":`, want: `{"session_id":`,
		},
		"broken JSON with hooks off": {
			settings: denySettings, args: []string{"codex"}, stdin: `{"session_id":`,
		},
		"event without a string cwd is sent unchanged": {
			settings: denySettings, args: []string{"claude"},
			stdin: `{"session_id":"s1","hook_event_name":"UserPromptSubmit","cwd":null,"prompt":"p"}`,
			want:  `{"session_id":"s1","hook_event_name":"UserPromptSubmit","cwd":null,"prompt":"p"}`,
		},
		"HOTTELL_OFF": {
			args: []string{"claude"}, stdin: claudeEvent("PostToolUse", "/Users/u/src"),
			env: map[string]string{offEnv: "1"},
		},
		"no agent":      {args: nil, stdin: claudeEvent("PostToolUse", "/Users/u/src")},
		"unknown agent": {args: []string{"cursor"}, stdin: claudeEvent("PostToolUse", "/Users/u/src")},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			records := hookRun(t, tc.settings, tc.args, tc.stdin, tc.env)
			if tc.want == "" {
				if len(records) != 0 {
					t.Fatalf("queued %d records, want none", len(records))
				}
				return
			}
			if len(records) != 1 {
				t.Fatalf("queued %d records, want 1", len(records))
			}
			if got := string(records[0].Payload); got != tc.want {
				t.Errorf("payload = %s, want %s", got, tc.want)
			}
			var meta hook.Meta
			if err := json.Unmarshal(records[0].Kind, &meta); err != nil {
				t.Fatal(err)
			}
			if meta.Kind != hook.KindHook || string(meta.Agent) != tc.args[0] {
				t.Errorf("kind = %+v, want hook of %s", meta, tc.args[0])
			}
			if age := time.Since(time.Unix(0, meta.ReceivedUnixNano)); age < 0 || age > time.Minute {
				t.Errorf("received %v ago, want now", age)
			}
		})
	}
}

// TestLegacyHook checks that the prototype's hook call queues an event exactly as
// hottell hook does, and that any other flag call is a hook call of no agent.
func TestLegacyHook(t *testing.T) {
	t.Parallel()

	stdin := claudeEvent("PostToolUse", "/Users/u/src")
	tests := map[string]struct {
		args []string
		// agent is the hook call it must match; empty means nothing is queued.
		agent string
	}{
		"-agent claude":           {args: []string{"-agent", "claude"}, agent: "claude"},
		"-agent codex":            {args: []string{"-agent", "codex"}, agent: "codex"},
		"--agent claude":          {args: []string{"--agent", "claude"}, agent: "claude"},
		"-agent=claude":           {args: []string{"-agent=claude"}, agent: "claude"},
		"--agent=codex":           {args: []string{"--agent=codex"}, agent: "codex"},
		"-config x -agent claude": {args: []string{"-config", "x", "-agent", "claude"}, agent: "claude"},
		"-agent codex -config=x":  {args: []string{"-agent", "codex", "-config=x"}, agent: "codex"},
		"-config -agent":          {args: []string{"-config", "-agent", "-agent", "claude"}, agent: "claude"},
		"-agent cursor":           {args: []string{"-agent", "cursor"}},
		"-agent without value":    {args: []string{"-agent"}},
		"-unknown":                {args: []string{"-unknown"}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := invokeRun(t, "", tc.args, stdin, nil)
			if tc.agent == "" {
				if len(got) != 0 {
					t.Fatalf("queued %d records, want none", len(got))
				}
				return
			}
			want := hookRun(t, "", []string{tc.agent}, stdin, nil)
			if len(got) != 1 || len(want) != 1 {
				t.Fatalf("queued %d records, hook %s queued %d, want 1 each", len(got), tc.agent, len(want))
			}
			if string(got[0].Payload) != string(want[0].Payload) {
				t.Errorf("payload = %s, want %s", got[0].Payload, want[0].Payload)
			}
			var gotMeta, wantMeta hook.Meta
			if err := json.Unmarshal(got[0].Kind, &gotMeta); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(want[0].Kind, &wantMeta); err != nil {
				t.Fatal(err)
			}
			if gotMeta.Kind != wantMeta.Kind || gotMeta.Agent != wantMeta.Agent {
				t.Errorf("kind = %+v, want %+v", gotMeta, wantMeta)
			}
		})
	}
}

// TestLegacyHookLogsUnknownAgent checks that a flag call without an agent reads stdin,
// stays silent, exits 0 and leaves one line in the hook log.
func TestLegacyHookLogsUnknownAgent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	env := map[string]string{"HOME": testHome, state.HomeEnv: root}
	stdin := strings.NewReader(claudeEvent("PostToolUse", "/Users/u/src"))
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-unknown"}, stdin, &stdout, &stderr, lookup(env)); code != exitOK {
		t.Errorf("exit code = %d, want %d", code, exitOK)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout = %q, stderr = %q, want both empty", stdout.String(), stderr.String())
	}
	if stdin.Len() != 0 {
		t.Errorf("%d bytes of stdin left unread, want all read", stdin.Len())
	}
	log, err := os.ReadFile(filepath.Join(state.PathsIn(root).Logs, hookLog))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(log), "\n"); lines != 1 || !strings.Contains(string(log), errUnknownAgent.Error()) {
		t.Errorf("hook log = %q, want one line with %q", log, errUnknownAgent)
	}
}

func TestHookLogsErrors(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	paths := state.PathsIn(root)
	if err := os.WriteFile(paths.SettingsFile(), []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": testHome, state.HomeEnv: root}

	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader(claudeEvent("PostToolUse", "/Users/u/src"))
	if code := run([]string{"hook", "claude"}, stdin, &stdout, &stderr, lookup(env)); code != exitOK {
		t.Errorf("exit code = %d, want %d", code, exitOK)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	log, err := os.ReadFile(filepath.Join(paths.Logs, hookLog))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "read settings") {
		t.Errorf("hook log = %q, want the settings error", log)
	}
	if records, _ := queue.Open(paths, 0, nil).Next(1 << 30); len(records) != 0 {
		t.Errorf("queued %d records with unknown settings, want none", len(records))
	}
}

func TestHookRecoversPanic(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	env := map[string]string{state.HomeEnv: root}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"hook", "claude"}, panicReader{}, &stdout, &stderr, lookup(env)); code != exitOK {
		t.Errorf("exit code = %d, want %d", code, exitOK)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	log, err := os.ReadFile(filepath.Join(state.PathsIn(root).Logs, hookLog))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "panic: broken reader") {
		t.Errorf("hook log = %q, want the panic", log)
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) {
	panic("broken reader") //nolint:forbidigo // the hook must survive a panic
}

// hookBudget is how long hottell hook may take from process start to exit on a 64 KB
// event.
const hookBudget = 50 * time.Millisecond

// BenchmarkHookProcess runs the built binary on a 64 KB event, from process start to
// exit, and fails when the median run takes longer than hookBudget. The median, not the
// mean, so that a run the scheduler delayed does not decide the result.
func BenchmarkHookProcess(b *testing.B) {
	dir := b.TempDir()
	bin := filepath.Join(dir, "hottell")
	if out, err := exec.CommandContext(b.Context(), "go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		b.Fatalf("build: %v\n%s", err, out)
	}
	root := filepath.Join(dir, "state")
	if err := os.MkdirAll(root, 0o700); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(denySettings), 0o600); err != nil {
		b.Fatal(err)
	}
	event := []byte(`{"session_id":"s1","hook_event_name":"PostToolUse","cwd":"/Users/u/src","prompt":"p","tool_response":"` +
		strings.Repeat("x", 64<<10) + `"}`)

	var took []time.Duration
	for b.Loop() {
		start := time.Now()
		cmd := exec.CommandContext(b.Context(), bin, "hook", "claude")
		cmd.Env = []string{"HOME=" + testHome, state.HomeEnv + "=" + root}
		cmd.Stdin = bytes.NewReader(event)
		out, err := cmd.Output()
		if err != nil || len(out) != 0 {
			b.Fatalf("hook: err %v, stdout %q", err, out)
		}
		took = append(took, time.Since(start))
	}
	slices.Sort(took)
	median := took[len(took)/2]
	b.ReportMetric(float64(median)/float64(time.Millisecond), "median-ms")
	if median > hookBudget {
		b.Fatalf("median hook run took %v, want at most %v", median, hookBudget)
	}
	runs := len(took)
	if records, _ := queue.Open(state.PathsIn(root), 0, nil).Next(1 << 30); len(records) != runs {
		b.Fatalf("queued %d records, want %d", len(records), runs)
	}
}
