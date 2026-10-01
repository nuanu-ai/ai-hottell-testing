package ioc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/cmd/api/ioc"
	authmock "git.alva.dev/alva/harness-telemetry/internal/application/auth/mock"
	keysmock "git.alva.dev/alva/harness-telemetry/internal/application/keys/mock"
	passkeysmock "git.alva.dev/alva/harness-telemetry/internal/application/passkeys/mock"
	"git.alva.dev/alva/harness-telemetry/internal/application/session/mock"
	settingsmock "git.alva.dev/alva/harness-telemetry/internal/application/settings/mock"
	usersmock "git.alva.dev/alva/harness-telemetry/internal/application/users/mock"
	"git.alva.dev/alva/harness-telemetry/internal/config"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func testConfig() config.Config {
	return config.Config{
		PublicOrigin:      "http://localhost:8080",
		WebAuthnRPID:      "localhost",
		WebAuthnRPOrigins: []string{"http://localhost:8080", "http://localhost:5173"},
	}
}

// newContainer assembles the container as New does and fails the test when it cannot.
func newContainer(t *testing.T, stores ioc.Stores, logger *slog.Logger, app fstest.MapFS) ioc.Container {
	t.Helper()
	container, err := ioc.New(pingerFunc(func(context.Context) error { return nil }), stores, testConfig(), "dev",
		logger, app)
	if err != nil {
		t.Fatalf("assemble container: %v", err)
	}
	return container
}

// stores returns Stores on mocks that expect nothing, but for sessions.
func stores(t *testing.T, sessions *mock.MockSessions) ioc.Stores {
	t.Helper()
	ctrl := gomock.NewController(t)
	return ioc.Stores{
		Users: usersmock.NewMockUsers(ctrl), PasskeyUsers: passkeysmock.NewMockUsers(ctrl),
		Links: usersmock.NewMockLinks(ctrl), Sessions: sessions, Passkeys: passkeysmock.NewMockPasskeys(ctrl),
		Ceremonies: passkeysmock.NewMockCeremonies(ctrl), AccessKeys: keysmock.NewMockAccessKeys(ctrl),
		Settings: settingsmock.NewMockSettings(ctrl), Tx: authmock.NewMockTxManager(ctrl),
	}
}

func TestRoutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		method   string
		path     string
		origin   string
		wantCode int
		wantBody string
	}{
		{name: "version", method: http.MethodGet, path: "/api/version", wantCode: http.StatusOK, wantBody: `{"version":"dev"}` + "\n"},
		{name: "unknown api path", method: http.MethodGet, path: "/api/nope", wantCode: http.StatusNotFound, wantBody: `{"code":"not_found","message":"no such API route"}` + "\n"},
		{name: "api root", method: http.MethodGet, path: "/api/", wantCode: http.StatusNotFound, wantBody: `{"code":"not_found","message":"no such API route"}` + "\n"},
		{name: "unknown api path with method", method: http.MethodPost, path: "/api/nope", origin: "http://localhost:8080", wantCode: http.StatusNotFound, wantBody: `{"code":"not_found","message":"no such API route"}` + "\n"},
		{name: "liveness", method: http.MethodGet, path: "/healthz", wantCode: http.StatusOK, wantBody: "ok"},
		{name: "readiness", method: http.MethodGet, path: "/readyz", wantCode: http.StatusServiceUnavailable, wantBody: "not ready"},
		{name: "spa root", method: http.MethodGet, path: "/", wantCode: http.StatusOK, wantBody: indexHTML},
		{name: "spa client route", method: http.MethodGet, path: "/users/123", wantCode: http.StatusOK, wantBody: indexHTML},
		{name: "spa file", method: http.MethodGet, path: "/favicon.svg", wantCode: http.StatusOK, wantBody: "<svg/>"},
		{
			name: "foreign origin", method: http.MethodPost, path: "/api/auth/login", origin: "https://evil.example",
			wantCode: http.StatusForbidden, wantBody: `{"code":"forbidden_origin","message":"Запрос пришёл не с этого сайта"}` + "\n",
		},
		{
			name: "allowed origin reaches routing", method: http.MethodPost, path: "/api/nope", origin: "http://localhost:5173",
			wantCode: http.StatusNotFound, wantBody: `{"code":"not_found","message":"no such API route"}` + "\n",
		},
		{name: "me without session", method: http.MethodGet, path: "/api/me", wantCode: http.StatusUnauthorized, wantBody: unauthenticated},
		{name: "logout without session", method: http.MethodPost, path: "/api/auth/logout", origin: "http://localhost:8080", wantCode: http.StatusUnauthorized, wantBody: unauthenticated},
		{name: "users without session", method: http.MethodGet, path: "/api/users", wantCode: http.StatusUnauthorized, wantBody: unauthenticated},
		{name: "passkeys without session", method: http.MethodGet, path: "/api/me/passkeys", wantCode: http.StatusUnauthorized, wantBody: unauthenticated},
		{name: "passkey delete without session", method: http.MethodDelete, path: "/api/me/passkeys/" + uuid.NewString(), origin: "http://localhost:8080", wantCode: http.StatusUnauthorized, wantBody: unauthenticated},
		{name: "keys without session", method: http.MethodGet, path: "/api/me/keys", wantCode: http.StatusUnauthorized, wantBody: unauthenticated},
		{name: "issue mcp key without session", method: http.MethodPost, path: "/api/me/keys/mcp", origin: "http://localhost:8080", wantCode: http.StatusUnauthorized, wantBody: unauthenticated},
		{name: "revoke mcp key without session", method: http.MethodDelete, path: "/api/me/keys/mcp", origin: "http://localhost:8080", wantCode: http.StatusUnauthorized, wantBody: unauthenticated},
		{name: "reissue ingest token without session", method: http.MethodPost, path: "/api/me/keys/ingest/reissue", origin: "http://localhost:8080", wantCode: http.StatusUnauthorized, wantBody: unauthenticated},
		{name: "telemetry settings without session", method: http.MethodGet, path: "/api/me/telemetry-settings", wantCode: http.StatusUnauthorized, wantBody: unauthenticated},
		{name: "revoke invite without session", method: http.MethodDelete, path: "/api/users/" + uuid.NewString(), origin: "http://localhost:8080", wantCode: http.StatusUnauthorized, wantBody: unauthenticated},
	}

	app := fstest.MapFS{
		"index.html":  {Data: []byte(indexHTML)},
		"favicon.svg": {Data: []byte("<svg/>")},
	}
	container := newContainer(t, stores(t, mock.NewMockSessions(gomock.NewController(t))), slog.New(slog.DiscardHandler), app)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, http.NoBody)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			container.Handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode || rec.Body.String() != tt.wantBody {
				t.Fatalf("got %d %q, want %d %q", rec.Code, rec.Body.String(), tt.wantCode, tt.wantBody)
			}
			if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
				t.Errorf("X-Frame-Options: got %q, want %q", got, "DENY")
			}
		})
	}
}

func TestSessionCookieWithoutLiveSessionIsCleared(t *testing.T) {
	t.Parallel()

	sessions := mock.NewMockSessions(gomock.NewController(t))
	sessions.EXPECT().GetByTokenHash(gomock.Any(), gomock.Any()).Return(domain.Session{}, domain.ErrSessionNotFound)
	container := newContainer(t, stores(t, sessions), slog.New(slog.DiscardHandler),
		fstest.MapFS{"index.html": {Data: []byte(indexHTML)}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/version", http.NoBody)
	req.AddCookie(&http.Cookie{Name: "ht_session", Value: strings.Repeat("A", 43)})
	container.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusOK)
	}
	if got, want := rec.Header().Get("Set-Cookie"), "ht_session=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax"; got != want {
		t.Fatalf("got Set-Cookie %q, want %q", got, want)
	}
}

