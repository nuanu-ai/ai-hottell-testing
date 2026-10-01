package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/handler"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/handler/mock"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/middleware"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/openapi"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

//nolint:gochecknoglobals // a fixed instant and session shared by the tests
var (
	now       = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	userID    = uuid.MustParse("0b6f3c1e-8a52-4a8e-9d4b-5f2a7c1d9e30")
	sessionID = uuid.MustParse("7d1e2f3a-4b5c-4d6e-8f70-819203a4b5c6")
)

const sessionCookie = "ht_session=tok; Path=/; Max-Age=2592000; HttpOnly; SameSite=Lax"

type fixedClock struct{}

func (fixedClock) Now() time.Time { return now }

type resolverFunc func(ctx context.Context, token string) (domain.Session, *time.Time, error)

func (f resolverFunc) Resolve(ctx context.Context, token string) (domain.Session, *time.Time, error) {
	return f(ctx, token)
}

// server serves the contract on auth and sessions as the service does: a request with any
// session cookie is signed in as userID with sessionID.
func server(auth handler.Auth, sessions handler.Sessions) http.Handler {
	return serve(auth, sessions, nil, nil, nil, nil, slog.New(slog.DiscardHandler))
}

// serve serves the contract on auth, sessions, users, passkeys, keys and settings as the
// service does, logging the failed operations to logger: a request with any session cookie
// is signed in as userID with sessionID.
func serve(
	auth handler.Auth, sessions handler.Sessions, users handler.Users, passkeys handler.Passkeys, keys handler.Keys,
	settings handler.Settings, logger *slog.Logger,
) http.Handler {
	cookies := middleware.NewCookies(false, fixedClock{})
	responseError := handler.ResponseError(logger)
	strict := openapi.NewStrictHandlerWithOptions(
		handler.NewAPI("dev", auth, sessions, users, passkeys, keys, settings, cookies, mcpURL),
		[]openapi.StrictMiddlewareFunc{middleware.RequireSession(
			"login", "beginPasskeyLogin", "finishPasskeyLogin", "getInvite", "acceptInvite", "getPasswordReset",
			"completePasswordReset", "getVersion",
		), middleware.EndCeremony(cookies, "finishPasskeyLogin", "finishPasskeyRegistration")},
		openapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  handler.RequestError,
			ResponseErrorHandlerFunc: responseError,
		},
	)
	resolver := resolverFunc(func(context.Context, string) (domain.Session, *time.Time, error) {
		return domain.Session{ID: sessionID, UserID: userID, ExpiresAt: now.Add(domain.SessionTTL)}, nil, nil
	})
	return openapi.HandlerWithOptions(strict, openapi.StdHTTPServerOptions{
		Middlewares:      []openapi.MiddlewareFunc{middleware.Session(resolver, cookies, responseError)},
		ErrorHandlerFunc: handler.RequestError,
	})
}

func do(t *testing.T, srv http.Handler, method, path, body string, signedIn bool) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader = http.NoBody
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "test-agent/1.0")
	if signedIn {
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "tok"})
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func wantError(t *testing.T, rec *httptest.ResponseRecorder, status int, want *domain.Error) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("got status %d, want %d", rec.Code, status)
	}
	var got openapi.Error
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Code != want.Code || got.Message != want.Message {
		t.Fatalf("got error %+v, want %s %q", got, want.Code, want.Message)
	}
}

func TestLogin(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	auth := mock.NewMockAuth(ctrl)
	auth.EXPECT().LoginWithPassword(gomock.Any(), "a@example.com", "secret password", "test-agent/1.0").
		Return("tok", now.Add(domain.SessionTTL), nil)

	rec := do(t, server(auth, mock.NewMockSessions(ctrl)), http.MethodPost, "/auth/login",
		`{"email":"a@example.com","password":"secret password"}`, false)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Values("Set-Cookie"); len(got) != 1 || got[0] != sessionCookie {
		t.Fatalf("got Set-Cookie %q, want [%q]", got, sessionCookie)
	}
}

func TestLoginInvalidCredentials(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	auth := mock.NewMockAuth(ctrl)
	auth.EXPECT().LoginWithPassword(gomock.Any(), "a@example.com", "wrong", "test-agent/1.0").
		Return("", time.Time{}, domain.ErrInvalidCredentials)

	rec := do(t, server(auth, mock.NewMockSessions(ctrl)), http.MethodPost, "/auth/login",
		`{"email":"a@example.com","password":"wrong"}`, false)

	wantError(t, rec, http.StatusUnauthorized, domain.ErrInvalidCredentials)
	if got := rec.Header().Get("Set-Cookie"); got != "" {
		t.Fatalf("got Set-Cookie %q, want none", got)
	}
}

func TestLogout(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	sessions := mock.NewMockSessions(ctrl)
	sessions.EXPECT().Close(gomock.Any(), sessionID).Return(nil)

	rec := do(t, server(mock.NewMockAuth(ctrl), sessions), http.MethodPost, "/auth/logout", "", true)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusNoContent)
	}
	want := "ht_session=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax"
	if got := rec.Header().Values("Set-Cookie"); len(got) != 1 || got[0] != want {
		t.Fatalf("got Set-Cookie %q, want [%q]", got, want)
	}
}

func TestGetMe(t *testing.T) {
	t.Parallel()

	email, err := domain.ParseEmail("a@example.com")
	if err != nil {
		t.Fatal(err)
	}
	name, err := domain.ParseUserName("Алва")
	if err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	auth := mock.NewMockAuth(ctrl)
	auth.EXPECT().Me(gomock.Any(), userID).Return(domain.User{ID: userID, Email: email, Name: name, HasPassword: true}, nil)

	rec := do(t, server(auth, mock.NewMockSessions(ctrl)), http.MethodGet, "/me", "", true)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusOK)
	}
	want := fmt.Sprintf(`{"email":"a@example.com","id":"%s","name":"Алва"}`, userID) + "\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("got body %q, want %q", got, want)
	}
}

func TestClosedOperationsWithoutSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPost, path: "/auth/logout"},
		{method: http.MethodGet, path: "/me"},
		{method: http.MethodPost, path: "/me/password", body: `{"currentPassword":"a","newPassword":"b"}`},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			rec := do(t, server(mock.NewMockAuth(ctrl), mock.NewMockSessions(ctrl)), tt.method, tt.path, tt.body, false)

			wantError(t, rec, http.StatusUnauthorized, domain.ErrUnauthenticated)
		})
	}
}

func TestChangePassword(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	auth := mock.NewMockAuth(ctrl)
	auth.EXPECT().ChangePassword(gomock.Any(), userID, sessionID, "old password", "new password").Return(nil)

	rec := do(t, server(auth, mock.NewMockSessions(ctrl)), http.MethodPost, "/me/password",
		`{"currentPassword":"old password","newPassword":"new password"}`, true)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestChangePasswordErrors(t *testing.T) {
	t.Parallel()

	for _, domainErr := range []*domain.Error{
		domain.ErrWrongCurrentPassword, domain.ErrPasswordTooShort, domain.ErrPasswordTooLong,
	} {
		t.Run(domainErr.Code, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			auth := mock.NewMockAuth(ctrl)
			auth.EXPECT().ChangePassword(gomock.Any(), userID, sessionID, "old password", "next").Return(domainErr)

			rec := do(t, server(auth, mock.NewMockSessions(ctrl)), http.MethodPost, "/me/password",
				`{"currentPassword":"old password","newPassword":"next"}`, true)

			wantError(t, rec, http.StatusBadRequest, domainErr)
		})
	}
}
