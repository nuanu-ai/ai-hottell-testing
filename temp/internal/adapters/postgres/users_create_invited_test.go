package postgres_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestUsers_CreateInvited(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		existing func(t *testing.T, email domain.Email)
		wantErr  error
	}{
		"new email": {existing: func(*testing.T, domain.Email) {}},
		"email of an invited user": {
			existing: func(t *testing.T, email domain.Email) {
				t.Helper()
				if _, err := postgres.NewUsers(testDB.Pool()).CreateInvited(t.Context(), email, "Первый"); err != nil {
					t.Fatalf("CreateInvited() error = %v", err)
				}
			},
			wantErr: domain.ErrEmailTaken,
		},
		"email of an active user": {
			existing: func(t *testing.T, email domain.Email) {
				t.Helper()
				if _, err := postgres.NewUsers(testDB.Pool()).CreateActive(t.Context(), email, "Первый", testHash); err != nil {
					t.Fatalf("CreateActive() error = %v", err)
				}
			},
			wantErr: domain.ErrEmailTaken,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			email := uniqueEmail(t)
			tc.existing(t, email)

			user, err := postgres.NewUsers(testDB.Pool()).CreateInvited(t.Context(), email, "Второй")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("CreateInvited() error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			if user.ID == uuid.Nil || user.Email != email || user.Name != "Второй" {
				t.Errorf("CreateInvited() = %+v, want a new user %s named Второй", user, email)
			}
			if user.HasPassword || user.Status() != domain.UserStatusInvited {
				t.Errorf("CreateInvited() status = %s, want %s", user.Status(), domain.UserStatusInvited)
			}
			if user.CreatedAt.IsZero() || user.LastLoginAt != nil {
				t.Errorf("CreateInvited() CreatedAt = %v, LastLoginAt = %v, want set and nil", user.CreatedAt, user.LastLoginAt)
			}
			if handle := webAuthnUserID(t, user.ID); len(handle) != 64 || bytes.Equal(handle, make([]byte, 64)) {
				t.Errorf("webauthn_user_id = %x, want 64 random bytes", handle)
			}
		})
	}
}

func TestUsers_CreateInvitedDistinctWebAuthnIDs(t *testing.T) {
	t.Parallel()

	first, second := createInvited(t), createInvited(t)

	if bytes.Equal(webAuthnUserID(t, first.ID), webAuthnUserID(t, second.ID)) {
		t.Fatal("two users got the same webauthn_user_id")
	}
}
