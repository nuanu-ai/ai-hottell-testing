package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// replaceLink issues a link of kind to userID by createdBy in testDB and returns it with
// its token hash.
func replaceLink(t *testing.T, userID uuid.UUID, kind domain.LinkKind, createdBy uuid.UUID) (domain.Link, []byte) {
	t.Helper()

	hash := tokenHash(t)
	expiresAt := time.Now().Add(domain.LinkTTL).Truncate(time.Microsecond)
	link, err := postgres.NewLinks(testDB.Pool()).Replace(t.Context(), userID, kind, hash, createdBy, expiresAt)
	if err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	return link, hash
}

// linkExists reports whether testDB holds the link with id, used or not.
func linkExists(t *testing.T, id uuid.UUID) bool {
	t.Helper()

	var exists bool
	err := testDB.Pool().QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM user_tokens WHERE id = $1)", id).Scan(&exists)
	if err != nil {
		t.Fatalf("select link: %v", err)
	}
	return exists
}

func TestLinks_Replace(t *testing.T) {
	t.Parallel()

	links := postgres.NewLinks(testDB.Pool())
	issuer, user := createActive(t), createInvited(t)
	hash := tokenHash(t)
	expiresAt := time.Now().Add(domain.LinkTTL).Truncate(time.Microsecond)

	got, err := links.Replace(t.Context(), user.ID, domain.LinkKindInvite, hash, issuer.ID, expiresAt)
	if err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	if got.ID == uuid.Nil {
		t.Error("Replace() ID is nil, want a generated id")
	}
	want := domain.Link{ID: got.ID, UserID: user.ID, Kind: domain.LinkKindInvite, ExpiresAt: expiresAt}
	assertLink(t, got, want)

	var createdBy uuid.UUID
	err = testDB.Pool().QueryRow(t.Context(), "SELECT created_by FROM user_tokens WHERE id = $1", got.ID).Scan(&createdBy)
	if err != nil {
		t.Fatalf("select created_by: %v", err)
	}
	if createdBy != issuer.ID {
		t.Errorf("stored created_by = %v, want %v", createdBy, issuer.ID)
	}
}

func TestLinks_Replace_Twice(t *testing.T) {
	t.Parallel()

	links := postgres.NewLinks(testDB.Pool())
	issuer, user := createActive(t), createActive(t)
	oldReset, oldHash := replaceLink(t, user.ID, domain.LinkKindReset, issuer.ID)
	otherKind, otherKindHash := replaceLink(t, user.ID, domain.LinkKindInvite, issuer.ID)

	newReset, newHash := replaceLink(t, user.ID, domain.LinkKindReset, issuer.ID)

	if _, err := links.GetByTokenHash(t.Context(), oldHash); !errors.Is(err, domain.ErrLinkNotFound) {
		t.Errorf("GetByTokenHash(old hash) error = %v, want %v", err, domain.ErrLinkNotFound)
	}
	if linkExists(t, oldReset.ID) {
		t.Error("replaced link exists")
	}
	got, err := links.GetByTokenHash(t.Context(), newHash)
	if err != nil {
		t.Fatalf("GetByTokenHash(new hash) error = %v", err)
	}
	assertLink(t, got, newReset)
	got, err = links.GetByTokenHash(t.Context(), otherKindHash)
	if err != nil {
		t.Fatalf("GetByTokenHash(link of the other kind) error = %v", err)
	}
	assertLink(t, got, otherKind)
}

func TestLinks_Replace_KeepsUsedLink(t *testing.T) {
	t.Parallel()

	links := postgres.NewLinks(testDB.Pool())
	issuer, user := createActive(t), createActive(t)
	used, usedHash := replaceLink(t, user.ID, domain.LinkKindReset, issuer.ID)
	if err := links.MarkUsed(t.Context(), used.ID, time.Now()); err != nil {
		t.Fatalf("MarkUsed() error = %v", err)
	}

	replaceLink(t, user.ID, domain.LinkKindReset, issuer.ID)

	got, err := links.GetByTokenHash(t.Context(), usedHash)
	if err != nil {
		t.Fatalf("GetByTokenHash(used link) error = %v, want the link", err)
	}
	if got.UsedAt == nil {
		t.Error("used link UsedAt = nil, want the time of use")
	}
}

