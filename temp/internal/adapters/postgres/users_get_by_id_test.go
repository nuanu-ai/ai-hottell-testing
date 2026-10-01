package postgres_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestUsers_GetByID(t *testing.T) {
	t.Parallel()

	invited, active := createInvited(t), createActive(t)

	tests := map[string]struct {
		id       uuid.UUID
		want     domain.User
		wantHash string
		wantErr  error
	}{
		"invited user": {id: invited.ID, want: invited},
		"active user":  {id: active.ID, want: active, wantHash: testHash},
		"unknown id":   {id: uuid.New(), wantErr: domain.ErrUserNotFound},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, hash, err := postgres.NewUsers(testDB.Pool()).GetByID(t.Context(), tc.id)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("GetByID() error = %v, want %v", err, tc.wantErr)
			}
			assertUser(t, got, tc.want)
			if hash != tc.wantHash {
				t.Errorf("GetByID() hash = %q, want %q", hash, tc.wantHash)
			}
		})
	}
}

// assertUser fails t unless got and want describe the same user.
func assertUser(t *testing.T, got, want domain.User) {
	t.Helper()

	if got.ID != want.ID || got.Email != want.Email || got.Name != want.Name ||
		got.HasPassword != want.HasPassword || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("user = %+v, want %+v", got, want)
	}
	switch {
	case got.LastLoginAt == nil && want.LastLoginAt == nil:
	case got.LastLoginAt == nil || want.LastLoginAt == nil || !got.LastLoginAt.Equal(*want.LastLoginAt):
		t.Errorf("user LastLoginAt = %v, want %v", got.LastLoginAt, want.LastLoginAt)
	}
}
