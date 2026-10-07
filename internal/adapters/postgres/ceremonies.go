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

// ceremoniesUserIDFkey is the foreign key of webauthn_ceremonies.user_id to users.
const ceremoniesUserIDFkey = "webauthn_ceremonies_user_id_fkey"

// Ceremonies keeps the state of WebAuthn ceremonies in progress in the
// webauthn_ceremonies table.
type Ceremonies struct {
	pool *pgxpool.Pool
}

// NewCeremonies returns a Ceremonies repository on pool; it runs in the transaction of
// the context when TxManager.WithinTx opened one.
func NewCeremonies(pool *pgxpool.Pool) *Ceremonies {
	return &Ceremonies{pool: pool}
}

// Save stores a ceremony of kind with its JSON sessionData until expiresAt and returns
// its id. userID and passkeyName are nil for a login; domain.ErrUserNotFound when the
// user with userID does not exist.
func (r *Ceremonies) Save(
	ctx context.Context, kind domain.CeremonyKind, userID *uuid.UUID, passkeyName *string, sessionData []byte,
	expiresAt time.Time,
) (uuid.UUID, error) {
	var id uuid.UUID
	err := conn(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO webauthn_ceremonies (id, kind, user_id, passkey_name, session_data, expires_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5)
		RETURNING id`,
		string(kind), userID, passkeyName, sessionData, expiresAt).Scan(&id)
	if err != nil {
		if isForeignKeyViolation(err, ceremoniesUserIDFkey) {
			return uuid.Nil, domain.ErrUserNotFound
		}
		return uuid.Nil, fmt.Errorf("insert ceremony: %w", err)
	}
	return id, nil
}

// Take returns the ceremony with id and removes it in the same statement, so it can be
// finished once; domain.ErrCeremonyNotFound when there is none, it has expired or it is
// not of kind.
func (r *Ceremonies) Take(
	ctx context.Context, id uuid.UUID, kind domain.CeremonyKind,
) (userID *uuid.UUID, passkeyName *string, sessionData []byte, err error) {
	err = conn(ctx, r.pool).QueryRow(ctx, `
		DELETE FROM webauthn_ceremonies
		WHERE id = $1 AND kind = $2 AND expires_at > now()
		RETURNING user_id, passkey_name, session_data`,
		id, string(kind)).Scan(&userID, &passkeyName, &sessionData)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil, domain.ErrCeremonyNotFound
	}
	if err != nil {
		return nil, nil, nil, fmt.Errorf("take ceremony: %w", err)
	}
	return userID, passkeyName, sessionData, nil
}

// DeleteExpired removes every expired ceremony and returns how many it removed.
func (r *Ceremonies) DeleteExpired(ctx context.Context) (int, error) {
	tag, err := conn(ctx, r.pool).Exec(ctx, "DELETE FROM webauthn_ceremonies WHERE expires_at <= now()")
	if err != nil {
		return 0, fmt.Errorf("delete expired ceremonies: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
