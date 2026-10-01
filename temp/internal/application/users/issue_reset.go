package users

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// IssueReset issues a password reset link to the active user with userID on behalf of
// actorID, so the previous unused one stops working, and returns its URL and expiry;
// domain.ErrCannotResetSelf when actorID is userID, as the own password is changed in the
// profile, domain.ErrUserNotActive when the user has not accepted the invitation,
// domain.ErrUserNotFound when there is no such user.
func (s *Service) IssueReset(ctx context.Context, actorID, userID uuid.UUID) (string, time.Time, error) {
	if actorID == userID {
		return "", time.Time{}, domain.ErrCannotResetSelf
	}
	user, _, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("issue reset: get user: %w", err)
	}
	// Active users are never deleted or turned back into invited ones, so the status
	// cannot change before the link is stored.
	if user.Status() != domain.UserStatusActive {
		return "", time.Time{}, domain.ErrUserNotActive
	}
	resetURL, expiresAt, err := s.issueLink(ctx, domain.LinkKindReset, actorID, userID)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("issue reset: %w", err)
	}
	return resetURL, expiresAt, nil
}
