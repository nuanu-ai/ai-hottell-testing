package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

// TelemetrySettings is the deny settings document of a user, in the form of
// docs/specs/hottell-contract/settings.schema.json without its version, which is kept beside
// the document. Its JSON is the full form: every field of the schema, defaults included.
type TelemetrySettings struct {
	Agents          AgentsTelemetrySettings `json:"agents"`
	Folders         FolderRules             `json:"folders"`
	BackfillHistory bool                    `json:"backfill_history"`
}

// AgentsTelemetrySettings holds the deny settings of each agent.
type AgentsTelemetrySettings struct {
	Claude AgentTelemetrySettings `json:"claude"`
	Codex  AgentTelemetrySettings `json:"codex"`
}

// AgentTelemetrySettings is what is denied of one agent: the agent itself, its sources,
// hook events and top-level hook fields, and categories of its native OTel content.
type AgentTelemetrySettings struct {
	Enabled       bool             `json:"enabled"`
	Sources       TelemetrySources `json:"sources"`
	HookEvents    DeniedList       `json:"hook_events"`
	HookFields    DeniedList       `json:"hook_fields"`
	NativeContent DeniedList       `json:"native_content"`
}

// TelemetrySources tells which sources of an agent are sent; false turns one off.
type TelemetrySources struct {
	Hooks         bool `json:"hooks"`
	Transcripts   bool `json:"transcripts"`
	NativeMetrics bool `json:"native_metrics"`
	NativeLogs    bool `json:"native_logs"`
	NativeTraces  bool `json:"native_traces"`
}

// DeniedList is a list of denied values of one kind.
type DeniedList struct {
	Denied []string `json:"denied"`
}

// FolderRules are the project folder patterns: a session is denied when its cwd matches a
// pattern of Denied and none of Allowed.
type FolderRules struct {
	Denied  []string `json:"denied"`
	Allowed []string `json:"allowed"`
}

// DefaultTelemetrySettings returns the settings of a user who has denied nothing: every
// agent and source is on, the lists are empty and history is not backfilled.
func DefaultTelemetrySettings() TelemetrySettings {
	return TelemetrySettings{
		Agents:  AgentsTelemetrySettings{Claude: defaultAgent(), Codex: defaultAgent()},
		Folders: FolderRules{Denied: []string{}, Allowed: []string{}},
	}
}

// defaultAgent returns the settings of an agent of which nothing is denied.
func defaultAgent() AgentTelemetrySettings {
	return AgentTelemetrySettings{
		Enabled: true,
		Sources: TelemetrySources{
			Hooks: true, Transcripts: true, NativeMetrics: true, NativeLogs: true, NativeTraces: true,
		},
		HookEvents:    DeniedList{Denied: []string{}},
		HookFields:    DeniedList{Denied: []string{}},
		NativeContent: DeniedList{Denied: []string{}},
	}
}

// ParseTelemetrySettings checks data against the settings schema and returns the document
// with defaults for the absent fields. Anything the schema does not allow — an unknown
// kind, agent, source, hook event or content category, a reserved or malformed hook field,
// a folder pattern not starting with / or ~/, a repeated value, a null or a value of the
// wrong type — is ErrInvalidTelemetrySettings. A version in data is checked and dropped:
// the server keeps it.
func ParseTelemetrySettings(data []byte) (TelemetrySettings, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return TelemetrySettings{}, invalidSettings("", "not JSON: %v", err)
	}
	if dec.More() {
		return TelemetrySettings{}, invalidSettings("", "data after the document")
	}

	settings := DefaultTelemetrySettings()
	err := object(root, "", map[string]func(any, string) error{
		"version": checkVersion,
		"agents": func(v any, path string) error {
			return object(v, path, map[string]func(any, string) error{
				"claude": agentRules(&settings.Agents.Claude, claudeHookEvents()),
				"codex":  agentRules(&settings.Agents.Codex, codexHookEvents()),
			})
		},
		"folders": func(v any, path string) error {
			return object(v, path, map[string]func(any, string) error{
				"denied":  list(&settings.Folders.Denied, checkFolderPattern),
				"allowed": list(&settings.Folders.Allowed, checkFolderPattern),
			})
		},
		"backfill_history": boolean(&settings.BackfillHistory),
	})
	if err != nil {
		return TelemetrySettings{}, err
	}
	return settings, nil
}

// agentRules returns the check of the settings of one agent, which it writes to agent.
func agentRules(agent *AgentTelemetrySettings, hookEvents []string) func(any, string) error {
	return func(v any, path string) error {
		return object(v, path, map[string]func(any, string) error{
			"enabled": boolean(&agent.Enabled),
			"sources": func(v any, path string) error {
				return object(v, path, map[string]func(any, string) error{
					"hooks":          boolean(&agent.Sources.Hooks),
					"transcripts":    boolean(&agent.Sources.Transcripts),
					"native_metrics": boolean(&agent.Sources.NativeMetrics),
					"native_logs":    boolean(&agent.Sources.NativeLogs),
					"native_traces":  boolean(&agent.Sources.NativeTraces),
				})
			},
			"hook_events":    deniedList(&agent.HookEvents, oneOf(hookEvents, "hook event")),
			"hook_fields":    deniedList(&agent.HookFields, checkHookField),
			"native_content": deniedList(&agent.NativeContent, oneOf(nativeContentCategories(), "content category")),
		})
	}
}

