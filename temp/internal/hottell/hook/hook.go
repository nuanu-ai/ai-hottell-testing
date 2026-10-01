// Package hook turns one hook event an agent passed to hottell hook into a queue record,
// after the deny settings (docs/specs/hottell-contract/ingest.md, «Событие хука»).
package hook

import (
	"encoding/json"
	"fmt"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
)

// KindHook is the Kind of a hook event record.
const KindHook = "hook"

// Meta is the record's Kind in the queue: what the payload is and where it came from.
type Meta struct {
	Kind  string       `json:"kind"`
	Agent policy.Agent `json:"agent"`
	// ReceivedUnixNano is when hottell hook finished reading stdin: the event's time.
	ReceivedUnixNano int64 `json:"received_unix_nano"`
}

// Queue is where the records go.
type Queue interface {
	Put(kind, payload []byte) (string, error)
}

// Handle applies the settings to event of agent, received at receivedAt, and puts the
// event into q when it is sent. A damaged event — not a JSON object, or without a string
// session_id, hook_event_name or cwd — is sent unchanged when the agent and its hooks
// are on: the event, folder and field denials have nothing to apply to.
func Handle(settings policy.Settings, agent policy.Agent, event []byte, receivedAt time.Time, q Queue) error {
	payload, send := event, hooksOn(settings, agent)
	if send && !damaged(event) {
		payload, send = policy.Hook(settings, agent, event)
	}
	if !send {
		return nil
	}

	kind, err := json.Marshal(Meta{Kind: KindHook, Agent: agent, ReceivedUnixNano: receivedAt.UnixNano()})
	if err != nil {
		return fmt.Errorf("encode record kind: %w", err)
	}
	if _, err := q.Put(kind, payload); err != nil {
		return fmt.Errorf("queue hook event: %w", err)
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

func on(v *bool) bool { return v == nil || *v }
