package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

const (
	newPassword = "battery staple"
	newHash     = "$argon2id$new"
)

func TestService_ChangePassword(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	stored, sessionID := testUser(true), uuid.New()
	d.users.EXPECT().GetByID(gomock.Any(), stored.ID).Return(stored, testHash, nil)
	d.hasher.EXPECT().Verify(testPassword, testHash).Return(true, nil)
	d.hasher.EXPECT().Hash(newPassword).Return(newHash, nil)
	d.users.EXPECT().SetPassword(gomock.Any(), stored.ID, newHash).Return(nil)
	// Every other session of the user is closed; the current one is kept.
	d.sessions.EXPECT().CloseOthers(gomock.Any(), stored.ID, sessionID).Return(nil)

	if err := svc.ChangePassword(t.Context(), stored.ID, sessionID, testPassword, newPassword); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}
}

func TestService_ChangePassword_Rejected(t *testing.T) {
	t.Parallel()

	stored := testUser(true)
	tests := map[string]struct {
		next   string
		expect func(d deps)
		want   error
	}{
		"new password too short": {
			next:   "short",
			expect: func(deps) {},
			want:   domain.ErrPasswordTooShort,
		},
		"new password too long": {
			next:   strings.Repeat("a", 129),
			expect: func(deps) {},
			want:   domain.ErrPasswordTooLong,
		},
		"wrong current password": {
			next: newPassword,
			expect: func(d deps) {
				d.users.EXPECT().GetByID(gomock.Any(), stored.ID).Return(stored, testHash, nil)
				d.hasher.EXPECT().Verify(testPassword, testHash).Return(false, nil)
			},
			want: domain.ErrWrongCurrentPassword,
		},
		"user without a password": {
			next: newPassword,
			expect: func(d deps) {
				d.users.EXPECT().GetByID(gomock.Any(), stored.ID).Return(testUser(false), "", nil)
			},
			want: domain.ErrWrongCurrentPassword,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			tc.expect(d)
			err := svc.ChangePassword(t.Context(), stored.ID, uuid.New(), testPassword, tc.next)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ChangePassword() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestService_ChangePassword_DependencyErrors(t *testing.T) {
	t.Parallel()

	errDB := errors.New("db down")
	stored, sessionID := testUser(true), uuid.New()
	tests := map[string]func(d deps){
		"get user fails": func(d deps) {
			d.users.EXPECT().GetByID(gomock.Any(), stored.ID).Return(domain.User{}, "", errDB)
		},
		"verify fails": func(d deps) {
			d.users.EXPECT().GetByID(gomock.Any(), stored.ID).Return(stored, testHash, nil)
			d.hasher.EXPECT().Verify(testPassword, testHash).Return(false, errDB)
		},
		"hash fails": func(d deps) {
			d.users.EXPECT().GetByID(gomock.Any(), stored.ID).Return(stored, testHash, nil)
			d.hasher.EXPECT().Verify(testPassword, testHash).Return(true, nil)
			d.hasher.EXPECT().Hash(newPassword).Return("", errDB)
		},
		"set password fails": func(d deps) {
			d.users.EXPECT().GetByID(gomock.Any(), stored.ID).Return(stored, testHash, nil)
			d.hasher.EXPECT().Verify(testPassword, testHash).Return(true, nil)
			d.hasher.EXPECT().Hash(newPassword).Return(newHash, nil)
			d.users.EXPECT().SetPassword(gomock.Any(), stored.ID, newHash).Return(errDB)
		},
		"close other sessions fails": func(d deps) {
			d.users.EXPECT().GetByID(gomock.Any(), stored.ID).Return(stored, testHash, nil)
			d.hasher.EXPECT().Verify(testPassword, testHash).Return(true, nil)
			d.hasher.EXPECT().Hash(newPassword).Return(newHash, nil)
			d.users.EXPECT().SetPassword(gomock.Any(), stored.ID, newHash).Return(nil)
			d.sessions.EXPECT().CloseOthers(gomock.Any(), stored.ID, sessionID).Return(errDB)
		},
	}
	for name, expect := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			expect(d)
			err := svc.ChangePassword(t.Context(), stored.ID, sessionID, testPassword, newPassword)
			if !errors.Is(err, errDB) {
				t.Fatalf("ChangePassword() error = %v, want %v", err, errDB)
			}
		})
	}
}
