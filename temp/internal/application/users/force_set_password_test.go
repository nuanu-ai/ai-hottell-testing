package users_test

import (
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestService_ForceSetPassword_InvalidInput(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		email, password string
		want            error
	}{
		"invalid email":  {email: "admin@", password: testPassword, want: domain.ErrInvalidEmail},
		"short password": {email: "admin@example.com", password: "short", want: domain.ErrPasswordTooShort},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, _ := newService(t)
			_, err := svc.ForceSetPassword(t.Context(), tc.email, tc.password)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ForceSetPassword() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestService_ForceSetPassword_UnknownUser(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	d.hasher.EXPECT().Hash(testPassword).Return(testHash, nil)
	d.users.EXPECT().GetByEmail(gomock.Any(), domain.Email("nobody@example.com")).
		Return(domain.User{}, "", domain.ErrUserNotFound)

	_, err := svc.ForceSetPassword(t.Context(), "Nobody@example.com", testPassword)
	if !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("ForceSetPassword() error = %v, want %v", err, domain.ErrUserNotFound)
	}
}

func TestService_ForceSetPassword(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		invited bool
	}{
		"active user":  {invited: false},
		"invited user": {invited: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			stored := testUser(!tc.invited)
			d.hasher.EXPECT().Hash(testPassword).Return(testHash, nil)
			d.users.EXPECT().GetByEmail(gomock.Any(), stored.Email).Return(stored, "", nil)
			d.users.EXPECT().SetPassword(gomock.Any(), stored.ID, testHash).Return(nil)
			if tc.invited {
				d.links.EXPECT().DeleteUnused(gomock.Any(), stored.ID, domain.LinkKindInvite).Return(nil)
			}
			d.sessions.EXPECT().DeleteAllForUser(gomock.Any(), stored.ID).Return(nil)

			got, err := svc.ForceSetPassword(t.Context(), stored.Email.String(), testPassword)
			if err != nil {
				t.Fatalf("ForceSetPassword() error = %v", err)
			}
			want := stored
			want.HasPassword = true
			if got != want {
				t.Fatalf("ForceSetPassword() = %+v, want %+v", got, want)
			}
		})
	}
}

func TestService_ForceSetPassword_DependencyErrors(t *testing.T) {
	t.Parallel()

	errDB := errors.New("db down")
	stored := testUser(false)
	tests := map[string]func(d deps){
		"hash fails": func(d deps) {
			d.hasher.EXPECT().Hash(testPassword).Return("", errDB)
		},
		"set password fails": func(d deps) {
			d.hasher.EXPECT().Hash(testPassword).Return(testHash, nil)
			d.users.EXPECT().GetByEmail(gomock.Any(), stored.Email).Return(stored, "", nil)
			d.users.EXPECT().SetPassword(gomock.Any(), stored.ID, testHash).Return(errDB)
		},
		"delete invitation fails": func(d deps) {
			d.hasher.EXPECT().Hash(testPassword).Return(testHash, nil)
			d.users.EXPECT().GetByEmail(gomock.Any(), stored.Email).Return(stored, "", nil)
			d.users.EXPECT().SetPassword(gomock.Any(), stored.ID, testHash).Return(nil)
			d.links.EXPECT().DeleteUnused(gomock.Any(), stored.ID, domain.LinkKindInvite).Return(errDB)
		},
		"close sessions fails": func(d deps) {
			d.hasher.EXPECT().Hash(testPassword).Return(testHash, nil)
			d.users.EXPECT().GetByEmail(gomock.Any(), stored.Email).Return(stored, "", nil)
			d.users.EXPECT().SetPassword(gomock.Any(), stored.ID, testHash).Return(nil)
			d.links.EXPECT().DeleteUnused(gomock.Any(), stored.ID, domain.LinkKindInvite).Return(nil)
			d.sessions.EXPECT().DeleteAllForUser(gomock.Any(), stored.ID).Return(errDB)
		},
	}
	for name, expect := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			expect(d)
			if _, err := svc.ForceSetPassword(t.Context(), stored.Email.String(), testPassword); !errors.Is(err, errDB) {
				t.Fatalf("ForceSetPassword() error = %v, want %v", err, errDB)
			}
		})
	}
}
