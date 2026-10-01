package postgres_test

import (
	"errors"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestUsers_GetByEmail(t *testing.T) {
	t.Parallel()

	invited, active := createInvited(t), createActive(t)

	tests := map[string]struct {
		email    domain.Email
		want     domain.User
		wantHash string
		wantErr  error
	}{
		"invited user":  {email: invited.Email, want: invited},
		"active user":   {email: active.Email, want: active, wantHash: testHash},
		"unknown email": {email: uniqueEmail(t), wantErr: domain.ErrUserNotFound},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, hash, err := postgres.NewUsers(testDB.Pool()).GetByEmail(t.Context(), tc.email)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("GetByEmail() error = %v, want %v", err, tc.wantErr)
			}
			assertUser(t, got, tc.want)
			if hash != tc.wantHash {
				t.Errorf("GetByEmail() hash = %q, want %q", hash, tc.wantHash)
			}
		})
	}
}
