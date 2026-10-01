package users

import (
	"context"
	"errors"
	"fmt"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// CreateFirst creates an active user with email, name and password, as the operator does
// for the first user. It is idempotent: when a user with email exists already, it changes
// nothing and returns that user with created false. Invalid input fails with the domain
// error of the value.
func (s *Service) CreateFirst(ctx context.Context, email, name, password string) (domain.User, bool, error) {
	addr, err := domain.ParseEmail(email)
	if err != nil {
		return domain.User{}, false, err
	}
	userName, err := domain.ParseUserName(name)
	if err != nil {
		return domain.User{}, false, err
	}
	if err := domain.ValidatePassword(password); err != nil {
		return domain.User{}, false, err
	}

	existing, err := s.existing(ctx, addr)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, domain.ErrUserNotFound) {
		return domain.User{}, false, err
	}

	hash, err := s.hasher.Hash(password)
	if err != nil {
		return domain.User{}, false, fmt.Errorf("hash password: %w", err)
	}
	user, err := s.users.CreateActive(ctx, addr, userName, hash)
	if errors.Is(err, domain.ErrEmailTaken) {
		// A concurrent run created the user between the lookup and the insert.
		existing, err := s.existing(ctx, addr)
		if err != nil {
			return domain.User{}, false, err
		}
		return existing, false, nil
	}
	if err != nil {
		return domain.User{}, false, fmt.Errorf("create user: %w", err)
	}
	return user, true, nil
}

// existing returns the user with email; domain.ErrUserNotFound when there is none.
func (s *Service) existing(ctx context.Context, email domain.Email) (domain.User, error) {
	user, _, err := s.users.GetByEmail(ctx, email)
	if errors.Is(err, domain.ErrUserNotFound) {
		return domain.User{}, err
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("get user: %w", err)
	}
	return user, nil
}
