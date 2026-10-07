// Package deep holds the use cases of the Deep reviews of sessions, the registry of
// proposals built from them and the skill opportunity reports, and the ports they reach the
// outside through.
package deep

import (
	"context"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
	"git.alva.dev/alva/harness-telemetry/internal/domain/journal"
	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// Reports keeps the versions of users' Deep reports.
type Reports interface {
	// InsertCandidate stores c as a candidate of its user and session, unless the user
	// already has a candidate or published version of the session with the same
	// CandidateSHA256: then it returns that version. The user's previous candidate of the
	// session becomes superseded; domain.ErrUserNotFound when there is no such user.
	InsertCandidate(ctx context.Context, c domain.DeepReport) (domain.DeepReport, error)
	// List returns the versions filter selects, newest submission first.
	List(ctx context.Context, filter domain.DeepReportFilter) ([]domain.DeepReport, error)
	// Candidate returns the version with id of the user with userID, in any status;
	// domain.ErrDeepReportNotFound when the user has no such version, another user's included.
	Candidate(ctx context.Context, userID, id uuid.UUID) (domain.DeepReport, error)
	// Publish stores review and publishes the candidate with id of the user with userID; the
	// user's published version of the session becomes superseded.
	// domain.ErrDeepReportNotCandidate when it is not a candidate. Within a transaction it is
	// part of it.
	Publish(ctx context.Context, userID, id uuid.UUID, review domain.DeepReview) (domain.DeepReport, error)
	// PublishedByUser returns the published versions of every session of the user with
	// userID, newest publication first.
	PublishedByUser(ctx context.Context, userID uuid.UUID) ([]domain.DeepReport, error)
}

// SkillReports keeps the versions of users' skill opportunity reports.
type SkillReports interface {
	// InsertCurrent stores report as the current report of its user; the previous current
	// one becomes superseded. A version of the user with the same document is returned as
	// it is, nothing written, the check under the same lock as the insert.
	// domain.ErrUserNotFound when there is no such user.
	InsertCurrent(ctx context.Context, report domain.SkillOpportunityReport) (domain.SkillOpportunityReport, error)
	// Current returns the current report of the user with userID;
	// domain.ErrSkillReportNotFound when the user has none.
	Current(ctx context.Context, userID uuid.UUID) (domain.SkillOpportunityReport, error)
	// History returns every report version of the user with userID, newest first.
	History(ctx context.Context, userID uuid.UUID) ([]domain.SkillOpportunityReport, error)
}

// Journal reads the decision journal (the Postgres adapter of HT-245).
type Journal interface {
	// Records returns the journal records of the user with userID, in journal order.
	Records(ctx context.Context, userID uuid.UUID) ([]journal.Record, error)
	// Lock takes, within the transaction of ctx, the advisory lock under which the journal
	// is appended to, and holds it to the transaction's end: a registry rebuilt under it
	// loses no record written meanwhile.
	Lock(ctx context.Context) error
}

// Proposals keeps the registry of each user: a projection rebuilt whole.
type Proposals interface {
	// ReplaceForUser makes proposals the whole registry of the user with userID and returns
	// how many appeared, changed or disappeared. Within a transaction it is part of it.
	ReplaceForUser(ctx context.Context, userID uuid.UUID, proposals []domain.Proposal) (int, error)
}

// TxManager runs a function within one transaction of the ports.
type TxManager interface {
	// WithinTx runs fn within a transaction: committed when fn returns nil, rolled back
	// when it returns an error.
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Transcripts reads the transcript lines the hottell binary sent (the ClickHouse reader).
type Transcripts interface {
	// SourcePrefix returns the sha256, in hex, of the lines 1 to records of the file key
	// names; an error when the store cannot tell, the prefix then not checked.
	SourcePrefix(ctx context.Context, key telemetry.TranscriptKey, records int) (string, error)
}
