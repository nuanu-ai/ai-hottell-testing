package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// LoginWithPassword signs the user with email and password in: it records the sign-in
// and opens a session, returning its token for the client and its expiry. A malformed or
// unknown email, an invited user without a password and a wrong password all fail with
// domain.ErrInvalidCredentials; the password is checked against a dummy hash when there is
// no stored one, so an unknown email answers as slowly as a wrong password.
func (s *Service) LoginWithPassword(ctx context.Context, email, password, userAgent string) (
	string, time.Time, error,
) {
	addr, err := domain.ParseEmail(email)
	if err != nil {
		return "", time.Time{}, domain.ErrInvalidCredentials
	}
	user, hash, err := s.users.GetByEmail(ctx, addr)
	if err != nil && !errors.Is(err, domain.ErrUserNotFound) {
		return "", time.Time{}, fmt.Errorf("get user: %w", err)
	}
	if hash == "" {
		// Unknown email or invited user: spend the time of a check all the same.
		if hash, err = s.dummy(); err != nil {
			return "", time.Time{}, err
		}
		if _, err := s.hasher.Verify(password, hash); err != nil {
			return "", time.Time{}, fmt.Errorf("verify dummy password: %w", err)
		}
		return "", time.Time{}, domain.ErrInvalidCredentials
	}
	ok, err := s.hasher.Verify(password, hash)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return "", time.Time{}, domain.ErrInvalidCredentials
	}

	var (
		token     string
		expiresAt time.Time
	)
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.users.TouchLogin(ctx, user.ID, s.clock.Now()); err != nil {
			return fmt.Errorf("touch login: %w", err)
		}
		token, expiresAt, err = s.sessions.Open(ctx, user.ID, userAgent)
		if err != nil {
			return fmt.Errorf("open session: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}
