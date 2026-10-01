package postgres_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// testIngestPlaintext stands in for the open value of a collector token.
const testIngestPlaintext = "ht_ingest_synthetic_value"

// replaceAccessKey issues a key of kind to userID in testDB, with testIngestPlaintext for an
// ingest key, and returns it with its hash.
func replaceAccessKey(t *testing.T, userID uuid.UUID, kind domain.AccessKeyKind) (domain.AccessKey, []byte) {
	t.Helper()

	plaintext := ""
	if kind == domain.AccessKeyKindIngest {
		plaintext = testIngestPlaintext
	}
	hash := tokenHash(t)
	key, err := postgres.NewAccessKeys(testDB.Pool()).Replace(t.Context(), userID, kind, hash, plaintext)
	if err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	return key, hash
}

// accessKeyExists reports whether testDB holds the key with id, revoked or not.
func accessKeyExists(t *testing.T, id uuid.UUID) bool {
	t.Helper()

	var exists bool
	err := testDB.Pool().QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM access_keys WHERE id = $1)", id).Scan(&exists)
	if err != nil {
		t.Fatalf("select access key: %v", err)
	}
	return exists
}

// revokedAt reads revoked_at of the key with id from testDB.
func revokedAt(t *testing.T, id uuid.UUID) *time.Time {
	t.Helper()

	var at *time.Time
	if err := testDB.Pool().QueryRow(t.Context(), "SELECT revoked_at FROM access_keys WHERE id = $1", id).Scan(&at); err != nil {
		t.Fatalf("select revoked_at: %v", err)
	}
	return at
}

func TestAccessKeys_Replace(t *testing.T) {
	t.Parallel()

	user := createActive(t)
	before := time.Now().Add(-time.Minute)

	for _, kind := range []domain.AccessKeyKind{domain.AccessKeyKindMCP, domain.AccessKeyKindIngest} {
		got, hash := replaceAccessKey(t, user.ID, kind)

		if got.ID == uuid.Nil {
			t.Errorf("Replace(%s) ID is nil, want a generated id", kind)
		}
		if got.UserID != user.ID || got.Kind != kind || got.LastUsedAt != nil || got.RevokedAt != nil ||
			got.CreatedAt.Before(before) {
			t.Errorf("Replace(%s) = %+v, want an active unused key of the user", kind, got)
		}
		found, err := postgres.NewAccessKeys(testDB.Pool()).FindByHash(t.Context(), hash)
		if err != nil {
			t.Fatalf("FindByHash(%s) error = %v", kind, err)
		}
		assertAccessKey(t, found, got)
	}
}

func TestAccessKeys_Replace_RevokesPrevious(t *testing.T) {
	t.Parallel()

	keys := postgres.NewAccessKeys(testDB.Pool())
	user := createActive(t)
	old, _ := replaceAccessKey(t, user.ID, domain.AccessKeyKindMCP)

	fresh, _ := replaceAccessKey(t, user.ID, domain.AccessKeyKindMCP)

	if revokedAt(t, old.ID) == nil {
		t.Error("replaced key revoked_at = NULL, want the time of replacement")
	}
	got, err := keys.Active(t.Context(), user.ID, domain.AccessKeyKindMCP)
	if err != nil {
		t.Fatalf("Active() error = %v", err)
	}
	assertAccessKey(t, got, fresh)
}

func TestAccessKeys_Replace_KindsAreIndependent(t *testing.T) {
	t.Parallel()

	keys := postgres.NewAccessKeys(testDB.Pool())
	user := createActive(t)
	mcp, mcpHash := replaceAccessKey(t, user.ID, domain.AccessKeyKindMCP)
	ingest, _ := replaceAccessKey(t, user.ID, domain.AccessKeyKindIngest)

	newIngest, _ := replaceAccessKey(t, user.ID, domain.AccessKeyKindIngest)

	if revokedAt(t, ingest.ID) == nil {
		t.Error("replaced ingest key revoked_at = NULL, want the time of replacement")
	}
	got, err := keys.FindByHash(t.Context(), mcpHash)
	if err != nil {
		t.Fatalf("FindByHash(mcp key) error = %v, want the key kept by an ingest replacement", err)
	}
	assertAccessKey(t, got, mcp)

	newMCP, _ := replaceAccessKey(t, user.ID, domain.AccessKeyKindMCP)

	got, err = keys.Active(t.Context(), user.ID, domain.AccessKeyKindIngest)
	if err != nil {
		t.Fatalf("Active(ingest) error = %v, want the key kept by an mcp replacement", err)
	}
	assertAccessKey(t, got, newIngest)
	got, err = keys.Active(t.Context(), user.ID, domain.AccessKeyKindMCP)
	if err != nil {
		t.Fatalf("Active(mcp) error = %v", err)
	}
	assertAccessKey(t, got, newMCP)
}

