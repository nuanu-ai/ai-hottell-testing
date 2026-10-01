package claude_test

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/claude"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

const testToken = "ht_col_0123456789abcdef"

func testCreds() state.Credentials {
	return state.Credentials{IngestURL: "https://telemetry.example.test/", CollectorToken: testToken}
}

func testResource() claude.Resource { return claude.Resource{Host: "mac-1", Version: "0.1.0"} }

// contractEnv is the env block of native-otel.md, section 1, when nothing is denied.
func contractEnv() map[string]string {
	return map[string]string{
		"CLAUDE_CODE_ENABLE_TELEMETRY":                "1",
		"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA":         "1",
		"OTEL_METRICS_EXPORTER":                       "otlp",
		"OTEL_LOGS_EXPORTER":                          "otlp",
		"OTEL_TRACES_EXPORTER":                        "otlp",
		"OTEL_EXPORTER_OTLP_ENDPOINT":                 "https://telemetry.example.test",
		"OTEL_EXPORTER_OTLP_PROTOCOL":                 "http/protobuf",
		"OTEL_EXPORTER_OTLP_HEADERS":                  "Authorization=Bearer ht_col_0123456789abcdef",
		"OTEL_METRIC_EXPORT_INTERVAL":                 "60000",
		"OTEL_LOGS_EXPORT_INTERVAL":                   "5000",
		"OTEL_TRACES_EXPORT_INTERVAL":                 "5000",
		"OTEL_LOG_USER_PROMPTS":                       "1",
		"OTEL_LOG_ASSISTANT_RESPONSES":                "1",
		"OTEL_LOG_TOOL_DETAILS":                       "1",
		"OTEL_LOG_TOOL_CONTENT":                       "1",
		"OTEL_LOG_RAW_API_BODIES":                     "1",
		"CLAUDE_CODE_OTEL_CONTENT_MAX_LENGTH":         "9007199254740991",
		"OTEL_ATTRIBUTE_VALUE_LENGTH_LIMIT":           "9007199254740991",
		"OTEL_LOGRECORD_ATTRIBUTE_VALUE_LENGTH_LIMIT": "9007199254740991",
		"OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT":      "9007199254740991",
		"OTEL_RESOURCE_ATTRIBUTES":                    "deployment.environment=hottell,host.name=mac-1,hottell.version=0.1.0",
	}
}

// with returns the contract env with the changes applied; an empty value deletes a key.
func with(changes map[string]string) map[string]string {
	env := contractEnv()
	for k, v := range changes {
		if v == "" {
			delete(env, k)
		} else {
			env[k] = v
		}
	}
	return env
}

// disabled is the env of an agent that is off, or whose three native sources are off.
func disabled() map[string]string {
	return with(map[string]string{
		"CLAUDE_CODE_ENABLE_TELEMETRY": "0",
		"OTEL_METRICS_EXPORTER":        "none",
		"OTEL_LOGS_EXPORTER":           "none",
		"OTEL_TRACES_EXPORTER":         "none",
		"OTEL_EXPORTER_OTLP_HEADERS":   "",
	})
}

