package postgres_test

import (
	"crypto/rand"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// newPasskey returns a passkey of userID with a credential id no other test uses.
func newPasskey(t *testing.T, userID uuid.UUID, name domain.PasskeyName) domain.Passkey {
	t.Helper()

	credentialID := make([]byte, 16)
	_, _ = rand.Read(credentialID)
	return domain.Passkey{
		UserID:          userID,
		CredentialID:    credentialID,
		PublicKey:       []byte{0xa5, 0x01, 0x02},
		AttestationType: "none",
		AAGUID:          make([]byte, 16),
		SignCount:       7,
		Transports:      []string{"internal", "hybrid"},
		BackupEligible:  true,
		BackupState:     false,
		Name:            name,
	}
}

// createPasskey stores a passkey of userID in testDB and returns it as stored.
func createPasskey(t *testing.T, userID uuid.UUID, name domain.PasskeyName) domain.Passkey {
	t.Helper()

	passkey, err := postgres.NewPasskeys(testDB.Pool()).Create(t.Context(), newPasskey(t, userID, name))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return passkey
}

// passkeyExists reports whether testDB holds the passkey with id.
func passkeyExists(t *testing.T, id uuid.UUID) bool {
	t.Helper()

	var exists bool
	err := testDB.Pool().QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM passkeys WHERE id = $1)", id).Scan(&exists)
	if err != nil {
		t.Fatalf("select passkey: %v", err)
	}
	return exists
}

func TestPasskeys_Create(t *testing.T) {
	t.Parallel()

	user := createActive(t)
	want := newPasskey(t, user.ID, "Ноутбук")
	before := time.Now().Add(-time.Second)

	got, err := postgres.NewPasskeys(testDB.Pool()).Create(t.Context(), want)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if got.ID == uuid.Nil {
		t.Error("Create() ID is nil, want a generated id")
	}
	if got.CreatedAt.Before(before) {
		t.Errorf("Create() CreatedAt = %v, want the time of creation", got.CreatedAt)
	}
	if got.LastUsedAt != nil {
		t.Errorf("Create() LastUsedAt = %v, want nil", got.LastUsedAt)
	}
	want.ID, want.CreatedAt = got.ID, got.CreatedAt
	assertPasskey(t, got, want)
}

func TestPasskeys_Create_UnknownUser(t *testing.T) {
	t.Parallel()

	_, err := postgres.NewPasskeys(testDB.Pool()).Create(t.Context(), newPasskey(t, uuid.New(), "Ноутбук"))
	if !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("Create() error = %v, want %v", err, domain.ErrUserNotFound)
	}
}

func TestPasskeys_ListByUser(t *testing.T) {
	t.Parallel()

	user := createActive(t)
	other := createActive(t)
	first := createPasskey(t, user.ID, "Первый")
	second := createPasskey(t, user.ID, "Второй")
	createPasskey(t, other.ID, "Чужой")
	// Swap the creation times so the order cannot come from insertion alone.
	_, err := testDB.Pool().Exec(t.Context(),
		"UPDATE passkeys SET created_at = created_at - interval '1 hour' WHERE id = $1", second.ID)
	if err != nil {
		t.Fatalf("update created_at: %v", err)
	}

	got, err := postgres.NewPasskeys(testDB.Pool()).ListByUser(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("ListByUser() error = %v", err)
	}

	ids := make([]uuid.UUID, 0, len(got))
	for _, p := range got {
		ids = append(ids, p.ID)
	}
	if want := []uuid.UUID{second.ID, first.ID}; !slices.Equal(ids, want) {
		t.Fatalf("ListByUser() ids = %v, want %v", ids, want)
	}
}

func TestPasskeys_ListByUser_None(t *testing.T) {
	t.Parallel()

	got, err := postgres.NewPasskeys(testDB.Pool()).ListByUser(t.Context(), createActive(t).ID)
	if err != nil {
		t.Fatalf("ListByUser() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListByUser() = %v, want none", got)
	}
}

func TestPasskeys_GetByCredentialID(t *testing.T) {
	t.Parallel()

	want := createPasskey(t, createActive(t).ID, "Ноутбук")

	got, err := postgres.NewPasskeys(testDB.Pool()).GetByCredentialID(t.Context(), want.CredentialID)
	if err != nil {
		t.Fatalf("GetByCredentialID() error = %v", err)
	}
	assertPasskey(t, got, want)
}

