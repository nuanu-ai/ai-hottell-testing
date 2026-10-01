package mcp

import (
	"context"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

//go:generate mockgen -destination=mock/external.go -package=mock . Keys,Users,Settings

// Keys recognises the MCP key a request carries and hands out the collector token.
type Keys interface {
	// Resolve returns the id of the user whose active key of kind plaintext is; a malformed
	// or unknown key, or a key of another kind, is domain.ErrAccessKeyNotFound, a revoked
	// one domain.ErrAccessKeyRevoked.
	Resolve(ctx context.Context, kind domain.AccessKeyKind, plaintext string) (uuid.UUID, error)
	// IngestToken returns the collector token of the user with userID, creating it when they
	// have none.
	IngestToken(ctx context.Context, userID uuid.UUID) (string, error)
}

// Users reads the user a request acts for.
type Users interface {
	// Me returns the user with userID; domain.ErrUserNotFound when there is none.
	Me(ctx context.Context, userID uuid.UUID) (domain.User, error)
}

// Settings reads the deny settings of a user and tells when they change.
type Settings interface {
	// Get returns the settings of the user with userID in full form and their version; the
	// defaults and version 0 when the user has never saved settings.
	Get(ctx context.Context, userID uuid.UUID) (domain.TelemetrySettings, int64, error)
	// Subscribe returns the channel the new versions of the settings of the user with userID
	// arrive on; it is closed once ctx is done.
	Subscribe(ctx context.Context, userID uuid.UUID) <-chan int64
}