func TestAccessKeys_Replace_UnknownUser(t *testing.T) {
	t.Parallel()

	_, err := postgres.NewAccessKeys(testDB.Pool()).
		Replace(t.Context(), uuid.New(), domain.AccessKeyKindMCP, tokenHash(t), "")
	if !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("Replace() error = %v, want %v", err, domain.ErrUserNotFound)
	}
}

func TestAccessKeys_Replace_Concurrent(t *testing.T) {
	t.Parallel()

	keys := postgres.NewAccessKeys(testDB.Pool())
	user := createActive(t)

	const n = 8
	errs := make(chan error, n)
	for range n {
		go func() {
			_, err := keys.Replace(t.Context(), user.ID, domain.AccessKeyKindIngest, tokenHash(t), testIngestPlaintext)
			errs <- err
		}()
	}
	for range n {
		if err := <-errs; err != nil {
			t.Errorf("concurrent Replace() error = %v", err)
		}
	}

	var active int
	err := testDB.Pool().QueryRow(t.Context(),
		"SELECT count(*) FROM access_keys WHERE user_id = $1 AND revoked_at IS NULL", user.ID).Scan(&active)
	if err != nil {
		t.Fatalf("count active keys: %v", err)
	}
	if active != 1 {
		t.Errorf("active keys = %d, want 1", active)
	}
}

func TestAccessKeys_Active(t *testing.T) {
	t.Parallel()

	keys := postgres.NewAccessKeys(testDB.Pool())
	user := createActive(t)
	replaceAccessKey(t, user.ID, domain.AccessKeyKindMCP)
	replaceAccessKey(t, user.ID, domain.AccessKeyKindIngest)

	tests := map[string]struct {
		kind          domain.AccessKeyKind
		wantPlaintext string
	}{
		"mcp key has no open value":     {kind: domain.AccessKeyKindMCP},
		"ingest key has its open value": {kind: domain.AccessKeyKindIngest, wantPlaintext: testIngestPlaintext},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := keys.Active(t.Context(), user.ID, tc.kind)
			if err != nil {
				t.Fatalf("Active() error = %v", err)
			}
			if got.Kind != tc.kind || got.Plaintext != tc.wantPlaintext {
				t.Errorf("Active() = {Kind: %s, Plaintext: %q}, want {Kind: %s, Plaintext: %q}",
					got.Kind, got.Plaintext, tc.kind, tc.wantPlaintext)
			}
		})
	}
}

func TestAccessKeys_Active_None(t *testing.T) {
	t.Parallel()

	keys := postgres.NewAccessKeys(testDB.Pool())
	user := createActive(t)
	replaceAccessKey(t, user.ID, domain.AccessKeyKindMCP)
	if err := keys.Revoke(t.Context(), user.ID, domain.AccessKeyKindMCP); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}

	for _, kind := range []domain.AccessKeyKind{domain.AccessKeyKindMCP, domain.AccessKeyKindIngest} {
		if _, err := keys.Active(t.Context(), user.ID, kind); !errors.Is(err, domain.ErrAccessKeyNotFound) {
			t.Errorf("Active(%s) error = %v, want %v", kind, err, domain.ErrAccessKeyNotFound)
		}
	}
}

func TestAccessKeys_Revoke(t *testing.T) {
	t.Parallel()

	keys := postgres.NewAccessKeys(testDB.Pool())
	user := createActive(t)
	mcp, _ := replaceAccessKey(t, user.ID, domain.AccessKeyKindMCP)
	ingest, _ := replaceAccessKey(t, user.ID, domain.AccessKeyKindIngest)

	if err := keys.Revoke(t.Context(), user.ID, domain.AccessKeyKindMCP); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}

	if revokedAt(t, mcp.ID) == nil {
		t.Error("revoked key revoked_at = NULL, want the time of revocation")
	}
	if revokedAt(t, ingest.ID) != nil {
		t.Error("key of the other kind is revoked")
	}
	err := keys.Revoke(t.Context(), user.ID, domain.AccessKeyKindMCP)
	if !errors.Is(err, domain.ErrAccessKeyNotFound) {
		t.Errorf("Revoke() again error = %v, want %v", err, domain.ErrAccessKeyNotFound)
	}
}