func parseSettings(t *testing.T, doc string) policy.Settings {
	t.Helper()
	s, err := policy.Parse([]byte(doc), "/Users/me")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newOTel(t *testing.T, settingsPath string) claude.OTel {
	t.Helper()
	return claude.OTel{
		SettingsPath: settingsPath,
		RecordPath:   filepath.Join(t.TempDir(), "claude-otel.json"),
		Resource:     testResource(),
	}
}

func envOf(t *testing.T, path string) map[string]any {
	t.Helper()
	var doc struct {
		Env map[string]any `json:"env"`
	}
	if err := json.Unmarshal(readFile(t, path), &doc); err != nil {
		t.Fatalf("settings are not JSON: %v", err)
	}
	return doc.Env
}

// otelEnv returns the env of the file without the keys of foreign.json.
func otelEnv(t *testing.T, path string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for k, v := range envOf(t, path) {
		if k == "FOO" || k == "LIMIT" {
			continue
		}
		s, ok := v.(string)
		if !ok {
			t.Fatalf("env %s = %v is not a string", k, v)
		}
		out[k] = s
	}
	return out
}

func assertEnv(t *testing.T, got, want map[string]string) {
	t.Helper()
	for _, k := range slices.Sorted(maps.Keys(want)) {
		if got[k] != want[k] {
			t.Errorf("env %s = %q, want %q", k, got[k], want[k])
		}
	}
	for _, k := range slices.Sorted(maps.Keys(got)) {
		if _, ok := want[k]; !ok {
			t.Errorf("env has unexpected %s = %q", k, got[k])
		}
	}
}

func TestApplyOTelDenials(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		settings string
		want     map[string]string
	}{
		{"nothing denied", `{}`, contractEnv()},
		{
			"prompts", `{"agents":{"claude":{"native_content":{"denied":["prompts"]}}}}`,
			with(map[string]string{"OTEL_LOG_USER_PROMPTS": "0"}),
		},
		{
			"assistant_responses", `{"agents":{"claude":{"native_content":{"denied":["assistant_responses"]}}}}`,
			with(map[string]string{"OTEL_LOG_ASSISTANT_RESPONSES": "0"}),
		},
		{
			"tool_details", `{"agents":{"claude":{"native_content":{"denied":["tool_details"]}}}}`,
			with(map[string]string{"OTEL_LOG_TOOL_DETAILS": "0"}),
		},
		{
			"tool_content", `{"agents":{"claude":{"native_content":{"denied":["tool_content"]}}}}`,
			with(map[string]string{"OTEL_LOG_TOOL_CONTENT": "0"}),
		},
		{
			"raw_api_bodies", `{"agents":{"claude":{"native_content":{"denied":["raw_api_bodies"]}}}}`,
			with(map[string]string{"OTEL_LOG_RAW_API_BODIES": "0"}),
		},
		{
			"native_metrics", `{"agents":{"claude":{"sources":{"native_metrics":false}}}}`,
			with(map[string]string{"OTEL_METRICS_EXPORTER": "none"}),
		},
		{
			"native_logs", `{"agents":{"claude":{"sources":{"native_logs":false}}}}`,
			with(map[string]string{"OTEL_LOGS_EXPORTER": "none"}),
		},
		{
			"native_traces", `{"agents":{"claude":{"sources":{"native_traces":false}}}}`,
			with(map[string]string{"OTEL_TRACES_EXPORTER": "none"}),
		},
		{
			"all native sources", `{"agents":{"claude":{"sources":{"native_metrics":false,"native_logs":false,"native_traces":false}}}}`,
			disabled(),
		},
		{
			"agent disabled", `{"agents":{"claude":{"enabled":false,"native_content":{"denied":["prompts"]}}}}`,
			with(map[string]string{
				"CLAUDE_CODE_ENABLE_TELEMETRY": "0",
				"OTEL_METRICS_EXPORTER":        "none",
				"OTEL_LOGS_EXPORTER":           "none",
				"OTEL_TRACES_EXPORTER":         "none",
				"OTEL_EXPORTER_OTLP_HEADERS":   "",
				"OTEL_LOG_USER_PROMPTS":        "0",
			}),
		},
		{
			"denials add up", `{"agents":{"claude":{"sources":{"native_logs":false},"native_content":{"denied":["prompts","tool_content"]}}}}`,
			with(map[string]string{"OTEL_LOGS_EXPORTER": "none", "OTEL_LOG_USER_PROMPTS": "0", "OTEL_LOG_TOOL_CONTENT": "0"}),
		},
		// native-otel.md, section 4: Claude Code ignores OTEL_* in project settings, so a
		// folder denial leaves the native OTel alone.
		{"folder", `{"folders":{"denied":["~/work/secret","/tmp/x/**"]}}`, contractEnv()},
		{
			"hook denials", `{"agents":{"claude":{"sources":{"hooks":false,"transcripts":false},"hook_events":{"denied":["Stop"]},"hook_fields":{"denied":["prompt"]}}},"backfill_history":true}`,
			contractEnv(),
		},
		{"codex denials", `{"agents":{"codex":{"enabled":false,"native_content":{"denied":["prompts"]}}}}`, contractEnv()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := fixture(t, "foreign.json")
			if err := newOTel(t, path).Apply(testCreds(), parseSettings(t, tc.settings)); err != nil {
				t.Fatal(err)
			}
			assertEnv(t, otelEnv(t, path), tc.want)
		})
	}
}

func TestApplyOTelNoFolderFiles(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	project := filepath.Join(home, "work", "secret")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	settings, err := policy.Parse([]byte(`{"folders":{"denied":["~/work/secret"]}}`), home)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "settings.json")
	if err := newOTel(t, path).Apply(testCreds(), settings); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(project); err != nil || len(entries) != 0 {
		t.Errorf("denied folder holds %v (err %v), want nothing written there", entries, err)
	}
}

func TestApplyOTelKeepsOthers(t *testing.T) {
	t.Parallel()
	path := fixture(t, "foreign.json")
	before := topKeys(t, path)
	if err := newOTel(t, path).Apply(testCreds(), parseSettings(t, `{}`)); err != nil {
		t.Fatal(err)
	}
	if got := topKeys(t, path); !slices.Equal(got, before) {
		t.Errorf("top-level keys = %v, want %v", got, before)
	}
	data := readFile(t, path)
	for _, want := range []string{`"FOO": "bar & <baz>"`, `"LIMIT": 1.50`, `"big": 12345678901234567890`, `"command": "~/bin/guard.sh"`} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("settings lost %s:\n%s", want, data)
		}
	}
	env := envOf(t, path)
	if len(env) != len(contractEnv())+2 {
		t.Errorf("env has %d keys, want %d", len(env), len(contractEnv())+2)
	}
}

