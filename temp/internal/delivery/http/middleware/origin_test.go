package middleware_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/middleware"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/openapi"
)

func TestOrigin(t *testing.T) {
	t.Parallel()

	const (
		public = "http://localhost:8080"
		vite   = "http://localhost:5173"
	)

	tests := []struct {
		name        string
		method      string
		path        string
		origin      string
		contentType string
		body        io.Reader
		chunked     bool
		wantCode    int
		wantError   string
	}{
		{name: "public origin with json", method: http.MethodPost, path: "/api/auth/login", origin: public, contentType: "application/json", body: strings.NewReader("{}"), wantCode: http.StatusOK},
		{name: "rp origin with json and charset", method: http.MethodPut, path: "/api/x", origin: vite, contentType: "application/json; charset=utf-8", body: strings.NewReader("{}"), wantCode: http.StatusOK},
		{name: "foreign origin", method: http.MethodPost, path: "/api/auth/login", origin: "https://evil.example", contentType: "application/json", body: strings.NewReader("{}"), wantCode: http.StatusForbidden, wantError: "forbidden_origin"},
		{name: "no origin", method: http.MethodPost, path: "/api/auth/logout", wantCode: http.StatusForbidden, wantError: "forbidden_origin"},
		{name: "origin with trailing slash", method: http.MethodPatch, path: "/api/x", origin: public + "/", wantCode: http.StatusForbidden, wantError: "forbidden_origin"},
		{name: "foreign origin on delete", method: http.MethodDelete, path: "/api/users/1", origin: "null", wantCode: http.StatusForbidden, wantError: "forbidden_origin"},
		{name: "body not json", method: http.MethodPost, path: "/api/auth/login", origin: public, contentType: "application/x-www-form-urlencoded", body: strings.NewReader("email=a"), wantCode: http.StatusUnsupportedMediaType, wantError: "unsupported_media_type"},
		{name: "body without content type", method: http.MethodPost, path: "/api/auth/login", origin: public, body: strings.NewReader("{}"), wantCode: http.StatusUnsupportedMediaType, wantError: "unsupported_media_type"},
		{name: "chunked body not json", method: http.MethodPost, path: "/api/auth/login", origin: public, contentType: "text/plain", body: strings.NewReader("{}"), chunked: true, wantCode: http.StatusUnsupportedMediaType, wantError: "unsupported_media_type"},
		{name: "delete without body", method: http.MethodDelete, path: "/api/me/passkeys/1", origin: public, wantCode: http.StatusOK},
		{name: "post without body", method: http.MethodPost, path: "/api/auth/logout", origin: vite, wantCode: http.StatusOK},
		{name: "get without origin", method: http.MethodGet, path: "/api/me", wantCode: http.StatusOK},
		{name: "post outside api", method: http.MethodPost, path: "/apix", wantCode: http.StatusOK},
	}

	guarded := middleware.Origin([]string{public, vite})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body := tt.body
			if body == nil {
				body = http.NoBody
			}
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, body)
			if tt.chunked {
				req.ContentLength = -1
			}
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			rec := httptest.NewRecorder()

			guarded.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("got status %d, want %d", rec.Code, tt.wantCode)
			}
			if tt.wantError == "" {
				return
			}
			var got openapi.Error
			if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if got.Code != tt.wantError || got.Message == "" {
				t.Fatalf("got error %+v, want code %s with a message", got, tt.wantError)
			}
		})
	}
}

func TestOriginForbiddenMessage(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/auth/logout", http.NoBody)
	req.Header.Set("Origin", "https://evil.example")

	middleware.Origin([]string{"http://localhost:8080"})(http.NotFoundHandler()).ServeHTTP(rec, req)

	want := `{"code":"forbidden_origin","message":"Запрос пришёл не с этого сайта"}` + "\n"
	if rec.Body.String() != want {
		t.Fatalf("got body %q, want %q", rec.Body.String(), want)
	}
}
