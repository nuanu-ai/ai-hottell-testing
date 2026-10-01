package postgres_test

import (
	"errors"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestUsers_GetByWebAuthnID(t *testing.T) {
	t.Parallel()

	user := createActive(t)

	tests := map[string]struct {
		handle  []byte
		want    domain.User
		wantErr error
	}{
		"known handle":   {handle: webAuthnUserID(t, user.ID), want: user},
		"unknown handle": {handle: make([]byte, 64), wantErr: domain.ErrUserNotFound},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := postgres.NewUsers(testDB.Pool()).GetByWebAuthnID(t.Context(), tc.handle)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("GetByWebAuthnID() error = %v, want %v", err, tc.wantErr)
			}
			assertUser(t, got, tc.want)
		})
	}
}
