package domain

import (
	"time"

	"github.com/google/uuid"
)

// Session lifetime rules.
const (
	// SessionTTL is how long a session lives after it is opened or extended.
	SessionTTL = 30 * 24 * time.Hour
	// sessionRenewBefore: a session is extended once less than this is left of it,
	// so activity moves the expiry at most once a day.
	sessionRenewBefore = 29 * 24 * time.Hour
)

// Session is a signed-in browser of a user.
type Session struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	ExpiresAt  time.Time
	LastSeenAt time.Time
}

// NextExpiry returns the expiry after activity at now and whether it moved: when less
// than 29 days are left, the session is extended to now plus 30 days.
func (s Session) NextExpiry(now time.Time) (time.Time, bool) {
	if s.ExpiresAt.Sub(now) < sessionRenewBefore {
		return now.Add(SessionTTL), true
	}
	return s.ExpiresAt, false
}
