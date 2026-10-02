// Package gitrepo tells which git repository a folder belongs to, so that the sessions
// of every worktree of one repository carry the same marker (ingest.md, attributes
// hottell.repo.root and hottell.repo.remote).
//
// It reads the .git file or directory and the repository's config directly and never
// runs git: hottell hook calls it on every event and must stay cheap.
package gitrepo

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Repo is the marker of a repository.
type Repo struct {
	// Root is the repository's common git directory, what git rev-parse
	// --git-common-dir gives as an absolute path with the symlinks resolved: the same
	// for the main checkout and every worktree.
	Root string
	// Remote is remote.origin.url without credentials, or "" when there is none.
	Remote string
}

// Find returns the repository of the absolute folder cwd: the nearest .git in cwd or
// above it. A folder outside any repository, a relative or missing one, or a .git that
// cannot be read gives false.
func Find(cwd string) (Repo, bool) {
	if cwd == "" || !filepath.IsAbs(cwd) {
		return Repo{}, false
	}
	dir, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return Repo{}, false
	}
	for {
		if gitDir, found, ok := gitDirOf(dir); found {
			if !ok {
				return Repo{}, false
			}
			return repoOf(gitDir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Repo{}, false
		}
		dir = parent
	}
}

// gitDirOf looks at dir/.git: found reports that it exists, ok that it names a git
// directory, returned as gitDir.
func gitDirOf(dir string) (gitDir string, found, ok bool) {
	dotGit := filepath.Join(dir, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return "", false, false
	}
	if info.IsDir() {
		return dotGit, true, true
	}
	// A worktree or a submodule: the file is one line "gitdir: <path>".
	data, err := os.ReadFile(dotGit)
	if err != nil {
		return "", true, false
	}
	line, _, _ := bytes.Cut(data, []byte("\n"))
	path, cut := strings.CutPrefix(strings.TrimSpace(string(line)), "gitdir:")
	path = strings.TrimSpace(path)
	if !cut || path == "" {
		return "", true, false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	return path, true, true
}

// repoOf turns a git directory into its repository: a worktree's directory names the
// common one in its commondir file.
func repoOf(gitDir string) (Repo, bool) {
	// Resolved first: a relative commondir joined to a symlink would collapse lexically
	// into the wrong folder.
	gitDir, err := filepath.EvalSymlinks(gitDir)
	if err != nil {
		return Repo{}, false
	}
	common := gitDir
	if data, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		if rel := strings.TrimSpace(string(data)); rel != "" {
			common = rel
			if !filepath.IsAbs(rel) {
				common = filepath.Join(gitDir, rel)
			}
		}
	}
	root, err := filepath.EvalSymlinks(common)
	if err != nil {
		return Repo{}, false
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return Repo{}, false
	}
	return Repo{Root: root, Remote: StripCredentials(originURL(filepath.Join(root, "config")))}, true
}

// originURL is the first url of [remote "origin"] in the git config file, or "".
func originURL(config string) string {
	f, err := os.Open(config)
	if err != nil {
		return ""
	}
	defer f.Close()
	inOrigin := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			inOrigin = isOriginSection(line)
			continue
		}
		if !inOrigin {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "url") {
			return configValue(value)
		}
	}
	return ""
}

// isOriginSection reports whether a section header is [remote "origin"]; the section
// name is case-insensitive, the subsection is not. What follows the header is ignored.
func isOriginSection(header string) bool {
	inner, _, ok := strings.Cut(strings.TrimPrefix(header, "["), "]")
	if !ok {
		return false
	}
	fields := strings.Fields(inner)
	return len(fields) == 2 && strings.EqualFold(fields[0], "remote") && fields[1] == `"origin"`
}

// configValue is a git config value: an unquoted one, or one wholly in quotes and
// followed by nothing but a comment. Anything else — a value quoted in part, with an
// escape, a continuation or a comment inside it — gives "": cut in the wrong place, a
// URL could keep its password past StripCredentials, so the remote is left out instead.
func configValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.ContainsRune(raw, '\\') {
		return ""
	}
	if !strings.HasPrefix(raw, `"`) {
		if strings.ContainsAny(raw, "\"#;") {
			return ""
		}
		return raw
	}
	value, rest, ok := strings.Cut(raw[1:], `"`)
	if !ok {
		return ""
	}
	if rest = strings.TrimSpace(rest); rest != "" && rest[0] != '#' && rest[0] != ';' {
		return ""
	}
	return value
}

// StripCredentials removes the credentials from a remote URL: the whole user info of
// any URL with a scheme — a login, a password or a token — except the user of an ssh
// URL, which keeps only its password out. A remote-helper URL (https::https://…) counts
// as not ssh. An scp-like address (git@host:path) and a path stay as they are.
func StripCredentials(remote string) string {
	scheme, rest, ok := strings.Cut(remote, "://")
	if !ok {
		return remote
	}
	authority, path := rest, ""
	if i := strings.Index(rest, "/"); i >= 0 {
		authority, path = rest[:i], rest[i:]
	}
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return remote
	}
	user, host := authority[:at], authority[at+1:]
	switch strings.ToLower(scheme) {
	case "ssh", "git+ssh", "ssh+git":
		// The user of an ssh URL is an account name, not a secret.
		user, _, _ = strings.Cut(user, ":")
	default:
		user = ""
	}
	if user != "" {
		host = user + "@" + host
	}
	return scheme + "://" + host + path
}

// cacheSize bounds the folders a Cache remembers; past it the cache starts over.
const cacheSize = 4096

// Cache remembers the repository of each folder for the life of the process: the
// daemon resolves a session's cwd once, not once per transcript line. It is safe for
// concurrent use.
type Cache struct {
	mu    sync.Mutex
	repos map[string]cached
}

type cached struct {
	repo Repo
	ok   bool
}

// NewCache returns an empty cache.
func NewCache() *Cache { return &Cache{repos: map[string]cached{}} }

// Find is the package Find, remembered per cwd.
func (c *Cache) Find(cwd string) (Repo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.repos[cwd]; ok {
		return v.repo, v.ok
	}
	repo, ok := Find(cwd)
	if len(c.repos) >= cacheSize {
		clear(c.repos)
	}
	c.repos[cwd] = cached{repo: repo, ok: ok}
	return repo, ok
}
