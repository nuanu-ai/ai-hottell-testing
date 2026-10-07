package users

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// Invite creates an invited user with email and name on behalf of actorID together with
// an invitation link that lives domain.LinkTTL, and returns the user and the link's URL;
// domain.ErrInvalidEmail or domain.ErrInvalidName for bad input, domain.ErrEmailTaken
// when the email belongs to another user.
func (s *Service) Invite(ctx context.Context, actorID uuid.UUID, email, name string) (domain.UserView, string, error) {
	parsedEmail, err := domain.ParseEmail(email)
	if err != nil {
		return domain.UserView{}, "", err
	}
	parsedName, err := domain.ParseUserName(name)
	if err != nil {
		return domain.UserView{}, "", err
	}

	var (
		view      domain.UserView
		inviteURL string
	)
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		user, err := s.users.CreateInvited(ctx, parsedEmail, parsedName)
		if err != nil {
			return fmt.Errorf("create invited user: %w", err)
		}
		url, expiresAt, err := s.issueInvite(ctx, actorID, user.ID)
		if err != nil {
			return err
		}
		view, inviteURL = domain.NewUserView(user, &expiresAt), url
		return nil
	})
	if err != nil {
		return domain.UserView{}, "", fmt.Errorf("invite user: %w", err)
	}
	return view, inviteURL, nil
}
