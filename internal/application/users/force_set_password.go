package users

import (
	"context"
	"fmt"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// ForceSetPassword sets password for the user with email without the current one, as the
// operator does when no other user can issue a reset link. An invited user becomes active
// and the invitation link stops working; every session of the user is closed. It returns
// domain.ErrUserNotFound when there is no such user and the domain error of an invalid value.
func (s *Service) ForceSetPassword(ctx context.Context, email, password string) (domain.User, error) {
	addr, err := domain.ParseEmail(email)
	if err != nil {
		return domain.User{}, err
	}
	if err := domain.ValidatePassword(password); err != nil {
		return domain.User{}, err
	}
	// Hashed before the transaction: argon2id is slow on purpose.
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return domain.User{}, fmt.Errorf("hash password: %w", err)
	}

	var user domain.User
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		user, err = s.existing(ctx, addr)
		if err != nil {
			return err
		}
		if err := s.users.SetPassword(ctx, user.ID, hash); err != nil {
			return fmt.Errorf("set password: %w", err)
		}
		if !user.HasPassword {
			if err := s.links.DeleteUnused(ctx, user.ID, domain.LinkKindInvite); err != nil {
				return fmt.Errorf("delete invitation: %w", err)
			}
		}
		if err := s.sessions.DeleteAllForUser(ctx, user.ID); err != nil {
			return fmt.Errorf("close sessions: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.User{}, err
	}
	user.HasPassword = true
	return user, nil
}
