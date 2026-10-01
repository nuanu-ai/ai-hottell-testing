package postgres_test

import (
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// testUserAgent stands in for the User-Agent of a browser.
const testUserAgent = "test-agent/1.0"

// tokenHash returns a hash no other test uses; the repositories store it as opaque bytes.
func tokenHash(t *testing.T) []byte {
	t.Helper()

	hash := make([]byte, 32)
	_, _ = rand.Read(hash)
	return hash
}

// createSession adds a session of userID expiring at expiresAt to testDB and returns it
// with its token hash.
func createSession(t *testing.T, userID uuid.UUID, expiresAt time.Time) (domain.Session, []byte) {
	t.Helper()

	hash := tokenHash(t)
	session, err := postgres.NewSessions(testDB.Pool()).Create(t.Context(), userID, hash, expiresAt, testUserAgent)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return session, hash
}

// sessionExists reports whether testDB holds the session with id, expired or not.
func sessionExists(t *testing.T, id uuid.UUID) bool {
	t.Helper()

	var exists bool
	err := testDB.Pool().QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM sessions WHERE id = $1)", id).Scan(&exists)
	if err != nil {
		t.Fatalf("select session: %v", err)
	}
	return exists
}

func TestSessions_Create(t *testing.T) {
	t.Parallel()

	user := createActive(t)
	expiresAt := time.Now().Add(domain.SessionTTL).Truncate(time.Microsecond)
	before := time.Now().Add(-time.Second)

	got, hash := createSession(t, user.ID, expiresAt)

	if got.ID == uuid.Nil {
		t.Error("Create() ID is nil, want a generated id")
	}
	if got.UserID != user.ID {
		t.Errorf("Create() UserID = %v, want %v", got.UserID, user.ID)
	}
	if !got.ExpiresAt.Equal(expiresAt) {
		t.Errorf("Create() ExpiresAt = %v, want %v", got.ExpiresAt, expiresAt)
	}
	if got.LastSeenAt.Before(before) {
		t.Errorf("Create() LastSeenAt = %v, want the time of creation", got.LastSeenAt)
	}

	var userAgent string
	err := testDB.Pool().QueryRow(t.Context(), "SELECT user_agent FROM sessions WHERE token_hash = $1", hash).Scan(&userAgent)
	if err != nil {
		t.Fatalf("select user_agent: %v", err)
	}
	if userAgent != testUserAgent {
		t.Errorf("stored user_agent = %q, want %q", userAgent, testUserAgent)
	}
}

func TestSessions_Create_UnknownUser(t *testing.T) {
	t.Parallel()

	_, err := postgres.NewSessions(testDB.Pool()).Create(t.Context(), uuid.New(), tokenHash(t), time.Now().Add(time.Hour), testUserAgent)
	if !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("Create() error = %v, want %v", err, domain.ErrUserNotFound)
	}
}

func TestSessions_GetByTokenHash(t *testing.T) {
	t.Parallel()

	user := createActive(t)
	live, liveHash := createSession(t, user.ID, time.Now().Add(time.Hour))
	_, expiredHash := createSession(t, user.ID, time.Now().Add(-time.Second))

	tests := map[string]struct {
		hash    []byte
		want    domain.Session
		wantErr error
	}{
		"live session":    {hash: liveHash, want: live},
		"expired session": {hash: expiredHash, wantErr: domain.ErrSessionNotFound},
		"unknown hash":    {hash: tokenHash(t), wantErr: domain.ErrSessionNotFound},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := postgres.NewSessions(testDB.Pool()).GetByTokenHash(t.Context(), tc.hash)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("GetByTokenHash() error = %v, want %v", err, tc.wantErr)
			}
			assertSession(t, got, tc.want)
		})
	}
}

func TestSessions_Extend(t *testing.T) {
	t.Parallel()

	sessions := postgres.NewSessions(testDB.Pool())
	session, hash := createSession(t, createActive(t).ID, time.Now().Add(time.Hour))
	expiresAt := time.Now().Add(domain.SessionTTL).Truncate(time.Microsecond)
	lastSeenAt := time.Now().Add(time.Minute).Truncate(time.Microsecond)

	if err := sessions.Extend(t.Context(), session.ID, expiresAt, lastSeenAt); err != nil {
		t.Fatalf("Extend() error = %v", err)
	}

	got, err := sessions.GetByTokenHash(t.Context(), hash)
	if err != nil {
		t.Fatalf("GetByTokenHash() error = %v", err)
	}
	want := domain.Session{ID: session.ID, UserID: session.UserID, ExpiresAt: expiresAt, LastSeenAt: lastSeenAt}
	assertSession(t, got, want)
}

