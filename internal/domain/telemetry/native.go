package telemetry

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNoNativePeriod is answered by a read of the native OpenTelemetry whose filter has neither a
// period (From and To, To after From) nor a session, so that a read never scans the whole store.
var ErrNoNativePeriod = errors.New("native reads need a period (from and to, to after from) or a session")

// ClaudeEvent is one event of Claude Code's native OpenTelemetry: an api_request, api_error,
// tool_result, tool_decision, user_prompt or subagent_completed. The fields an event does not
// carry are zero: the collector stores every attribute as text, and the reader turns the numbers
// that are absent or not numeric into 0.
type ClaudeEvent struct {
	Time time.Time
	// UserID is the person the server attributed the record to (hottell.user.id); uuid.Nil
	// when it did not.
	UserID    uuid.UUID
	Event     string
	SessionID string
	PromptID  string
	Model     string
	// InputTokens, OutputTokens, CacheReadTokens and CacheCreationTokens are the tokens of an
	// api_request; InputTokens does not include the cache tokens.
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
	// CostUSD is Claude Code's own estimate of the request's cost, not an invoice.
	CostUSD float64
	// CostRecorded tells that the event carried CostUSD as a finite, non-negative number: a
	// CostUSD of 0 without it is a cost nobody recorded, not a free request.
	CostRecorded bool
	// DurationMs is the duration of a request or a tool.
	DurationMs int64
	// QuerySource, ToolName, ToolUseID, Success, Error, Decision, Source, StatusCode and AgentType
	// are the attributes of the other events, empty when the event does not carry them. Error is
	// cut to its first 300 characters.
	QuerySource string
	ToolName    string
	ToolUseID   string
	Success     bool
	Error       string
	Decision    string
	Source      string
	StatusCode  string
	AgentType   string
}

// ClaudeMetric is one data point of a claude_code.* metric of Claude Code's native
// OpenTelemetry. Time is kept to the second.
type ClaudeMetric struct {
	Time time.Time
	// UserID is the person the server attributed the point to; uuid.Nil when it did not.
	UserID    uuid.UUID
	Metric    string
	SessionID string
	// Type is the metric's "type" attribute, for example the token kind of claude_code.token.usage.
	Type  string
	Model string
	Value float64
}

// CodexSSE is one codex.sse_event of Codex's native OpenTelemetry that belongs to a session.
// Only the event of Kind "response.completed" carries the token counts; on the others they are 0.
type CodexSSE struct {
	Time time.Time
	// UserID is the person the server attributed the event to; uuid.Nil when it did not.
	UserID uuid.UUID
	// SessionID is the event's conversation.id, the Codex session (thread).
	SessionID string
	// Service is the client's originator, the service.name: codex_exec, codex-app-server and so on.
	Service         string
	Kind            string
	Model           string
	InputTokens     int64
	CachedTokens    int64
	OutputTokens    int64
	ReasoningTokens int64
}

// CodexCoverage counts the Codex events of one service, event name and endpoint, split by
// whether they are linked to a session, that is, carry a conversation.id.
type CodexCoverage struct {
	Service  string
	Event    string
	Endpoint string
	// Linked tells whether the counted events carry a conversation.id.
	Linked bool
	// Events is the number of events; Sessions the number of distinct conversation.id among them.
	Events   uint64
	Sessions uint64
}
