package telemetry

import "time"

// The sources of the records a person sends.
const (
	// ReceivedHooks: the hook events of hottell.
	ReceivedHooks = "hooks"
	// ReceivedTranscripts: the transcript lines of hottell.
	ReceivedTranscripts = "transcripts"
	// ReceivedOTel: the native OpenTelemetry of the agent.
	ReceivedOTel = "otel"
)

// LastReceived is when the store last took a record of an agent from a source.
type LastReceived struct {
	// Agent is "claude" or "codex".
	Agent string
	// Source is ReceivedHooks, ReceivedTranscripts or ReceivedOTel.
	Source string
	At     time.Time
}
