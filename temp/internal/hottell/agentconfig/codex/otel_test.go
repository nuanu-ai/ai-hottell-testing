package codex_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/codex"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

const (
	serviceURL = "https://telemetry.example.test"
	token      = "ht_col_0123456789abcdef"
	// hookTrust is the hook trust table of foreign-otel.toml.
	hookTrust = "[hooks.state.\"/fixture/.codex/hooks.json:session_start:0:0\"]\n" +
		"trusted_hash = \"sha256:0000000000000000000000000000000000000000000000000000000000000000\"\n"
)

var creds = state.Credentials{IngestURL: serviceURL, CollectorToken: token} //nolint:gochecknoglobals // read-only fixture

// setup copies a fixture into a fresh CODEX_HOME and returns it with fresh state paths.
// An empty fixture name leaves config.toml absent.
func setup(t *testing.T, fixture string) (string, state.Paths) {
	t.Helper()
	home := t.TempDir()
	if fixture != "" {
		data, err := os.ReadFile(filepath.Join("testdata", fixture))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(codex.ConfigFile(home), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home, state.PathsIn(t.TempDir())
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	return readFile(t, filepath.Join("testdata", name))
}

func apply(t *testing.T, home string, paths state.Paths, settings policy.Settings) string {
	t.Helper()
	if err := codex.ApplyOTel(home, paths, creds, settings); err != nil {
		t.Fatalf("ApplyOTel: %v", err)
	}
	return readFile(t, codex.ConfigFile(home))
}

func remove(t *testing.T, home string, paths state.Paths) string {
	t.Helper()
	if err := codex.RemoveOTel(home, paths); err != nil {
		t.Fatalf("RemoveOTel: %v", err)
	}
	data, err := os.ReadFile(codex.ConfigFile(home))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func TestApplyOTelEmptyConfig(t *testing.T) {
	t.Parallel()

	want := fixture(t, "want-allowed.toml")
	for _, fixture := range []string{"", "empty.toml"} {
		t.Run("fixture "+fixture, func(t *testing.T) {
			t.Parallel()

			home, paths := setup(t, fixture)
			if got := apply(t, home, paths, policy.Settings{}); got != want {
				t.Errorf("config.toml:\n%s\nwant:\n%s", got, want)
			}
			if got := remove(t, home, paths); got != "" {
				t.Errorf("after RemoveOTel config.toml = %q, want empty", got)
			}
		})
	}
}

func TestApplyOTelIngestURLWithPath(t *testing.T) {
	t.Parallel()

	home, paths := setup(t, "")
	c := state.Credentials{IngestURL: serviceURL + "/v1/logs/", CollectorToken: token}
	if err := codex.ApplyOTel(home, paths, c, policy.Settings{}); err != nil {
		t.Fatal(err)
	}
	want := fixture(t, "want-allowed.toml")
	if got := readFile(t, codex.ConfigFile(home)); got != want {
		t.Errorf("config.toml:\n%s\nwant:\n%s", got, want)
	}
}

func TestApplyOTelNeedsCredentials(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]state.Credentials{
		"no URL":   {CollectorToken: token},
		"no token": {IngestURL: serviceURL},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			home, paths := setup(t, "foreign-otel.toml")
			if err := codex.ApplyOTel(home, paths, c, policy.Settings{}); err == nil {
				t.Fatal("ApplyOTel succeeded without credentials")
			}
			if got, want := readFile(t, codex.ConfigFile(home)), fixture(t, "foreign-otel.toml"); got != want {
				t.Errorf("config.toml changed:\n%s", got)
			}
		})
	}
}

// TestApplyOTelForeignFamily: config.toml with the user's own [otel] family scattered
// over the file, with comments, a multi-line string that looks like TOML, an array
// spanning lines, the MCP server and hook trust.
func TestApplyOTelForeignFamily(t *testing.T) {
	t.Parallel()

	original := fixture(t, "foreign-otel.toml")
	home, paths := setup(t, "foreign-otel.toml")

	got := apply(t, home, paths, policy.Settings{})
	if want := fixture(t, "foreign-otel.applied.toml"); got != want {
		t.Errorf("after ApplyOTel config.toml:\n%s\nwant:\n%s", got, want)
	}
	if !strings.HasSuffix(got, "\n\n"+fixture(t, "want-allowed.toml")) {
		t.Errorf("hottell's family is not the whole family at the end:\n%s", got)
	}
	if !strings.Contains(got, hookTrust) {
		t.Errorf("hook trust lost:\n%s", got)
	}

	restored := remove(t, home, paths)
	if want := fixture(t, "foreign-otel.removed.toml"); restored != want {
		t.Errorf("after RemoveOTel config.toml:\n%s\nwant:\n%s", restored, want)
	}
	// Every line comes back; only the [otel] tables and their comments moved to the end.
	for _, line := range strings.Split(original, "\n") {
		if !strings.Contains(restored, line+"\n") {
			t.Errorf("line %q of the original is missing after RemoveOTel", line)
		}
	}
	if _, err := os.Stat(filepath.Join(paths.Root, "codex-otel-backup.json")); !os.IsNotExist(err) {
		t.Errorf("backup still in the state after RemoveOTel: %v", err)
	}
}

