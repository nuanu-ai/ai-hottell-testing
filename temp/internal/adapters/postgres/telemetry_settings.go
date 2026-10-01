package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// telemetrySettingsUserIDFkey is the foreign key of telemetry_settings.user_id to users.
const telemetrySettingsUserIDFkey = "telemetry_settings_user_id_fkey"

// TelemetrySettings keeps the deny settings of users in the telemetry_settings table.
type TelemetrySettings struct {
	pool *pgxpool.Pool
}

// NewTelemetrySettings returns a TelemetrySettings repository on pool; it runs in the
// transaction of the context when TxManager.WithinTx opened one.
func NewTelemetrySettings(pool *pgxpool.Pool) *TelemetrySettings {
	return &TelemetrySettings{pool: pool}
}

// Get returns the settings document of the user with userID and its version; an empty
// document and version 0 when the user has never saved settings.
func (r *TelemetrySettings) Get(ctx context.Context, userID uuid.UUID) (json.RawMessage, int64, error) {
	var (
		document json.RawMessage
		version  int64
	)
	err := conn(ctx, r.pool).QueryRow(ctx,
		"SELECT document, version FROM telemetry_settings WHERE user_id = $1", userID).Scan(&document, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		// Nothing is denied: every field of the document takes its default.
		return json.RawMessage(`{}`), 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("select telemetry settings: %w", err)
	}
	return document, version, nil
}

// Save stores document as the settings of the user with userID with version
// expectedVersion+1; domain.ErrSettingsVersionConflict when the stored version is not
// expectedVersion, domain.ErrUserNotFound when there is no such user.
//
// A missing row counts as version 0: the row is inserted only for expectedVersion 0 and
// replaced only while its version is still expectedVersion, so of two concurrent saves
// from the same version one wins and the other gets the conflict.
func (r *TelemetrySettings) Save(
	ctx context.Context, userID uuid.UUID, document json.RawMessage, expectedVersion int64,
) error {
	var version int64
	err := conn(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO telemetry_settings (user_id, document, version, updated_at)
		SELECT $1, $2::jsonb, $3::bigint + 1, now()
		WHERE $3::bigint = 0 OR EXISTS (SELECT 1 FROM telemetry_settings WHERE user_id = $1)
		ON CONFLICT (user_id) DO UPDATE
		SET document = EXCLUDED.document, version = EXCLUDED.version, updated_at = EXCLUDED.updated_at
		WHERE telemetry_settings.version = $3::bigint
		RETURNING version`,
		userID, document, expectedVersion).Scan(&version)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.ErrSettingsVersionConflict
	case isForeignKeyViolation(err, telemetrySettingsUserIDFkey):
		return domain.ErrUserNotFound
	case err != nil:
		return fmt.Errorf("upsert telemetry settings: %w", err)
	}
	return nil
}
