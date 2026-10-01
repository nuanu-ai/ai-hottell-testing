package session_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/application/auth"
	"git.alva.dev/alva/harness-telemetry/internal/application/session"
	"git.alva.dev/alva/harness-telemetry/internal/application/session/mock"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

const day = 24 * time.Hour

//nolint:gochecknoglobals // fixed test time
var now = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

type ports struct {
	sessions *mock.MockSessions
	tokens   *mock.MockTokenIssuer
	clock    *mock.MockClock
}

func newService(t *testing.T) (*session.Service, ports) {
	t.Helper()
	ctrl := gomock.NewController(t)
	p := ports{
		sessions: mock.NewMockSessions(ctrl),
		tokens:   mock.NewMockTokenIssuer(ctrl),
		clock:    mock.NewMockClock(ctrl),
	}
	return session.NewService(p.sessions, p.tokens, p.clock), p
}

func TestServiceOpen(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		userAgent     string
		wantUserAgent string
	}{
		"short user agent kept":     {userAgent: "Mozilla/5.0", wantUserAgent: "Mozilla/5.0"},
		"255 characters kept":       {userAgent: strings.Repeat("я", 255), wantUserAgent: strings.Repeat("я", 255)},
		"longer cut to 255 letters": {userAgent: strings.Repeat("я", 300), wantUserAgent: strings.Repeat("я", 255)},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, p := newService(t)
			userID := uuid.New()
			hash := []byte("hash")
			p.tokens.EXPECT().New().Return("token", hash, nil)
			p.clock.EXPECT().Now().Return(now)
			p.sessions.EXPECT().
				Create(gomock.Any(), userID, hash, now.Add(30*day), tc.wantUserAgent).
				Return(domain.Session{ID: uuid.New(), UserID: userID, ExpiresAt: now.Add(30 * day)}, nil)

			token, expiresAt, err := svc.Open(context.Background(), userID, tc.userAgent)
			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			if token != "token" || !expiresAt.Equal(now.Add(30*day)) {
				t.Fatalf("Open() = (%q, %v), want (%q, %v)", token, expiresAt, "token", now.Add(30*day))
			}
		})
	}
}

func TestServiceOpenUnknownUser(t *testing.T) {
	t.Parallel()

	svc, p := newService(t)
	p.tokens.EXPECT().New().Return("token", []byte("hash"), nil)
	p.clock.EXPECT().Now().Return(now)
	p.sessions.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(domain.Session{}, domain.ErrUserNotFound)

	_, _, err := svc.Open(context.Background(), uuid.New(), "")
	if !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("Open() error = %v, want %v", err, domain.ErrUserNotFound)
	}
}

func TestServiceResolve(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		left       time.Duration
		wantExtend bool
	}{
		"just opened":                       {left: 30 * day},
		"29 days and a minute left":         {left: 29*day + time.Minute},
		"exactly 29 days left":              {left: 29 * day},
		"just under 29 days left: extended": {left: 29*day - time.Second, wantExtend: true},
		"one day left: extended":            {left: day, wantExtend: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, p := newService(t)
			hash := []byte("hash")
			stored := domain.Session{
				ID: uuid.New(), UserID: uuid.New(), ExpiresAt: now.Add(tc.left), LastSeenAt: now.Add(-day),
			}
			p.tokens.EXPECT().Hash("token").Return(hash, nil)
			p.sessions.EXPECT().GetByTokenHash(gomock.Any(), hash).Return(stored, nil)
			p.clock.EXPECT().Now().Return(now)
			want := stored
			if tc.wantExtend {
				want.ExpiresAt = now.Add(30 * day)
				want.LastSeenAt = now
				p.sessions.EXPECT().Extend(gomock.Any(), stored.ID, now.Add(30*day), now).Return(nil)
			}

			got, cookieExpiry, err := svc.Resolve(context.Background(), "token")
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got != want {
				t.Fatalf("Resolve() session = %+v, want %+v", got, want)
			}
			switch {
			case tc.wantExtend && (cookieExpiry == nil || !cookieExpiry.Equal(now.Add(30*day))):
				t.Fatalf("Resolve() cookie expiry = %v, want %v", cookieExpiry, now.Add(30*day))
			case !tc.wantExtend && cookieExpiry != nil:
				t.Fatalf("Resolve() cookie expiry = %v, want nil", *cookieExpiry)
			}
		})
	}
}

