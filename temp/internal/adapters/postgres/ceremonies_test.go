package postgres_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// testSessionData stands in for the WebAuthn session data: the repository stores it as JSON.
const testSessionData = `{"challenge": "c2FtcGxl", "userVerification": "preferred"}`

// saveCeremony stores a ceremony in testDB and returns its id.
func saveCeremony(
	t *testing.T, kind domain.CeremonyKind, userID *uuid.UUID, passkeyName *string, expiresAt time.Time,
) uuid.UUID {
	t.Helper()

	id, err := postgres.NewCeremonies(testDB.Pool()).Save(
		t.Context(), kind, userID, passkeyName, []byte(testSessionData), expiresAt)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	return id
}

// ceremonyExists reports whether testDB holds the ceremony with id, expired or not.
func ceremonyExists(t *testing.T, id uuid.UUID) bool {
	t.Helper()

	var exists bool
	err := testDB.Pool().QueryRow(t.Context(),
		"SELECT EXISTS (SELECT 1 FROM webauthn_ceremonies WHERE id = $1)", id).Scan(&exists)
	if err != nil {
		t.Fatalf("select ceremony: %v", err)
	}
	return exists
}

func TestCeremonies_SaveTake_Register(t *testing.T) {
	t.Parallel()

	user := createActive(t)
	name := "Ноутбук"
	id := saveCeremony(t, domain.CeremonyKindRegister, &user.ID, &name, time.Now().Add(5*time.Minute))

	userID, passkeyName, sessionData, err := postgres.NewCeremonies(testDB.Pool()).Take(
		t.Context(), id, domain.CeremonyKindRegister)
	if err != nil {
		t.Fatalf("Take() error = %v", err)
	}

	if userID == nil || *userID != user.ID {
		t.Errorf("Take() userID = %v, want %v", userID, user.ID)
	}
	if passkeyName == nil || *passkeyName != name {
		t.Errorf("Take() passkeyName = %v, want %q", passkeyName, name)
	}
	assertSameJSON(t, sessionData, []byte(testSessionData))
}

func TestCeremonies_SaveTake_Login(t *testing.T) {
	t.Parallel()

	id := saveCeremony(t, domain.CeremonyKindLogin, nil, nil, time.Now().Add(5*time.Minute))

	userID, passkeyName, sessionData, err := postgres.NewCeremonies(testDB.Pool()).Take(
		t.Context(), id, domain.CeremonyKindLogin)
	if err != nil {
		t.Fatalf("Take() error = %v", err)
	}

	if userID != nil {
		t.Errorf("Take() userID = %v, want nil", *userID)
	}
	if passkeyName != nil {
		t.Errorf("Take() passkeyName = %q, want nil", *passkeyName)
	}
	assertSameJSON(t, sessionData, []byte(testSessionData))
}

func TestCeremonies_Save_UnknownUser(t *testing.T) {
	t.Parallel()

	userID, name := uuid.New(), "Ноутбук"
	_, err := postgres.NewCeremonies(testDB.Pool()).Save(t.Context(), domain.CeremonyKindRegister, &userID, &name,
		[]byte(testSessionData), time.Now().Add(5*time.Minute))
	if !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("Save() error = %v, want %v", err, domain.ErrUserNotFound)
	}
}

func TestCeremonies_Take_Again(t *testing.T) {
	t.Parallel()

	repo := postgres.NewCeremonies(testDB.Pool())
	id := saveCeremony(t, domain.CeremonyKindLogin, nil, nil, time.Now().Add(5*time.Minute))
	if _, _, _, err := repo.Take(t.Context(), id, domain.CeremonyKindLogin); err != nil {
		t.Fatalf("first Take() error = %v", err)
	}

	_, _, _, err := repo.Take(t.Context(), id, domain.CeremonyKindLogin)
	if !errors.Is(err, domain.ErrCeremonyNotFound) {
		t.Fatalf("second Take() error = %v, want %v", err, domain.ErrCeremonyNotFound)
	}
}

func TestCeremonies_Take_NotFound(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		id   uuid.UUID
		kind domain.CeremonyKind
		// kept: the live ceremony stays, since it was not taken. An expired one may be gone
		// already: DeleteExpired runs in parallel.
		kept bool
	}{
		"unknown ceremony": {id: uuid.New(), kind: domain.CeremonyKindLogin},
		"expired ceremony": {
			id:   saveCeremony(t, domain.CeremonyKindLogin, nil, nil, time.Now().Add(-time.Second)),
			kind: domain.CeremonyKindLogin,
		},
		"other kind": {
			id:   saveCeremony(t, domain.CeremonyKindLogin, nil, nil, time.Now().Add(5*time.Minute)),
			kind: domain.CeremonyKindRegister,
			kept: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, _, _, err := postgres.NewCeremonies(testDB.Pool()).Take(t.Context(), tc.id, tc.kind)
			if !errors.Is(err, domain.ErrCeremonyNotFound) {
				t.Fatalf("Take() error = %v, want %v", err, domain.ErrCeremonyNotFound)
			}
			if tc.kept && !ceremonyExists(t, tc.id) {
				t.Error("ceremony is gone, want it kept")
			}
		})
	}
}

func TestCeremonies_DeleteExpired(t *testing.T) {
	t.Parallel()

	expired1 := saveCeremony(t, domain.CeremonyKindLogin, nil, nil, time.Now().Add(-time.Hour))
	expired2 := saveCeremony(t, domain.CeremonyKindLogin, nil, nil, time.Now().Add(-time.Second))
	live := saveCeremony(t, domain.CeremonyKindLogin, nil, nil, time.Now().Add(time.Hour))

	n, err := postgres.NewCeremonies(testDB.Pool()).DeleteExpired(t.Context())
	if err != nil {
		t.Fatalf("DeleteExpired() error = %v", err)
	}
	// Tests running in parallel may add expired ceremonies of their own.
	if n < 2 {
		t.Errorf("DeleteExpired() = %d, want at least 2", n)
	}
	for _, id := range []uuid.UUID{expired1, expired2} {
		if ceremonyExists(t, id) {
			t.Errorf("expired ceremony %v exists", id)
		}
	}
	if !ceremonyExists(t, live) {
		t.Error("live ceremony is gone")
	}
}

func TestCeremonies_UserDeleteCascades(t *testing.T) {
	t.Parallel()

	user := createInvited(t)
	name := "Ноутбук"
	id := saveCeremony(t, domain.CeremonyKindRegister, &user.ID, &name, time.Now().Add(5*time.Minute))

	if err := postgres.NewUsers(testDB.Pool()).DeleteInvited(t.Context(), user.ID); err != nil {
		t.Fatalf("DeleteInvited() error = %v", err)
	}
	if ceremonyExists(t, id) {
		t.Error("ceremony of the deleted user exists")
	}
}

// assertSameJSON fails t unless got and want are the same JSON value: jsonb keeps neither
// the spacing nor the key order.
func assertSameJSON(t *testing.T, got, want []byte) {
	t.Helper()

	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("session data %q is not JSON: %v", got, err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("want %q is not JSON: %v", want, err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("session data = %s, want %s", got, want)
	}
}