// TestLinkTokenStaysOutOfLogs fails the link operations of both families on the store and
// checks that the log of the service names the route with {token}, never the token.
func TestLinkTokenStaysOutOfLogs(t *testing.T) {
	t.Parallel()

	// A well-formed token: it reaches the store, where the lookup fails.
	token := strings.Repeat("Q", 43)
	tests := []struct {
		method string
		path   string
		route  string
	}{
		{method: http.MethodGet, path: "/api/invites/" + token, route: "GET /api/invites/{token}"},
		{method: http.MethodPost, path: "/api/invites/" + token + "/accept", route: "POST /api/invites/{token}/accept"},
		{method: http.MethodGet, path: "/api/password-resets/" + token, route: "GET /api/password-resets/{token}"},
		{
			method: http.MethodPost, path: "/api/password-resets/" + token + "/complete",
			route: "POST /api/password-resets/{token}/complete",
		},
	}

	for _, tt := range tests {
		t.Run(tt.route, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			links := usersmock.NewMockLinks(ctrl)
			links.EXPECT().GetByTokenHash(gomock.Any(), gomock.Any()).Return(domain.Link{}, errors.New("database down"))
			stores := stores(t, mock.NewMockSessions(ctrl))
			stores.Links = links
			var logs bytes.Buffer
			container := newContainer(t, stores, slog.New(slog.NewJSONHandler(&logs, nil)),
				fstest.MapFS{"index.html": {Data: []byte(indexHTML)}})

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, strings.NewReader(`{"password":"new password"}`))
			req.Header.Set("Origin", "http://localhost:8080")
			req.Header.Set("Content-Type", "application/json")
			container.Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("got status %d, want %d", rec.Code, http.StatusInternalServerError)
			}
			var entry struct {
				Route string `json:"route"`
			}
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatalf("decode log entry %q: %v", logs.String(), err)
			}
			if entry.Route != tt.route {
				t.Fatalf("got route %q, want %q", entry.Route, tt.route)
			}
			if bytes.Contains(logs.Bytes(), []byte(token)) {
				t.Fatalf("log %q holds the token of the path", logs.String())
			}
		})
	}
}

