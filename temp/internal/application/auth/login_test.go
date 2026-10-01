package auth_test

import (
	"errors"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestService_LoginWithPassword(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	stored := testUser(true)
	expiresAt := now.Add(domain.SessionTTL)
	d.users.EXPECT().GetByEmail(gomock.Any(), stored.Email).Return(stored, testHash, nil)
	d.hasher.EXPECT().Verify(testPassword, testHash).Return(true, nil)
	d.clock.EXPECT().Now().Return(now)
	gomock.InOrder(
		d.users.EXPECT().TouchLogin(gomock.Any(), stored.ID, now).Return(nil),
		d.sessions.EXPECT().Open(gomock.Any(), stored.ID, "Mozilla/5.0").Return("token", expiresAt, nil),
	)

	token, gotExpiry, err := svc.LoginWithPassword(t.Context(), " User@Example.com ", testPassword, "Mozilla/5.0")
	if err != nil {
		t.Fatalf("LoginWithPassword() error = %v", err)
	}
	if token != "token" || !gotExpiry.Equal(expiresAt) {
		t.Fatalf("LoginWithPassword() = (%q, %v), want (%q, %v)", token, gotExpiry, "token", expiresAt)
	}
}

// Every way a sign-in fails on its input answers the same error, so it tells nothing
// about which accounts exist.
func TestService_LoginWithPassword_InvalidCredentials(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		email  string
		expect func(d deps)
	}{
		"malformed email": {
			email:  "user@",
			expect: func(deps) {},
		},
		"unknown email": {
			email: "nobody@example.com",
			expect: func(d deps) {
				d.users.EXPECT().GetByEmail(gomock.Any(), domain.Email("nobody@example.com")).
					Return(domain.User{}, "", domain.ErrUserNotFound)
				d.hasher.EXPECT().Hash(gomock.Any()).Return(dummyHash, nil)
				d.hasher.EXPECT().Verify(testPassword, dummyHash).Return(false, nil)
			},
		},
		"invited user without a password": {
			email: "user@example.com",
			expect: func(d deps) {
				d.users.EXPECT().GetByEmail(gomock.Any(), domain.Email("user@example.com")).
					Return(testUser(false), "", nil)
				d.hasher.EXPECT().Hash(gomock.Any()).Return(dummyHash, nil)
				d.hasher.EXPECT().Verify(testPassword, dummyHash).Return(false, nil)
			},
		},
		"wrong password": {
			email: "user@example.com",
			expect: func(d deps) {
				d.users.EXPECT().GetByEmail(gomock.Any(), domain.Email("user@example.com")).
					Return(testUser(true), testHash, nil)
				d.hasher.EXPECT().Verify(testPassword, testHash).Return(false, nil)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			tc.expect(d)

			token, expiresAt, err := svc.LoginWithPassword(t.Context(), tc.email, testPassword, "")
			if err != domain.ErrInvalidCredentials { //nolint:errorlint // the very same error, not one wrapping it
				t.Fatalf("LoginWithPassword() error = %v, want %v", err, domain.ErrInvalidCredentials)
			}
			if token != "" || !expiresAt.IsZero() {
				t.Fatalf("LoginWithPassword() = (%q, %v), want no session", token, expiresAt)
			}
		})
	}
}

// The dummy hash is made once and reused by every later sign-in of an unknown email.
func TestService_LoginWithPassword_DummyHashMadeOnce(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	d.users.EXPECT().GetByEmail(gomock.Any(), gomock.Any()).Return(domain.User{}, "", domain.ErrUserNotFound).Times(2)
	d.hasher.EXPECT().Hash(gomock.Any()).Return(dummyHash, nil).Times(1)
	d.hasher.EXPECT().Verify(gomock.Any(), dummyHash).Return(false, nil).Times(2)

	for range 2 {
		if _, _, err := svc.LoginWithPassword(t.Context(), "nobody@example.com", testPassword, ""); !errors.Is(
			err, domain.ErrInvalidCredentials) {
			t.Fatalf("LoginWithPassword() error = %v, want %v", err, domain.ErrInvalidCredentials)
		}
	}
}

func TestService_LoginWithPassword_DependencyErrors(t *testing.T) {
	t.Parallel()

	errDB := errors.New("db down")
	stored := testUser(true)
	tests := map[string]func(d deps){
		"get user fails": func(d deps) {
			d.users.EXPECT().GetByEmail(gomock.Any(), stored.Email).Return(domain.User{}, "", errDB)
		},
		"dummy hash fails": func(d deps) {
			d.users.EXPECT().GetByEmail(gomock.Any(), stored.Email).Return(domain.User{}, "", domain.ErrUserNotFound)
			d.hasher.EXPECT().Hash(gomock.Any()).Return("", errDB)
		},
		"verify fails": func(d deps) {
			d.users.EXPECT().GetByEmail(gomock.Any(), stored.Email).Return(stored, testHash, nil)
			d.hasher.EXPECT().Verify(testPassword, testHash).Return(false, errDB)
		},
		"touch login fails": func(d deps) {
			d.users.EXPECT().GetByEmail(gomock.Any(), stored.Email).Return(stored, testHash, nil)
			d.hasher.EXPECT().Verify(testPassword, testHash).Return(true, nil)
			d.clock.EXPECT().Now().Return(now)
			d.users.EXPECT().TouchLogin(gomock.Any(), stored.ID, now).Return(errDB)
		},
		"open session fails": func(d deps) {
			d.users.EXPECT().GetByEmail(gomock.Any(), stored.Email).Return(stored, testHash, nil)
			d.hasher.EXPECT().Verify(testPassword, testHash).Return(true, nil)
			d.clock.EXPECT().Now().Return(now)
			d.users.EXPECT().TouchLogin(gomock.Any(), stored.ID, now).Return(nil)
			d.sessions.EXPECT().Open(gomock.Any(), stored.ID, "").Return("", time.Time{}, errDB)
		},
	}
	for name, expect := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			expect(d)
			_, _, err := svc.LoginWithPassword(t.Context(), stored.Email.String(), testPassword, "")
			if !errors.Is(err, errDB) {
				t.Fatalf("LoginWithPassword() error = %v, want %v", err, errDB)
			}
		})
	}
}
