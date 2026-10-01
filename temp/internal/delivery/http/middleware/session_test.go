package middleware_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/handler"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/middleware"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/openapi"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

//nolint:gochecknoglobals // a fixed instant shared by the tests
var now = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return now }

type resolverFunc func(ctx context.Context, token string) (domain.Session, *time.Time, error)

func (f resolverFunc) Resolve(ctx context.Context, token string) (domain.Session, *time.Time, error) {
	return f(ctx, token)
}

func TestSetSessionCookie(t *testing.T) {
	t.Parallel()

	for _, secure := range []bool{false, true} {
		rec := httptest.NewRecorder()
		middleware.NewCookies(secure, fixedClock{}).SetSessionCookie(rec, "tok", now.Add(domain.SessionTTL))

		want := "ht_session=tok; Path=/; Max-Age=2592000; HttpOnly; SameSite=Lax"
		if secure {
			want = "ht_session=tok; Path=/; Max-Age=2592000; HttpOnly; Secure; SameSite=Lax"
		}
		if got := rec.Header().Get("Set-Cookie"); got != want {
			t.Fatalf("secure=%v: got Set-Cookie %q, want %q", secure, got, want)
		}
	}
}

func TestCeremonyCookie(t *testing.T) {
	t.Parallel()

	for _, secure := range []bool{false, true} {
		cookies := middleware.NewCookies(secure, fixedClock{})
		flag := ""
		if secure {
			flag = " Secure;"
		}
		tests := []struct {
			got  *http.Cookie
			want string
		}{
			{got: cookies.CeremonyCookie("cer"), want: "ht_webauthn=cer; Path=/api; Max-Age=300; HttpOnly;" + flag + " SameSite=Strict"},
			{got: cookies.ExpiredCeremonyCookie(), want: "ht_webauthn=; Path=/api; Max-Age=0; HttpOnly;" + flag + " SameSite=Strict"},
		}
		for _, tt := range tests {
			if got := tt.got.String(); got != tt.want {
				t.Fatalf("secure=%v: got cookie %q, want %q", secure, got, tt.want)
			}
		}
	}
}

func TestClearSessionCookie(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	middleware.NewCookies(false, fixedClock{}).ClearSessionCookie(rec)

	want := "ht_session=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax"
	if got := rec.Header().Get("Set-Cookie"); got != want {
		t.Fatalf("got Set-Cookie %q, want %q", got, want)
	}
}

func TestSession(t *testing.T) {
	t.Parallel()

	userID := uuid.New()
	sessionID := uuid.New()
	session := domain.Session{ID: sessionID, UserID: userID, ExpiresAt: now.Add(29 * 24 * time.Hour)}
	extended := now.Add(domain.SessionTTL)
	errDown := errors.New("database down")

	tests := []struct {
		name          string
		cookie        string
		resolveErr    error
		expiresAt     *time.Time
		wantCode      int
		wantSetCookie string
		wantSession   bool
	}{
		{name: "no cookie", wantCode: http.StatusOK},
		{name: "live session", cookie: "tok", wantCode: http.StatusOK, wantSession: true},
		{
			name: "extended session", cookie: "tok", expiresAt: &extended, wantCode: http.StatusOK, wantSession: true,
			wantSetCookie: "ht_session=tok; Path=/; Max-Age=2592000; HttpOnly; SameSite=Lax",
		},
		{
			name: "no live session", cookie: "stale", resolveErr: domain.ErrSessionNotFound, wantCode: http.StatusOK,
			wantSetCookie: "ht_session=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax",
		},
		{
			name: "malformed cookie", cookie: "%%%", resolveErr: domain.ErrUnauthenticated, wantCode: http.StatusOK,
			wantSetCookie: "ht_session=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax",
		},
		{name: "store failure", cookie: "tok", resolveErr: errDown, wantCode: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resolver := resolverFunc(func(_ context.Context, token string) (domain.Session, *time.Time, error) {
				if token != tt.cookie {
					t.Errorf("resolved token %q, want %q", token, tt.cookie)
				}
				if tt.resolveErr != nil {
					return domain.Session{}, nil, tt.resolveErr
				}
				return session, tt.expiresAt, nil
			})
			var gotUser, gotSession uuid.UUID
			var gotOK bool
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotUser, gotOK = middleware.UserID(r.Context())
				gotSession, _ = middleware.SessionID(r.Context())
				w.WriteHeader(http.StatusOK)
			})
			mw := middleware.Session(resolver, middleware.NewCookies(false, fixedClock{}), handler.ResponseError(slog.New(slog.DiscardHandler)))

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/me", http.NoBody)
			if tt.cookie != "" {
				req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: tt.cookie})
			}
			rec := httptest.NewRecorder()
			mw(next).ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("got status %d, want %d", rec.Code, tt.wantCode)
			}
			if got := rec.Header().Get("Set-Cookie"); got != tt.wantSetCookie {
				t.Fatalf("got Set-Cookie %q, want %q", got, tt.wantSetCookie)
			}
			if gotOK != tt.wantSession {
				t.Fatalf("got session in context %v, want %v", gotOK, tt.wantSession)
			}
			if tt.wantSession && (gotUser != userID || gotSession != sessionID) {
				t.Fatalf("got user %s session %s, want %s %s", gotUser, gotSession, userID, sessionID)
			}
		})
	}
}

func TestRequireSession(t *testing.T) {
	t.Parallel()

	signedIn := func(t *testing.T) context.Context {
		t.Helper()
		var ctx context.Context
		resolver := resolverFunc(func(context.Context, string) (domain.Session, *time.Time, error) {
			return domain.Session{ID: uuid.New(), UserID: uuid.New()}, nil, nil
		})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "tok"})
		middleware.Session(resolver, middleware.NewCookies(false, fixedClock{}), nil)(
			http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { ctx = r.Context() }),
		).ServeHTTP(httptest.NewRecorder(), req)
		return ctx
	}

	tests := []struct {
		name        string
		operationID string
		signedIn    bool
		wantCode    int
	}{
		{name: "closed without session", operationID: "GetMe", wantCode: http.StatusUnauthorized},
		{name: "closed with session", operationID: "GetMe", signedIn: true, wantCode: http.StatusOK},
		{name: "open without session", operationID: "Login", wantCode: http.StatusOK},
		{name: "open spelled as the contract", operationID: "getVersion", wantCode: http.StatusOK},
		{name: "unlisted is closed", operationID: "RevokeInvite", wantCode: http.StatusUnauthorized},
	}

	require := middleware.RequireSession("login", "getVersion")
	respondError := handler.ResponseError(slog.New(slog.DiscardHandler))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tt.signedIn {
				ctx = signedIn(t)
			}
			op := require(func(context.Context, http.ResponseWriter, *http.Request, any) (any, error) {
				return "ok", nil
			}, tt.operationID)
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/me", http.NoBody)

			if _, err := op(ctx, rec, req, nil); err != nil {
				respondError(rec, req, err)
			}

			if rec.Code != tt.wantCode {
				t.Fatalf("got status %d, want %d", rec.Code, tt.wantCode)
			}
			if tt.wantCode != http.StatusUnauthorized {
				return
			}
			var got openapi.Error
			if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if got.Code != "unauthenticated" {
				t.Fatalf("got code %q, want unauthenticated", got.Code)
			}
		})
	}
}
