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

// accessKeyColumns are the columns scanAccessKey reads, in its order.
const accessKeyColumns = "id, user_id, kind, plaintext, created_at, last_used_at, revoked_at"

// AccessKeys keeps MCP keys and collector tokens in the access_keys table.
type AccessKeys struct {
	pool *pgxpool.Pool
	tx   *TxManager
}

// NewAccessKeys returns an AccessKeys repository on pool; it runs in the transaction of
// the context when TxManager.WithinTx opened one.
func NewAccessKeys(pool *pgxpool.Pool) *AccessKeys {
	return &AccessKeys{pool: pool, tx: NewTxManager(pool)}
}

// Active returns the active key of kind of the user with userID, with its open value for
// an ingest key; domain.ErrAccessKeyNotFound when there is none.
func (r *AccessKeys) Active(ctx context.Context, userID uuid.UUID, kind domain.AccessKeyKind) (domain.AccessKey, error) {
	row := conn(ctx, r.pool).QueryRow(ctx,
		"SELECT "+accessKeyColumns+" FROM access_keys WHERE user_id = $1 AND kind = $2 AND revoked_at IS NULL",
		userID, string(kind))
	key, err := scanAccessKey(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AccessKey{}, domain.ErrAccessKeyNotFound
	}
	if err != nil {
		return domain.AccessKey{}, fmt.Errorf("select active access key: %w", err)
	}
	return key, nil
}

// Latest returns the key of kind of the user with userID that tells the state of the
// user's sending: the active one, else the last one created, revoked; domain.ErrAccessKeyNotFound
// when the user never had one.
func (r *AccessKeys) Latest(ctx context.Context, userID uuid.UUID, kind domain.AccessKeyKind) (domain.AccessKey, error) {
	row := conn(ctx, r.pool).QueryRow(ctx,
		"SELECT "+accessKeyColumns+" FROM access_keys WHERE user_id = $1 AND kind = $2 "+
			"ORDER BY revoked_at IS NULL DESC, created_at DESC, id DESC LIMIT 1",
		userID, string(kind))
	key, err := scanAccessKey(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AccessKey{}, domain.ErrAccessKeyNotFound
	}
	if err != nil {
		return domain.AccessKey{}, fmt.Errorf("select latest access key: %w", err)
	}
	return key, nil
}

// Replace revokes the active key of kind of the user with userID, if there is one, and in
// the same transaction creates a new one found later by hash; plaintext is the open value
// of an ingest key and empty for an MCP key. domain.ErrUserNotFound when there is no such
// user.
func (r *AccessKeys) Replace(
	ctx context.Context, userID uuid.UUID, kind domain.AccessKeyKind, hash []byte, plaintext string,
) (domain.AccessKey, error) {
	var key domain.AccessKey
	err := r.tx.WithinTx(ctx, func(ctx context.Context) error {
		// Locking the user serializes replacements for one user, so a concurrent one revokes
		// the key this one inserts instead of colliding with it on the unique index.
		var locked int
		err := conn(ctx, r.pool).QueryRow(ctx, "SELECT 1 FROM users WHERE id = $1 FOR NO KEY UPDATE", userID).Scan(&locked)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("lock user: %w", err)
		}
		if err := r.revoke(ctx, userID, kind); err != nil {
			return err
		}
		var open *string
		if plaintext != "" {
			open = &plaintext
		}
		row := conn(ctx, r.pool).QueryRow(ctx, `
			INSERT INTO access_keys (id, user_id, kind, key_hash, plaintext)
			VALUES (gen_random_uuid(), $1, $2, $3, $4)
			RETURNING `+accessKeyColumns,
			userID, string(kind), hash, open)
		if key, err = scanAccessKey(row); err != nil {
			return fmt.Errorf("insert access key: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.AccessKey{}, fmt.Errorf("replace access key: %w", err)
	}
	return key, nil
}

// Revoke revokes the active key of kind of the user with userID;
// domain.ErrAccessKeyNotFound when there is none.
func (r *AccessKeys) Revoke(ctx context.Context, userID uuid.UUID, kind domain.AccessKeyKind) error {
	tag, err := conn(ctx, r.pool).Exec(ctx,
		"UPDATE access_keys SET revoked_at = now() WHERE user_id = $1 AND kind = $2 AND revoked_at IS NULL",
		userID, string(kind))
	if err != nil {
		return fmt.Errorf("revoke access key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrAccessKeyNotFound
	}
	return nil
}

// FindByHash returns the active key with hash; domain.ErrAccessKeyRevoked when the key is
// revoked and domain.ErrAccessKeyNotFound when there is no such key.
func (r *AccessKeys) FindByHash(ctx context.Context, hash []byte) (domain.AccessKey, error) {
	row := conn(ctx, r.pool).QueryRow(ctx, "SELECT "+accessKeyColumns+" FROM access_keys WHERE key_hash = $1", hash)
	key, err := scanAccessKey(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AccessKey{}, domain.ErrAccessKeyNotFound
	}
	if err != nil {
		return domain.AccessKey{}, fmt.Errorf("select access key: %w", err)
	}
	if key.RevokedAt != nil {
		return domain.AccessKey{}, domain.ErrAccessKeyRevoked
	}
	return key, nil
}

// Touch records at as the last use of the active key with id; domain.ErrAccessKeyRevoked
// when the key is revoked and domain.ErrAccessKeyNotFound when there is no such key.
func (r *AccessKeys) Touch(ctx context.Context, id uuid.UUID, at time.Time) error {
	db := conn(ctx, r.pool)
	tag, err := db.Exec(ctx, "UPDATE access_keys SET last_used_at = $2 WHERE id = $1 AND revoked_at IS NULL", id, at)
	if err != nil {
		return fmt.Errorf("touch access key: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}

	var exists bool
	if err := db.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM access_keys WHERE id = $1)", id).Scan(&exists); err != nil {
		return fmt.Errorf("check access key: %w", err)
	}
	if exists {
		return domain.ErrAccessKeyRevoked
	}
	return domain.ErrAccessKeyNotFound
}

// revoke revokes the active key of kind of the user with userID, if there is one.
func (r *AccessKeys) revoke(ctx context.Context, userID uuid.UUID, kind domain.AccessKeyKind) error {
	if err := r.Revoke(ctx, userID, kind); err != nil && !errors.Is(err, domain.ErrAccessKeyNotFound) {
		return err
	}
	return nil
}

// scanAccessKey reads the accessKeyColumns of row into an access key.
func scanAccessKey(row pgx.Row) (domain.AccessKey, error) {
	var (
		k         domain.AccessKey
		kind      string
		plaintext *string
	)
	if err := row.Scan(&k.ID, &k.UserID, &kind, &plaintext, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt); err != nil {
		return domain.AccessKey{}, err
	}
	k.Kind = domain.AccessKeyKind(kind)
	if plaintext != nil {
		k.Plaintext = *plaintext
	}
	return k, nil
}
