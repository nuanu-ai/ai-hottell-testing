package session

import (
	"context"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/application/auth"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

//go:generate mockgen -destination=mock/external.go -package=mock . Sessions,TokenIssuer,Clock

// Sessions stores signed-in browsers; only the hash of a session token is kept.
type Sessions interface {
	// Create opens a session of the user with userID, found later by tokenHash;
	// domain.ErrUserNotFound when there is no such user.
	Create(ctx context.Context, userID uuid.UUID, tokenHash []byte, expiresAt time.Time, userAgent string) (
		domain.Session, error)
	// GetByTokenHash returns the live session with tokenHash; domain.ErrSessionNotFound
	// when there is none.
	GetByTokenHash(ctx context.Context, tokenHash []byte) (domain.Session, error)
	// Extend moves the expiry of the session with id and records lastSeenAt;
	// domain.ErrSessionNotFound when there is no such session.
	Extend(ctx context.Context, id uuid.UUID, expiresAt, lastSeenAt time.Time) error
	// Delete closes the session with id; a session already gone is not an error.
	Delete(ctx context.Context, id uuid.UUID) error
	// DeleteAllForUser closes every session of the user with userID.
	DeleteAllForUser(ctx context.Context, userID uuid.UUID) error
	// DeleteAllForUserExcept closes every session of the user with userID but keepID.
	DeleteAllForUserExcept(ctx context.Context, userID, keepID uuid.UUID) error
}

// TokenIssuer issues session tokens; the port is shared with the other sign-in use cases.
type TokenIssuer = auth.TokenIssuer

// Clock tells the current time; the port is shared with the other sign-in use cases.
type Clock = auth.Clock
