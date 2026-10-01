package domain

import (
	"time"

	"github.com/google/uuid"
)

// Passkey is a WebAuthn credential a user signs in with. LastUsedAt is nil until the
// first sign-in with it.
type Passkey struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	CredentialID    []byte
	PublicKey       []byte
	AttestationType string
	AAGUID          []byte
	SignCount       uint32
	Transports      []string
	BackupEligible  bool
	BackupState     bool
	Name            PasskeyName
	CreatedAt       time.Time
	LastUsedAt      *time.Time
}

// CeremonyKind is which WebAuthn ceremony is in progress.
type CeremonyKind string

// Ceremony kinds.
const (
	// CeremonyKindRegister adds a passkey to a signed-in user.
	CeremonyKindRegister CeremonyKind = "register"
	// CeremonyKindLogin signs in with a passkey.
	CeremonyKindLogin CeremonyKind = "login"
)
