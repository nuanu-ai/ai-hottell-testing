package handler_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/handler/mock"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

//nolint:gochecknoglobals // fixed users and links shared by the tests
var (
	otherID   = uuid.MustParse("5c2d9e41-3f6a-4b7c-8d9e-0a1b2c3d4e5f")
	expiresAt = now.Add(domain.LinkTTL)
)

// linkToken stands for the token of a one-time link in a path.
const linkToken = "tokenFromThePathXYZ"

// usersServer serves the contract on users; failed operations are logged to logs.
func usersServer(t *testing.T, users *mock.MockUsers, logs *bytes.Buffer) http.Handler {
	t.Helper()
	ctrl := gomock.NewController(t)
	return serve(mock.NewMockAuth(ctrl), mock.NewMockSessions(ctrl), users, nil, nil, nil,
		slog.New(slog.NewJSONHandler(logs, nil)))
}

func userView(t *testing.T, id uuid.UUID, email, name string, status domain.UserStatus) domain.UserView {
	t.Helper()
	parsedEmail, err := domain.ParseEmail(email)
	if err != nil {
		t.Fatal(err)
	}
	parsedName, err := domain.ParseUserName(name)
	if err != nil {
		t.Fatal(err)
	}
	return domain.UserView{ID: id, Email: parsedEmail, Name: parsedName, Status: status, CreatedAt: now.Add(-time.Hour)}
}

func wantBody(t *testing.T, rec *httptest.ResponseRecorder, status int, want string) {
	t.Helper()
	if want != "" {
		want += "\n"
	}
	if rec.Code != status || rec.Body.String() != want {
		t.Fatalf("got %d %q, want %d %q", rec.Code, rec.Body.String(), status, want)
	}
}

func TestListUsers(t *testing.T) {
	t.Parallel()

	lastLogin := now.Add(-time.Minute)
	me := userView(t, userID, "a@example.com", "Алва", domain.UserStatusActive)
	me.LastLoginAt = &lastLogin
	invited := userView(t, otherID, "b@example.com", "Бета", domain.UserStatusInvited)
	invited.InviteExpiresAt = &expiresAt
	users := mock.NewMockUsers(gomock.NewController(t))
	users.EXPECT().List(gomock.Any()).Return([]domain.UserView{me, invited}, nil)

	var logs bytes.Buffer
	rec := do(t, usersServer(t, users, &logs), http.MethodGet, "/users", "", true)

	wantBody(t, rec, http.StatusOK, fmt.Sprintf(`{"items":[`+
		`{"createdAt":"2026-09-30T11:00:00Z","email":"a@example.com","id":"%s","inviteExpiresAt":null,"isMe":true,`+
		`"lastLoginAt":"2026-09-30T11:59:00Z","name":"Алва","status":"active"},`+
		`{"createdAt":"2026-09-30T11:00:00Z","email":"b@example.com","id":"%s","inviteExpiresAt":"2026-10-07T12:00:00Z",`+
		`"isMe":false,"lastLoginAt":null,"name":"Бета","status":"invited"}]}`, userID, otherID))
}

func TestInviteUser(t *testing.T) {
	t.Parallel()

	invited := userView(t, otherID, "b@example.com", "Бета", domain.UserStatusInvited)
	invited.InviteExpiresAt = &expiresAt
	users := mock.NewMockUsers(gomock.NewController(t))
	users.EXPECT().Invite(gomock.Any(), userID, "B@example.com", "Бета").
		Return(invited, "http://localhost:8080/invite/tok", nil)

	var logs bytes.Buffer
	rec := do(t, usersServer(t, users, &logs), http.MethodPost, "/users", `{"email":"B@example.com","name":"Бета"}`, true)

	wantBody(t, rec, http.StatusCreated, fmt.Sprintf(`{"expiresAt":"2026-10-07T12:00:00Z",`+
		`"inviteUrl":"http://localhost:8080/invite/tok","user":{"createdAt":"2026-09-30T11:00:00Z",`+
		`"email":"b@example.com","id":"%s","inviteExpiresAt":"2026-10-07T12:00:00Z","isMe":false,`+
		`"lastLoginAt":null,"name":"Бета","status":"invited"}}`, otherID))
}

func TestReissueInvite(t *testing.T) {
	t.Parallel()

	users := mock.NewMockUsers(gomock.NewController(t))
	users.EXPECT().ReissueInvite(gomock.Any(), userID, otherID).Return("http://localhost:8080/invite/new", expiresAt, nil)

	var logs bytes.Buffer
	rec := do(t, usersServer(t, users, &logs), http.MethodPost, "/users/"+otherID.String()+"/invite", "", true)

	wantBody(t, rec, http.StatusOK, `{"expiresAt":"2026-10-07T12:00:00Z","inviteUrl":"http://localhost:8080/invite/new"}`)
}

