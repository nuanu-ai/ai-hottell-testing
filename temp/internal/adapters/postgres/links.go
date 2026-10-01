package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// linkColumns are the columns scanLink reads, in its order.
const linkColumns = "id, user_id, kind, expires_at, used_at"

// Links keeps one-time invitation and password reset links in the user_tokens table.
type Links struct {
	pool *pgxpool.Pool
	tx   *TxManager
}

// NewLinks returns a Links repository on pool; it runs in the transaction of the context
// when TxManager.WithinTx opened one.
func NewLinks(pool *pgxpool.Pool) *Links {
	return &Links{pool: pool, tx: NewTxManager(pool)}
}

// Replace issues a link of kind to the user with userID, found later by tokenHash, and in
// the same transaction removes the unused link of that kind the user had, so a reissue
// makes the old link unusable; domain.ErrUserNotFound when there is no such user.
func (r *Links) Replace(
	ctx context.Context, userID uuid.UUID, kind domain.LinkKind, tokenHash []byte, createdBy uuid.UUID, expiresAt time.Time,
) (domain.Link, error) {
	var link domain.Link
	err := r.tx.WithinTx(ctx, func(ctx context.Context) error {
		// Locking the user serializes reissues to one user, so a concurrent one deletes
		// the link this one inserts instead of colliding with it on the unique index.
		var locked int
		err := conn(ctx, r.pool).QueryRow(ctx, "SELECT 1 FROM users WHERE id = $1 FOR NO KEY UPDATE", userID).Scan(&locked)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("lock user: %w", err)
		}
		if err := r.DeleteUnused(ctx, userID, kind); err != nil {
			return err
		}
		row := conn(ctx, r.pool).QueryRow(ctx, `
			INSERT INTO user_tokens (id, user_id, kind, token_hash, created_by, expires_at)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, $5)
			RETURNING `+linkColumns,
			userID, string(kind), tokenHash, createdBy, expiresAt)
		if link, err = scanLink(row); err != nil {
			return fmt.Errorf("insert link: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.Link{}, fmt.Errorf("replace link: %w", err)
	}
	return link, nil
}

// DeleteUnused removes the unused link of kind of the user with userID, if there is one.
func (r *Links) DeleteUnused(ctx context.Context, userID uuid.UUID, kind domain.LinkKind) error {
	_, err := conn(ctx, r.pool).Exec(ctx,
		"DELETE FROM user_tokens WHERE user_id = $1 AND kind = $2 AND used_at IS NULL", userID, string(kind))
	if err != nil {
		return fmt.Errorf("delete unused link: %w", err)
	}
	return nil
}

// GetByTokenHash returns the link with tokenHash, used and expired ones included;
// domain.ErrLinkNotFound when there is none.
func (r *Links) GetByTokenHash(ctx context.Context, tokenHash []byte) (domain.Link, error) {
	return r.getBy(ctx, "token_hash = $1", tokenHash)
}

// GetActiveForUser returns the unused link of kind of the user with userID, expired or
// not; domain.ErrLinkNotFound when there is none.
func (r *Links) GetActiveForUser(ctx context.Context, userID uuid.UUID, kind domain.LinkKind) (domain.Link, error) {
	return r.getBy(ctx, "user_id = $1 AND kind = $2 AND used_at IS NULL", userID, string(kind))
}

// MarkUsed records at as the time the link with id was used; domain.ErrLinkUsed when it
// was used already, so of two concurrent acceptances only one wins, and
// domain.ErrLinkNotFound when there is no such link.
func (r *Links) MarkUsed(ctx context.Context, id uuid.UUID, at time.Time) error {
	db := conn(ctx, r.pool)
	tag, err := db.Exec(ctx, "UPDATE user_tokens SET used_at = $2 WHERE id = $1 AND used_at IS NULL", id, at)
	if err != nil {
		return fmt.Errorf("mark link used: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}

	var exists bool
	if err := db.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM user_tokens WHERE id = $1)", id).Scan(&exists); err != nil {
		return fmt.Errorf("check link: %w", err)
	}
	if exists {
		return domain.ErrLinkUsed
	}
	return domain.ErrLinkNotFound
}

// getBy returns the link the where clause selects; domain.ErrLinkNotFound when none matches.
func (r *Links) getBy(ctx context.Context, where string, args ...any) (domain.Link, error) {
	row := conn(ctx, r.pool).QueryRow(ctx, "SELECT "+linkColumns+" FROM user_tokens WHERE "+where, args...)
	link, err := scanLink(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Link{}, domain.ErrLinkNotFound
	}
	if err != nil {
		return domain.Link{}, fmt.Errorf("select link: %w", err)
	}
	return link, nil
}

// scanLink reads the linkColumns of row into a link.
func scanLink(row pgx.Row) (domain.Link, error) {
	var (
		l    domain.Link
		kind string
	)
	if err := row.Scan(&l.ID, &l.UserID, &kind, &l.ExpiresAt, &l.UsedAt); err != nil {
		return domain.Link{}, err
	}
	l.Kind = domain.LinkKind(kind)
	return l, nil
}
