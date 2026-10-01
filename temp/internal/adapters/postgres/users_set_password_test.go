package postgres_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestUsers_SetPassword(t *testing.T) {
	t.Parallel()

	const newHash = "$argon2id$v=19$m=65536,t=3,p=4$bmV3$bmV3"

	tests := map[string]struct {
		id      func(t *testing.T) uuid.UUID
		wantErr error
	}{
		"invited user accepts": {id: func(t *testing.T) uuid.UUID { t.Helper(); return createInvited(t).ID }},
		"active user changes":  {id: func(t *testing.T) uuid.UUID { t.Helper(); return createActive(t).ID }},
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
			before := updatedAt(t, id)

			err := users.SetPassword(t.Context(), id, newHash)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("SetPassword() error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			user, hash, err := users.GetByID(t.Context(), id)
			if err != nil {
				t.Fatalf("GetByID() error = %v", err)
			}
			if hash != newHash || user.Status() != domain.UserStatusActive {
				t.Errorf("after SetPassword() hash = %q, status = %s, want %q, active", hash, user.Status(), newHash)
			}
			if after := updatedAt(t, id); !after.After(*before) {
				t.Errorf("updated_at = %v, want after %v", after, before)
			}
		})
	}
}
