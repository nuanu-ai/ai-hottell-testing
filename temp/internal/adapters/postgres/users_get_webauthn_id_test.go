package postgres_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestUsers_GetWebAuthnID(t *testing.T) {
	t.Parallel()

	user := createActive(t)

	tests := map[string]struct {
		id      uuid.UUID
		want    []byte
		wantErr error
	}{
		"known user": {id: user.ID, want: webAuthnUserID(t, user.ID)},
		"unknown id": {id: uuid.New(), wantErr: domain.ErrUserNotFound},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := postgres.NewUsers(testDB.Pool()).GetWebAuthnID(t.Context(), tc.id)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("GetWebAuthnID() error = %v, want %v", err, tc.wantErr)
			}
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("GetWebAuthnID() = %x, want %x", got, tc.want)
			}
		})
	}
}
