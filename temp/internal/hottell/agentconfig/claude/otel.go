package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

// Keys of the env section, docs/specs/hottell-contract/native-otel.md, section 1.
const (
	keyEnableTelemetry  = "CLAUDE_CODE_ENABLE_TELEMETRY"
	keyEnhancedTraces   = "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA"
	keyMetricsExporter  = "OTEL_METRICS_EXPORTER"
	keyLogsExporter     = "OTEL_LOGS_EXPORTER"
	keyTracesExporter   = "OTEL_TRACES_EXPORTER"
	keyEndpoint         = "OTEL_EXPORTER_OTLP_ENDPOINT"
	keyProtocol         = "OTEL_EXPORTER_OTLP_PROTOCOL"
	keyHeaders          = "OTEL_EXPORTER_OTLP_HEADERS"
	keyMetricInterval   = "OTEL_METRIC_EXPORT_INTERVAL"
	keyLogsInterval     = "OTEL_LOGS_EXPORT_INTERVAL"
	keyTracesInterval   = "OTEL_TRACES_EXPORT_INTERVAL"
	keyUserPrompts      = "OTEL_LOG_USER_PROMPTS"
	keyAssistantReplies = "OTEL_LOG_ASSISTANT_RESPONSES"
	keyToolDetails      = "OTEL_LOG_TOOL_DETAILS"
	keyToolContent      = "OTEL_LOG_TOOL_CONTENT"
	keyRawAPIBodies     = "OTEL_LOG_RAW_API_BODIES"
	keyContentMax       = "CLAUDE_CODE_OTEL_CONTENT_MAX_LENGTH"
	keyAttributeMax     = "OTEL_ATTRIBUTE_VALUE_LENGTH_LIMIT"
	keyLogAttributeMax  = "OTEL_LOGRECORD_ATTRIBUTE_VALUE_LENGTH_LIMIT"
	keySpanAttributeMax = "OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT"
	keyResource         = "OTEL_RESOURCE_ATTRIBUTES"
)

const (
	on       = "1"
	off      = "0"
	otlp     = "otlp"
	none     = "none"
	protocol = "http/protobuf"
	// maxLength is Number.MAX_SAFE_INTEGER, the largest integer Claude Code reads
	// exactly.
	maxLength   = "9007199254740991"
	environment = "hottell"
)

// categoryKey returns the env key that sends a content category of the settings.
func categoryKey(category string) (string, bool) {
	switch category {
	case "prompts":
		return keyUserPrompts, true
	case "assistant_responses":
		return keyAssistantReplies, true
	case "tool_details":
		return keyToolDetails, true
	case "tool_content":
		return keyToolContent, true
	case "raw_api_bodies":
		return keyRawAPIBodies, true
	}
	return "", false
}

// Resource names this machine and this binary in the resource attributes.
type Resource struct {
	// Host is the short name of the machine.
	Host string
	// Version is the version of the hottell binary.
	Version string
}

// OTel writes the native OpenTelemetry of Claude Code: the keys of native-otel.md in the
// env section of the settings file, docs/specs/hottell-contract/native-otel.md.
//
// A folder denial changes nothing here: Claude Code ignores OTEL_* in project settings
// (native-otel.md, section 4; apply-timing.md), so nothing is written into a folder's
// .claude directory.
type OTel struct {
	// SettingsPath is ~/.claude/settings.json.
	SettingsPath string
	// RecordPath is the hottell state file that remembers the env keys hottell wrote and
	// their values, so that they can be taken out again without touching others.
	RecordPath string
	// Resource goes into OTEL_RESOURCE_ATTRIBUTES.
	Resource Resource
}

// Apply makes the env section hold the keys that settings call for, with creds as the
// endpoint and the collector token. A key hottell wrote before that settings no longer
// call for is taken out, unless someone has changed its value since. Other keys are left
// alone. A file that already holds that is not written, so a repeated call changes
// nothing.
func (o OTel) Apply(creds state.Credentials, settings policy.Settings) error {
	want, err := Env(creds, settings, o.Resource)
	if err != nil {
		return err
	}
	written, err := o.readRecord()
	if err != nil {
		return err
	}
	// Until the file is written, the record keeps what may still be in it as well.
	pending := maps.Clone(written)
	maps.Copy(pending, want)
	if err := o.writeRecord(written, pending); err != nil {
		return err
	}
	if err := editSection(o.SettingsPath, "env", func(env *object) (bool, error) {
		return applyEnv(env, want, written)
	}); err != nil {
		return err
	}
	return o.writeRecord(pending, want)
}