// object checks that v is a JSON object whose every key has a rule and applies the rules.
func object(v any, path string, rules map[string]func(any, string) error) error {
	m, ok := v.(map[string]any)
	if !ok {
		return invalidSettings(path, "want an object")
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Sorted, so the same document always fails on the same key.
	slices.Sort(keys)
	for _, k := range keys {
		rule, ok := rules[k]
		if !ok {
			return invalidSettings(path, "unknown field %q", k)
		}
		if err := rule(m[k], join(path, k)); err != nil {
			return err
		}
	}
	return nil
}

// deniedList returns the check of an object holding only a denied list.
func deniedList(dst *DeniedList, item func(string, string) error) func(any, string) error {
	return func(v any, path string) error {
		return object(v, path, map[string]func(any, string) error{"denied": list(&dst.Denied, item)})
	}
}

// list returns the check of an array of unique strings, each passing item, written to dst.
func list(dst *[]string, item func(s, path string) error) func(any, string) error {
	return func(v any, path string) error {
		items, ok := v.([]any)
		if !ok {
			return invalidSettings(path, "want an array")
		}
		out := make([]string, 0, len(items))
		for i, it := range items {
			itemPath := fmt.Sprintf("%s[%d]", path, i)
			s, ok := it.(string)
			if !ok {
				return invalidSettings(itemPath, "want a string")
			}
			if slices.Contains(out, s) {
				return invalidSettings(itemPath, "repeated value %q", s)
			}
			if err := item(s, itemPath); err != nil {
				return err
			}
			out = append(out, s)
		}
		*dst = out
		return nil
	}
}

// boolean returns the check of a boolean written to dst.
func boolean(dst *bool) func(any, string) error {
	return func(v any, path string) error {
		b, ok := v.(bool)
		if !ok {
			return invalidSettings(path, "want a boolean")
		}
		*dst = b
		return nil
	}
}

// checkVersion checks that v is a non-negative integer.
func checkVersion(v any, path string) error {
	n, ok := v.(json.Number)
	if !ok {
		return invalidSettings(path, "want an integer")
	}
	f, err := n.Float64()
	if err != nil || f < 0 || f != math.Trunc(f) {
		return invalidSettings(path, "want a non-negative integer")
	}
	return nil
}

// oneOf returns the check that a string is one of allowed.
func oneOf(allowed []string, what string) func(string, string) error {
	return func(s, path string) error {
		if !slices.Contains(allowed, s) {
			return invalidSettings(path, "unknown %s %q", what, s)
		}
		return nil
	}
}

// hookFieldPattern is the pattern of a top-level hook field name.
var hookFieldPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`) //nolint:gochecknoglobals // compiled once

// checkHookField checks a top-level hook field name: the service fields that tie a record
// to its session cannot be denied.
func checkHookField(s, path string) error {
	if !hookFieldPattern.MatchString(s) {
		return invalidSettings(path, "malformed hook field %q", s)
	}
	if slices.Contains([]string{"session_id", "hook_event_name", "cwd"}, s) {
		return invalidSettings(path, "hook field %q cannot be denied", s)
	}
	return nil
}

// checkFolderPattern checks that a folder pattern starts with / or ~/.
func checkFolderPattern(s, path string) error {
	if !strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "~/") {
		return invalidSettings(path, "folder pattern %q must start with / or ~/", s)
	}
	return nil
}

// claudeHookEvents returns the hook events of Claude Code.
func claudeHookEvents() []string {
	return []string{
		"SessionStart", "Setup", "InstructionsLoaded", "UserPromptSubmit", "UserPromptExpansion",
		"MessageDisplay", "PreToolUse", "PermissionRequest", "PermissionDenied", "PostToolUse",
		"PostToolUseFailure", "PostToolBatch", "Notification", "SubagentStart", "SubagentStop",
		"TaskCreated", "TaskCompleted", "Stop", "StopFailure", "TeammateIdle", "ConfigChange",
		"CwdChanged", "DirectoryAdded", "FileChanged", "WorktreeCreate", "WorktreeRemove",
		"PreCompact", "PostCompact", "PreModelSwitch", "PostModelSwitch", "Elicitation",
		"ElicitationResult", "SessionEnd",
	}
}

// codexHookEvents returns the hook events of Codex CLI.
func codexHookEvents() []string {
	return []string{
		"SessionStart", "SessionEnd", "UserPromptSubmit", "PreToolUse", "PermissionRequest",
		"PostToolUse", "PreCompact", "PostCompact", "SubagentStart", "SubagentStop", "Stop", "Interrupt",
	}
}

// nativeContentCategories returns the categories of native OTel content.
func nativeContentCategories() []string {
	return []string{"prompts", "assistant_responses", "tool_details", "tool_content", "raw_api_bodies"}
}

// join returns the path of key inside path.
func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// invalidSettings returns ErrInvalidTelemetrySettings with where and why the document is
// invalid.
func invalidSettings(path, format string, args ...any) error {
	reason := fmt.Sprintf(format, args...)
	if path != "" {
		reason = path + ": " + reason
	}
	return fmt.Errorf("%w: %s", ErrInvalidTelemetrySettings, reason)
}