// TestApplyOTelRootDottedFamily: the family written as root-level otel.x pairs, and a
// profile's own otel key that is not part of it.
func TestApplyOTelRootDottedFamily(t *testing.T) {
	t.Parallel()

	home, paths := setup(t, "root-otel.toml")

	if got, want := apply(t, home, paths, policy.Settings{}), fixture(t, "root-otel.applied.toml"); got != want {
		t.Errorf("after ApplyOTel config.toml:\n%s\nwant:\n%s", got, want)
	}
	if got, want := remove(t, home, paths), fixture(t, "root-otel.removed.toml"); got != want {
		t.Errorf("after RemoveOTel config.toml:\n%s\nwant:\n%s", got, want)
	}
}

func TestApplyOTelIdempotent(t *testing.T) {
	t.Parallel()

	for _, fixture := range []string{"", "foreign-otel.toml", "root-otel.toml"} {
		t.Run("fixture "+fixture, func(t *testing.T) {
			t.Parallel()

			home, paths := setup(t, fixture)
			denied := policy.Settings{Agents: policy.AgentsSettings{Codex: policy.AgentSettings{
				NativeContent: policy.Denied{Denied: []string{"prompts"}},
			}}}
			first := apply(t, home, paths, denied)
			backup := readFile(t, filepath.Join(paths.Root, "codex-otel-backup.json"))
			info, err := os.Stat(codex.ConfigFile(home))
			if err != nil {
				t.Fatal(err)
			}

			if second := apply(t, home, paths, denied); second != first {
				t.Errorf("second apply changed config.toml:\n%s\nfirst:\n%s", second, first)
			}
			again, err := os.Stat(codex.ConfigFile(home))
			if err != nil {
				t.Fatal(err)
			}
			if !again.ModTime().Equal(info.ModTime()) {
				t.Error("second apply rewrote config.toml")
			}

			// A new settings version replaces hottell's family and keeps the user's copy.
			if third := apply(t, home, paths, policy.Settings{}); third == first {
				t.Error("changed settings did not change config.toml")
			}
			if got := readFile(t, filepath.Join(paths.Root, "codex-otel-backup.json")); got != backup {
				t.Errorf("backup overwritten by a later apply:\n%s\nwant:\n%s", got, backup)
			}
		})
	}
}

func TestRemoveOTelWithoutBackup(t *testing.T) {
	t.Parallel()

	t.Run("user's family stays", func(t *testing.T) {
		t.Parallel()

		home, paths := setup(t, "foreign-otel.toml")
		if got, want := remove(t, home, paths), fixture(t, "foreign-otel.toml"); got != want {
			t.Errorf("RemoveOTel changed a config it never wrote:\n%s", got)
		}
	})
	t.Run("hottell's family goes", func(t *testing.T) {
		t.Parallel()

		home, paths := setup(t, "hottell-no-backup.toml")
		if got, want := remove(t, home, paths), "model = \"gpt-5-codex\"\n"; got != want {
			t.Errorf("config.toml = %q, want %q", got, want)
		}
	})
}

func TestApplyOTelKeepsModeAndSymlink(t *testing.T) {
	t.Parallel()

	home, paths := setup(t, "")
	target := filepath.Join(t.TempDir(), "dotfiles-config.toml")
	if err := os.WriteFile(target, []byte("model = \"gpt-5-codex\"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, codex.ConfigFile(home)); err != nil {
		t.Fatal(err)
	}

	apply(t, home, paths, policy.Settings{})

	if fi, err := os.Lstat(codex.ConfigFile(home)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config.toml is no longer a symlink: %v", err)
	}
	fi, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", fi.Mode().Perm())
	}
	if got, want := readFile(t, target), "model = \"gpt-5-codex\"\n\n"+fixture(t, "want-allowed.toml"); got != want {
		t.Errorf("the symlink's target:\n%s\nwant:\n%s", got, want)
	}
}

