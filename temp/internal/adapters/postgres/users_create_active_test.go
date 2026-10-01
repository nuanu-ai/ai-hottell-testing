package postgres_test

import (
	"errors"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestUsers_CreateActive(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		taken   bool
		wantErr error
	}{
		"new email":   {},
		"taken email": {taken: true, wantErr: domain.ErrEmailTaken},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			users := postgres.NewUsers(testDB.Pool())
			email := uniqueEmail(t)
			if tc.taken {
				if _, err := users.CreateInvited(t.Context(), email, "Первый"); err != nil {
					t.Fatalf("CreateInvited() error = %v", err)
				}
			}

			user, err := users.CreateActive(t.Context(), email, "Второй", testHash)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("CreateActive() error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			if user.Email != email || user.Status() != domain.UserStatusActive {
				t.Errorf("CreateActive() = %+v, want an active user %s", user, email)
			}
			if handle := webAuthnUserID(t, user.ID); len(handle) != 64 {
				t.Errorf("webauthn_user_id has %d bytes, want 64", len(handle))
			}
			_, hash, err := users.GetByID(t.Context(), user.ID)
			if err != nil || hash != testHash {
				t.Errorf("GetByID() hash = %q, err = %v, want the stored hash", hash, err)
			}
		})
	}
}
