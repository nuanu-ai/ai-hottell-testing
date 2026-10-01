package domain_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// contractExamples is where the examples of the settings schema live.
const contractExamples = "../../docs/specs/hottell-contract/examples"

func readExample(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(contractExamples, name))
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	return data
}

func TestParseTelemetrySettingsDefaults(t *testing.T) {
	t.Parallel()

	for name, data := range map[string][]byte{
		"empty object":   []byte(`{}`),
		"empty example":  readExample(t, "settings-empty.json"),
		"empty sections": []byte(`{"agents":{"claude":{},"codex":{"sources":{}}},"folders":{}}`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := domain.ParseTelemetrySettings(data)
			if err != nil {
				t.Fatalf("ParseTelemetrySettings() error = %v", err)
			}
			if want := domain.DefaultTelemetrySettings(); !reflect.DeepEqual(got, want) {
				t.Fatalf("ParseTelemetrySettings() = %+v, want %+v", got, want)
			}
		})
	}
}

func TestParseTelemetrySettingsFullExample(t *testing.T) {
	t.Parallel()

	got, err := domain.ParseTelemetrySettings(readExample(t, "settings-full.json"))
	if err != nil {
		t.Fatalf("ParseTelemetrySettings() error = %v", err)
	}

	want := domain.DefaultTelemetrySettings()
	want.Agents.Claude.Sources.NativeTraces = false
	want.Agents.Claude.HookEvents.Denied = []string{"MessageDisplay"}
	want.Agents.Claude.HookFields.Denied = []string{"tool_response"}
	want.Agents.Claude.NativeContent.Denied = []string{"raw_api_bodies"}
	want.Agents.Codex.Enabled = false
	want.Folders = domain.FolderRules{Denied: []string{"~/work/**"}, Allowed: []string{"~/work/oss/**"}}
	want.BackfillHistory = true
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseTelemetrySettings() = %+v, want %+v", got, want)
	}
}

// The full form carries every field of the schema but the version, which is kept beside
// the document, and parses back to the same settings.
func TestTelemetrySettingsFullForm(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(domain.DefaultTelemetrySettings())
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var example map[string]any
	if err := json.Unmarshal(readExample(t, "settings-empty.json"), &example); err != nil {
		t.Fatalf("Unmarshal(example) error = %v", err)
	}
	delete(example, "version")
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !reflect.DeepEqual(got, example) {
		t.Fatalf("full form = %s, want the empty example without version", data)
	}

	back, err := domain.ParseTelemetrySettings(data)
	if err != nil {
		t.Fatalf("ParseTelemetrySettings(full form) error = %v", err)
	}
	if !reflect.DeepEqual(back, domain.DefaultTelemetrySettings()) {
		t.Fatalf("ParseTelemetrySettings(full form) = %+v, want defaults", back)
	}
}

func TestParseTelemetrySettingsInvalid(t *testing.T) {
	t.Parallel()

	tests := map[string][]byte{
		"invalid example":         readExample(t, "invalid-unknown-kind.json"),
		"not json":                []byte(`{`),
		"trailing data":           []byte(`{} {}`),
		"not an object":           []byte(`[]`),
		"null document":           []byte(`null`),
		"unknown kind":            []byte(`{"mcp_servers":{"denied":["github"]}}`),
		"unknown agent":           []byte(`{"agents":{"gemini":{}}}`),
		"field of other case":     []byte(`{"Agents":{}}`),
		"unknown agent field":     []byte(`{"agents":{"claude":{"mcp_servers":{"denied":[]}}}}`),
		"unknown source":          []byte(`{"agents":{"claude":{"sources":{"history":false}}}}`),
		"source not boolean":      []byte(`{"agents":{"claude":{"sources":{"hooks":"no"}}}}`),
		"enabled null":            []byte(`{"agents":{"codex":{"enabled":null}}}`),
		"unknown hook event":      []byte(`{"agents":{"claude":{"hook_events":{"denied":["SessionPause"]}}}}`),
		"event of other agent":    []byte(`{"agents":{"codex":{"hook_events":{"denied":["MessageDisplay"]}}}}`),
		"repeated hook event":     []byte(`{"agents":{"claude":{"hook_events":{"denied":["Stop","Stop"]}}}}`),
		"denied null":             []byte(`{"agents":{"claude":{"hook_events":{"denied":null}}}}`),
		"denied not strings":      []byte(`{"agents":{"claude":{"hook_fields":{"denied":[1]}}}}`),
		"unknown denied sibling":  []byte(`{"agents":{"claude":{"hook_fields":{"allowed":[]}}}}`),
		"reserved hook field":     []byte(`{"agents":{"claude":{"hook_fields":{"denied":["session_id"]}}}}`),
		"malformed hook field":    []byte(`{"agents":{"codex":{"hook_fields":{"denied":["tool_input.command"]}}}}`),
		"unknown content":         []byte(`{"agents":{"claude":{"native_content":{"denied":["secrets"]}}}}`),
		"relative folder":         []byte(`{"folders":{"denied":["work/**"]}}`),
		"unknown folder list":     []byte(`{"folders":{"ignored":[]}}`),
		"backfill not boolean":    []byte(`{"backfill_history":1}`),
		"negative version":        []byte(`{"version":-1}`),
		"fractional version":      []byte(`{"version":1.5}`),
		"version string":          []byte(`{"version":"3"}`),
		"null in allowed folders": []byte(`{"folders":{"allowed":[null]}}`),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := domain.ParseTelemetrySettings(data)
			if !errors.Is(err, domain.ErrInvalidTelemetrySettings) {
				t.Fatalf("ParseTelemetrySettings(%s) error = %v, want %v", data, err, domain.ErrInvalidTelemetrySettings)
			}
		})
	}
}

// A version in the document is valid but not part of the settings: the server keeps it.
func TestParseTelemetrySettingsDropsVersion(t *testing.T) {
	t.Parallel()

	got, err := domain.ParseTelemetrySettings([]byte(`{"version":7,"backfill_history":true}`))
	if err != nil {
		t.Fatalf("ParseTelemetrySettings() error = %v", err)
	}
	want := domain.DefaultTelemetrySettings()
	want.BackfillHistory = true
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseTelemetrySettings() = %+v, want %+v", got, want)
	}
}
