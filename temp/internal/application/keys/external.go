// Package keys issues, revokes and recognizes the access keys of the hottell binary: the
// MCP key and the collector token.
package keys

import (
	"context"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/application/auth"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

//go:generate mockgen -destination=mock/external.go -package=mock . AccessKeys,TokenIssuer,Clock

// AccessKeys stores access keys: a user has at most one active key of each kind. An MCP
// key is kept only as its hash, an ingest key as its hash and its open value.
type AccessKeys interface {
	// Active returns the active key of kind of the user with userID, with its open value
	// for an ingest key; domain.ErrAccessKeyNotFound when there is none.
	Active(ctx context.Context, userID uuid.UUID, kind domain.AccessKeyKind) (domain.AccessKey, error)
	// Replace revokes the active key of kind of the user with userID, if there is one, and
	// in the same transaction creates a new one found later by hash; plaintext is the open
	// value of an ingest key and empty for an MCP key. domain.ErrUserNotFound when there is
	// no such user.
	Replace(ctx context.Context, userID uuid.UUID, kind domain.AccessKeyKind, hash []byte, plaintext string) (
		domain.AccessKey, error)
	// Revoke revokes the active key of kind of the user with userID;
	// domain.ErrAccessKeyNotFound when there is none.
	Revoke(ctx context.Context, userID uuid.UUID, kind domain.AccessKeyKind) error
	// FindByHash returns the active key with hash; domain.ErrAccessKeyRevoked when the key
	// is revoked and domain.ErrAccessKeyNotFound when there is no such key.
	FindByHash(ctx context.Context, hash []byte) (domain.AccessKey, error)
	// Touch records at as the last use of the active key with id;
	// domain.ErrAccessKeyRevoked when the key is revoked and domain.ErrAccessKeyNotFound
	// when there is no such key.
	Touch(ctx context.Context, id uuid.UUID, at time.Time) error
}

// TokenIssuer issues the open values of keys and their hashes; the port is shared with the
// sign-in use cases.
type TokenIssuer = auth.TokenIssuer

// Clock tells the current time; the port is shared with the sign-in use cases.
type Clock = auth.Clock
