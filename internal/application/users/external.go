//go:generate mockgen -source=external.go -destination=./mock/external.go -package mock

package users

import (
	"context"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// Users keeps the users.
type Users interface {
	// List returns every user.
	List(ctx context.Context) ([]domain.User, error)
	// GetByID returns the user with id and its password hash, empty for an invited user;
	// domain.ErrUserNotFound when there is none.
	GetByID(ctx context.Context, id uuid.UUID) (domain.User, string, error)
	// GetByEmail returns the user with email and its password hash, empty for an invited
	// user; domain.ErrUserNotFound when there is none.
	GetByEmail(ctx context.Context, email domain.Email) (domain.User, string, error)
	// CreateActive adds a user with a password; domain.ErrEmailTaken when email is taken.
	CreateActive(ctx context.Context, email domain.Email, name domain.UserName, passwordHash string) (domain.User, error)
	// CreateInvited adds a user without a password; domain.ErrEmailTaken when email is taken.
	CreateInvited(ctx context.Context, email domain.Email, name domain.UserName) (domain.User, error)
	// SetPassword replaces the password hash of the user with id; domain.ErrUserNotFound
	// when there is none.
	SetPassword(ctx context.Context, id uuid.UUID, passwordHash string) error
	// TouchLogin records at as the last sign-in of the user with id; domain.ErrUserNotFound
	// when there is none.
	TouchLogin(ctx context.Context, id uuid.UUID, at time.Time) error
	// DeleteInvited removes the user with id while the invitation is not accepted;
	// domain.ErrUserNotInvited when the user has a password, domain.ErrUserNotFound
	// when there is none.
	DeleteInvited(ctx context.Context, id uuid.UUID) error
}

// Links keeps the one-time links.
type Links interface {
	// Replace issues a link of kind to the user with userID, found later by tokenHash,
	// and removes the unused link of that kind the user had; domain.ErrUserNotFound
	// when there is no such user.
	Replace(
		ctx context.Context, userID uuid.UUID, kind domain.LinkKind, tokenHash []byte, createdBy uuid.UUID, expiresAt time.Time,
	) (domain.Link, error)
	// GetByTokenHash returns the link with tokenHash, used and expired ones included;
	// domain.ErrLinkNotFound when there is none.
	GetByTokenHash(ctx context.Context, tokenHash []byte) (domain.Link, error)
	// MarkUsed records at as the time the link with id was used; domain.ErrLinkUsed when
	// it was used already, domain.ErrLinkNotFound when there is no such link.
	MarkUsed(ctx context.Context, id uuid.UUID, at time.Time) error
	// GetActiveForUser returns the unused link of kind of the user with userID, expired
	// or not; domain.ErrLinkNotFound when there is none.
	GetActiveForUser(ctx context.Context, userID uuid.UUID, kind domain.LinkKind) (domain.Link, error)
	// DeleteUnused removes the unused link of kind of the user with userID, if there is one.
	DeleteUnused(ctx context.Context, userID uuid.UUID, kind domain.LinkKind) error
}

// sessionRepository keeps the sessions.
type sessionRepository interface {
	// DeleteAllForUser closes every session of the user with userID.
	DeleteAllForUser(ctx context.Context, userID uuid.UUID) error
}

// passwordHasher hashes passwords for storage.
type passwordHasher interface {
	// Hash returns the storable hash of password.
	Hash(password string) (string, error)
}

// tokenIssuer issues one-time tokens: the token goes to the client, only its hash is stored.
type tokenIssuer interface {
	// New returns a fresh random token and the hash to store for it.
	New() (token string, hash []byte, err error)
	// Hash returns the stored hash of token, or auth.ErrMalformedToken for a string that
	// cannot be an issued token.
	Hash(token string) ([]byte, error)
}

// sessionOpener opens sessions of signed-in browsers.
type sessionOpener interface {
	// Open opens a session of the user with userID and returns its token for the client
	// and its expiry.
	Open(ctx context.Context, userID uuid.UUID, userAgent string) (string, time.Time, error)
}

// clock tells the current time.
type clock interface {
	Now() time.Time
}

// txManager runs fn within a transaction the repositories take from the context.
type txManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// PublicOrigin is the origin the service is reached at, HT_PUBLIC_ORIGIN; links in
// responses are built from it.
type PublicOrigin string
