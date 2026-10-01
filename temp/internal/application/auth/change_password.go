package auth

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// ChangePassword replaces the password of the signed-in user with userID by next once
// current matches it, and closes every other session of the user; the session with
// sessionID it is changed from stays open. It fails with domain.ErrWrongCurrentPassword
// when current does not match and with the domain error of an invalid next.
func (s *Service) ChangePassword(ctx context.Context, userID, sessionID uuid.UUID, current, next string) error {
	if err := domain.ValidatePassword(next); err != nil {
		return err
	}
	_, stored, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	if stored == "" {
		return domain.ErrWrongCurrentPassword
	}
	ok, err := s.hasher.Verify(current, stored)
	if err != nil {
		return fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return domain.ErrWrongCurrentPassword
	}
	// Hashed before the transaction: argon2id is slow on purpose.
	hash, err := s.hasher.Hash(next)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.users.SetPassword(ctx, userID, hash); err != nil {
			return fmt.Errorf("set password: %w", err)
		}
		if err := s.sessions.CloseOthers(ctx, userID, sessionID); err != nil {
			return fmt.Errorf("close other sessions: %w", err)
		}
		return nil
	})
}