func TestRevokeInvite(t *testing.T) {
	t.Parallel()

	users := mock.NewMockUsers(gomock.NewController(t))
	users.EXPECT().RevokeInvite(gomock.Any(), otherID).Return(nil)

	var logs bytes.Buffer
	rec := do(t, usersServer(t, users, &logs), http.MethodDelete, "/users/"+otherID.String(), "", true)

	wantBody(t, rec, http.StatusNoContent, "")
}

func TestIssuePasswordReset(t *testing.T) {
	t.Parallel()

	users := mock.NewMockUsers(gomock.NewController(t))
	users.EXPECT().IssueReset(gomock.Any(), userID, otherID).Return("http://localhost:8080/reset/tok", expiresAt, nil)

	var logs bytes.Buffer
	rec := do(t, usersServer(t, users, &logs), http.MethodPost, "/users/"+otherID.String()+"/password-reset", "", true)

	wantBody(t, rec, http.StatusOK, `{"expiresAt":"2026-10-07T12:00:00Z","resetUrl":"http://localhost:8080/reset/tok"}`)
}

func TestLookupLinks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		kind domain.LinkKind
	}{
		{path: "/invites/" + linkToken, kind: domain.LinkKindInvite},
		{path: "/password-resets/" + linkToken, kind: domain.LinkKindReset},
	}

	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			t.Parallel()

			users := mock.NewMockUsers(gomock.NewController(t))
			users.EXPECT().LookupLink(gomock.Any(), linkToken, tt.kind).Return("b@example.com", "Бета", nil)

			var logs bytes.Buffer
			rec := do(t, usersServer(t, users, &logs), http.MethodGet, tt.path, "", false)

			wantBody(t, rec, http.StatusOK, `{"email":"b@example.com","name":"Бета"}`)
		})
	}
}

func TestSpendLinks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		path   string
		expect func(users *mock.MockUsers) *gomock.Call
	}{
		{
			name: "accept invite", path: "/invites/" + linkToken + "/accept",
			expect: func(users *mock.MockUsers) *gomock.Call {
				return users.EXPECT().AcceptInvite(gomock.Any(), linkToken, "new password", "test-agent/1.0")
			},
		},
		{
			name: "complete reset", path: "/password-resets/" + linkToken + "/complete",
			expect: func(users *mock.MockUsers) *gomock.Call {
				return users.EXPECT().CompleteReset(gomock.Any(), linkToken, "new password", "test-agent/1.0")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			users := mock.NewMockUsers(gomock.NewController(t))
			tt.expect(users).Return("tok", now.Add(domain.SessionTTL), nil)

			var logs bytes.Buffer
			rec := do(t, usersServer(t, users, &logs), http.MethodPost, tt.path, `{"password":"new password"}`, false)

			if rec.Code != http.StatusNoContent {
				t.Fatalf("got status %d, want %d", rec.Code, http.StatusNoContent)
			}
			if got := rec.Header().Values("Set-Cookie"); len(got) != 1 || got[0] != sessionCookie {
				t.Fatalf("got Set-Cookie %q, want [%q]", got, sessionCookie)
			}
		})
	}
}

