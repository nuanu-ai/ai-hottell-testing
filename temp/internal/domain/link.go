package domain

import (
	"time"

	"github.com/google/uuid"
)

// LinkKind is what a one-time link lets its holder do.
type LinkKind string

// Link kinds.
const (
	// LinkKindInvite lets an invited user set the first password.
	LinkKindInvite LinkKind = "invite"
	// LinkKindReset lets a user set a new password.
	LinkKindReset LinkKind = "reset"
)

// LinkTTL is how long a link of either kind stays usable after it is issued.
const LinkTTL = 7 * 24 * time.Hour

// Link is a one-time link issued to a user. UsedAt is nil until the link is used.
type Link struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Kind      LinkKind
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// CheckUsable returns ErrLinkUsed for a used link, ErrLinkExpired for a link whose
// expiry is not after now, and nil otherwise.
func (l Link) CheckUsable(now time.Time) error {
	if l.UsedAt != nil {
		return ErrLinkUsed
	}
	if !now.Before(l.ExpiresAt) {
		return ErrLinkExpired
	}
	return nil
}
