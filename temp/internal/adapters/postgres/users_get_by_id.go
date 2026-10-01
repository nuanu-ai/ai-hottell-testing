package postgres

import (
	"context"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// GetByID returns the user with id and its password hash, empty for an invited user;
// domain.ErrUserNotFound when there is none.
func (r *Users) GetByID(ctx context.Context, id uuid.UUID) (domain.User, string, error) {
	return r.getBy(ctx, "id = $1", id)
}