func TestLinks_Replace_Concurrent(t *testing.T) {
	t.Parallel()

	const reissues = 8
	links := postgres.NewLinks(testDB.Pool())
	issuer, user := createActive(t), createInvited(t)

	// Reissues of one link at once, as a double click sends them: each succeeds, the last wins.
	errs := make(chan error, reissues)
	var wg sync.WaitGroup
	for range reissues {
		wg.Go(func() {
			_, err := links.Replace(t.Context(), user.ID, domain.LinkKindInvite, tokenHash(t), issuer.ID, time.Now().Add(time.Hour))
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Replace() error = %v", err)
		}
	}

	var count int
	err := testDB.Pool().QueryRow(t.Context(),
		"SELECT count(*) FROM user_tokens WHERE user_id = $1 AND used_at IS NULL", user.ID).Scan(&count)
	if err != nil {
		t.Fatalf("count links: %v", err)
	}
	if count != 1 {
		t.Errorf("unused links after concurrent Replace() = %d, want 1", count)
	}
}

func TestLinks_Replace_UnknownUser(t *testing.T) {
	t.Parallel()

	issuer := createActive(t)
	_, err := postgres.NewLinks(testDB.Pool()).Replace(
		t.Context(), uuid.New(), domain.LinkKindInvite, tokenHash(t), issuer.ID, time.Now().Add(time.Hour))
	if !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("Replace() error = %v, want %v", err, domain.ErrUserNotFound)
	}
}

func TestLinks_Replace_WithinTx(t *testing.T) {
	t.Parallel()

	links := postgres.NewLinks(testDB.Pool())
	issuer, user := createActive(t), createInvited(t)
	_, oldHash := replaceLink(t, user.ID, domain.LinkKindInvite, issuer.ID)
	errRollback := errors.New("roll back")

	err := postgres.NewTxManager(testDB.Pool()).WithinTx(t.Context(), func(ctx context.Context) error {
		if _, err := links.Replace(ctx, user.ID, domain.LinkKindInvite, tokenHash(t), issuer.ID, time.Now().Add(time.Hour)); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("WithinTx() error = %v, want %v", err, errRollback)
	}
	// The outer transaction rolled back, so the old link was never replaced.
	if _, err := links.GetByTokenHash(t.Context(), oldHash); err != nil {
		t.Errorf("GetByTokenHash(old hash) error = %v, want the link", err)
	}
}

func TestLinks_DeleteUnused(t *testing.T) {
	t.Parallel()

	links := postgres.NewLinks(testDB.Pool())
	issuer, user := createActive(t), createActive(t)
	used, _ := replaceLink(t, user.ID, domain.LinkKindReset, issuer.ID)
	if err := links.MarkUsed(t.Context(), used.ID, time.Now()); err != nil {
		t.Fatalf("MarkUsed() error = %v", err)
	}
	unused, _ := replaceLink(t, user.ID, domain.LinkKindReset, issuer.ID)
	otherKind, _ := replaceLink(t, user.ID, domain.LinkKindInvite, issuer.ID)

	if err := links.DeleteUnused(t.Context(), user.ID, domain.LinkKindReset); err != nil {
		t.Fatalf("DeleteUnused() error = %v", err)
	}
	if linkExists(t, unused.ID) {
		t.Error("unused link exists")
	}
	if !linkExists(t, used.ID) {
		t.Error("used link is gone")
	}
	if !linkExists(t, otherKind.ID) {
		t.Error("link of the other kind is gone")
	}
	// Nothing left to delete is not an error.
	if err := links.DeleteUnused(t.Context(), user.ID, domain.LinkKindReset); err != nil {
		t.Errorf("repeated DeleteUnused() error = %v, want nil", err)
	}
}

func TestLinks_GetByTokenHash(t *testing.T) {
	t.Parallel()

	issuer, user := createActive(t), createInvited(t)
	link, hash := replaceLink(t, user.ID, domain.LinkKindInvite, issuer.ID)

	tests := map[string]struct {
		hash    []byte
		want    domain.Link
		wantErr error
	}{
		"issued link":  {hash: hash, want: link},
		"unknown hash": {hash: tokenHash(t), wantErr: domain.ErrLinkNotFound},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := postgres.NewLinks(testDB.Pool()).GetByTokenHash(t.Context(), tc.hash)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("GetByTokenHash() error = %v, want %v", err, tc.wantErr)
			}
			assertLink(t, got, tc.want)
		})
	}
}

