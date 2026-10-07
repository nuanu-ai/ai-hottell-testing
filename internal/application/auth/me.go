package auth

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// Me returns the signed-in user with userID; domain.ErrUserNotFound when there is none.
func (s *Service) Me(ctx context.Context, userID uuid.UUID) (domain.User, error) {
	user, _, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return domain.User{}, fmt.Errorf("get user: %w", err)
	}
	return user, nil
}
