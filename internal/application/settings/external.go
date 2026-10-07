//go:generate mockgen -source=external.go -destination=./mock/external.go -package mock

// Package settings holds the use cases of the deny settings and the ports they reach the outside through.
package settings

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
)

// Settings keeps the deny settings of the users, one document with its version per user.
type Settings interface {
	// Get returns the settings document of the user with userID and its version; an empty
	// document and version 0 when the user has never saved settings.
	Get(ctx context.Context, userID uuid.UUID) (json.RawMessage, int64, error)
	// Save stores document as the settings of the user with userID with version
	// expectedVersion+1; domain.ErrSettingsVersionConflict when the stored version is not
	// expectedVersion, domain.ErrUserNotFound when there is no such user.
	Save(ctx context.Context, userID uuid.UUID, document json.RawMessage, expectedVersion int64) error
}

// Notifier tells the subscribers of a user that the settings of the user changed.
type Notifier interface {
	// Publish tells every subscriber of the user with userID that their settings now have
	// version; it never blocks on a slow subscriber, which gets the latest version.
	Publish(userID uuid.UUID, version int64)
	// Subscribe returns the channel the versions of the settings of the user with userID
	// arrive on after the call; it is closed and the subscription dropped once ctx is done.
	Subscribe(ctx context.Context, userID uuid.UUID) <-chan int64
}
