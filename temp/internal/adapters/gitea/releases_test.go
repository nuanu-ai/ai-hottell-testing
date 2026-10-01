package gitea_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/gitea"
)

const (
	repo  = "alva/harness-telemetry"
	token = "test-token"
)

// fakeGitea serves the release list and the release files of repo, counting the list requests.
type fakeGitea struct {
	lists    atomic.Int32
	mu       sync.Mutex
	releases string
	status   int
}

func (f *fakeGitea) set(status int, releases string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.releases = status, releases
}

func newFakeGitea(t *testing.T, releases string) (*fakeGitea, *httptest.Server) {
	t.Helper()
	f := &fakeGitea{status: http.StatusOK, releases: releases}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/repos/"+repo+"/releases", func(w http.ResponseWriter, r *http.Request) {
		f.lists.Add(1)
		if r.Header.Get("Authorization") != "token "+token {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		w.WriteHeader(f.status)
		_, _ = io.WriteString(w, f.releases)
	})
	mux.HandleFunc("GET /"+repo+"/releases/download/{tag}/{name}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token "+token {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, r.PathValue("tag")+"/"+r.PathValue("name"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, srv
}

const twoReleases = `[
	{"tag_name":"v2.0.0","assets":[{"name":"hottell-darwin-arm64"}]},
	{"tag_name":"hottell-v0.3.0","draft":true,"assets":[{"name":"hottell-darwin-arm64"}]},
	{"tag_name":"hottell-v0.2.0-rc1","prerelease":true,"assets":[{"name":"hottell-darwin-arm64"}]},
	{"tag_name":"hottell-v0.1.1","assets":[{"name":"hottell-darwin-arm64"},{"name":"install.sh"}]},
	{"tag_name":"hottell-v0.1.0","assets":[{"name":"hottell-darwin-arm64"},{"name":"SHA256SUMS"}]}
]`

// clock is a settable time.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func read(t *testing.T, r *gitea.Releases, name string) (string, error) {
	t.Helper()
	body, size, err := r.Open(t.Context(), name)
	if err != nil {
		return "", err
	}
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if size != int64(len(data)) {
		t.Fatalf("size of %s = %d, read %d bytes", name, size, len(data))
	}
	return string(data), nil
}

func TestOpenTakesTheNewestPublishedHottellRelease(t *testing.T) {
	t.Parallel()
	_, srv := newFakeGitea(t, twoReleases)
	releases := gitea.New(srv.URL, repo, token, (&clock{}).Now)

	got, err := read(t, releases, "install.sh")
	if err != nil || got != "hottell-v0.1.1/install.sh" {
		t.Fatalf("Open(install.sh) = %q, %v; want the file of hottell-v0.1.1", got, err)
	}
	// SHA256SUMS is only in an older release: the latest one is not searched past.
	if _, err := read(t, releases, "SHA256SUMS"); !errors.Is(err, gitea.ErrNoRelease) {
		t.Fatalf("Open(SHA256SUMS) error = %v, want ErrNoRelease", err)
	}
}

func TestOpenCachesTheLatestReleaseForFiveMinutes(t *testing.T) {
	t.Parallel()
	fake, srv := newFakeGitea(t, twoReleases)
	now := &clock{now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	releases := gitea.New(srv.URL, repo, token, now.Now)

	for range 3 {
		if _, err := read(t, releases, "hottell-darwin-arm64"); err != nil {
			t.Fatalf("Open: %v", err)
		}
	}
	if n := fake.lists.Load(); n != 1 {
		t.Fatalf("release list read %d times within the cache, want 1", n)
	}

	fake.set(http.StatusOK, `[{"tag_name":"hottell-v0.4.0","assets":[{"name":"hottell-darwin-arm64"}]}]`)
	now.advance(gitea.CacheTTL - time.Second)
	if got, _ := read(t, releases, "hottell-darwin-arm64"); got != "hottell-v0.1.1/hottell-darwin-arm64" {
		t.Fatalf("before expiry got %q, want the cached release", got)
	}
	now.advance(time.Second)
	if got, _ := read(t, releases, "hottell-darwin-arm64"); got != "hottell-v0.4.0/hottell-darwin-arm64" {
		t.Fatalf("after expiry got %q, want the new release", got)
	}
	if n := fake.lists.Load(); n != 2 {
		t.Fatalf("release list read %d times, want 2", n)
	}
}

func TestOpenFails(t *testing.T) {
	t.Parallel()

	t.Run("no hottell release", func(t *testing.T) {
		t.Parallel()
		_, srv := newFakeGitea(t, `[{"tag_name":"v1.0.0","assets":[]}]`)
		_, err := read(t, gitea.New(srv.URL, repo, token, (&clock{}).Now), "install.sh")
		if !errors.Is(err, gitea.ErrNoRelease) {
			t.Fatalf("error = %v, want ErrNoRelease", err)
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		t.Parallel()
		_, srv := newFakeGitea(t, twoReleases)
		if _, err := read(t, gitea.New(srv.URL, repo, "other", (&clock{}).Now), "install.sh"); err == nil {
			t.Fatal("want an error when Gitea refuses the token")
		}
	})

	t.Run("gitea unreachable", func(t *testing.T) {
		t.Parallel()
		_, srv := newFakeGitea(t, twoReleases)
		srv.Close()
		if _, err := read(t, gitea.New(srv.URL, repo, token, (&clock{}).Now), "install.sh"); err == nil {
			t.Fatal("want an error when Gitea is unreachable")
		}
	})

	t.Run("failure is not cached", func(t *testing.T) {
		t.Parallel()
		fake, srv := newFakeGitea(t, twoReleases)
		fake.set(http.StatusInternalServerError, "")
		releases := gitea.New(srv.URL, repo, token, (&clock{}).Now)
		if _, err := read(t, releases, "install.sh"); err == nil {
			t.Fatal("want an error on status 500")
		}
		fake.set(http.StatusOK, twoReleases)
		if _, err := read(t, releases, "install.sh"); err != nil {
			t.Fatalf("after Gitea recovers: %v", err)
		}
	})
}
