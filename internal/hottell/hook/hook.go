// Package hook turns one hook event an agent passed to hottell hook into a queue record,
// after the deny settings (docs/specs/hottell-contract/ingest.md, «Событие хука»).
package hook

import (
	"encoding/json"
	"fmt"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/gitrepo"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/skills"
)

// KindHook is the Kind of a hook event record.
const KindHook = "hook"

// Meta is the record's Kind in the queue: what the payload is and where it came from.
type Meta struct {
	Kind  string       `json:"kind"`
	Agent policy.Agent `json:"agent"`
	// ReceivedUnixNano is when hottell hook finished reading stdin: the event's time.
	ReceivedUnixNano int64 `json:"received_unix_nano"`
	// RepoRoot and RepoRemote mark the git repository of the event's cwd; empty outside
	// a repository and for a damaged event.
	RepoRoot   string `json:"repo_root,omitempty"`
	RepoRemote string `json:"repo_remote,omitempty"`
}

// Queue is where the records go.
type Queue interface {
	Put(kind, payload []byte) (string, error)
}

// Handle applies the settings to event of agent, received at receivedAt, and puts the
// event into q when it is sent. A damaged event — not a JSON object, or without a string
// session_id, hook_event_name or cwd — is sent unchanged when the agent and its hooks
// are on: the event, folder and field denials have nothing to apply to. A SessionStart
// that is sent also leaves a skills snapshot request in skillsDir for the daemon; a
// damaged event never does. The record is marked with the git repository of the
// event's cwd, read from the files under it without running git.
func Handle(settings policy.Settings, agent policy.Agent, event []byte, receivedAt time.Time, q Queue, skillsDir string) error {
	payload, send := event, hooksOn(settings, agent)
	intact := send && !damaged(event)
	if intact {
		payload, send = policy.Hook(settings, agent, event)
	}
	if !send {
		return nil
	}

	meta := Meta{Kind: KindHook, Agent: agent, ReceivedUnixNano: receivedAt.UnixNano()}
	if intact {
		if repo, ok := gitrepo.Find(cwdOf(payload)); ok {
			meta.RepoRoot, meta.RepoRemote = repo.Root, repo.Remote
		}
	}
	kind, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("encode record kind: %w", err)
	}
	if _, err := q.Put(kind, payload); err != nil {
		return fmt.Errorf("queue hook event: %w", err)
	}
	// Only an event with all three string fields asks, and such an event is not damaged:
	// it has passed every denial.
	if req, ok := skills.IsRequested(payload); ok {
		req.Agent, req.At = agent, receivedAt
		return skills.WriteRequest(skillsDir, req)
	}
	return nil
}

// hooksOn reports whether the agent and its hooks source are on.
func hooksOn(settings policy.Settings, agent policy.Agent) bool {
	var a policy.AgentSettings
	switch agent {
	case policy.Claude:
		a = settings.Agents.Claude
	case policy.Codex:
		a = settings.Agents.Codex
	default:
		return false
	}
	return on(a.Enabled) && on(a.Sources.Hooks)
}

// damaged reports whether event lacks what every event of an agent has.
func damaged(event []byte) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(event, &fields); err != nil || fields == nil {
		return true
	}
	for _, name := range []string{"session_id", "hook_event_name", "cwd"} {
		// A null decodes to a nil pointer: it counts as absent.
		var v *string
		if err := json.Unmarshal(fields[name], &v); err != nil || v == nil {
			return true
		}
	}
	return false
}

// cwdOf is the string cwd of an event, or "".
func cwdOf(event []byte) string {
	var v struct {
		Cwd string `json:"cwd"`
	}
	_ = json.Unmarshal(event, &v)
	return v.Cwd
}

func on(v *bool) bool { return v == nil || *v }
