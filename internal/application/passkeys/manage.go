package passkeys

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// List returns the passkeys of the user with userID, oldest first.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]domain.Passkey, error) {
	passkeys, err := s.passkeys.ListByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list passkeys: %w", err)
	}
	return passkeys, nil
}

// Delete removes the passkey with passkeyID of the user with userID; the password stays,
// so the user can still sign in. It fails with domain.ErrPasskeyNotFound when there is
// no such passkey or it belongs to another user.
func (s *Service) Delete(ctx context.Context, userID, passkeyID uuid.UUID) error {
	if err := s.passkeys.Delete(ctx, passkeyID, userID); err != nil {
		return fmt.Errorf("delete passkey: %w", err)
	}
	return nil
}
