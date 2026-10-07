package postgres

import (
	"context"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// GetByWebAuthnID returns the user whose WebAuthn user handle is webAuthnUserID, as a
// passkey sign-in finds it; domain.ErrUserNotFound when there is none.
func (r *Users) GetByWebAuthnID(ctx context.Context, webAuthnUserID []byte) (domain.User, error) {
	user, _, err := r.getBy(ctx, "webauthn_user_id = $1", webAuthnUserID)
	return user, err
}