// TestPasskeyLoginBeginIsOpen begins a passkey sign-in without a session through the
// assembled relying party and checks it keeps the ceremony and sets its cookie.
func TestPasskeyLoginBeginIsOpen(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ceremonyID := uuid.New()
	ceremonies := passkeysmock.NewMockCeremonies(ctrl)
	ceremonies.EXPECT().Save(gomock.Any(), domain.CeremonyKindLogin, nil, nil, gomock.Any(), gomock.Any()).
		Return(ceremonyID, nil)
	stores := stores(t, mock.NewMockSessions(ctrl))
	stores.Ceremonies = ceremonies
	container := newContainer(t, stores, slog.New(slog.DiscardHandler), fstest.MapFS{"index.html": {Data: []byte(indexHTML)}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/auth/passkey/login/begin", strings.NewReader(`{}`))
	req.Header.Set("Origin", "http://localhost:8080")
	req.Header.Set("Content-Type", "application/json")
	container.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d %q, want %d", rec.Code, rec.Body.String(), http.StatusOK)
	}
	var body struct {
		Options struct {
			RPID string `json:"rpId"`
		} `json:"options"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Options.RPID != "localhost" {
		t.Fatalf("got body %q (%v), want options for rpId localhost", rec.Body.String(), err)
	}
	want := "ht_webauthn=" + ceremonyID.String() + "; Path=/api; Max-Age=300; HttpOnly; SameSite=Strict"
	if got := rec.Header().Get("Set-Cookie"); got != want {
		t.Fatalf("got Set-Cookie %q, want %q", got, want)
	}
}

// TestPasskeyFinishDeletesCeremonyCookie finishes a passkey sign-in without a ceremony
// and checks the assembled service still deletes the ceremony cookie.
func TestPasskeyFinishDeletesCeremonyCookie(t *testing.T) {
	t.Parallel()

	container := newContainer(t, stores(t, mock.NewMockSessions(gomock.NewController(t))), slog.New(slog.DiscardHandler),
		fstest.MapFS{"index.html": {Data: []byte(indexHTML)}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/auth/passkey/login/finish",
		strings.NewReader(`{"credential":{}}`))
	req.Header.Set("Origin", "http://localhost:8080")
	req.Header.Set("Content-Type", "application/json")
	container.Handler.ServeHTTP(rec, req)

	want := `{"code":"passkey_ceremony_expired","message":"Время на подтверждение истекло. Попробуйте ещё раз"}` + "\n"
	if rec.Code != http.StatusBadRequest || rec.Body.String() != want {
		t.Fatalf("got %d %q, want %d %q", rec.Code, rec.Body.String(), http.StatusBadRequest, want)
	}
	if got, want := rec.Header().Get("Set-Cookie"), "ht_webauthn=; Path=/api; Max-Age=0; HttpOnly; SameSite=Strict"; got != want {
		t.Fatalf("got Set-Cookie %q, want %q", got, want)
	}
}

// TestIssueMcpKeyAnswersMcpAddress issues an MCP key through the assembled keys use cases
// and checks the answer names the MCP server under the public origin.
func TestIssueMcpKeyAnswersMcpAddress(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	userID := uuid.New()
	sessions := mock.NewMockSessions(ctrl)
	sessions.EXPECT().GetByTokenHash(gomock.Any(), gomock.Any()).
		Return(domain.Session{ID: uuid.New(), UserID: userID, ExpiresAt: time.Now().Add(domain.SessionTTL)}, nil)
	sessions.EXPECT().Extend(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	accessKeys := keysmock.NewMockAccessKeys(ctrl)
	accessKeys.EXPECT().Replace(gomock.Any(), userID, domain.AccessKeyKindMCP, gomock.Any(), "").
		Return(domain.AccessKey{}, nil)
	stores := stores(t, sessions)
	stores.AccessKeys = accessKeys
	container := newContainer(t, stores, slog.New(slog.DiscardHandler), fstest.MapFS{"index.html": {Data: []byte(indexHTML)}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/me/keys/mcp", http.NoBody)
	req.Header.Set("Origin", "http://localhost:8080")
	req.AddCookie(&http.Cookie{Name: "ht_session", Value: strings.Repeat("A", 43)})
	container.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("got status %d %q, want %d", rec.Code, rec.Body.String(), http.StatusCreated)
	}
	var body struct {
		Key        string `json:"key"`
		McpURL     string `json:"mcpUrl"`
		ServerName string `json:"serverName"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Key == "" || body.McpURL != "http://localhost:8080/mcp" || body.ServerName != "hottell" {
		t.Fatalf("got key set %t, mcpUrl %q, serverName %q; want a key, http://localhost:8080/mcp, hottell",
			body.Key != "", body.McpURL, body.ServerName)
	}
}

func TestNewFailsWithoutRelyingPartyID(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	cfg.WebAuthnRPID = ""
	_, err := ioc.New(pingerFunc(func(context.Context) error { return nil }),
		stores(t, mock.NewMockSessions(gomock.NewController(t))), cfg, "dev", slog.New(slog.DiscardHandler),
		fstest.MapFS{"index.html": {Data: []byte(indexHTML)}})
	if err == nil {
		t.Fatal("got no error, want one for the missing WebAuthn RP ID")
	}
}

const unauthenticated = `{"code":"unauthenticated","message":"Войдите, чтобы продолжить"}` + "\n"

const indexHTML = `<!doctype html><div id="root"></div>`

type pingerFunc func(context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error { return f(ctx) }

// TestIngestBypassesSessionAndOrigin sends OTLP requests to the assembled service and checks
// that the ingest answers them by the collector token alone, past the SPA, the Origin check
// and the session, and passes an accepted one to the collector.
func TestIngestBypassesSessionAndOrigin(t *testing.T) {
	t.Parallel()

	// A well-formed synthetic collector token.
	ingestToken := strings.Repeat("A", 43)
	usedAt := time.Now()
	tests := []struct {
		name          string
		method        string
		authorization string
		wantCode      int
		wantForwarded bool
	}{
		{name: "no token", method: http.MethodPost, wantCode: http.StatusUnauthorized},
		{name: "get", method: http.MethodGet, wantCode: http.StatusMethodNotAllowed},
		{
			name: "collector token from a foreign origin", method: http.MethodPost, authorization: "Bearer " + ingestToken,
			wantCode: http.StatusOK, wantForwarded: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			forwarded := make(chan string, 1)
			collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				forwarded <- r.URL.Path
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(collector.Close)

			ctrl := gomock.NewController(t)
			accessKeys := keysmock.NewMockAccessKeys(ctrl)
			accessKeys.EXPECT().FindByHash(gomock.Any(), gomock.Any()).Return(domain.AccessKey{
				ID: uuid.New(), UserID: uuid.New(), Kind: domain.AccessKeyKindIngest, LastUsedAt: &usedAt,
			}, nil).AnyTimes()
			stores := stores(t, mock.NewMockSessions(ctrl))
			stores.AccessKeys = accessKeys
			cfg := testConfig()
			cfg.CollectorURL = collector.URL
			container, err := ioc.New(pingerFunc(func(context.Context) error { return nil }), stores, cfg, "dev",
				slog.New(slog.DiscardHandler), fstest.MapFS{"index.html": {Data: []byte(indexHTML)}})
			if err != nil {
				t.Fatalf("assemble container: %v", err)
			}

			rec := httptest.NewRecorder()
			// The empty body is a valid, empty ExportLogsServiceRequest.
			req := httptest.NewRequestWithContext(t.Context(), tt.method, "/v1/logs", strings.NewReader(""))
			req.Header.Set("Content-Type", "application/x-protobuf")
			req.Header.Set("Origin", "https://evil.example")
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}
			container.Handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("got %d %q, want %d", rec.Code, rec.Body.String(), tt.wantCode)
			}
			if got := len(forwarded) == 1; got != tt.wantForwarded {
				t.Fatalf("forwarded to the collector: got %v, want %v", got, tt.wantForwarded)
			}
		})
	}
}