func ptr(b bool) *bool { return &b }

func content(categories ...string) policy.AgentSettings {
	return policy.AgentSettings{NativeContent: policy.Denied{Denied: categories}}
}

// TestApplyOTelDenials covers every kind of denial of settings.md against the mapping
// table of native-otel.md section 3; each golden file is the config written from an
// empty one.
func TestApplyOTelDenials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings policy.Settings
		want     string
	}{
		{name: "nothing denied", want: "want-allowed.toml"},
		{name: "agent disabled", settings: codexOnly(policy.AgentSettings{Enabled: ptr(false)}), want: "want-off.toml"},
		{name: "all native sources off", settings: codexOnly(policy.AgentSettings{Sources: policy.Sources{
			NativeMetrics: ptr(false), NativeLogs: ptr(false), NativeTraces: ptr(false),
		}}), want: "want-off.toml"},
		{name: "source native_metrics", settings: codexOnly(policy.AgentSettings{Sources: policy.Sources{NativeMetrics: ptr(false)}}), want: "want-no-metrics.toml"},
		{name: "source native_logs", settings: codexOnly(policy.AgentSettings{Sources: policy.Sources{NativeLogs: ptr(false)}}), want: "want-no-logs.toml"},
		{name: "source native_traces", settings: codexOnly(policy.AgentSettings{Sources: policy.Sources{NativeTraces: ptr(false)}}), want: "want-no-traces.toml"},
		{name: "sources hooks and transcripts", settings: codexOnly(policy.AgentSettings{Sources: policy.Sources{
			Hooks: ptr(false), Transcripts: ptr(false),
		}}), want: "want-allowed.toml"},
		{name: "hook events and fields", settings: codexOnly(policy.AgentSettings{
			HookEvents: policy.Denied{Denied: []string{"Stop"}}, HookFields: policy.Denied{Denied: []string{"cwd"}},
		}), want: "want-allowed.toml"},
		{name: "category prompts", settings: codexOnly(content("prompts")), want: "want-no-prompts.toml"},
		{name: "category assistant_responses", settings: codexOnly(content("assistant_responses")), want: "want-no-responses.toml"},
		{name: "category tool_details", settings: codexOnly(content("tool_details")), want: "want-allowed.toml"},
		{name: "category tool_content", settings: codexOnly(content("tool_content")), want: "want-no-tool-content.toml"},
		{name: "category raw_api_bodies", settings: codexOnly(content("raw_api_bodies")), want: "want-allowed.toml"},
		{name: "denials add up", settings: codexOnly(policy.AgentSettings{
			Enabled:       ptr(false),
			NativeContent: policy.Denied{Denied: []string{"prompts", "tool_content"}},
		}), want: "want-off-no-tool-content.toml"},
		{name: "folder denial", settings: policy.Settings{Folders: policy.Folders{Denied: []string{"~/**"}}}, want: "want-allowed.toml"},
		{name: "Claude Code disabled", settings: policy.Settings{Agents: policy.AgentsSettings{
			Claude: policy.AgentSettings{Enabled: ptr(false)},
		}}, want: "want-allowed.toml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			home, paths := setup(t, "")
			if got, want := apply(t, home, paths, tt.settings), fixture(t, tt.want); got != want {
				t.Errorf("config.toml:\n%s\nwant %s:\n%s", got, tt.want, want)
			}
		})
	}
}

func codexOnly(a policy.AgentSettings) policy.Settings {
	return policy.Settings{Agents: policy.AgentsSettings{Codex: a}}
}

func TestApplyOTelBrokenConfig(t *testing.T) {
	t.Parallel()

	home, paths := setup(t, "broken.toml")
	if err := codex.ApplyOTel(home, paths, creds, policy.Settings{}); err == nil {
		t.Fatal("ApplyOTel succeeded on a config it cannot parse")
	}
	if got, want := readFile(t, codex.ConfigFile(home)), fixture(t, "broken.toml"); got != want {
		t.Errorf("config.toml changed:\n%s", got)
	}
	if err := codex.RemoveOTel(home, paths); err == nil {
		t.Fatal("RemoveOTel succeeded on a config it cannot parse")
	}
}
