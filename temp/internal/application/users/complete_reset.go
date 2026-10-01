package users

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// CompleteReset sets password for the user the reset link token opens, uses the link up,
// closes every session of the user and then opens a new one for userAgent; it returns
// the session token and its expiry. It fails with the link errors of LookupLink, and with
// domain.ErrPasswordTooShort or domain.ErrPasswordTooLong, leaving the link usable. The
// session is opened after the password is stored: when opening it fails, the link is
// spent and the user signs in with the new password.
func (s *Service) CompleteReset(ctx context.Context, token, password, userAgent string) (string, time.Time, error) {
	userID, err := s.spendLink(ctx, token, password, domain.LinkKindReset,
		func(ctx context.Context, userID uuid.UUID, _ time.Time) error {
			if err := s.sessions.DeleteAllForUser(ctx, userID); err != nil {
				return fmt.Errorf("close sessions: %w", err)
			}
			return nil
		})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("complete reset: %w", err)
	}
	sessionToken, expiresAt, err := s.opener.Open(ctx, userID, userAgent)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("complete reset: %w", err)
	}
	return sessionToken, expiresAt, nil
}
