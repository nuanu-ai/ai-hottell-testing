package telemetry

import (
	"context"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// NativeReader reads the native OpenTelemetry of the agents, the events and metrics they send
// themselves, not the records of hottell. Every method honours the whole telemetry.Filter, which
// must carry a period (From and To, To after From) or a SessionID: otherwise the method answers
// telemetry.ErrNoNativePeriod. A method of one agent answers nothing when Filter.Agent names the
// other. Every method answers telemetry.ErrUnavailable while the store cannot be read.
type NativeReader interface {
	// ClaudeEvents returns the events of Claude Code, oldest first.
	ClaudeEvents(ctx context.Context, f telemetry.Filter) ([]telemetry.ClaudeEvent, error)
	// ClaudeMetrics returns the claude_code.* data points, oldest first.
	ClaudeMetrics(ctx context.Context, f telemetry.Filter) ([]telemetry.ClaudeMetric, error)
	// CodexSSE returns the codex.sse_event events that carry a conversation.id, oldest first.
	CodexSSE(ctx context.Context, f telemetry.Filter) ([]telemetry.CodexSSE, error)
	// CodexCoverage counts the Codex events by service, event and link to a session.
	CodexCoverage(ctx context.Context, f telemetry.Filter) ([]telemetry.CodexCoverage, error)
}
