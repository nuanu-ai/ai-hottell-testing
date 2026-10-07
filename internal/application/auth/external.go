// Package auth holds the sign-in use cases and the ports they reach the outside through.
package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

//go:generate mockgen -destination=mock/external.go -package=mock . Users,SessionService,PasswordHasher,Clock,TxManager

// ErrMalformedToken reports a string that cannot be a token this service issued;
// callers answer it as a link or session that is not found.
var ErrMalformedToken = errors.New("malformed token")

// PasswordHasher hashes passwords for storage and checks a password against a stored hash.
type PasswordHasher interface {
	// Hash returns the storable hash of password, salted afresh on every call.
	Hash(password string) (string, error)
	// Verify reports whether password matches hash; a hash it cannot parse is an error.
	Verify(password, hash string) (bool, error)
}

// TokenIssuer issues one-time tokens: the token goes to the client, only its hash is stored.
type TokenIssuer interface {
	// New returns a fresh random token and the hash to store for it.
	New() (token string, hash []byte, err error)
	// Hash returns the stored hash of token, or ErrMalformedToken for a string that
	// cannot be an issued token.
	Hash(token string) ([]byte, error)
}

// Clock tells the current time.
type Clock interface {
	Now() time.Time
}

// Users keeps the users.
type Users interface {
	// GetByEmail returns the user with email and its password hash, empty for an invited
	// user; domain.ErrUserNotFound when there is none.
	GetByEmail(ctx context.Context, email domain.Email) (domain.User, string, error)
	// GetByID returns the user with id and its password hash, empty for an invited user;
	// domain.ErrUserNotFound when there is none.
	GetByID(ctx context.Context, id uuid.UUID) (domain.User, string, error)
	// TouchLogin records at as the last sign-in of the user with id.
	TouchLogin(ctx context.Context, id uuid.UUID, at time.Time) error
	// SetPassword replaces the password hash of the user with id.
	SetPassword(ctx context.Context, id uuid.UUID, passwordHash string) error
}

// SessionService opens and closes the sessions of signed-in browsers.
type SessionService interface {
	// Open opens a session of the user with userID and returns its token for the client
	// and its expiry.
	Open(ctx context.Context, userID uuid.UUID, userAgent string) (string, time.Time, error)
	// CloseOthers closes every session of the user with userID but keepID.
	CloseOthers(ctx context.Context, userID, keepID uuid.UUID) error
}

// TxManager runs fn within a transaction the repositories take from the context.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}
