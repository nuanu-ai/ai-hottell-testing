// Package gitea reads the files of the latest hottell release from Gitea: the repository
// is private, so the service takes them with its own token and serves them to the users.
package gitea

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// TagPrefix starts the tag of every hottell release (HT-102).
	TagPrefix = "hottell-v"
	// CacheTTL is how long the latest release, once found, is reused.
	CacheTTL = 5 * time.Minute
	// listLimit is how many of the newest releases are searched for a hottell one.
	listLimit = 50
	// listTimeout bounds the request listing the releases.
	listTimeout = 10 * time.Second
	// headerTimeout bounds the wait for the headers of a file; its body streams unbounded.
	headerTimeout = 30 * time.Second
	// maxListBytes bounds the release list read.
	maxListBytes = 8 << 20
)

// ErrNoRelease is returned when the repository has no published hottell release, or the
// latest one lacks the requested file.
var ErrNoRelease = errors.New("no hottell release")

// Releases opens the files of the latest published hottell release of a Gitea repository.
type Releases struct {
	baseURL string
	repo    string
	token   string
	client  *http.Client
	now     func() time.Time

	mu      sync.Mutex
	latest  release
	expires time.Time
}

// release is the part of a Gitea release the service needs.
type release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []asset `json:"assets"`
}

type asset struct {
	Name string `json:"name"`
}

// New returns Releases of the Gitea at baseURL (no trailing "/") and its repository repo
// (owner/name), read with token, anonymously when empty; now tells the time the cache
// expires by.
func New(baseURL, repo, token string, now func() time.Time) *Releases {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = headerTimeout
	return &Releases{
		baseURL: baseURL,
		repo:    repo,
		token:   token,
		// No overall timeout: a binary streams for as long as the user's download takes.
		// A redirect to another host drops the Authorization header.
		client: &http.Client{Transport: transport},
		now:    now,
	}
}

// Open returns the content of the file name of the latest hottell release and its size,
// -1 when Gitea does not tell it; the caller closes the content. It fails with ErrNoRelease
// when there is no such release or file, and with another error when Gitea cannot be read.
func (r *Releases) Open(ctx context.Context, name string) (io.ReadCloser, int64, error) {
	latest, err := r.latestRelease(ctx)
	if err != nil {
		return nil, 0, err
	}
	if !latest.has(name) {
		return nil, 0, fmt.Errorf("%s has no file %s: %w", latest.Tag, name, ErrNoRelease)
	}
	fileURL := r.baseURL + "/" + r.repo + "/releases/download/" + url.PathEscape(latest.Tag) + "/" +
		url.PathEscape(name)
	resp, err := r.get(ctx, fileURL)
	if err != nil {
		return nil, 0, fmt.Errorf("download %s of %s: %w", name, latest.Tag, err)
	}
	return resp.Body, resp.ContentLength, nil
}

// latestRelease returns the cached latest release, finding it anew once the cache expires.
// A failure is not cached: the next request asks Gitea again.
func (r *Releases) latestRelease(ctx context.Context) (release, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.latest.Tag != "" && r.now().Before(r.expires) {
		return r.latest, nil
	}
	latest, err := r.findLatest(ctx)
	if err != nil {
		return release{}, err
	}
	r.latest, r.expires = latest, r.now().Add(CacheTTL)
	return latest, nil
}

// findLatest returns the newest published release tagged hottell-v*; Gitea lists the
// releases newest first.
func (r *Releases) findLatest(ctx context.Context) (release, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	listURL := fmt.Sprintf("%s/api/v1/repos/%s/releases?limit=%d", r.baseURL, r.repo, listLimit)
	resp, err := r.get(ctx, listURL)
	if err != nil {
		return release{}, fmt.Errorf("list releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var releases []release
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxListBytes)).Decode(&releases); err != nil {
		return release{}, fmt.Errorf("decode releases: %w", err)
	}
	for _, rel := range releases {
		if strings.HasPrefix(rel.Tag, TagPrefix) && !rel.Draft && !rel.Prerelease {
			return rel, nil
		}
	}
	return release{}, fmt.Errorf("%s: %w", r.repo, ErrNoRelease)
}

// get sends a GET to rawURL with the token and returns a 200 response; any other status
// closes the response and fails.
func (r *Releases) get(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return nil, err
	}
	if r.token != "" {
		req.Header.Set("Authorization", "token "+r.token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("GET %s: status %d", req.URL.Redacted(), resp.StatusCode)
	}
	return resp, nil
}

func (rel release) has(name string) bool {
	for _, a := range rel.Assets {
		if a.Name == name {
			return true
		}
	}
	return false
}
