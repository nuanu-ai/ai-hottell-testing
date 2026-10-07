package telemetry

import (
	"context"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// SessionReader reads the sessions and the skill snapshots the collector stores. Both reads
// answer telemetry.ErrUnavailable while the telemetry store cannot be reached.
type SessionReader interface {
	// Sessions returns the sessions with hook events or transcript lines in the period of the
	// filter, the latest activity first. The period [filter.From, filter.To) is required:
	// telemetry.ErrNoSessionPeriod without it.
	Sessions(ctx context.Context, filter telemetry.Filter) ([]telemetry.SessionSummary, error)
	// SkillSnapshot returns the latest skill snapshot of the session of the person and agent;
	// false when there is none.
	SkillSnapshot(
		ctx context.Context, userID uuid.UUID, agent, sessionID string,
	) (telemetry.SkillSnapshot, bool, error)
	// SkillSnapshotsInPeriod returns, in one read, the latest skill snapshot of every session
	// with a snapshot stored in [f.From, f.To), ordered by person, agent and session; f.UserID
	// and f.Agent narrow it when set; a damaged snapshot is an entry with Err set, not an error
	// of the read. telemetry.ErrNoSessionPeriod without a period.
	SkillSnapshotsInPeriod(ctx context.Context, f telemetry.Filter) ([]telemetry.SessionSkillSnapshot, error)
	// SkillSnapshotsOfSessions returns, in one read and with no period, the latest skill
	// snapshot of every person and agent of the sessions sessionIDs, as SkillSnapshotsInPeriod
	// returns them; no ids read nothing.
	SkillSnapshotsOfSessions(ctx context.Context, sessionIDs []string) ([]telemetry.SessionSkillSnapshot, error)
}
