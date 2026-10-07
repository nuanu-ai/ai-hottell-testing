package ioc

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/application/deep"
	"git.alva.dev/alva/harness-telemetry/internal/application/journal"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
	jr "git.alva.dev/alva/harness-telemetry/internal/domain/journal"
)

// reviewReads gives the API the Deep reports, the registry, the journal and the skill reports
// (handler.Review).
type reviewReads struct {
	stores  *DeepStores
	deep    *deep.Service
	journal *journal.Service
	people  userNames
}

func (r reviewReads) DeepReports(ctx context.Context, filter domain.DeepReportFilter) ([]domain.DeepReport, error) {
	return r.stores.Reports.List(ctx, filter) //nolint:wrapcheck // the store names what failed
}

func (r reviewReads) Proposals(ctx context.Context, filter domain.ProposalFilter) ([]domain.UserProposal, error) {
	return r.stores.Proposals.List(ctx, filter) //nolint:wrapcheck // the store names what failed
}

func (r reviewReads) Proposal(ctx context.Context, userID uuid.UUID, proposalID string) (domain.Proposal, error) {
	return r.stores.Proposals.Get(ctx, userID, proposalID) //nolint:wrapcheck // a domain error picks the status
}

// Decide records the owner's decision through the journal service.
func (r reviewReads) Decide(ctx context.Context, userID uuid.UUID, proposalID, status, authorityRef string) error {
	_, err := r.journal.AppendDecision(ctx, userID, proposalID, status, authorityRef)
	return err //nolint:wrapcheck // a refusal is the answer as it is
}

// JournalRecords reads one user's journal, or the whole journal for uuid.Nil.
func (r reviewReads) JournalRecords(ctx context.Context, userID uuid.UUID) ([]jr.Record, error) {
	if userID == uuid.Nil {
		return r.stores.Journal.All(ctx) //nolint:wrapcheck // the store names what failed
	}
	return r.stores.Journal.Records(ctx, userID) //nolint:wrapcheck // the store names what failed
}

// VerifyJournal checks the links of the chain; a broken one is answered by the seq of its first
// bad record. The web has no head recorded outside the table, so a journal cut short at its end
// is caught by the CLI check (HT-333, HT-396), not here.
func (r reviewReads) VerifyJournal(ctx context.Context) (int, int64, error) {
	n, _, err := r.stores.Journal.Verify(ctx, nil)
	if seq, ok := journalBreak(err); ok {
		return n, seq, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("verify decision journal: %w", err)
	}
	return n, 0, nil
}

// journalBreak is the seq of the first record a broken chain names.
func journalBreak(err error) (int64, bool) {
	var broken *postgres.ChainBrokenError
	if errors.As(err, &broken) {
		return broken.Seq, true
	}
	return 0, false
}

func (r reviewReads) Skills(ctx context.Context, userID uuid.UUID) (deep.SkillReportView, error) {
	return r.deep.Skills(ctx, userID) //nolint:wrapcheck // a domain error picks the answer
}

// UserNames names every user.
func (r reviewReads) UserNames(ctx context.Context) (map[uuid.UUID]string, error) {
	all, err := r.people.users.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	names := make(map[uuid.UUID]string, len(all))
	for _, u := range all {
		names[u.ID] = string(u.Name)
	}
	return names, nil
}
