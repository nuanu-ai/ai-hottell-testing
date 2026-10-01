// Package webauthn runs the relying party side of the WebAuthn ceremonies on
// go-webauthn; nothing else in the service knows the library.
package webauthn

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	gowebauthn "github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/application/passkeys"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

const (
	rpDisplayName = "Телеметрия агентов"
	// ceremonyTimeout is how long the browser and the relying party wait for the user.
	ceremonyTimeout = 5 * time.Minute
)

var _ passkeys.WebAuthn = (*RelyingParty)(nil)

// RelyingParty runs registration and discoverable sign-in ceremonies of passkeys.
type RelyingParty struct {
	webauthn *gowebauthn.WebAuthn
}

// New returns a RelyingParty for rpID that accepts ceremonies from rpOrigins.
func New(rpID string, rpOrigins []string) (*RelyingParty, error) {
	if rpID == "" {
		return nil, errors.New("webauthn: RP ID is required")
	}
	timeout := gowebauthn.TimeoutConfig{Enforce: true, Timeout: ceremonyTimeout, TimeoutUVD: ceremonyTimeout}
	wa, err := gowebauthn.New(&gowebauthn.Config{
		RPID:                  rpID,
		RPDisplayName:         rpDisplayName,
		RPOrigins:             rpOrigins,
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationPreferred,
		},
		Timeouts: gowebauthn.TimeoutsConfig{Login: timeout, Registration: timeout},
	})
	if err != nil {
		return nil, fmt.Errorf("webauthn: %w", err)
	}
	return &RelyingParty{webauthn: wa}, nil
}

// BeginRegistration returns the creation options for a new discoverable passkey of user
// that none of existing may answer, and the session data to finish it with.
func (rp *RelyingParty) BeginRegistration(user domain.User, webauthnUserID []byte, existing []domain.Passkey) (
	optionsJSON, sessionData []byte, err error,
) {
	owner := newOwner(user, webauthnUserID, existing)
	creation, session, err := rp.webauthn.BeginRegistration(owner,
		gowebauthn.WithExclusions(gowebauthn.Credentials(owner.credentials).CredentialDescriptors()))
	if err != nil {
		return nil, nil, fmt.Errorf("begin registration: %w", err)
	}
	return marshal(creation.Response, session)
}

// FinishRegistration verifies responseJSON against sessionData and returns the new
// passkey of user; domain.ErrPasskeyVerificationFailed when it does not verify.
func (rp *RelyingParty) FinishRegistration(user domain.User, webauthnUserID []byte, existing []domain.Passkey,
	sessionData, responseJSON []byte,
) (domain.Passkey, error) {
	session, err := unmarshalSession(sessionData)
	if err != nil {
		return domain.Passkey{}, err
	}
	response, err := protocol.ParseCredentialCreationResponseBytes(responseJSON)
	if err != nil {
		return domain.Passkey{}, verificationFailed(err)
	}
	credential, err := rp.webauthn.CreateCredential(newOwner(user, webauthnUserID, existing), session, response)
	if err != nil {
		return domain.Passkey{}, verificationFailed(err)
	}

	transports := make([]string, len(credential.Transport))
	for i, t := range credential.Transport {
		transports[i] = string(t)
	}
	return domain.Passkey{
		UserID:          user.ID,
		CredentialID:    credential.ID,
		PublicKey:       credential.PublicKey,
		AttestationType: credential.AttestationType,
		AAGUID:          credential.Authenticator.AAGUID,
		SignCount:       credential.Authenticator.SignCount,
		Transports:      transports,
		BackupEligible:  credential.Flags.BackupEligible,
		BackupState:     credential.Flags.BackupState,
	}, nil
}

// BeginLogin returns the request options for a sign-in with any discoverable passkey,
// and the session data to finish it with.
func (rp *RelyingParty) BeginLogin() (optionsJSON, sessionData []byte, err error) {
	assertion, session, err := rp.webauthn.BeginDiscoverableLogin(
		gowebauthn.WithUserVerification(protocol.VerificationPreferred))
	if err != nil {
		return nil, nil, fmt.Errorf("begin login: %w", err)
	}
	return marshal(assertion.Response, session)
}

