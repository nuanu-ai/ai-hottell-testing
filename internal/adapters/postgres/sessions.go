package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

const (
	// foreignKeyViolation is the SQLSTATE of a foreign key violation.
	foreignKeyViolation = "23503"
	// sessionsUserIDFkey is the foreign key of sessions.user_id to users.
	sessionsUserIDFkey = "sessions_user_id_fkey"
	// sessionColumns are the columns scanSession reads, in its order.
	sessionColumns = "id, user_id, expires_at, last_seen_at"
)

// Sessions keeps signed-in browsers in the sessions table.
type Sessions struct {
	pool *pgxpool.Pool
}

// NewSessions returns a Sessions repository on pool; it runs in the transaction of the
// context when TxManager.WithinTx opened one.
func NewSessions(pool *pgxpool.Pool) *Sessions {
	return &Sessions{pool: pool}
}

// Create opens a session of the user with userID, found later by tokenHash;
// domain.ErrUserNotFound when there is no such user.
func (r *Sessions) Create(
	ctx context.Context, userID uuid.UUID, tokenHash []byte, expiresAt time.Time, userAgent string,
) (domain.Session, error) {
	row := conn(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO sessions (id, user_id, token_hash, expires_at, user_agent)
		VALUES (gen_random_uuid(), $1, $2, $3, $4)
		RETURNING `+sessionColumns,
		userID, tokenHash, expiresAt, userAgent)
	session, err := scanSession(row)
	if err != nil {
		if isForeignKeyViolation(err, sessionsUserIDFkey) {
			return domain.Session{}, domain.ErrUserNotFound
		}
		return domain.Session{}, fmt.Errorf("insert session: %w", err)
	}
	return session, nil
}

// GetByTokenHash returns the session with tokenHash while it has not expired;
// domain.ErrSessionNotFound when there is no such live session.
func (r *Sessions) GetByTokenHash(ctx context.Context, tokenHash []byte) (domain.Session, error) {
	row := conn(ctx, r.pool).QueryRow(ctx,
		"SELECT "+sessionColumns+" FROM sessions WHERE token_hash = $1 AND expires_at > now()", tokenHash)
	session, err := scanSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Session{}, domain.ErrSessionNotFound
	}
	if err != nil {
		return domain.Session{}, fmt.Errorf("select session: %w", err)
	}
	return session, nil
}

// Extend moves the expiry of the session with id to expiresAt and records lastSeenAt;
// domain.ErrSessionNotFound when there is no such session.
func (r *Sessions) Extend(ctx context.Context, id uuid.UUID, expiresAt, lastSeenAt time.Time) error {
	tag, err := conn(ctx, r.pool).Exec(ctx,
		"UPDATE sessions SET expires_at = $2, last_seen_at = $3 WHERE id = $1", id, expiresAt, lastSeenAt)
	if err != nil {
		return fmt.Errorf("update session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrSessionNotFound
	}
	return nil
}

// Delete closes the session with id; a session that is already gone is not an error.
func (r *Sessions) Delete(ctx context.Context, id uuid.UUID) error {
	return r.delete(ctx, "DELETE FROM sessions WHERE id = $1", id)
}

// DeleteAllForUser closes every session of the user with userID.
func (r *Sessions) DeleteAllForUser(ctx context.Context, userID uuid.UUID) error {
	return r.delete(ctx, "DELETE FROM sessions WHERE user_id = $1", userID)
}

// DeleteAllForUserExcept closes every session of the user with userID but keepID.
func (r *Sessions) DeleteAllForUserExcept(ctx context.Context, userID, keepID uuid.UUID) error {
	return r.delete(ctx, "DELETE FROM sessions WHERE user_id = $1 AND id <> $2", userID, keepID)
}

// DeleteExpired removes every expired session and returns how many it removed.
func (r *Sessions) DeleteExpired(ctx context.Context) (int, error) {
	tag, err := conn(ctx, r.pool).Exec(ctx, "DELETE FROM sessions WHERE expires_at <= now()")
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (r *Sessions) delete(ctx context.Context, sql string, args ...any) error {
	if _, err := conn(ctx, r.pool).Exec(ctx, sql, args...); err != nil {
		return fmt.Errorf("delete sessions: %w", err)
	}
	return nil
}

// scanSession reads the sessionColumns of row into a session.
func scanSession(row pgx.Row) (domain.Session, error) {
	var s domain.Session
	if err := row.Scan(&s.ID, &s.UserID, &s.ExpiresAt, &s.LastSeenAt); err != nil {
		return domain.Session{}, err
	}
	return s, nil
}

// isForeignKeyViolation reports whether err is a violation of the foreign key constraint.
func isForeignKeyViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation && pgErr.ConstraintName == constraint
}
