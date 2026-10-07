//go:generate mockgen -source=external.go -destination=./mock/external.go -package mock

// Package deliverystatus holds the use cases of the delivery status the hottell binary
// reports and the ports they reach the outside through.
package deliverystatus

import (
	"context"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// Statuses keeps the last delivery status of each user.
type Statuses interface {
	// Save keeps status as the delivery status of the user with userID in place of the
	// previous one; domain.ErrUserNotFound when there is no such user.
	Save(ctx context.Context, userID uuid.UUID, status domain.DeliveryStatus) error
	// Get returns the delivery status of the user with userID, and false when the user has
	// never reported one.
	Get(ctx context.Context, userID uuid.UUID) (domain.DeliveryStatus, bool, error)
}

// Clock tells the current time.
type Clock interface {
	Now() time.Time
}
