package domain

import (
	"time"

	"github.com/google/uuid"
)

// UserStatus is computed from whether the user has set a password.
type UserStatus string

// User statuses.
const (
	// UserStatusInvited: the invitation is not accepted yet, the user has no password.
	UserStatusInvited UserStatus = "invited"
	// UserStatusActive: the user has set a password.
	UserStatusActive UserStatus = "active"
)

// User is a person who can sign in. LastLoginAt is nil until the first sign-in.
type User struct {
	ID          uuid.UUID
	Email       Email
	Name        UserName
	HasPassword bool
	CreatedAt   time.Time
	LastLoginAt *time.Time
}

// Status returns invited until the user sets a password, active afterwards.
func (u User) Status() UserStatus {
	if u.HasPassword {
		return UserStatusActive
	}
	return UserStatusInvited
}
