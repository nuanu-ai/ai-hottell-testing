// Package policy applies the user's deny settings to a hook event and to a transcript
// line, in the order docs/specs/hottell-contract/settings.md sets. Both functions are
// pure: they read only their arguments, so the hook command and the transcript readers
// share them.
package policy

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Agent names an agent as the settings document does.
type Agent string

// Agents the settings know.
const (
	Claude Agent = "claude"
	Codex  Agent = "codex"
)

// Settings is the deny settings document of settings.schema.json. A field left out of
// the document keeps its default: everything is allowed and history is not backfilled.
type Settings struct {
	Version         int64          `json:"version"`
	Agents          AgentsSettings `json:"agents"`
	Folders         Folders        `json:"folders"`
	BackfillHistory bool           `json:"backfill_history"`

	// Home is the home directory of this machine; it replaces ~ in folder patterns.
	Home string `json:"-"`
}

// AgentsSettings holds the denials of each agent.
type AgentsSettings struct {
	Claude AgentSettings `json:"claude"`
	Codex  AgentSettings `json:"codex"`
}

// AgentSettings holds the denials of one agent. A nil Enabled means enabled.
type AgentSettings struct {
	Enabled       *bool   `json:"enabled,omitempty"`
	Sources       Sources `json:"sources"`
	HookEvents    Denied  `json:"hook_events"`
	HookFields    Denied  `json:"hook_fields"`
	NativeContent Denied  `json:"native_content"`
}

// Sources switches the agent's data sources off. A nil source is on.
type Sources struct {
	Hooks         *bool `json:"hooks,omitempty"`
	Transcripts   *bool `json:"transcripts,omitempty"`
	NativeMetrics *bool `json:"native_metrics,omitempty"`
	NativeLogs    *bool `json:"native_logs,omitempty"`
	NativeTraces  *bool `json:"native_traces,omitempty"`
}

// Denied is a list of denied names.
type Denied struct {
	Denied []string `json:"denied"`
}

// Folders holds the folder patterns: a cwd is denied when it matches a Denied pattern
// and no Allowed one.
type Folders struct {
	Denied  []string `json:"denied"`
	Allowed []string `json:"allowed"`
}

// Parse decodes a settings document; home is the home directory of this machine.
func Parse(data []byte, home string) (Settings, error) {
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{}, fmt.Errorf("decode settings: %w", err)
	}
	s.Home = home
	return s, nil
}

// Hook applies the denials to a hook event of agent: it reports whether the event is
// sent and returns it with the denied top-level fields cut out. An event that is not a
// JSON object is not sent, since no denial can be checked on it.
func Hook(settings Settings, agent Agent, event []byte) (out []byte, send bool) {
	a, ok := settings.agent(agent)
	if !ok || !on(a.Enabled) || !on(a.Sources.Hooks) {
		return nil, false
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(event, &fields); err != nil || fields == nil {
		return nil, false
	}

	if cwd, ok := stringField(fields, "cwd"); ok && settings.folderDenied(cwd) {
		return nil, false
	}
	if name, ok := stringField(fields, "hook_event_name"); ok && slices.Contains(a.HookEvents.Denied, name) {
		return nil, false
	}

	cut := false
	for _, f := range a.HookFields.Denied {
		if _, ok := fields[f]; ok {
			delete(fields, f)
			cut = true
		}
	}
	if !cut {
		return event, true
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, false
	}
	return out, true
}

// Transcript reports whether a transcript line of agent is sent for a session run in
// cwd. A transcript goes whole or not at all, so the line itself is not inspected.
func Transcript(settings Settings, agent Agent, cwd string, _ []byte) bool {
	a, ok := settings.agent(agent)
	if !ok || !on(a.Enabled) || !on(a.Sources.Transcripts) {
		return false
	}
	return !settings.folderDenied(cwd)
}

func (s Settings) agent(agent Agent) (AgentSettings, bool) {
	switch agent {
	case Claude:
		return s.Agents.Claude, true
	case Codex:
		return s.Agents.Codex, true
	default:
		return AgentSettings{}, false
	}
}

// folderDenied reports whether cwd matches a denied pattern and no allowed one. An
// unknown cwd matches nothing.
func (s Settings) folderDenied(cwd string) bool {
	if cwd == "" {
		return false
	}
	matches := func(patterns []string) bool {
		return slices.ContainsFunc(patterns, func(p string) bool { return s.match(p, cwd) })
	}
	return matches(s.Folders.Denied) && !matches(s.Folders.Allowed)
}

// match compares cwd with pattern whole: ~ is the home directory, ** is any number of
// path segments including none, everything else is literal and case-sensitive, and a
// trailing slash on either side is ignored.
func (s Settings) match(pattern, cwd string) bool {
	switch {
	case pattern == "~":
		pattern = s.Home
	case strings.HasPrefix(pattern, "~/"):
		if s.Home == "" {
			return false
		}
		pattern = strings.TrimSuffix(s.Home, "/") + pattern[1:]
	}
	return matchSegments(segments(pattern), segments(cwd))
}

// segments splits path at slashes; the root / is the single empty segment, so /** matches
// it.
func segments(path string) []string {
	trimmed := strings.TrimRight(path, "/")
	if trimmed == "" {
		if path == "" {
			return nil
		}
		return []string{""}
	}
	return strings.Split(trimmed, "/")
}

func matchSegments(pattern, path []string) bool {
	if len(pattern) == 0 {
		return len(path) == 0
	}
	if pattern[0] == "**" {
		for i := 0; i <= len(path); i++ {
			if matchSegments(pattern[1:], path[i:]) {
				return true
			}
		}
		return false
	}
	return len(path) > 0 && pattern[0] == path[0] && matchSegments(pattern[1:], path[1:])
}

func stringField(fields map[string]json.RawMessage, name string) (string, bool) {
	raw, ok := fields[name]
	if !ok {
		return "", false
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", false
	}
	return v, true
}

func on(v *bool) bool { return v == nil || *v }
