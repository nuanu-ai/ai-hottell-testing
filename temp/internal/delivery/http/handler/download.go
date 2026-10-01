package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
)

// installScript is the file of a hottell release /install.sh serves.
const installScript = "install.sh"

// originPlaceholder is what the service replaces with its public origin in install.sh.
const originPlaceholder = "__ORIGIN__"

// maxInstallScriptBytes bounds the install.sh read into memory.
const maxInstallScriptBytes = 1 << 20

// releaseUnavailable answers a request for a release file the service cannot get.
const releaseUnavailable = "the hottell release is unavailable, try again later\n"

// downloadFiles are the only files /download/{name} serves.
func downloadFiles() []string {
	return []string{"hottell-darwin-arm64", "hottell-darwin-amd64", "SHA256SUMS"}
}

// Download serves the files of the latest hottell release without a session: /install.sh
// with the public origin put in, and the binaries with their SHA256SUMS under /download/.
type Download struct {
	releases Releases
	origin   string
	logger   *slog.Logger
}

// NewDownload returns a Download of the files releases opens; origin replaces __ORIGIN__
// in install.sh; logger records why a file cannot be served.
func NewDownload(releases Releases, origin string, logger *slog.Logger) *Download {
	return &Download{releases: releases, origin: origin, logger: logger}
}

// InstallScript serves install.sh of the latest release, __ORIGIN__ replaced with the
// public origin.
func (d *Download) InstallScript(w http.ResponseWriter, r *http.Request) {
	script, err := d.readInstallScript(r.Context())
	if err != nil {
		d.unavailable(w, r, installScript, err)
		return
	}
	script = bytes.ReplaceAll(script, []byte(originPlaceholder), []byte(d.origin))
	header := w.Header()
	header.Set("Content-Type", "text/plain; charset=utf-8")
	header.Set("Content-Length", strconv.Itoa(len(script)))
	header.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(script)
}

// File streams the file of the latest release the {name} path value names; a name not in
// the list is not found.
func (d *Download) File(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !slices.Contains(downloadFiles(), name) {
		writeText(w, http.StatusNotFound, "no such file\n")
		return
	}
	body, size, err := d.releases.Open(r.Context(), name)
	if err != nil {
		d.unavailable(w, r, name, err)
		return
	}
	defer func() { _ = body.Close() }()
	header := w.Header()
	header.Set("Content-Type", "application/octet-stream")
	if name == "SHA256SUMS" {
		header.Set("Content-Type", "text/plain; charset=utf-8")
	}
	if size >= 0 {
		header.Set("Content-Length", strconv.FormatInt(size, 10))
	}
	header.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, body); err != nil {
		// The status is sent: the client sees a short body, and install.sh a checksum mismatch.
		d.logger.WarnContext(r.Context(), "stream release file", "file", name, "error", err)
	}
}

func (d *Download) readInstallScript(ctx context.Context) ([]byte, error) {
	body, _, err := d.releases.Open(ctx, installScript)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	script, err := io.ReadAll(io.LimitReader(body, maxInstallScriptBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", installScript, err)
	}
	if len(script) > maxInstallScriptBytes {
		return nil, fmt.Errorf("%s is over %d bytes", installScript, maxInstallScriptBytes)
	}
	return script, nil
}

func (d *Download) unavailable(w http.ResponseWriter, r *http.Request, name string, err error) {
	d.logger.ErrorContext(r.Context(), "open release file", "file", name, "error", err)
	writeText(w, http.StatusServiceUnavailable, releaseUnavailable)
}