func TestAccessKeys_FindByHash_Revoked(t *testing.T) {
	t.Parallel()

	keys := postgres.NewAccessKeys(testDB.Pool())
	user := createActive(t)
	_, replacedHash := replaceAccessKey(t, user.ID, domain.AccessKeyKindIngest)
	replaceAccessKey(t, user.ID, domain.AccessKeyKindIngest)
	_, revokedHash := replaceAccessKey(t, user.ID, domain.AccessKeyKindMCP)
	if err := keys.Revoke(t.Context(), user.ID, domain.AccessKeyKindMCP); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}

	for name, hash := range map[string][]byte{"replaced": replacedHash, "revoked": revokedHash} {
		got, err := keys.FindByHash(t.Context(), hash)
		if !errors.Is(err, domain.ErrAccessKeyRevoked) {
			t.Errorf("FindByHash(%s key) error = %v, want %v", name, err, domain.ErrAccessKeyRevoked)
		}
		if got.ID != uuid.Nil {
			t.Errorf("FindByHash(%s key) = %+v, want no key", name, got)
		}
	}
}

func TestAccessKeys_FindByHash_Unknown(t *testing.T) {
	t.Parallel()

	_, err := postgres.NewAccessKeys(testDB.Pool()).FindByHash(t.Context(), tokenHash(t))
	if !errors.Is(err, domain.ErrAccessKeyNotFound) {
		t.Fatalf("FindByHash() error = %v, want %v", err, domain.ErrAccessKeyNotFound)
	}
}

func TestAccessKeys_Touch(t *testing.T) {
	t.Parallel()

	keys := postgres.NewAccessKeys(testDB.Pool())
	user := createActive(t)
	key, hash := replaceAccessKey(t, user.ID, domain.AccessKeyKindMCP)
	at := time.Now().Truncate(time.Microsecond)

	if err := keys.Touch(t.Context(), key.ID, at); err != nil {
		t.Fatalf("Touch() error = %v", err)
	}

	got, err := keys.FindByHash(t.Context(), hash)
	if err != nil {
		t.Fatalf("FindByHash() error = %v", err)
	}
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(at) {
		t.Errorf("LastUsedAt = %v, want %v", got.LastUsedAt, at)
	}
}

func TestAccessKeys_Touch_Errors(t *testing.T) {
	t.Parallel()

	keys := postgres.NewAccessKeys(testDB.Pool())
	user := createActive(t)
	revoked, _ := replaceAccessKey(t, user.ID, domain.AccessKeyKindMCP)
	if err := keys.Revoke(t.Context(), user.ID, domain.AccessKeyKindMCP); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}

	tests := map[string]struct {
		id      uuid.UUID
		wantErr error
	}{
		"revoked key": {id: revoked.ID, wantErr: domain.ErrAccessKeyRevoked},
		"unknown key": {id: uuid.New(), wantErr: domain.ErrAccessKeyNotFound},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := keys.Touch(t.Context(), tc.id, time.Now()); !errors.Is(err, tc.wantErr) {
				t.Fatalf("Touch() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestAccessKeys_UserDeleteCascades(t *testing.T) {
	t.Parallel()

	user := createInvited(t)
	mcp, _ := replaceAccessKey(t, user.ID, domain.AccessKeyKindMCP)
	ingest, _ := replaceAccessKey(t, user.ID, domain.AccessKeyKindIngest)

	if err := postgres.NewUsers(testDB.Pool()).DeleteInvited(t.Context(), user.ID); err != nil {
		t.Fatalf("DeleteInvited() error = %v", err)
	}
	for _, id := range []uuid.UUID{mcp.ID, ingest.ID} {
		if accessKeyExists(t, id) {
			t.Errorf("access key %v of the deleted user exists", id)
		}
	}
}

// assertAccessKey fails t unless got and want describe the same key.
func assertAccessKey(t *testing.T, got, want domain.AccessKey) {
	t.Helper()

	sameTime := func(a, b *time.Time) bool { return (a == nil) == (b == nil) && (a == nil || a.Equal(*b)) }
	if got.ID != want.ID || got.UserID != want.UserID || got.Kind != want.Kind || got.Plaintext != want.Plaintext ||
		!got.CreatedAt.Equal(want.CreatedAt) || !sameTime(got.LastUsedAt, want.LastUsedAt) ||
		!sameTime(got.RevokedAt, want.RevokedAt) {
		t.Errorf("access key = %+v, want %+v", got, want)
	}
}