func TestMcpBypassesSessionAndOrigin(t *testing.T) {
	t.Parallel()

	// A well-formed synthetic MCP key.
	mcpKey := strings.Repeat("Q", 43)
	usedAt := time.Now()
	tests := []struct {
		name          string
		authorization string
		wantCode      int
	}{
		{name: "no key", wantCode: http.StatusUnauthorized},
		{name: "collector token", authorization: "Bearer " + strings.Repeat("A", 43), wantCode: http.StatusUnauthorized},
		{name: "MCP key from a foreign origin", authorization: "Bearer " + mcpKey, wantCode: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			accessKeys := keysmock.NewMockAccessKeys(ctrl)
			accessKeys.EXPECT().FindByHash(gomock.Any(), gomock.Any()).DoAndReturn(
				func(context.Context, []byte) (domain.AccessKey, error) {
					kind := domain.AccessKeyKindIngest
					if tt.authorization == "Bearer "+mcpKey {
						kind = domain.AccessKeyKindMCP
					}
					return domain.AccessKey{ID: uuid.New(), UserID: uuid.New(), Kind: kind, LastUsedAt: &usedAt}, nil
				}).AnyTimes()
			stores := stores(t, mock.NewMockSessions(ctrl))
			stores.AccessKeys = accessKeys
			container := newContainer(t, stores, slog.New(slog.DiscardHandler),
				fstest.MapFS{"index.html": {Data: []byte(indexHTML)}})

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", strings.NewReader(
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",`+
					`"capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set("Origin", "https://evil.example")
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}
			container.Handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("got %d %q, want %d", rec.Code, rec.Body.String(), tt.wantCode)
			}
			if tt.wantCode == http.StatusOK && !strings.Contains(rec.Body.String(), `"name":"hottell"`) {
				t.Fatalf("initialize: got %q, want the hottell server", rec.Body.String())
			}
		})
	}
}