func TestSessions_Extend_UnknownSession(t *testing.T) {
	t.Parallel()

	err := postgres.NewSessions(testDB.Pool()).Extend(t.Context(), uuid.New(), time.Now().Add(time.Hour), time.Now())
	if !errors.Is(err, domain.ErrSessionNotFound) {
		t.Fatalf("Extend() error = %v, want %v", err, domain.ErrSessionNotFound)
	}
}

func TestSessions_Delete(t *testing.T) {
	t.Parallel()

	sessions := postgres.NewSessions(testDB.Pool())
	user := createActive(t)
	deleted, _ := createSession(t, user.ID, time.Now().Add(time.Hour))
	kept, _ := createSession(t, user.ID, time.Now().Add(time.Hour))

	if err := sessions.Delete(t.Context(), deleted.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if sessionExists(t, deleted.ID) {
		t.Error("deleted session exists")
	}
	if !sessionExists(t, kept.ID) {
		t.Error("other session of the user is gone")
	}
	// Deleting a session twice, as a repeated sign-out does, is not an error.
	if err := sessions.Delete(t.Context(), deleted.ID); err != nil {
		t.Errorf("repeated Delete() error = %v, want nil", err)
	}
}

func TestSessions_DeleteAllForUser(t *testing.T) {
	t.Parallel()

	user, other := createActive(t), createActive(t)
	first, _ := createSession(t, user.ID, time.Now().Add(time.Hour))
	second, _ := createSession(t, user.ID, time.Now().Add(time.Hour))
	others, _ := createSession(t, other.ID, time.Now().Add(time.Hour))

	if err := postgres.NewSessions(testDB.Pool()).DeleteAllForUser(t.Context(), user.ID); err != nil {
		t.Fatalf("DeleteAllForUser() error = %v", err)
	}
	for _, id := range []uuid.UUID{first.ID, second.ID} {
		if sessionExists(t, id) {
			t.Errorf("session %v of the user exists", id)
		}
	}
	if !sessionExists(t, others.ID) {
		t.Error("session of another user is gone")
	}
}

func TestSessions_DeleteAllForUserExcept(t *testing.T) {
	t.Parallel()

	user, other := createActive(t), createActive(t)
	keep, _ := createSession(t, user.ID, time.Now().Add(time.Hour))
	drop, _ := createSession(t, user.ID, time.Now().Add(time.Hour))
	others, _ := createSession(t, other.ID, time.Now().Add(time.Hour))

	err := postgres.NewSessions(testDB.Pool()).DeleteAllForUserExcept(t.Context(), user.ID, keep.ID)
	if err != nil {
		t.Fatalf("DeleteAllForUserExcept() error = %v", err)
	}
	if !sessionExists(t, keep.ID) {
		t.Error("kept session is gone")
	}
	if sessionExists(t, drop.ID) {
		t.Error("other session of the user exists")
	}
	if !sessionExists(t, others.ID) {
		t.Error("session of another user is gone")
	}
}

func TestSessions_DeleteExpired(t *testing.T) {
	t.Parallel()

	user := createActive(t)
	expired1, _ := createSession(t, user.ID, time.Now().Add(-time.Hour))
	expired2, _ := createSession(t, user.ID, time.Now().Add(-time.Second))
	live, _ := createSession(t, user.ID, time.Now().Add(time.Hour))

	n, err := postgres.NewSessions(testDB.Pool()).DeleteExpired(t.Context())
	if err != nil {
		t.Fatalf("DeleteExpired() error = %v", err)
	}
	// Tests running in parallel may add expired sessions of their own.
	if n < 2 {
		t.Errorf("DeleteExpired() = %d, want at least 2", n)
	}
	for _, id := range []uuid.UUID{expired1.ID, expired2.ID} {
		if sessionExists(t, id) {
			t.Errorf("expired session %v exists", id)
		}
	}
	if !sessionExists(t, live.ID) {
		t.Error("live session is gone")
	}
}

func TestSessions_UserDeleteCascades(t *testing.T) {
	t.Parallel()

	user := createInvited(t)
	session, _ := createSession(t, user.ID, time.Now().Add(time.Hour))

	if err := postgres.NewUsers(testDB.Pool()).DeleteInvited(t.Context(), user.ID); err != nil {
		t.Fatalf("DeleteInvited() error = %v", err)
	}
	if sessionExists(t, session.ID) {
		t.Error("session of the deleted user exists")
	}
}

// assertSession fails t unless got and want describe the same session.
func assertSession(t *testing.T, got, want domain.Session) {
	t.Helper()

	if got.ID != want.ID || got.UserID != want.UserID ||
		!got.ExpiresAt.Equal(want.ExpiresAt) || !got.LastSeenAt.Equal(want.LastSeenAt) {
		t.Errorf("session = %+v, want %+v", got, want)
	}
}
