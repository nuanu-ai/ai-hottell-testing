// Package spa serves the built single-page application.
package spa

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

const (
	indexFile = "index.html"
	// assetsDir holds the files Vite names by content hash, so they never change under a name.
	assetsDir   = "assets/"
	assetsCache = "public, max-age=31536000, immutable"
	// indexCache makes the browser revalidate the page, so a new build reaches it at once.
	indexCache = "no-cache"
	apiPrefix  = "/api/"
	notBuilt   = "Фронтенд не собран: выполните task build:web"
)

// Handler serves the files of the built application and answers every other path
// with index.html, so the client-side router resolves it.
type Handler struct {
	files fs.FS
}

// New returns a Handler serving files, the root of the built application.
func New(files fs.FS) *Handler {
	return &Handler{files: files}
}

// ServeHTTP serves an existing file, or index.html for any other path outside /api.
// Without index.html the application is not built and the answer is 503.
// Only GET and HEAD read the application.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path+"/" == apiPrefix || strings.HasPrefix(r.URL.Path, apiPrefix) {
		http.NotFound(w, r)
		return
	}

	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name != indexFile && h.serveFile(w, r, name, cacheControl(name)) {
		return
	}
	if !h.serveFile(w, r, indexFile, indexCache) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, notBuilt)
	}
}

// serveFile serves the regular file name with the given Cache-Control
// and reports false when there is no such file.
func (h *Handler) serveFile(w http.ResponseWriter, r *http.Request, name, cache string) bool {
	f, err := h.files.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	content, ok := f.(io.ReadSeeker)
	if !ok {
		data, err := io.ReadAll(f)
		if err != nil {
			return false
		}
		content = bytes.NewReader(data)
	}

	if cache != "" {
		w.Header().Set("Cache-Control", cache)
	}
	http.ServeContent(w, r, name, info.ModTime(), content)
	return true
}

func cacheControl(name string) string {
	if strings.HasPrefix(name, assetsDir) {
		return assetsCache
	}
	return ""
}
