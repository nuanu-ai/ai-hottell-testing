package users

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// ReissueInvite issues a new invitation link to the invited user with userID on behalf of
// actorID, so the previous link stops working, and returns its URL and expiry;
// domain.ErrUserNotInvited when the user has accepted the invitation,
// domain.ErrUserNotFound when there is no such user.
func (s *Service) ReissueInvite(ctx context.Context, actorID, userID uuid.UUID) (string, time.Time, error) {
	var (
		inviteURL string
		expiresAt time.Time
	)
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		var err error
		// Replace locks the user first, so the status read after it cannot miss an
		// acceptance that commits in between; an accepted user rolls the new link back.
		if inviteURL, expiresAt, err = s.issueInvite(ctx, actorID, userID); err != nil {
			return err
		}
		user, _, err := s.users.GetByID(ctx, userID)
		if err != nil {
			return fmt.Errorf("get user: %w", err)
		}
		if user.Status() != domain.UserStatusInvited {
			return domain.ErrUserNotInvited
		}
		return nil
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("reissue invite: %w", err)
	}
	return inviteURL, expiresAt, nil
}
