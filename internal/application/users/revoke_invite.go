package users

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// RevokeInvite deletes the invited user with userID, and with it the invitation link;
// domain.ErrUserNotInvited when the user has accepted the invitation,
// domain.ErrUserNotFound when there is no such user.
func (s *Service) RevokeInvite(ctx context.Context, userID uuid.UUID) error {
	if err := s.users.DeleteInvited(ctx, userID); err != nil {
		return fmt.Errorf("revoke invite: %w", err)
	}
	return nil
}