func TestApplyOTelRepeated(t *testing.T) {
	t.Parallel()
	path := fixture(t, "foreign.json")
	o := newOTel(t, path)
	settings := parseSettings(t, `{"agents":{"claude":{"native_content":{"denied":["prompts"]}}}}`)
	if err := o.Apply(testCreds(), settings); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	for _, p := range []string{path, o.RecordPath} {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}
	data, record := readFile(t, path), readFile(t, o.RecordPath)
	if err := o.Apply(testCreds(), settings); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readFile(t, path), data) || !bytes.Equal(readFile(t, o.RecordPath), record) {
		t.Error("a repeated apply changed the files")
	}
	for _, p := range []string{path, o.RecordPath} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(past) {
			t.Errorf("a repeated apply rewrote %s", p)
		}
	}
}

func TestApplyOTelChange(t *testing.T) {
	t.Parallel()
	path := fixture(t, "foreign.json")
	o := newOTel(t, path)
	if err := o.Apply(testCreds(), parseSettings(t, `{}`)); err != nil {
		t.Fatal(err)
	}
	if err := o.Apply(testCreds(), parseSettings(t, `{"agents":{"claude":{"enabled":false}}}`)); err != nil {
		t.Fatal(err)
	}
	assertEnv(t, otelEnv(t, path), disabled())

	rotated := state.Credentials{IngestURL: "https://other.example.test", CollectorToken: "ht_col_new"}
	if err := o.Apply(rotated, parseSettings(t, `{}`)); err != nil {
		t.Fatal(err)
	}
	assertEnv(t, otelEnv(t, path), with(map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": "https://other.example.test",
		"OTEL_EXPORTER_OTLP_HEADERS":  "Authorization=Bearer ht_col_new",
	}))
}

func TestRemoveOTel(t *testing.T) {
	t.Parallel()
	path := fixture(t, "foreign.json")
	original := readFile(t, path)
	o := newOTel(t, path)
	if err := o.Apply(testCreds(), parseSettings(t, `{}`)); err != nil {
		t.Fatal(err)
	}
	if err := o.Remove(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); !bytes.Equal(got, original) {
		t.Errorf("after remove the settings are\n%s\nwant the original\n%s", got, original)
	}
	if _, err := os.Stat(o.RecordPath); !os.IsNotExist(err) {
		t.Errorf("record still exists: %v", err)
	}
	if err := o.Remove(); err != nil {
		t.Errorf("a repeated remove: %v", err)
	}
}

func TestRemoveOTelDropsEmptyEnv(t *testing.T) {
	t.Parallel()
	path := fixture(t, "empty.json")
	o := newOTel(t, path)
	if err := o.Apply(testCreds(), parseSettings(t, `{}`)); err != nil {
		t.Fatal(err)
	}
	if err := o.Remove(); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(topKeys(t, path), "env") {
		t.Errorf("env left behind:\n%s", readFile(t, path))
	}
}

func TestRemoveOTelKeepsChangedValues(t *testing.T) {
	t.Parallel()
	path := fixture(t, "foreign.json")
	o := newOTel(t, path)
	if err := o.Apply(testCreds(), parseSettings(t, `{}`)); err != nil {
		t.Fatal(err)
	}
	edited := bytes.Replace(readFile(t, path), []byte(`"OTEL_LOGS_EXPORT_INTERVAL": "5000"`), []byte(`"OTEL_LOGS_EXPORT_INTERVAL": "1000"`), 1)
	if err := os.WriteFile(path, edited, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := o.Remove(); err != nil {
		t.Fatal(err)
	}
	assertEnv(t, otelEnv(t, path), map[string]string{"OTEL_LOGS_EXPORT_INTERVAL": "1000"})
}

func TestApplyOTelRejects(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		creds state.Credentials
		res   claude.Resource
	}{
		"token with comma":  {state.Credentials{IngestURL: "https://x.test", CollectorToken: "a,b"}, testResource()},
		"token with equals": {state.Credentials{IngestURL: "https://x.test", CollectorToken: "a=b"}, testResource()},
		"no token":          {state.Credentials{IngestURL: "https://x.test"}, testResource()},
		"no URL":            {state.Credentials{CollectorToken: "t"}, testResource()},
		"host with comma":   {testCreds(), claude.Resource{Host: "a,b", Version: "1"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := fixture(t, "foreign.json")
			original := readFile(t, path)
			o := claude.OTel{SettingsPath: path, RecordPath: filepath.Join(t.TempDir(), "r.json"), Resource: tc.res}
			if err := o.Apply(tc.creds, parseSettings(t, `{}`)); err == nil {
				t.Fatal("apply succeeded")
			}
			if !bytes.Equal(readFile(t, path), original) {
				t.Error("a rejected apply changed the settings")
			}
		})
	}
}

func TestApplyOTelMalformed(t *testing.T) {
	t.Parallel()
	path := fixture(t, "empty.json")
	if err := os.WriteFile(path, []byte(`{"env": ["x"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := newOTel(t, path).Apply(testCreds(), parseSettings(t, `{}`))
	if err == nil {
		t.Fatal("apply over a malformed env succeeded")
	}
	if got := string(readFile(t, path)); got != `{"env": ["x"]}` {
		t.Errorf("malformed settings rewritten: %s", got)
	}
}
