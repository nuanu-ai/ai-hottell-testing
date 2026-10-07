package users

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// AcceptInvite sets password for the invited user the invitation link token opens, uses
// the link up, records the sign-in and opens a session for userAgent; it returns the
// session token and its expiry. It fails with the link errors of LookupLink, and with
// domain.ErrPasswordTooShort or domain.ErrPasswordTooLong, leaving the link usable. The
// session is opened after the password is stored: when opening it fails, the link is
// spent and the user signs in with the new password.
func (s *Service) AcceptInvite(ctx context.Context, token, password, userAgent string) (string, time.Time, error) {
	userID, err := s.spendLink(ctx, token, password, domain.LinkKindInvite,
		func(ctx context.Context, userID uuid.UUID, now time.Time) error {
			if err := s.users.TouchLogin(ctx, userID, now); err != nil {
				return fmt.Errorf("touch login: %w", err)
			}
			return nil
		})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("accept invite: %w", err)
	}
	sessionToken, expiresAt, err := s.opener.Open(ctx, userID, userAgent)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("accept invite: %w", err)
	}
	return sessionToken, expiresAt, nil
}
