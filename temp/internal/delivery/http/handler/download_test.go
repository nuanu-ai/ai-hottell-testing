package handler_test

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/handler"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/handler/mock"
)

const testOrigin = "https://telemetry.example.test"

// serveDownload routes the request as the service does and returns the recorded answer.
func serveDownload(t *testing.T, releases handler.Releases, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	download := handler.NewDownload(releases, testOrigin, slog.New(slog.DiscardHandler))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /install.sh", download.InstallScript)
	mux.HandleFunc("GET /download/{name...}", download.File)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, path, http.NoBody))
	return rec
}

func content(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }

func TestInstallScriptPutsInTheOrigin(t *testing.T) {
	t.Parallel()
	releases := mock.NewMockReleases(gomock.NewController(t))
	releases.EXPECT().Open(gomock.Any(), "install.sh").
		Return(content("origin='__ORIGIN__'\ncurl \"__ORIGIN__/download/x\"\n"), int64(-1), nil)

	rec := serveDownload(t, releases, http.MethodGet, "/install.sh")

	want := "origin='" + testOrigin + "'\ncurl \"" + testOrigin + "/download/x\"\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("got %d %q, want 200 %q", rec.Code, rec.Body.String(), want)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
}

func TestFileStreamsTheReleaseFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, contentType string
	}{
		{name: "hottell-darwin-arm64", contentType: "application/octet-stream"},
		{name: "hottell-darwin-amd64", contentType: "application/octet-stream"},
		{name: "SHA256SUMS", contentType: "text/plain; charset=utf-8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			releases := mock.NewMockReleases(gomock.NewController(t))
			body := "bytes of " + tt.name
			releases.EXPECT().Open(gomock.Any(), tt.name).Return(content(body), int64(len(body)), nil)

			rec := serveDownload(t, releases, http.MethodGet, "/download/"+tt.name)

			if rec.Code != http.StatusOK || rec.Body.String() != body {
				t.Fatalf("got %d %q", rec.Code, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); ct != tt.contentType {
				t.Fatalf("Content-Type = %q, want %q", ct, tt.contentType)
			}
			if cl := rec.Header().Get("Content-Length"); cl != strconv.Itoa(len(body)) {
				t.Fatalf("Content-Length = %q", cl)
			}
		})
	}
}

func TestFileRefusesNamesOffTheList(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/download/install.sh", "/download/hottell", "/download/", "/download/hottell-darwin-arm64/x", "/download/..%2Fsecret"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			// Nothing is opened: the mock expects no call.
			rec := serveDownload(t, mock.NewMockReleases(gomock.NewController(t)), http.MethodGet, path)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("got %d %q, want 404", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestUnavailableReleaseAnswers503(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/install.sh", "/download/hottell-darwin-arm64", "/download/SHA256SUMS"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			releases := mock.NewMockReleases(gomock.NewController(t))
			releases.EXPECT().Open(gomock.Any(), gomock.Any()).Return(nil, int64(0), errors.New("gitea is down"))

			rec := serveDownload(t, releases, http.MethodGet, path)

			if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "unavailable") {
				t.Fatalf("got %d %q, want 503 with a text", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHeadSendsNoBody(t *testing.T) {
	t.Parallel()
	releases := mock.NewMockReleases(gomock.NewController(t))
	releases.EXPECT().Open(gomock.Any(), "hottell-darwin-arm64").Return(content("binary"), int64(6), nil)

	rec := serveDownload(t, releases, http.MethodHead, "/download/hottell-darwin-arm64")

	if rec.Code != http.StatusOK || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "6" {
		t.Fatalf("got %d, body %q, Content-Length %q", rec.Code, rec.Body.String(), rec.Header().Get("Content-Length"))
	}
}