func TestLinks_MarkUsed(t *testing.T) {
	t.Parallel()

	links := postgres.NewLinks(testDB.Pool())
	issuer, user := createActive(t), createInvited(t)
	link, hash := replaceLink(t, user.ID, domain.LinkKindInvite, issuer.ID)
	usedAt := time.Now().Truncate(time.Microsecond)

	if err := links.MarkUsed(t.Context(), link.ID, usedAt); err != nil {
		t.Fatalf("MarkUsed() error = %v", err)
	}
	got, err := links.GetByTokenHash(t.Context(), hash)
	if err != nil {
		t.Fatalf("GetByTokenHash() error = %v", err)
	}
	link.UsedAt = &usedAt
	assertLink(t, got, link)

	// The second acceptance of the same link loses and keeps the first time of use.
	if err := links.MarkUsed(t.Context(), link.ID, usedAt.Add(time.Minute)); !errors.Is(err, domain.ErrLinkUsed) {
		t.Errorf("second MarkUsed() error = %v, want %v", err, domain.ErrLinkUsed)
	}
	got, err = links.GetByTokenHash(t.Context(), hash)
	if err != nil {
		t.Fatalf("GetByTokenHash() error = %v", err)
	}
	assertLink(t, got, link)
}

func TestLinks_MarkUsed_UnknownLink(t *testing.T) {
	t.Parallel()

	err := postgres.NewLinks(testDB.Pool()).MarkUsed(t.Context(), uuid.New(), time.Now())
	if !errors.Is(err, domain.ErrLinkNotFound) {
		t.Fatalf("MarkUsed() error = %v, want %v", err, domain.ErrLinkNotFound)
	}
}

func TestLinks_GetActiveForUser(t *testing.T) {
	t.Parallel()

	links := postgres.NewLinks(testDB.Pool())
	issuer, invited, accepted := createActive(t), createInvited(t), createActive(t)
	invite, _ := replaceLink(t, invited.ID, domain.LinkKindInvite, issuer.ID)
	used, _ := replaceLink(t, accepted.ID, domain.LinkKindInvite, issuer.ID)
	if err := links.MarkUsed(t.Context(), used.ID, time.Now()); err != nil {
		t.Fatalf("MarkUsed() error = %v", err)
	}

	tests := map[string]struct {
		userID  uuid.UUID
		kind    domain.LinkKind
		want    domain.Link
		wantErr error
	}{
		"unused link":        {userID: invited.ID, kind: domain.LinkKindInvite, want: invite},
		"only a used link":   {userID: accepted.ID, kind: domain.LinkKindInvite, wantErr: domain.ErrLinkNotFound},
		"no link of kind":    {userID: invited.ID, kind: domain.LinkKindReset, wantErr: domain.ErrLinkNotFound},
		"user with no links": {userID: issuer.ID, kind: domain.LinkKindInvite, wantErr: domain.ErrLinkNotFound},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := links.GetActiveForUser(t.Context(), tc.userID, tc.kind)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("GetActiveForUser() error = %v, want %v", err, tc.wantErr)
			}
			assertLink(t, got, tc.want)
		})
	}
}

func TestLinks_UserDeleteCascades(t *testing.T) {
	t.Parallel()

	issuer, user := createActive(t), createInvited(t)
	link, _ := replaceLink(t, user.ID, domain.LinkKindInvite, issuer.ID)

	if err := postgres.NewUsers(testDB.Pool()).DeleteInvited(t.Context(), user.ID); err != nil {
		t.Fatalf("DeleteInvited() error = %v", err)
	}
	if linkExists(t, link.ID) {
		t.Error("link of the deleted user exists")
	}
}

func TestLinks_IssuerDeleteKeepsLink(t *testing.T) {
	t.Parallel()

	issuer, user := createInvited(t), createActive(t)
	link, hash := replaceLink(t, user.ID, domain.LinkKindReset, issuer.ID)

	if err := postgres.NewUsers(testDB.Pool()).DeleteInvited(t.Context(), issuer.ID); err != nil {
		t.Fatalf("DeleteInvited() error = %v", err)
	}
	got, err := postgres.NewLinks(testDB.Pool()).GetByTokenHash(t.Context(), hash)
	if err != nil {
		t.Fatalf("GetByTokenHash() error = %v, want the link", err)
	}
	assertLink(t, got, link)
}

// assertLink fails t unless got and want describe the same link.
func assertLink(t *testing.T, got, want domain.Link) {
	t.Helper()

	sameUse := (got.UsedAt == nil) == (want.UsedAt == nil) &&
		(got.UsedAt == nil || got.UsedAt.Equal(*want.UsedAt))
	if got.ID != want.ID || got.UserID != want.UserID || got.Kind != want.Kind ||
		!got.ExpiresAt.Equal(want.ExpiresAt) || !sameUse {
		t.Errorf("link = %+v, want %+v", got, want)
	}
}
