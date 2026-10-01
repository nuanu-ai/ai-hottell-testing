package handler_test

import (
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

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/handler/mock"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/middleware"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

//nolint:gochecknoglobals // a fixed ceremony and passkey shared by the tests
var (
	ceremonyID = uuid.MustParse("3a9c1d2e-5f60-4718-9a2b-3c4d5e6f7081")
	passkeyID  = uuid.MustParse("9e8d7c6b-5a49-4382-a716-f5e4d3c2b1a0")
)

const (
	optionsJSON     = `{"challenge":"Y2hhbGxlbmdl","rpId":"localhost","timeout":300000}`
	credentialJSON  = `{"id":"Y3JlZA","rawId":"Y3JlZA","type":"public-key"}`
	ceremonyCookie  = "ht_webauthn=3a9c1d2e-5f60-4718-9a2b-3c4d5e6f7081; Path=/api; Max-Age=300; HttpOnly; SameSite=Strict"
	expiredCeremony = "ht_webauthn=; Path=/api; Max-Age=0; HttpOnly; SameSite=Strict"
)

// passkeysServer serves the contract on passkeys.
func passkeysServer(t *testing.T, passkeys *mock.MockPasskeys) http.Handler {
	t.Helper()
	ctrl := gomock.NewController(t)
	return serve(mock.NewMockAuth(ctrl), mock.NewMockSessions(ctrl), mock.NewMockUsers(ctrl), passkeys,
		mock.NewMockKeys(ctrl), nil, slog.New(slog.DiscardHandler))
}

// doCeremony sends a request as do does, carrying the ceremony cookie with ceremony
// unless it is empty.
func doCeremony(
	t *testing.T, srv http.Handler, method, path, body string, signedIn bool, ceremony string,
) *httptest.ResponseRecorder {
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
	if ceremony != "" {
		req.AddCookie(&http.Cookie{Name: middleware.CeremonyCookieName, Value: ceremony})
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func wantCookies(t *testing.T, rec *httptest.ResponseRecorder, want ...string) {
	t.Helper()
	got := rec.Header().Values("Set-Cookie")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got Set-Cookie %q, want %q", got, want)
	}
}

func TestBeginPasskeyLogin(t *testing.T) {
	t.Parallel()

	passkeys := mock.NewMockPasskeys(gomock.NewController(t))
	passkeys.EXPECT().BeginLogin(gomock.Any()).Return(ceremonyID, []byte(optionsJSON), nil)

	rec := doCeremony(t, passkeysServer(t, passkeys), http.MethodPost, "/auth/passkey/login/begin", `{}`, false, "")

	wantBody(t, rec, http.StatusOK, `{"options":`+optionsJSON+`}`)
	wantCookies(t, rec, ceremonyCookie)
}

func TestFinishPasskeyLogin(t *testing.T) {
	t.Parallel()

	passkeys := mock.NewMockPasskeys(gomock.NewController(t))
	passkeys.EXPECT().FinishLogin(gomock.Any(), ceremonyID, []byte(credentialJSON), "test-agent/1.0").
		Return("tok", now.Add(domain.SessionTTL), nil)

	rec := doCeremony(t, passkeysServer(t, passkeys), http.MethodPost, "/auth/passkey/login/finish",
		`{"credential":`+credentialJSON+`}`, false, ceremonyID.String())

	wantBody(t, rec, http.StatusNoContent, "")
	wantCookies(t, rec, expiredCeremony, sessionCookie)
}

func TestFinishPasskeyLoginFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ceremony string
		err      error
		want     *domain.Error
	}{
		{
			name: "verification failed", ceremony: ceremonyID.String(),
			err: fmt.Errorf("finish login: %w", domain.ErrPasskeyVerificationFailed), want: domain.ErrPasskeyVerificationFailed,
		},
		{
			name: "ceremony expired", ceremony: ceremonyID.String(),
			err: fmt.Errorf("take ceremony: %w", domain.ErrCeremonyNotFound), want: domain.ErrPasskeyCeremonyExpired,
		},
		{name: "no ceremony cookie", want: domain.ErrPasskeyCeremonyExpired},
		{name: "malformed ceremony cookie", ceremony: "not-a-ceremony", want: domain.ErrPasskeyCeremonyExpired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			passkeys := mock.NewMockPasskeys(gomock.NewController(t))
			if tt.err != nil {
				passkeys.EXPECT().FinishLogin(gomock.Any(), ceremonyID, gomock.Any(), gomock.Any()).
					Return("", time.Time{}, tt.err)
			}

			rec := doCeremony(t, passkeysServer(t, passkeys), http.MethodPost, "/auth/passkey/login/finish",
				`{"credential":`+credentialJSON+`}`, false, tt.ceremony)

			wantError(t, rec, http.StatusBadRequest, tt.want)
			// The ceremony is used up either way, so its cookie goes too.
			wantCookies(t, rec, expiredCeremony)
		})
	}
}

