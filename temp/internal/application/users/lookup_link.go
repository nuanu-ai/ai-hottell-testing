package users

import (
	"context"
	"errors"
	"fmt"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// LookupLink returns the email and the name of the user a link of kind that token opens
// was issued to, for the invitation and the reset pages; domain.ErrLinkNotFound for a
// malformed or unknown token or a link of another kind, domain.ErrLinkUsed for a used
// link, domain.ErrLinkExpired for an expired one.
func (s *Service) LookupLink(ctx context.Context, token string, kind domain.LinkKind) (string, string, error) {
	link, err := s.usableLink(ctx, token, kind)
	if err != nil {
		return "", "", fmt.Errorf("lookup link: %w", err)
	}
	user, _, err := s.users.GetByID(ctx, link.UserID)
	if errors.Is(err, domain.ErrUserNotFound) {
		// The user was deleted since the read, and the link with it.
		return "", "", domain.ErrLinkNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("get user: %w", err)
	}
	return user.Email.String(), string(user.Name), nil
}