func TestPasskeys_GetByCredentialID_Unknown(t *testing.T) {
	t.Parallel()

	_, err := postgres.NewPasskeys(testDB.Pool()).GetByCredentialID(t.Context(), []byte("unknown credential"))
	if !errors.Is(err, domain.ErrPasskeyNotFound) {
		t.Fatalf("GetByCredentialID() error = %v, want %v", err, domain.ErrPasskeyNotFound)
	}
}

func TestPasskeys_RecordUse(t *testing.T) {
	t.Parallel()

	repo := postgres.NewPasskeys(testDB.Pool())
	passkey := createPasskey(t, createActive(t).ID, "Ноутбук")
	at := time.Now().Add(time.Minute).Truncate(time.Microsecond)

	if err := repo.RecordUse(t.Context(), passkey.ID, 42, true, at); err != nil {
		t.Fatalf("RecordUse() error = %v", err)
	}

	got, err := repo.GetByCredentialID(t.Context(), passkey.CredentialID)
	if err != nil {
		t.Fatalf("GetByCredentialID() error = %v", err)
	}
	passkey.SignCount, passkey.BackupState, passkey.LastUsedAt = 42, true, &at
	assertPasskey(t, got, passkey)
}

func TestPasskeys_RecordUse_Unknown(t *testing.T) {
	t.Parallel()

	err := postgres.NewPasskeys(testDB.Pool()).RecordUse(t.Context(), uuid.New(), 1, false, time.Now())
	if !errors.Is(err, domain.ErrPasskeyNotFound) {
		t.Fatalf("RecordUse() error = %v, want %v", err, domain.ErrPasskeyNotFound)
	}
}

func TestPasskeys_Delete(t *testing.T) {
	t.Parallel()

	user := createActive(t)
	passkey := createPasskey(t, user.ID, "Ноутбук")
	kept := createPasskey(t, user.ID, "Телефон")

	if err := postgres.NewPasskeys(testDB.Pool()).Delete(t.Context(), passkey.ID, user.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if passkeyExists(t, passkey.ID) {
		t.Error("deleted passkey exists")
	}
	if !passkeyExists(t, kept.ID) {
		t.Error("other passkey of the user is gone")
	}
}

func TestPasskeys_Delete_Unknown(t *testing.T) {
	t.Parallel()

	err := postgres.NewPasskeys(testDB.Pool()).Delete(t.Context(), uuid.New(), createActive(t).ID)
	if !errors.Is(err, domain.ErrPasskeyNotFound) {
		t.Fatalf("Delete() error = %v, want %v", err, domain.ErrPasskeyNotFound)
	}
}

func TestPasskeys_Delete_OtherUser(t *testing.T) {
	t.Parallel()

	passkey := createPasskey(t, createActive(t).ID, "Ноутбук")

	err := postgres.NewPasskeys(testDB.Pool()).Delete(t.Context(), passkey.ID, createActive(t).ID)
	if !errors.Is(err, domain.ErrPasskeyNotFound) {
		t.Fatalf("Delete() error = %v, want %v", err, domain.ErrPasskeyNotFound)
	}
	if !passkeyExists(t, passkey.ID) {
		t.Error("passkey is gone after another user deleted it")
	}
}

func TestPasskeys_UserDeleteCascades(t *testing.T) {
	t.Parallel()

	user := createInvited(t)
	passkey := createPasskey(t, user.ID, "Ноутбук")

	if err := postgres.NewUsers(testDB.Pool()).DeleteInvited(t.Context(), user.ID); err != nil {
		t.Fatalf("DeleteInvited() error = %v", err)
	}
	if passkeyExists(t, passkey.ID) {
		t.Error("passkey of the deleted user exists")
	}
}

// assertPasskey fails t unless got and want describe the same passkey.
func assertPasskey(t *testing.T, got, want domain.Passkey) {
	t.Helper()

	sameLastUse := (got.LastUsedAt == nil) == (want.LastUsedAt == nil) &&
		(got.LastUsedAt == nil || got.LastUsedAt.Equal(*want.LastUsedAt))
	if got.ID != want.ID || got.UserID != want.UserID || !slices.Equal(got.CredentialID, want.CredentialID) ||
		!slices.Equal(got.PublicKey, want.PublicKey) || got.AttestationType != want.AttestationType ||
		!slices.Equal(got.AAGUID, want.AAGUID) || got.SignCount != want.SignCount ||
		!slices.Equal(got.Transports, want.Transports) || got.BackupEligible != want.BackupEligible ||
		got.BackupState != want.BackupState || got.Name != want.Name || !got.CreatedAt.Equal(want.CreatedAt) ||
		!sameLastUse {
		t.Errorf("passkey = %+v, want %+v", got, want)
	}
}