func TestListMyPasskeys(t *testing.T) {
	t.Parallel()

	usedAt := now.Add(-time.Minute)
	passkeys := mock.NewMockPasskeys(gomock.NewController(t))
	passkeys.EXPECT().List(gomock.Any(), userID).Return([]domain.Passkey{
		{ID: passkeyID, UserID: userID, Name: "Ноутбук", CreatedAt: now.Add(-time.Hour), LastUsedAt: &usedAt},
		{ID: ceremonyID, UserID: userID, Name: "Телефон", CreatedAt: now.Add(-time.Minute)},
	}, nil)

	rec := doCeremony(t, passkeysServer(t, passkeys), http.MethodGet, "/me/passkeys", "", true, "")

	wantBody(t, rec, http.StatusOK, `{"items":[`+
		`{"createdAt":"2026-09-30T11:00:00Z","id":"`+passkeyID.String()+`","lastUsedAt":"2026-09-30T11:59:00Z","name":"Ноутбук"},`+
		`{"createdAt":"2026-09-30T11:59:00Z","id":"`+ceremonyID.String()+`","lastUsedAt":null,"name":"Телефон"}]}`)
}

func TestListMyPasskeysEmpty(t *testing.T) {
	t.Parallel()

	passkeys := mock.NewMockPasskeys(gomock.NewController(t))
	passkeys.EXPECT().List(gomock.Any(), userID).Return(nil, nil)

	rec := doCeremony(t, passkeysServer(t, passkeys), http.MethodGet, "/me/passkeys", "", true, "")

	wantBody(t, rec, http.StatusOK, `{"items":[]}`)
}

func TestBeginPasskeyRegistration(t *testing.T) {
	t.Parallel()

	passkeys := mock.NewMockPasskeys(gomock.NewController(t))
	passkeys.EXPECT().BeginRegistration(gomock.Any(), userID, "Ноутбук").Return(ceremonyID, []byte(optionsJSON), nil)

	rec := doCeremony(t, passkeysServer(t, passkeys), http.MethodPost, "/me/passkeys/register/begin",
		`{"name":"Ноутбук"}`, true, "")

	wantBody(t, rec, http.StatusOK, `{"options":`+optionsJSON+`}`)
	wantCookies(t, rec, ceremonyCookie)
}

func TestBeginPasskeyRegistrationInvalidName(t *testing.T) {
	t.Parallel()

	passkeys := mock.NewMockPasskeys(gomock.NewController(t))
	passkeys.EXPECT().BeginRegistration(gomock.Any(), userID, "").Return(uuid.Nil, nil, domain.ErrInvalidPasskeyName)

	rec := doCeremony(t, passkeysServer(t, passkeys), http.MethodPost, "/me/passkeys/register/begin",
		`{"name":""}`, true, "")

	wantError(t, rec, http.StatusBadRequest, domain.ErrInvalidPasskeyName)
	wantCookies(t, rec)
}

