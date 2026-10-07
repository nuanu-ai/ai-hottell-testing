package passkeys

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// BeginRegistration starts adding a passkey named name to the signed-in user with
// userID and returns the ceremony to finish within five minutes and the options for the
// browser. It fails with domain.ErrInvalidPasskeyName when name is not 1–64 characters.
func (s *Service) BeginRegistration(ctx context.Context, userID uuid.UUID, name string) (uuid.UUID, []byte, error) {
	passkeyName, err := domain.ParsePasskeyName(name)
	if err != nil {
		return uuid.Nil, nil, err
	}
	user, webAuthnID, existing, err := s.owner(ctx, userID)
	if err != nil {
		return uuid.Nil, nil, err
	}
	optionsJSON, sessionData, err := s.webauthn.BeginRegistration(user, webAuthnID, existing)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("begin registration: %w", err)
	}
	kept := passkeyName.String()
	ceremonyID, err := s.ceremonies.Save(ctx, domain.CeremonyKindRegister, &userID, &kept, sessionData,
		s.clock.Now().Add(ceremonyTTL))
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("save ceremony: %w", err)
	}
	return ceremonyID, optionsJSON, nil
}

// FinishRegistration verifies responseJSON of the browser against the ceremony with
// ceremonyID that the user with userID began, and stores the new passkey under the name
// given at the begin. The ceremony is used up either way. It fails with
// domain.ErrCeremonyNotFound when the ceremony is unknown, expired or another user's,
// and with domain.ErrPasskeyVerificationFailed when the response does not verify.
func (s *Service) FinishRegistration(
	ctx context.Context, userID, ceremonyID uuid.UUID, responseJSON []byte,
) (domain.Passkey, error) {
	owner, name, sessionData, err := s.ceremonies.Take(ctx, ceremonyID, domain.CeremonyKindRegister)
	if err != nil {
		return domain.Passkey{}, fmt.Errorf("take ceremony: %w", err)
	}
	if owner == nil || *owner != userID || name == nil {
		return domain.Passkey{}, domain.ErrCeremonyNotFound
	}
	user, webAuthnID, existing, err := s.owner(ctx, userID)
	if err != nil {
		return domain.Passkey{}, err
	}
	passkey, err := s.webauthn.FinishRegistration(user, webAuthnID, existing, sessionData, responseJSON)
	if err != nil {
		return domain.Passkey{}, fmt.Errorf("finish registration: %w", err)
	}
	passkey.UserID = userID
	// Checked by BeginRegistration before it was kept with the ceremony.
	passkey.Name = domain.PasskeyName(*name)
	created, err := s.passkeys.Create(ctx, passkey)
	if err != nil {
		return domain.Passkey{}, fmt.Errorf("create passkey: %w", err)
	}
	return created, nil
}

// owner returns the user with userID, the user's WebAuthn user handle and passkeys.
func (s *Service) owner(ctx context.Context, userID uuid.UUID) (domain.User, []byte, []domain.Passkey, error) {
	user, _, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return domain.User{}, nil, nil, fmt.Errorf("get user: %w", err)
	}
	webAuthnID, err := s.users.GetWebAuthnID(ctx, userID)
	if err != nil {
		return domain.User{}, nil, nil, fmt.Errorf("get webauthn user id: %w", err)
	}
	existing, err := s.passkeys.ListByUser(ctx, userID)
	if err != nil {
		return domain.User{}, nil, nil, fmt.Errorf("list passkeys: %w", err)
	}
	return user, webAuthnID, existing, nil
}
