package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// TouchLogin records at as the last sign-in of the user with id; domain.ErrUserNotFound
// when there is none.
func (r *Users) TouchLogin(ctx context.Context, id uuid.UUID, at time.Time) error {
	return r.update(ctx, "UPDATE users SET last_login_at = $2 WHERE id = $1", id, at)
}