func TestFinishPasskeyRegistration(t *testing.T) {
	t.Parallel()

	passkeys := mock.NewMockPasskeys(gomock.NewController(t))
	passkeys.EXPECT().FinishRegistration(gomock.Any(), userID, ceremonyID, []byte(credentialJSON)).
		Return(domain.Passkey{ID: passkeyID, UserID: userID, Name: "Ноутбук", CreatedAt: now}, nil)

	rec := doCeremony(t, passkeysServer(t, passkeys), http.MethodPost, "/me/passkeys/register/finish",
		`{"credential":`+credentialJSON+`}`, true, ceremonyID.String())

	wantBody(t, rec, http.StatusCreated,
		`{"createdAt":"2026-09-30T12:00:00Z","id":"`+passkeyID.String()+`","lastUsedAt":null,"name":"Ноутбук"}`)
	wantCookies(t, rec, expiredCeremony)
}

func TestFinishPasskeyRegistrationFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ceremony string
		err      error
		want     *domain.Error
	}{
		{
			name: "verification failed", ceremony: ceremonyID.String(),
			err:  fmt.Errorf("finish registration: %w", domain.ErrPasskeyVerificationFailed),
			want: domain.ErrPasskeyVerificationFailed,
		},
		{name: "ceremony expired", ceremony: ceremonyID.String(), err: domain.ErrCeremonyNotFound, want: domain.ErrPasskeyCeremonyExpired},
		{name: "no ceremony cookie", want: domain.ErrPasskeyCeremonyExpired},
		{name: "malformed ceremony cookie", ceremony: "not-a-ceremony", want: domain.ErrPasskeyCeremonyExpired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			passkeys := mock.NewMockPasskeys(gomock.NewController(t))
			if tt.err != nil {
				passkeys.EXPECT().FinishRegistration(gomock.Any(), userID, ceremonyID, gomock.Any()).
					Return(domain.Passkey{}, tt.err)
			}

			rec := doCeremony(t, passkeysServer(t, passkeys), http.MethodPost, "/me/passkeys/register/finish",
				`{"credential":`+credentialJSON+`}`, true, tt.ceremony)

			wantError(t, rec, http.StatusBadRequest, tt.want)
			// The ceremony is used up either way, so its cookie goes too.
			wantCookies(t, rec, expiredCeremony)
		})
	}
}

func TestDeleteMyPasskey(t *testing.T) {
	t.Parallel()

	passkeys := mock.NewMockPasskeys(gomock.NewController(t))
	passkeys.EXPECT().Delete(gomock.Any(), userID, passkeyID).Return(nil)

	rec := doCeremony(t, passkeysServer(t, passkeys), http.MethodDelete, "/me/passkeys/"+passkeyID.String(), "", true, "")

	wantBody(t, rec, http.StatusNoContent, "")
}

func TestDeleteMyPasskeyNotFound(t *testing.T) {
	t.Parallel()

	passkeys := mock.NewMockPasskeys(gomock.NewController(t))
	passkeys.EXPECT().Delete(gomock.Any(), userID, passkeyID).Return(fmt.Errorf("delete passkey: %w", domain.ErrPasskeyNotFound))

	rec := doCeremony(t, passkeysServer(t, passkeys), http.MethodDelete, "/me/passkeys/"+passkeyID.String(), "", true, "")

	wantError(t, rec, http.StatusNotFound, domain.ErrPasskeyNotFound)
}

func TestPasskeyOperationsNeedSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/me/passkeys"},
		{method: http.MethodPost, path: "/me/passkeys/register/begin", body: `{"name":"Ноутбук"}`},
		{method: http.MethodPost, path: "/me/passkeys/register/finish", body: `{"credential":` + credentialJSON + `}`},
		{method: http.MethodDelete, path: "/me/passkeys/" + passkeyID.String()},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			t.Parallel()

			// The mock expects no call: the operation is refused before the use case.
			passkeys := mock.NewMockPasskeys(gomock.NewController(t))
			rec := doCeremony(t, passkeysServer(t, passkeys), tt.method, tt.path, tt.body, false, ceremonyID.String())

			wantError(t, rec, http.StatusUnauthorized, domain.ErrUnauthenticated)
		})
	}
}
