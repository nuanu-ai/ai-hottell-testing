package postgres

import (
	"context"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// CreateInvited adds a user who has no password until the invitation is accepted;
// domain.ErrEmailTaken when a user with email exists.
func (r *Users) CreateInvited(ctx context.Context, email domain.Email, name domain.UserName) (domain.User, error) {
	return r.create(ctx, email, name, nil)
}
