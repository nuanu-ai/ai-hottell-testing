package users

import (
	"context"
	"errors"
	"fmt"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// List returns every user in the order the repository keeps them; an invited user
// carries the expiry of the unused invitation, expired or not, so the page can tell
// a lapsed invitation from a live one.
func (s *Service) List(ctx context.Context) ([]domain.UserView, error) {
	users, err := s.users.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	views := make([]domain.UserView, 0, len(users))
	for _, user := range users {
		var expiresAt *time.Time
		if user.Status() == domain.UserStatusInvited {
			link, err := s.links.GetActiveForUser(ctx, user.ID, domain.LinkKindInvite)
			switch {
			case err == nil:
				expiresAt = &link.ExpiresAt
			case !errors.Is(err, domain.ErrLinkNotFound):
				return nil, fmt.Errorf("get invite link: %w", err)
			}
		}
		views = append(views, domain.NewUserView(user, expiresAt))
	}
	return views, nil
}
