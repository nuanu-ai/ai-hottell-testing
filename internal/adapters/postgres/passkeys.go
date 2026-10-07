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

const (
	// passkeysUserIDFkey is the foreign key of passkeys.user_id to users.
	passkeysUserIDFkey = "passkeys_user_id_fkey"
	// passkeyColumns are the columns scanPasskey reads, in its order.
	passkeyColumns = "id, user_id, credential_id, public_key, attestation_type, aaguid, sign_count, transports, " +
		"backup_eligible, backup_state, name, created_at, last_used_at"
)

// Passkeys keeps the WebAuthn credentials of users in the passkeys table.
type Passkeys struct {
	pool *pgxpool.Pool
}

// NewPasskeys returns a Passkeys repository on pool; it runs in the transaction of the
// context when TxManager.WithinTx opened one.
func NewPasskeys(pool *pgxpool.Pool) *Passkeys {
	return &Passkeys{pool: pool}
}

// Create stores passkey under a generated id and creation time, ignoring the ones it
// carries, and returns it as stored; domain.ErrUserNotFound when its user does not exist.
func (r *Passkeys) Create(ctx context.Context, passkey domain.Passkey) (domain.Passkey, error) {
	transports := passkey.Transports
	if transports == nil {
		transports = []string{}
	}
	row := conn(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO passkeys (id, user_id, credential_id, public_key, attestation_type, aaguid, sign_count,
			transports, backup_eligible, backup_state, name)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING `+passkeyColumns,
		passkey.UserID, passkey.CredentialID, passkey.PublicKey, passkey.AttestationType, passkey.AAGUID,
		int64(passkey.SignCount), transports, passkey.BackupEligible, passkey.BackupState, passkey.Name.String())
	created, err := scanPasskey(row)
	if err != nil {
		if isForeignKeyViolation(err, passkeysUserIDFkey) {
			return domain.Passkey{}, domain.ErrUserNotFound
		}
		return domain.Passkey{}, fmt.Errorf("insert passkey: %w", err)
	}
	return created, nil
}

// ListByUser returns the passkeys of the user with userID, oldest first.
func (r *Passkeys) ListByUser(ctx context.Context, userID uuid.UUID) ([]domain.Passkey, error) {
	rows, err := conn(ctx, r.pool).Query(ctx,
		"SELECT "+passkeyColumns+" FROM passkeys WHERE user_id = $1 ORDER BY created_at, id", userID)
	if err != nil {
		return nil, fmt.Errorf("select passkeys: %w", err)
	}
	passkeys, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Passkey, error) {
		return scanPasskey(row)
	})
	if err != nil {
		return nil, fmt.Errorf("scan passkeys: %w", err)
	}
	return passkeys, nil
}

// GetByCredentialID returns the passkey with credentialID; domain.ErrPasskeyNotFound
// when there is none.
func (r *Passkeys) GetByCredentialID(ctx context.Context, credentialID []byte) (domain.Passkey, error) {
	row := conn(ctx, r.pool).QueryRow(ctx,
		"SELECT "+passkeyColumns+" FROM passkeys WHERE credential_id = $1", credentialID)
	passkey, err := scanPasskey(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Passkey{}, domain.ErrPasskeyNotFound
	}
	if err != nil {
		return domain.Passkey{}, fmt.Errorf("select passkey: %w", err)
	}
	return passkey, nil
}

// RecordUse records a sign-in at at with the passkey with id: its new signature counter
// and backup state; domain.ErrPasskeyNotFound when there is no such passkey.
func (r *Passkeys) RecordUse(ctx context.Context, id uuid.UUID, signCount uint32, backupState bool, at time.Time) error {
	tag, err := conn(ctx, r.pool).Exec(ctx,
		"UPDATE passkeys SET sign_count = $2, backup_state = $3, last_used_at = $4 WHERE id = $1",
		id, int64(signCount), backupState, at)
	if err != nil {
		return fmt.Errorf("update passkey: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrPasskeyNotFound
	}
	return nil
}

// Delete removes the passkey with id of the user with userID; domain.ErrPasskeyNotFound
// when there is no such passkey or it belongs to another user.
func (r *Passkeys) Delete(ctx context.Context, id, userID uuid.UUID) error {
	tag, err := conn(ctx, r.pool).Exec(ctx, "DELETE FROM passkeys WHERE id = $1 AND user_id = $2", id, userID)
	if err != nil {
		return fmt.Errorf("delete passkey: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrPasskeyNotFound
	}
	return nil
}

// scanPasskey reads the passkeyColumns of row into a passkey.
func scanPasskey(row pgx.Row) (domain.Passkey, error) {
	var (
		p         domain.Passkey
		signCount int64
		name      string
	)
	err := row.Scan(&p.ID, &p.UserID, &p.CredentialID, &p.PublicKey, &p.AttestationType, &p.AAGUID, &signCount,
		&p.Transports, &p.BackupEligible, &p.BackupState, &name, &p.CreatedAt, &p.LastUsedAt)
	if err != nil {
		return domain.Passkey{}, err
	}
	// The column is bigint only to hold every uint32; nothing else writes it.
	p.SignCount = uint32(signCount) //nolint:gosec // written from a uint32 by Create and RecordUse
	p.Name = domain.PasskeyName(name)
	return p, nil
}
