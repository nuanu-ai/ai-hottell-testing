package postgres

import (
	"context"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// CreateActive adds a user with a password, as the CLI does for the first user;
// domain.ErrEmailTaken when a user with email exists.
func (r *Users) CreateActive(
	ctx context.Context, email domain.Email, name domain.UserName, passwordHash string,
) (domain.User, error) {
	return r.create(ctx, email, name, &passwordHash)
}
