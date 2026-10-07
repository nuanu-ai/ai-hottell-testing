package ingest

import (
	"context"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

//go:generate mockgen -destination=mock/external.go -package=mock . Keys

// Keys recognises the collector token a request carries.
type Keys interface {
	// Resolve returns the id of the user whose active key of kind plaintext is; a malformed
	// or unknown key, or a key of another kind, is domain.ErrAccessKeyNotFound, a revoked
	// one domain.ErrAccessKeyRevoked.
	Resolve(ctx context.Context, kind domain.AccessKeyKind, plaintext string) (uuid.UUID, error)
}
