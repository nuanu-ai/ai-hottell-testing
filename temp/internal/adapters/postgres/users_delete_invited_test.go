package postgres_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestUsers_DeleteInvited(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		id       func(t *testing.T) uuid.UUID
		wantErr  error
		wantGone bool
	}{
		"invited user": {
			id:       func(t *testing.T) uuid.UUID { t.Helper(); return createInvited(t).ID },
			wantGone: true,
		},
		"active user": {
			id:      func(t *testing.T) uuid.UUID { t.Helper(); return createActive(t).ID },
			wantErr: domain.ErrUserNotInvited,
		},
		"unknown id": {
			id:       func(*testing.T) uuid.UUID { return uuid.New() },
			wantErr:  domain.ErrUserNotFound,
			wantGone: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			users := postgres.NewUsers(testDB.Pool())
			id := tc.id(t)

			err := users.DeleteInvited(t.Context(), id)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("DeleteInvited() error = %v, want %v", err, tc.wantErr)
			}
			_, _, err = users.GetByID(t.Context(), id)
			if gone := errors.Is(err, domain.ErrUserNotFound); gone != tc.wantGone {
				t.Errorf("after DeleteInvited() user gone = %t, want %t", gone, tc.wantGone)
			}
		})
	}
}
