package postgres

import (
	"context"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// GetByEmail returns the user with email and its password hash, empty for an invited user;
// domain.ErrUserNotFound when there is none.
func (r *Users) GetByEmail(ctx context.Context, email domain.Email) (domain.User, string, error) {
	return r.getBy(ctx, "email = $1", email.String())
}
