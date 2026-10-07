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

// deliveryStatusUserIDFkey is the foreign key of delivery_status.user_id to users.
const deliveryStatusUserIDFkey = "delivery_status_user_id_fkey"

// DeliveryStatuses keeps the last delivery status of each user in the delivery_status table.
type DeliveryStatuses struct {
	pool *pgxpool.Pool
}

// NewDeliveryStatuses returns a DeliveryStatuses repository on pool; it runs in the
// transaction of the context when TxManager.WithinTx opened one.
func NewDeliveryStatuses(pool *pgxpool.Pool) *DeliveryStatuses {
	return &DeliveryStatuses{pool: pool}
}

// Save keeps status as the delivery status of the user with userID in place of the previous
// one; domain.ErrUserNotFound when there is no such user.
func (r *DeliveryStatuses) Save(ctx context.Context, userID uuid.UUID, status domain.DeliveryStatus) error {
	_, err := conn(ctx, r.pool).Exec(ctx, `
		INSERT INTO delivery_status (user_id, queue_records, queue_bytes, last_success_at, last_error, last_error_at,
			unauthorized, settings_version, binary_version, reported_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (user_id) DO UPDATE
		SET queue_records = EXCLUDED.queue_records, queue_bytes = EXCLUDED.queue_bytes,
			last_success_at = EXCLUDED.last_success_at, last_error = EXCLUDED.last_error,
			last_error_at = EXCLUDED.last_error_at, unauthorized = EXCLUDED.unauthorized,
			settings_version = EXCLUDED.settings_version, binary_version = EXCLUDED.binary_version,
			reported_at = EXCLUDED.reported_at`,
		userID, status.QueueRecords, status.QueueBytes, status.LastSuccessAt, status.LastError, status.LastErrorAt,
		status.Unauthorized, status.SettingsVersion, status.BinaryVersion, status.ReportedAt)
	switch {
	case isForeignKeyViolation(err, deliveryStatusUserIDFkey):
		return domain.ErrUserNotFound
	case err != nil:
		return fmt.Errorf("upsert delivery status: %w", err)
	}
	return nil
}

// Get returns the delivery status of the user with userID, and false when the user has
// never reported one.
func (r *DeliveryStatuses) Get(ctx context.Context, userID uuid.UUID) (domain.DeliveryStatus, bool, error) {
	var status domain.DeliveryStatus
	err := conn(ctx, r.pool).QueryRow(ctx, `
		SELECT queue_records, queue_bytes, last_success_at, last_error, last_error_at, unauthorized,
			settings_version, binary_version, reported_at
		FROM delivery_status WHERE user_id = $1`, userID).Scan(
		&status.QueueRecords, &status.QueueBytes, &status.LastSuccessAt, &status.LastError, &status.LastErrorAt,
		&status.Unauthorized, &status.SettingsVersion, &status.BinaryVersion, &status.ReportedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DeliveryStatus{}, false, nil
	}
	if err != nil {
		return domain.DeliveryStatus{}, false, fmt.Errorf("select delivery status: %w", err)
	}
	return status, true, nil
}
