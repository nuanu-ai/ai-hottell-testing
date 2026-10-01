package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// DeleteInvited removes the user with id while the invitation is not accepted;
// domain.ErrUserNotInvited when the user has a password, domain.ErrUserNotFound when
// there is none.
func (r *Users) DeleteInvited(ctx context.Context, id uuid.UUID) error {
	db := conn(ctx, r.pool)
	// The condition is in the DELETE itself, so a concurrent acceptance that sets the
	// password first leaves the user in place.
	tag, err := db.Exec(ctx, "DELETE FROM users WHERE id = $1 AND password_hash IS NULL", id)
	if err != nil {
		return fmt.Errorf("delete invited user: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}

	var exists bool
	if err := db.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)", id).Scan(&exists); err != nil {
		return fmt.Errorf("check user: %w", err)
	}
	if exists {
		return domain.ErrUserNotInvited
	}
	return domain.ErrUserNotFound
}
