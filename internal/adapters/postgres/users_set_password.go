package postgres

import (
	"context"

	"github.com/google/uuid"
)

// SetPassword replaces the password hash of the user with id; domain.ErrUserNotFound
// when there is none.
func (r *Users) SetPassword(ctx context.Context, id uuid.UUID, passwordHash string) error {
	return r.update(ctx, "UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1", id, passwordHash)
}
