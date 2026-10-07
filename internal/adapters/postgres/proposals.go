package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// proposalsUserIDFkey is the foreign key of proposals.user_id to users.
const proposalsUserIDFkey = "proposals_user_id_fkey"

// Proposals keeps the proposal registry of each user in the proposals table: a projection
// rebuilt whole for a user.
type Proposals struct {
	pool *pgxpool.Pool
	tx   *TxManager
}

// NewProposals returns a Proposals repository on pool; it runs in the transaction of the
// context when TxManager.WithinTx opened one.
func NewProposals(pool *pgxpool.Pool) *Proposals {
	return &Proposals{pool: pool, tx: NewTxManager(pool)}
}

// ReplaceForUser makes proposals the whole registry of the user with userID, atomically:
// the proposals that are gone are deleted, new ones inserted, changed ones replaced. It
// returns how many proposals appeared, changed or disappeared. domain.ErrUserNotFound when
// there is no such user. Called within a transaction, it is part of it.
func (r *Proposals) ReplaceForUser(ctx context.Context, userID uuid.UUID, proposals []domain.Proposal) (int, error) {
	ids := make([]string, len(proposals))
	for i, p := range proposals {
		ids[i] = p.ProposalID
	}
	var changed int64
	err := r.tx.WithinTx(ctx, func(ctx context.Context) error {
		q := conn(ctx, r.pool)
		tag, err := q.Exec(ctx, "DELETE FROM proposals WHERE user_id = $1 AND NOT (proposal_id = ANY($2))", userID, ids)
		if err != nil {
			return fmt.Errorf("delete gone proposals: %w", err)
		}
		changed += tag.RowsAffected()
		for _, p := range proposals {
			// An unchanged document is not rewritten, so it neither counts nor moves updated_at.
			tag, err := q.Exec(ctx, `
				INSERT INTO proposals (user_id, proposal_id, document, updated_at) VALUES ($1, $2, $3::jsonb, now())
				ON CONFLICT (user_id, proposal_id) DO UPDATE
				SET document = EXCLUDED.document, updated_at = EXCLUDED.updated_at
				WHERE proposals.document IS DISTINCT FROM EXCLUDED.document`,
				userID, p.ProposalID, string(p.Document))
			if isForeignKeyViolation(err, proposalsUserIDFkey) {
				return domain.ErrUserNotFound
			}
			if err != nil {
				return fmt.Errorf("upsert proposal %s: %w", p.ProposalID, err)
			}
			changed += tag.RowsAffected()
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return int(changed), nil
}

// List returns the proposals filter selects, by user and then proposal id.
func (r *Proposals) List(ctx context.Context, filter domain.ProposalFilter) ([]domain.UserProposal, error) {
	rows, err := conn(ctx, r.pool).Query(ctx, `
		SELECT user_id, proposal_id, document, updated_at FROM proposals
		WHERE $1::uuid IS NULL OR user_id = $1 ORDER BY user_id, proposal_id`, nullUUID(filter.UserID))
	if err != nil {
		return nil, fmt.Errorf("select proposals: %w", err)
	}
	proposals, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.UserProposal, error) {
		var (
			p        domain.UserProposal
			document string
		)
		err := row.Scan(&p.UserID, &p.ProposalID, &document, &p.UpdatedAt)
		p.Document = []byte(document)
		return p, err
	})
	if err != nil {
		return nil, fmt.Errorf("select proposals: %w", err)
	}
	return proposals, nil
}

// Get returns the proposal with proposalID of the user with userID;
// domain.ErrProposalNotFound when there is none.
func (r *Proposals) Get(ctx context.Context, userID uuid.UUID, proposalID string) (domain.Proposal, error) {
	var (
		p        domain.Proposal
		document string
	)
	err := conn(ctx, r.pool).QueryRow(ctx, `
		SELECT proposal_id, document, updated_at FROM proposals WHERE user_id = $1 AND proposal_id = $2`,
		userID, proposalID).Scan(&p.ProposalID, &document, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Proposal{}, domain.ErrProposalNotFound
	}
	if err != nil {
		return domain.Proposal{}, fmt.Errorf("select proposal: %w", err)
	}
	p.Document = []byte(document)
	return p, nil
}

// nullUUID is id as a query argument, NULL for uuid.Nil.
func nullUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}
