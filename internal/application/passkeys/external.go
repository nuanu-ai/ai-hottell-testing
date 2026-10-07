// Package passkeys holds the passkey use cases and the ports they reach the outside through.
package passkeys

import (
	"context"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

//go:generate mockgen -destination=mock/external.go -package=mock . Users,Passkeys,Ceremonies,WebAuthn,SessionService,Clock

// Users keeps the users.
type Users interface {
	// GetByID returns the user with id and its password hash, empty for an invited user;
	// domain.ErrUserNotFound when there is none.
	GetByID(ctx context.Context, id uuid.UUID) (domain.User, string, error)
	// GetWebAuthnID returns the WebAuthn user handle of the user with id;
	// domain.ErrUserNotFound when there is none.
	GetWebAuthnID(ctx context.Context, id uuid.UUID) ([]byte, error)
	// GetByWebAuthnID returns the user whose WebAuthn user handle is webAuthnUserID;
	// domain.ErrUserNotFound when there is none.
	GetByWebAuthnID(ctx context.Context, webAuthnUserID []byte) (domain.User, error)
	// TouchLogin records at as the last sign-in of the user with id.
	TouchLogin(ctx context.Context, id uuid.UUID, at time.Time) error
}

// Passkeys keeps the passkeys of users.
type Passkeys interface {
	// Create stores passkey under a generated id and creation time and returns it as stored.
	Create(ctx context.Context, passkey domain.Passkey) (domain.Passkey, error)
	// ListByUser returns the passkeys of the user with userID, oldest first.
	ListByUser(ctx context.Context, userID uuid.UUID) ([]domain.Passkey, error)
	// RecordUse records a sign-in at at with the passkey with id: its new signature
	// counter and backup state; domain.ErrPasskeyNotFound when there is no such passkey.
	RecordUse(ctx context.Context, id uuid.UUID, signCount uint32, backupState bool, at time.Time) error
	// Delete removes the passkey with id of the user with userID;
	// domain.ErrPasskeyNotFound when there is no such passkey or it belongs to another user.
	Delete(ctx context.Context, id, userID uuid.UUID) error
}

// Ceremonies keeps the state of WebAuthn ceremonies in progress.
type Ceremonies interface {
	// Save stores a ceremony of kind with its sessionData until expiresAt and returns its
	// id; userID and passkeyName are nil for a login.
	Save(ctx context.Context, kind domain.CeremonyKind, userID *uuid.UUID, passkeyName *string,
		sessionData []byte, expiresAt time.Time) (uuid.UUID, error)
	// Take returns the ceremony with id and removes it, so it can be finished once;
	// domain.ErrCeremonyNotFound when there is none, it has expired or it is not of kind.
	Take(ctx context.Context, id uuid.UUID, kind domain.CeremonyKind) (
		userID *uuid.UUID, passkeyName *string, sessionData []byte, err error)
}

// SessionService opens the sessions of signed-in browsers.
type SessionService interface {
	// Open opens a session of the user with userID and returns its token for the client
	// and its expiry.
	Open(ctx context.Context, userID uuid.UUID, userAgent string) (string, time.Time, error)
}

// Clock tells the current time.
type Clock interface {
	Now() time.Time
}

// PasskeyLookup finds the owner of the passkey a browser signed in with, by the user
// handle and the credential ID in its response: the user, the user's WebAuthn user
// handle and every passkey of the user.
type PasskeyLookup func(userHandle, credentialID []byte) (domain.User, []byte, []domain.Passkey, error)

// WebAuthn runs the relying party side of the WebAuthn ceremonies. optionsJSON is the
// publicKey member of the options the browser passes to the authenticator; sessionData
// is the opaque state kept between the begin and the finish of one ceremony. A browser
// response that fails verification is domain.ErrPasskeyVerificationFailed.
type WebAuthn interface {
	// BeginRegistration starts adding a discoverable passkey to user, whose WebAuthn user
	// handle is webauthnUserID; the existing passkeys of the user are excluded.
	BeginRegistration(user domain.User, webauthnUserID []byte, existing []domain.Passkey) (
		optionsJSON, sessionData []byte, err error)
	// FinishRegistration verifies responseJSON of the browser against sessionData and
	// returns the new passkey of user without an ID, a name or timestamps.
	FinishRegistration(user domain.User, webauthnUserID []byte, existing []domain.Passkey,
		sessionData, responseJSON []byte) (domain.Passkey, error)
	// BeginLogin starts a sign-in with any discoverable passkey, no user named up front.
	BeginLogin() (optionsJSON, sessionData []byte, err error)
	// FinishLogin verifies responseJSON of the browser against sessionData and the
	// passkey lookup finds, and returns the owner, the passkey used and its new signature
	// counter and backup state; an error of lookup is returned as is.
	FinishLogin(sessionData, responseJSON []byte, lookup PasskeyLookup) (
		userID uuid.UUID, credentialID []byte, signCount uint32, backupState bool, err error)
}