// Remove takes out of the env section every key hottell wrote, unless someone has changed
// its value since, and forgets them. Other keys are left alone; no record, or no file, is
// nothing to remove.
func (o OTel) Remove() error {
	written, err := o.readRecord()
	if err != nil || len(written) == 0 {
		return err
	}
	if err := editSection(o.SettingsPath, "env", func(env *object) (bool, error) {
		return applyEnv(env, nil, written)
	}); err != nil {
		return err
	}
	return o.writeRecord(written, nil)
}

// Env returns the env keys and values for settings, by the tables of native-otel.md,
// sections 1 and 3.
func Env(creds state.Credentials, settings policy.Settings, res Resource) (map[string]string, error) {
	endpoint := strings.TrimRight(creds.IngestURL, "/")
	if endpoint == "" {
		return nil, errors.New("no ingest URL")
	}
	for name, v := range map[string]string{"collector token": creds.CollectorToken, "host": res.Host, "version": res.Version} {
		if v == "" || strings.ContainsAny(v, ",=") {
			return nil, fmt.Errorf("%s %q is empty or holds , or =", name, v)
		}
	}

	env := map[string]string{
		keyEnableTelemetry:  on,
		keyEnhancedTraces:   on,
		keyMetricsExporter:  otlp,
		keyLogsExporter:     otlp,
		keyTracesExporter:   otlp,
		keyEndpoint:         endpoint,
		keyProtocol:         protocol,
		keyHeaders:          "Authorization=Bearer " + creds.CollectorToken,
		keyMetricInterval:   "60000",
		keyLogsInterval:     "5000",
		keyTracesInterval:   "5000",
		keyUserPrompts:      on,
		keyAssistantReplies: on,
		keyToolDetails:      on,
		keyToolContent:      on,
		keyRawAPIBodies:     on,
		keyContentMax:       maxLength,
		keyAttributeMax:     maxLength,
		keyLogAttributeMax:  maxLength,
		keySpanAttributeMax: maxLength,
		keyResource:         "deployment.environment=" + environment + ",host.name=" + res.Host + ",hottell.version=" + res.Version,
	}

	agent := settings.Agents.Claude
	for _, category := range agent.NativeContent.Denied {
		if key, ok := categoryKey(category); ok {
			env[key] = off
		}
	}
	sources := agent.Sources
	if !enabled(sources.NativeMetrics) {
		env[keyMetricsExporter] = none
	}
	if !enabled(sources.NativeLogs) {
		env[keyLogsExporter] = none
	}
	if !enabled(sources.NativeTraces) {
		env[keyTracesExporter] = none
	}
	allOff := !enabled(sources.NativeMetrics) && !enabled(sources.NativeLogs) && !enabled(sources.NativeTraces)
	if !enabled(agent.Enabled) || allOff {
		env[keyEnableTelemetry] = off
		env[keyMetricsExporter] = none
		env[keyLogsExporter] = none
		env[keyTracesExporter] = none
		delete(env, keyHeaders)
	}
	return env, nil
}

// applyEnv sets every key of want, and takes out every key of written that want lacks
// while it still holds the value hottell wrote.
func applyEnv(env *object, want, written map[string]string) (bool, error) {
	changed := false
	for _, key := range slices.Sorted(maps.Keys(written)) {
		if _, keep := want[key]; keep {
			continue
		}
		if raw, found := env.get(key); found && holds(raw, written[key]) {
			env.remove(key)
			changed = true
		}
	}
	for _, key := range slices.Sorted(maps.Keys(want)) {
		if raw, found := env.get(key); found && holds(raw, want[key]) {
			continue
		}
		value, err := marshal(want[key])
		if err != nil {
			return false, err
		}
		env.set(key, value)
		changed = true
	}
	return changed, nil
}

// holds reports whether raw is the JSON string value.
func holds(raw json.RawMessage, value string) bool {
	var s string
	return json.Unmarshal(raw, &s) == nil && s == value
}

func enabled(v *bool) bool { return v == nil || *v }

// record is the state file of the env keys hottell wrote.
type record struct {
	Env map[string]string `json:"env"`
}

func (o OTel) readRecord() (map[string]string, error) {
	data, err := os.ReadFile(o.RecordPath)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", o.RecordPath, err)
	}
	var r record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("decode %s: %w", o.RecordPath, err)
	}
	if r.Env == nil {
		r.Env = map[string]string{}
	}
	return r.Env, nil
}

// writeRecord saves env as the record, unless it equals the saved one old. An empty
// record is removed.
func (o OTel) writeRecord(old, env map[string]string) error {
	if maps.Equal(old, env) {
		return nil
	}
	if len(env) == 0 {
		if err := os.Remove(o.RecordPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", o.RecordPath, err)
		}
		return nil
	}
	data, err := json.Marshal(record{Env: env})
	if err != nil {
		return fmt.Errorf("encode %s: %w", o.RecordPath, err)
	}
	return state.WriteFileAtomic(o.RecordPath, data)
}
