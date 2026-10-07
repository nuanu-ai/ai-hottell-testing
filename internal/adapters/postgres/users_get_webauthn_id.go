package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// GetWebAuthnID returns the WebAuthn user handle of the user with id, as adding a
// passkey needs it; domain.ErrUserNotFound when there is none.
func (r *Users) GetWebAuthnID(ctx context.Context, id uuid.UUID) ([]byte, error) {
	var handle []byte
	err := conn(ctx, r.pool).QueryRow(ctx, "SELECT webauthn_user_id FROM users WHERE id = $1", id).Scan(&handle)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select webauthn user id: %w", err)
	}
	return handle, nil
}
