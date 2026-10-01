package postgres_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestUsers_TouchLogin(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.September, 30, 12, 0, 0, 0, time.UTC)

	tests := map[string]struct {
		id      func(t *testing.T) uuid.UUID
		wantErr error
	}{
		"active user": {id: func(t *testing.T) uuid.UUID { t.Helper(); return createActive(t).ID }},
		"unknown id": {
			id:      func(*testing.T) uuid.UUID { return uuid.New() },
			wantErr: domain.ErrUserNotFound,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			users := postgres.NewUsers(testDB.Pool())
			id := tc.id(t)

			err := users.TouchLogin(t.Context(), id, at)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("TouchLogin() error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			user, _, err := users.GetByID(t.Context(), id)
			if err != nil {
				t.Fatalf("GetByID() error = %v", err)
			}
			if user.LastLoginAt == nil || !user.LastLoginAt.Equal(at) {
				t.Errorf("after TouchLogin() LastLoginAt = %v, want %v", user.LastLoginAt, at)
			}
		})
	}
}
