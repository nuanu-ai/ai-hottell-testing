package postgres_test

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// testHash stands in for a password hash: the repository stores it as opaque text.
const testHash = "$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA"

// uniqueEmail returns an address no other test uses, so tests share testDB in parallel.
func uniqueEmail(t *testing.T) domain.Email {
	t.Helper()

	return domain.Email(uuid.NewString() + "@example.com")
}

// createInvited adds an invited user to testDB.
func createInvited(t *testing.T) domain.User {
	t.Helper()

	user, err := postgres.NewUsers(testDB.Pool()).CreateInvited(t.Context(), uniqueEmail(t), "Приглашённый")
	if err != nil {
		t.Fatalf("CreateInvited() error = %v", err)
	}
	return user
}

// createActive adds a user with testHash as the password hash to testDB.
func createActive(t *testing.T) domain.User {
	t.Helper()

	user, err := postgres.NewUsers(testDB.Pool()).CreateActive(t.Context(), uniqueEmail(t), "Активный", testHash)
	if err != nil {
		t.Fatalf("CreateActive() error = %v", err)
	}
	return user
}

// webAuthnUserID reads the WebAuthn user handle of the user with id from testDB.
func webAuthnUserID(t *testing.T, id uuid.UUID) []byte {
	t.Helper()

	var handle []byte
	err := testDB.Pool().QueryRow(t.Context(), "SELECT webauthn_user_id FROM users WHERE id = $1", id).Scan(&handle)
	if err != nil {
		t.Fatalf("select webauthn_user_id: %v", err)
	}
	return handle
}

// updatedAt reads updated_at of the user with id from testDB; nil when there is no such user.
func updatedAt(t *testing.T, id uuid.UUID) *time.Time {
	t.Helper()

	var at time.Time
	err := testDB.Pool().QueryRow(t.Context(), "SELECT updated_at FROM users WHERE id = $1", id).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		t.Fatalf("select updated_at: %v", err)
	}
	return &at
}