// FinishLogin verifies responseJSON against sessionData and the passkeys lookup returns
// for its user handle, and returns the owner, the passkey used and its new signature
// counter and backup state; an error of lookup as is, domain.ErrPasskeyVerificationFailed
// when the response does not verify. A counter that did not grow is not an error: the
// stored one is returned and the clone warning of go-webauthn is not reported.
func (rp *RelyingParty) FinishLogin(sessionData, responseJSON []byte, lookup passkeys.PasskeyLookup) (
	userID uuid.UUID, credentialID []byte, signCount uint32, backupState bool, err error,
) {
	session, err := unmarshalSession(sessionData)
	if err != nil {
		return uuid.Nil, nil, 0, false, err
	}
	response, err := protocol.ParseCredentialRequestResponseBytes(responseJSON)
	if err != nil {
		return uuid.Nil, nil, 0, false, verificationFailed(err)
	}

	var lookupErr error
	handler := func(rawID, userHandle []byte) (gowebauthn.User, error) {
		user, webauthnUserID, existing, err := lookup(userHandle, rawID)
		if err != nil {
			lookupErr = err
			return nil, err
		}
		// The user handle is not signed, so only passkeys of the user found by it may
		// answer: a passkey of someone else never signs this user in.
		owned := make([]domain.Passkey, 0, len(existing))
		for _, p := range existing {
			if p.UserID == user.ID {
				owned = append(owned, p)
			}
		}
		return newOwner(user, webauthnUserID, owned), nil
	}
	user, credential, err := rp.webauthn.ValidatePasskeyLogin(handler, session, response)
	if lookupErr != nil {
		return uuid.Nil, nil, 0, false, lookupErr
	}
	if err != nil {
		return uuid.Nil, nil, 0, false, verificationFailed(err)
	}
	owner, ok := user.(*owner)
	if !ok {
		return uuid.Nil, nil, 0, false, fmt.Errorf("finish login: unexpected user %T", user)
	}
	return owner.user.ID, credential.ID, credential.Authenticator.SignCount, credential.Flags.BackupState, nil
}

// owner is a user as go-webauthn sees it.
type owner struct {
	user        domain.User
	handle      []byte
	credentials []gowebauthn.Credential
}

func newOwner(user domain.User, webauthnUserID []byte, existing []domain.Passkey) *owner {
	credentials := make([]gowebauthn.Credential, len(existing))
	for i, p := range existing {
		credentials[i] = credential(p)
	}
	return &owner{user: user, handle: webauthnUserID, credentials: credentials}
}

func (o *owner) WebAuthnID() []byte                           { return o.handle }
func (o *owner) WebAuthnName() string                         { return o.user.Email.String() }
func (o *owner) WebAuthnDisplayName() string                  { return o.user.Name.String() }
func (o *owner) WebAuthnCredentials() []gowebauthn.Credential { return o.credentials }

// credential restores the credential record of passkey.
func credential(p domain.Passkey) gowebauthn.Credential {
	transports := make([]protocol.AuthenticatorTransport, len(p.Transports))
	for i, t := range p.Transports {
		transports[i] = protocol.AuthenticatorTransport(t)
	}
	var flags protocol.AuthenticatorFlags
	if p.BackupEligible {
		flags |= protocol.FlagBackupEligible
	}
	if p.BackupState {
		flags |= protocol.FlagBackupState
	}
	return gowebauthn.Credential{
		ID:              p.CredentialID,
		PublicKey:       p.PublicKey,
		AttestationType: p.AttestationType,
		Transport:       transports,
		Flags:           gowebauthn.NewCredentialFlags(flags),
		Authenticator:   gowebauthn.Authenticator{AAGUID: p.AAGUID, SignCount: p.SignCount},
	}
}

func marshal(options any, session *gowebauthn.SessionData) (optionsJSON, sessionData []byte, err error) {
	if optionsJSON, err = json.Marshal(options); err != nil {
		return nil, nil, fmt.Errorf("encode options: %w", err)
	}
	if sessionData, err = json.Marshal(session); err != nil {
		return nil, nil, fmt.Errorf("encode session data: %w", err)
	}
	return optionsJSON, sessionData, nil
}

// unmarshalSession decodes session data this adapter encoded; it is kept by the service,
// so a failure is not the browser's.
func unmarshalSession(data []byte) (gowebauthn.SessionData, error) {
	var session gowebauthn.SessionData
	if err := json.Unmarshal(data, &session); err != nil {
		return gowebauthn.SessionData{}, fmt.Errorf("decode session data: %w", err)
	}
	return session, nil
}

// verificationFailed reports a browser response that does not verify, keeping why.
func verificationFailed(err error) error {
	return fmt.Errorf("%w: %w", domain.ErrPasskeyVerificationFailed, err)
}
