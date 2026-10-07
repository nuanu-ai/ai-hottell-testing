package domain

import (
	"time"

	"github.com/google/uuid"
)

// UserView is a user as the users page lists it. InviteExpiresAt is the expiry of the
// unused invitation and is set only for an invited user that has one.
type UserView struct {
	ID              uuid.UUID
	Email           Email
	Name            UserName
	Status          UserStatus
	CreatedAt       time.Time
	LastLoginAt     *time.Time
	InviteExpiresAt *time.Time
}

// NewUserView returns the view of user with the invitation expiry inviteExpiresAt,
// dropped unless the user is invited.
func NewUserView(user User, inviteExpiresAt *time.Time) UserView {
	view := UserView{
		ID:          user.ID,
		Email:       user.Email,
		Name:        user.Name,
		Status:      user.Status(),
		CreatedAt:   user.CreatedAt,
		LastLoginAt: user.LastLoginAt,
	}
	if view.Status == UserStatusInvited {
		view.InviteExpiresAt = inviteExpiresAt
	}
	return view
}
