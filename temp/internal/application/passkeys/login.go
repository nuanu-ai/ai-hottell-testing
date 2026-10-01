package passkeys

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// BeginLogin starts a sign-in with any discoverable passkey, no email asked, and returns
// the ceremony to finish within five minutes and the options for the browser.
func (s *Service) BeginLogin(ctx context.Context) (uuid.UUID, []byte, error) {
	optionsJSON, sessionData, err := s.webauthn.BeginLogin()
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("begin login: %w", err)
	}
	ceremonyID, err := s.ceremonies.Save(ctx, domain.CeremonyKindLogin, nil, nil, sessionData,
		s.clock.Now().Add(ceremonyTTL))
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("save ceremony: %w", err)
	}
	return ceremonyID, optionsJSON, nil
}

// FinishLogin verifies responseJSON of the browser against the login ceremony with
// ceremonyID, finding the user by the user handle in it, then records the sign-in on the
// passkey and the user and opens a session, returning its token for the client and its
// expiry. The ceremony is used up either way. It fails with domain.ErrCeremonyNotFound
// when the ceremony is unknown, expired or already finished, and with
// domain.ErrPasskeyVerificationFailed when the response does not verify or names an
// unknown or invited user or a passkey the user does not have, a deleted one included.
func (s *Service) FinishLogin(ctx context.Context, ceremonyID uuid.UUID, responseJSON []byte, userAgent string) (
	string, time.Time, error,
) {
	_, _, sessionData, err := s.ceremonies.Take(ctx, ceremonyID, domain.CeremonyKindLogin)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("take ceremony: %w", err)
	}

	var owned []domain.Passkey
	lookup := func(userHandle, credentialID []byte) (domain.User, []byte, []domain.Passkey, error) {
		user, err := s.users.GetByWebAuthnID(ctx, userHandle)
		if errors.Is(err, domain.ErrUserNotFound) {
			return domain.User{}, nil, nil, domain.ErrPasskeyVerificationFailed
		}
		if err != nil {
			return domain.User{}, nil, nil, fmt.Errorf("get user: %w", err)
		}
		// An invited user cannot add a passkey; refused all the same.
		if user.Status() == domain.UserStatusInvited {
			return domain.User{}, nil, nil, domain.ErrPasskeyVerificationFailed
		}
		existing, err := s.passkeys.ListByUser(ctx, user.ID)
		if err != nil {
			return domain.User{}, nil, nil, fmt.Errorf("list passkeys: %w", err)
		}
		if _, ok := byCredentialID(existing, credentialID); !ok {
			return domain.User{}, nil, nil, domain.ErrPasskeyVerificationFailed
		}
		owned = existing
		return user, userHandle, existing, nil
	}
	userID, credentialID, signCount, backupState, err := s.webauthn.FinishLogin(sessionData, responseJSON, lookup)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("finish login: %w", err)
	}
	passkey, ok := byCredentialID(owned, credentialID)
	if !ok {
		return "", time.Time{}, domain.ErrPasskeyVerificationFailed
	}

	now := s.clock.Now()
	if err := s.passkeys.RecordUse(ctx, passkey.ID, signCount, backupState, now); err != nil {
		return "", time.Time{}, fmt.Errorf("record passkey use: %w", err)
	}
	if err := s.users.TouchLogin(ctx, userID, now); err != nil {
		return "", time.Time{}, fmt.Errorf("touch login: %w", err)
	}
	token, expiresAt, err := s.sessions.Open(ctx, userID, userAgent)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("open session: %w", err)
	}
	return token, expiresAt, nil
}

// byCredentialID returns the passkey among passkeys with credentialID.
func byCredentialID(passkeys []domain.Passkey, credentialID []byte) (domain.Passkey, bool) {
	for _, p := range passkeys {
		if bytes.Equal(p.CredentialID, credentialID) {
			return p, true
		}
	}
	return domain.Passkey{}, false
}
