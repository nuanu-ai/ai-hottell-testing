package telemetry

import (
	"context"
	"iter"
	"time"

	"github.com/google/uuid"

	domain "git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// HookReader reads the hook events the collector stores. Every read answers
// domain.ErrUnavailable while the telemetry store cannot be reached.
type HookReader interface {
	// HookEvents returns the hook events matching the filter, oldest first. The period
	// [filter.From, filter.To) is required.
	HookEvents(ctx context.Context, filter domain.Filter) ([]domain.HookEvent, error)
	// HookEventSeq yields what HookEvents returns, in its order, as the store reads it; an error
	// is the last pair it yields.
	HookEventSeq(ctx context.Context, filter domain.Filter) iter.Seq2[domain.HookEvent, error]
	// Pulse returns the bars of the ten minutes before q.Now and the sessions active in the two
	// minutes before it, of the person and agent of q when set.
	Pulse(ctx context.Context, q domain.PulseQuery) (domain.Pulse, error)
	// ActivityPulse returns, in one read, the hook events of each person by hour over [from, to),
	// one count per hour from from, the empty hours 0; the people without an event are absent.
	ActivityPulse(ctx context.Context, from, to time.Time) (map[uuid.UUID][]int, error)
	// SessionPrompts returns, in one read, the UserPromptSubmit events of the sessions stored in
	// [from, to), oldest first, each with only its time, person, agent, session and prompt.
	SessionPrompts(ctx context.Context, sessions []domain.ActiveSession, from, to time.Time) ([]domain.HookEvent, error)
}