func TestServiceResolveRejectsToken(t *testing.T) {
	t.Parallel()

	tests := map[string]func(p ports){
		"malformed token": func(p ports) {
			p.tokens.EXPECT().Hash("token").Return(nil, auth.ErrMalformedToken)
		},
		"unknown or expired token": func(p ports) {
			p.tokens.EXPECT().Hash("token").Return([]byte("hash"), nil)
			p.sessions.EXPECT().GetByTokenHash(gomock.Any(), []byte("hash")).
				Return(domain.Session{}, domain.ErrSessionNotFound)
		},
		"expired by the application clock": func(p ports) {
			p.tokens.EXPECT().Hash("token").Return([]byte("hash"), nil)
			p.sessions.EXPECT().GetByTokenHash(gomock.Any(), []byte("hash")).
				Return(domain.Session{ID: uuid.New(), ExpiresAt: now.Add(-time.Second)}, nil)
			p.clock.EXPECT().Now().Return(now)
		},
		"expiring exactly now": func(p ports) {
			p.tokens.EXPECT().Hash("token").Return([]byte("hash"), nil)
			p.sessions.EXPECT().GetByTokenHash(gomock.Any(), []byte("hash")).
				Return(domain.Session{ID: uuid.New(), ExpiresAt: now}, nil)
			p.clock.EXPECT().Now().Return(now)
		},
	}

	for name, expect := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, p := newService(t)
			expect(p)

			_, cookieExpiry, err := svc.Resolve(context.Background(), "token")
			if !errors.Is(err, domain.ErrUnauthenticated) {
				t.Fatalf("Resolve() error = %v, want %v", err, domain.ErrUnauthenticated)
			}
			if cookieExpiry != nil {
				t.Fatalf("Resolve() cookie expiry = %v, want nil", *cookieExpiry)
			}
		})
	}
}

func TestServiceClose(t *testing.T) {
	t.Parallel()

	userID, sessionID, keepID := uuid.New(), uuid.New(), uuid.New()
	failure := errors.New("database is down")

	tests := map[string]struct {
		expect func(p ports) *gomock.Call
		call   func(svc *session.Service) error
	}{
		"Close": {
			expect: func(p ports) *gomock.Call { return p.sessions.EXPECT().Delete(gomock.Any(), sessionID) },
			call:   func(svc *session.Service) error { return svc.Close(context.Background(), sessionID) },
		},
		"CloseAll": {
			expect: func(p ports) *gomock.Call { return p.sessions.EXPECT().DeleteAllForUser(gomock.Any(), userID) },
			call:   func(svc *session.Service) error { return svc.CloseAll(context.Background(), userID) },
		},
		"CloseOthers": {
			expect: func(p ports) *gomock.Call {
				return p.sessions.EXPECT().DeleteAllForUserExcept(gomock.Any(), userID, keepID)
			},
			call: func(svc *session.Service) error { return svc.CloseOthers(context.Background(), userID, keepID) },
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, p := newService(t)
			tc.expect(p).Return(nil)
			if err := tc.call(svc); err != nil {
				t.Fatalf("%s() error = %v", name, err)
			}
		})
		t.Run(name+" failure", func(t *testing.T) {
			t.Parallel()

			svc, p := newService(t)
			tc.expect(p).Return(failure)
			if err := tc.call(svc); !errors.Is(err, failure) {
				t.Fatalf("%s() error = %v, want %v", name, err, failure)
			}
		})
	}
}
