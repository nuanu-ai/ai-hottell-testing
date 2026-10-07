package postgres

import (
	"context"
	"crypto/rand"
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
	// webAuthnUserIDLen is the length of the WebAuthn user handle, the maximum the spec allows.
	webAuthnUserIDLen = 64
	// uniqueViolation is the SQLSTATE of a unique constraint violation.
	uniqueViolation = "23505"
	// usersEmailKey is the unique constraint on users.email.
	usersEmailKey = "users_email_key"
	// userColumns are the columns scanUser reads, in its order.
	userColumns = "id, email, name, password_hash, created_at, last_login_at"
)

// Users keeps users in the users table.
type Users struct {
	pool *pgxpool.Pool
}

// NewUsers returns a Users repository on pool; it runs in the transaction of the context
// when TxManager.WithinTx opened one.
func NewUsers(pool *pgxpool.Pool) *Users {
	return &Users{pool: pool}
}

// create inserts a user with passwordHash, nil for an invited user.
func (r *Users) create(ctx context.Context, email domain.Email, name domain.UserName, passwordHash *string) (domain.User, error) {
	webAuthnUserID := make([]byte, webAuthnUserIDLen)
	// crypto/rand.Read never returns an error: it crashes the program on failure.
	_, _ = rand.Read(webAuthnUserID)

	row := conn(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash, webauthn_user_id)
		VALUES ($1, $2, $3, $4)
		RETURNING `+userColumns,
		email.String(), name.String(), passwordHash, webAuthnUserID)
	user, _, err := scanUser(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == usersEmailKey {
			return domain.User{}, domain.ErrEmailTaken
		}
		return domain.User{}, fmt.Errorf("insert user: %w", err)
	}
	return user, nil
}

// getBy returns the user the where clause selects with its password hash, empty for an
// invited user; domain.ErrUserNotFound when none matches.
func (r *Users) getBy(ctx context.Context, where string, arg any) (domain.User, string, error) {
	row := conn(ctx, r.pool).QueryRow(ctx, "SELECT "+userColumns+" FROM users WHERE "+where, arg)
	user, hash, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, "", domain.ErrUserNotFound
	}
	if err != nil {
		return domain.User{}, "", fmt.Errorf("select user: %w", err)
	}
	return user, hash, nil
}

// update runs a single-row UPDATE by user id in $1; domain.ErrUserNotFound when no row matches.
func (r *Users) update(ctx context.Context, sql string, args ...any) error {
	tag, err := conn(ctx, r.pool).Exec(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrUserNotFound
	}
	return nil
}

// scanUser reads the userColumns of row into a user and its password hash, empty when
// the user is invited.
func scanUser(row pgx.Row) (domain.User, string, error) {
	var (
		id          uuid.UUID
		email, name string
		hash        *string
		createdAt   time.Time
		lastLoginAt *time.Time
	)
	if err := row.Scan(&id, &email, &name, &hash, &createdAt, &lastLoginAt); err != nil {
		return domain.User{}, "", err
	}
	user := domain.User{
		ID:          id,
		Email:       domain.Email(email),
		Name:        domain.UserName(name),
		HasPassword: hash != nil,
		CreatedAt:   createdAt,
		LastLoginAt: lastLoginAt,
	}
	if hash == nil {
		return user, "", nil
	}
	return user, *hash, nil
}