// TestUsersOperationErrors answers every error of the contract table with its status and
// body, and never logs a domain error.
func TestUsersOperationErrors(t *testing.T) {
	t.Parallel()

	userPath := "/users/" + otherID.String()
	tests := []struct {
		name     string
		method   string
		path     string
		body     string
		signedIn bool
		expect   func(users *mock.MockUsers, err error)
		status   int
		errs     []*domain.Error
	}{
		{
			name: "invite user", method: http.MethodPost, path: "/users", body: `{"email":"b","name":""}`, signedIn: true,
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().Invite(gomock.Any(), userID, "b", "").Return(domain.UserView{}, "", err)
			},
			status: http.StatusBadRequest, errs: []*domain.Error{domain.ErrInvalidEmail, domain.ErrInvalidName},
		},
		{
			name: "invite user", method: http.MethodPost, path: "/users", body: `{"email":"b","name":""}`, signedIn: true,
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().Invite(gomock.Any(), userID, "b", "").Return(domain.UserView{}, "", err)
			},
			status: http.StatusConflict, errs: []*domain.Error{domain.ErrEmailTaken},
		},
		{
			name: "reissue invite", method: http.MethodPost, path: userPath + "/invite", signedIn: true,
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().ReissueInvite(gomock.Any(), userID, otherID).Return("", time.Time{}, err)
			},
			status: http.StatusNotFound, errs: []*domain.Error{domain.ErrUserNotFound},
		},
		{
			name: "reissue invite", method: http.MethodPost, path: userPath + "/invite", signedIn: true,
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().ReissueInvite(gomock.Any(), userID, otherID).Return("", time.Time{}, err)
			},
			status: http.StatusConflict, errs: []*domain.Error{domain.ErrUserNotInvited},
		},
		{
			name: "revoke invite", method: http.MethodDelete, path: userPath, signedIn: true,
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().RevokeInvite(gomock.Any(), otherID).Return(err)
			},
			status: http.StatusNotFound, errs: []*domain.Error{domain.ErrUserNotFound},
		},
		{
			name: "revoke invite", method: http.MethodDelete, path: userPath, signedIn: true,
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().RevokeInvite(gomock.Any(), otherID).Return(err)
			},
			status: http.StatusConflict, errs: []*domain.Error{domain.ErrUserNotInvited},
		},
		{
			name: "issue reset", method: http.MethodPost, path: userPath + "/password-reset", signedIn: true,
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().IssueReset(gomock.Any(), userID, otherID).Return("", time.Time{}, err)
			},
			status: http.StatusNotFound, errs: []*domain.Error{domain.ErrUserNotFound},
		},
		{
			name: "issue reset", method: http.MethodPost, path: userPath + "/password-reset", signedIn: true,
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().IssueReset(gomock.Any(), userID, otherID).Return("", time.Time{}, err)
			},
			status: http.StatusConflict, errs: []*domain.Error{domain.ErrUserNotActive, domain.ErrCannotResetSelf},
		},
		{
			name: "get invite", method: http.MethodGet, path: "/invites/" + linkToken,
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().LookupLink(gomock.Any(), linkToken, domain.LinkKindInvite).Return("", "", err)
			},
			status: http.StatusNotFound, errs: []*domain.Error{domain.ErrLinkNotFound},
		},
		{
			name: "get invite", method: http.MethodGet, path: "/invites/" + linkToken,
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().LookupLink(gomock.Any(), linkToken, domain.LinkKindInvite).Return("", "", err)
			},
			status: http.StatusGone, errs: []*domain.Error{domain.ErrLinkUsed, domain.ErrLinkExpired},
		},
		{
			name: "accept invite", method: http.MethodPost, path: "/invites/" + linkToken + "/accept", body: `{"password":"p"}`,
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().AcceptInvite(gomock.Any(), linkToken, "p", "test-agent/1.0").Return("", time.Time{}, err)
			},
			status: http.StatusBadRequest, errs: []*domain.Error{domain.ErrPasswordTooShort, domain.ErrPasswordTooLong},
		},
		{
			name: "accept invite", method: http.MethodPost, path: "/invites/" + linkToken + "/accept", body: `{"password":"p"}`,
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().AcceptInvite(gomock.Any(), linkToken, "p", "test-agent/1.0").Return("", time.Time{}, err)
			},
			status: http.StatusNotFound, errs: []*domain.Error{domain.ErrLinkNotFound},
		},
		{
			name: "accept invite", method: http.MethodPost, path: "/invites/" + linkToken + "/accept", body: `{"password":"p"}`,
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().AcceptInvite(gomock.Any(), linkToken, "p", "test-agent/1.0").Return("", time.Time{}, err)
			},
			status: http.StatusGone, errs: []*domain.Error{domain.ErrLinkUsed, domain.ErrLinkExpired},
		},
		{
			name: "get password reset", method: http.MethodGet, path: "/password-resets/" + linkToken,
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().LookupLink(gomock.Any(), linkToken, domain.LinkKindReset).Return("", "", err)
			},
			status: http.StatusNotFound, errs: []*domain.Error{domain.ErrLinkNotFound},
		},
		{
			name: "get password reset", method: http.MethodGet, path: "/password-resets/" + linkToken,
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().LookupLink(gomock.Any(), linkToken, domain.LinkKindReset).Return("", "", err)
			},
			status: http.StatusGone, errs: []*domain.Error{domain.ErrLinkUsed, domain.ErrLinkExpired},
		},
		{
			name: "complete reset", method: http.MethodPost, path: "/password-resets/" + linkToken + "/complete",
			body: `{"password":"p"}`,
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().CompleteReset(gomock.Any(), linkToken, "p", "test-agent/1.0").Return("", time.Time{}, err)
			},
			status: http.StatusBadRequest, errs: []*domain.Error{domain.ErrPasswordTooShort, domain.ErrPasswordTooLong},
		},
		{
			name: "complete reset", method: http.MethodPost, path: "/password-resets/" + linkToken + "/complete",
			body: `{"password":"p"}`,
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().CompleteReset(gomock.Any(), linkToken, "p", "test-agent/1.0").Return("", time.Time{}, err)
			},
			status: http.StatusNotFound, errs: []*domain.Error{domain.ErrLinkNotFound},
		},
		{
			name: "complete reset", method: http.MethodPost, path: "/password-resets/" + linkToken + "/complete",
			body: `{"password":"p"}`,
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().CompleteReset(gomock.Any(), linkToken, "p", "test-agent/1.0").Return("", time.Time{}, err)
			},
			status: http.StatusGone, errs: []*domain.Error{domain.ErrLinkUsed, domain.ErrLinkExpired},
		},
	}

	for _, tt := range tests {
		for _, domainErr := range tt.errs {
			t.Run(tt.name+" "+domainErr.Code, func(t *testing.T) {
				t.Parallel()

				users := mock.NewMockUsers(gomock.NewController(t))
				// Wrapped as a use case returns it.
				tt.expect(users, fmt.Errorf("use case: %w", domainErr))

				var logs bytes.Buffer
				rec := do(t, usersServer(t, users, &logs), tt.method, tt.path, tt.body, tt.signedIn)

				wantError(t, rec, tt.status, domainErr)
				if rec.Header().Get("Set-Cookie") != "" {
					t.Fatalf("got Set-Cookie %q, want none", rec.Header().Get("Set-Cookie"))
				}
				if logs.Len() != 0 {
					t.Fatalf("a domain error was logged: %s", logs.String())
				}
			})
		}
	}
}

