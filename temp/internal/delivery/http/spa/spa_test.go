package spa_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/spa"
)

const indexHTML = `<!doctype html><div id="root"></div>`

func TestHandler(t *testing.T) {
	t.Parallel()

	built := fstest.MapFS{
		"index.html":            {Data: []byte(indexHTML)},
		"favicon.svg":           {Data: []byte("<svg/>")},
		"assets/index-abc1.js":  {Data: []byte("console.log(1)")},
		"assets/index-abc1.css": {Data: []byte("body{}")},
	}
	unbuilt := fstest.MapFS{}

	tests := []struct {
		name      string
		files     fstest.MapFS
		method    string
		path      string
		wantCode  int
		wantType  string
		wantCache string
		wantBody  string
	}{
		{name: "root", files: built, path: "/", wantCode: http.StatusOK, wantType: "text/html; charset=utf-8", wantCache: "no-cache", wantBody: indexHTML},
		{name: "index file", files: built, path: "/index.html", wantCode: http.StatusOK, wantType: "text/html; charset=utf-8", wantCache: "no-cache", wantBody: indexHTML},
		{name: "file", files: built, path: "/favicon.svg", wantCode: http.StatusOK, wantType: "image/svg+xml", wantBody: "<svg/>"},
		{name: "hashed script", files: built, path: "/assets/index-abc1.js", wantCode: http.StatusOK, wantType: "text/javascript; charset=utf-8", wantCache: "public, max-age=31536000, immutable", wantBody: "console.log(1)"},
		{name: "hashed stylesheet", files: built, path: "/assets/index-abc1.css", wantCode: http.StatusOK, wantType: "text/css; charset=utf-8", wantCache: "public, max-age=31536000, immutable", wantBody: "body{}"},
		{name: "client route", files: built, path: "/users/123", wantCode: http.StatusOK, wantType: "text/html; charset=utf-8", wantCache: "no-cache", wantBody: indexHTML},
		{name: "directory", files: built, path: "/assets/", wantCode: http.StatusOK, wantType: "text/html; charset=utf-8", wantCache: "no-cache", wantBody: indexHTML},
		{name: "path escaping the root", files: built, path: "/../index.html", wantCode: http.StatusOK, wantType: "text/html; charset=utf-8", wantCache: "no-cache", wantBody: indexHTML},
		{name: "api path", files: built, path: "/api/nope", wantCode: http.StatusNotFound},
		{name: "head", files: built, method: http.MethodHead, path: "/users/123", wantCode: http.StatusOK, wantType: "text/html; charset=utf-8", wantCache: "no-cache"},
		{name: "post", files: built, method: http.MethodPost, path: "/users/123", wantCode: http.StatusMethodNotAllowed, wantType: "text/plain; charset=utf-8", wantBody: "Method Not Allowed\n"},
		{name: "not built", files: unbuilt, path: "/", wantCode: http.StatusServiceUnavailable, wantType: "text/plain; charset=utf-8", wantBody: "Фронтенд не собран: выполните task build:web"},
		{name: "not built client route", files: unbuilt, path: "/users/123", wantCode: http.StatusServiceUnavailable, wantType: "text/plain; charset=utf-8", wantBody: "Фронтенд не собран: выполните task build:web"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			method := tt.method
			if method == "" {
				method = http.MethodGet
			}
			rec := httptest.NewRecorder()
			spa.New(tt.files).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, tt.path, http.NoBody))

			if rec.Code != tt.wantCode {
				t.Fatalf("code: got %d, want %d; body %q", rec.Code, tt.wantCode, rec.Body.String())
			}
			if tt.wantCode == http.StatusNotFound {
				if rec.Body.String() == indexHTML {
					t.Fatalf("an API path got the SPA")
				}
				return
			}
			if got := rec.Header().Get("Content-Type"); got != tt.wantType {
				t.Errorf("Content-Type: got %q, want %q", got, tt.wantType)
			}
			if got := rec.Header().Get("Cache-Control"); got != tt.wantCache {
				t.Errorf("Cache-Control: got %q, want %q", got, tt.wantCache)
			}
			if got := rec.Body.String(); got != tt.wantBody {
				t.Errorf("body: got %q, want %q", got, tt.wantBody)
			}
		})
	}
}
