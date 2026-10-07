// Package telemetry holds the vocabulary of reading the telemetry the collector stores: the
// filter a read takes and the errors it answers.
package telemetry

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrUnavailable is answered by a read while the telemetry store cannot be reached or refuses
// the reader. The service keeps serving the ingest and sign-in, which do not read the store.
var ErrUnavailable = errors.New("telemetry store unavailable")

// Filter narrows a read of the telemetry. Everyone sees everyone's telemetry, so the person is
// an optional narrowing, not an access rule.
type Filter struct {
	// UserID is the person whose telemetry is read; uuid.Nil reads every person's.
	UserID uuid.UUID
	// Agent is "claude" or "codex"; empty reads both.
	Agent string
	// SessionID is the agent's session; empty reads every session.
	SessionID string
	// From and To bound the time of the records read, From included and To excluded.
	From, To time.Time
}