func TestUsersOperationsWithoutSession(t *testing.T) {
	t.Parallel()

	userPath := "/users/" + otherID.String()
	tests := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/users"},
		{method: http.MethodPost, path: "/users", body: `{"email":"b@example.com","name":"Бета"}`},
		{method: http.MethodPost, path: userPath + "/invite"},
		{method: http.MethodDelete, path: userPath},
		{method: http.MethodPost, path: userPath + "/password-reset"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			t.Parallel()

			var logs bytes.Buffer
			rec := do(t, usersServer(t, mock.NewMockUsers(gomock.NewController(t)), &logs), tt.method, tt.path, tt.body, false)

			wantError(t, rec, http.StatusUnauthorized, domain.ErrUnauthenticated)
		})
	}
}

// TestLinkTokenStaysOutOfLogs fails every link operation with an internal error, which is
// logged, and checks that the log names the route with {token} and never the token.
func TestLinkTokenStaysOutOfLogs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method string
		path   string
		body   string
		route  string
		expect func(users *mock.MockUsers, err error)
	}{
		{
			method: http.MethodGet, path: "/invites/" + linkToken, route: "GET /invites/{token}",
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().LookupLink(gomock.Any(), linkToken, domain.LinkKindInvite).Return("", "", err)
			},
		},
		{
			method: http.MethodPost, path: "/invites/" + linkToken + "/accept", body: `{"password":"new password"}`,
			route: "POST /invites/{token}/accept",
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().AcceptInvite(gomock.Any(), linkToken, "new password", gomock.Any()).Return("", time.Time{}, err)
			},
		},
		{
			method: http.MethodGet, path: "/password-resets/" + linkToken, route: "GET /password-resets/{token}",
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().LookupLink(gomock.Any(), linkToken, domain.LinkKindReset).Return("", "", err)
			},
		},
		{
			method: http.MethodPost, path: "/password-resets/" + linkToken + "/complete", body: `{"password":"new password"}`,
			route: "POST /password-resets/{token}/complete",
			expect: func(users *mock.MockUsers, err error) {
				users.EXPECT().CompleteReset(gomock.Any(), linkToken, "new password", gomock.Any()).Return("", time.Time{}, err)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.route, func(t *testing.T) {
			t.Parallel()

			users := mock.NewMockUsers(gomock.NewController(t))
			tt.expect(users, errors.New("database down"))

			var logs bytes.Buffer
			rec := do(t, usersServer(t, users, &logs), tt.method, tt.path, tt.body, false)

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
			if bytes.Contains(logs.Bytes(), []byte(linkToken)) {
				t.Fatalf("log %q holds the token of the path", logs.String())
			}
		})
	}
}
