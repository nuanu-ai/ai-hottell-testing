package users_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

const (
	testPassword = "correct horse"
	testHash     = "$argon2id$hash"
)

func testUser(hasPassword bool) domain.User {
	return domain.User{
		ID:          uuid.MustParse("7b0c2b8e-4c5e-4d7a-9d3a-1f2e3d4c5b6a"),
		Email:       "admin@example.com",
		Name:        "Админ",
		HasPassword: hasPassword,
		CreatedAt:   time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC),
	}
}

func TestService_CreateFirst_InvalidInput(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		email, name, password string
		want                  error
	}{
		"invalid email":     {email: "admin", name: "Админ", password: testPassword, want: domain.ErrInvalidEmail},
		"empty name":        {email: "admin@example.com", name: " ", password: testPassword, want: domain.ErrInvalidName},
		"short password":    {email: "admin@example.com", name: "Админ", password: "1234567", want: domain.ErrPasswordTooShort},
		"too long password": {email: "admin@example.com", name: "Админ", password: string(make([]byte, 129)), want: domain.ErrPasswordTooLong},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// No repository or hasher call is expected: the mocks fail the test on any.
			svc, _ := newService(t)
			_, _, err := svc.CreateFirst(t.Context(), tc.email, tc.name, tc.password)
			if !errors.Is(err, tc.want) {
				t.Fatalf("CreateFirst() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestService_CreateFirst_Creates(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	want := testUser(true)
	d.users.EXPECT().GetByEmail(gomock.Any(), domain.Email("admin@example.com")).
		Return(domain.User{}, "", domain.ErrUserNotFound)
	d.hasher.EXPECT().Hash(testPassword).Return(testHash, nil)
	d.users.EXPECT().CreateActive(gomock.Any(), domain.Email("admin@example.com"), domain.UserName("Админ"), testHash).
		Return(want, nil)

	got, created, err := svc.CreateFirst(t.Context(), " Admin@Example.com ", " Админ ", testPassword)
	if err != nil {
		t.Fatalf("CreateFirst() error = %v", err)
	}
	if !created || got != want {
		t.Fatalf("CreateFirst() = %+v, %v; want %+v, true", got, created, want)
	}
}

func TestService_CreateFirst_Exists(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	want := testUser(false)
	// Neither Hash nor CreateActive is expected: an existing user is left as is.
	d.users.EXPECT().GetByEmail(gomock.Any(), domain.Email("admin@example.com")).Return(want, "", nil)

	got, created, err := svc.CreateFirst(t.Context(), "admin@example.com", "Другое имя", testPassword)
	if err != nil {
		t.Fatalf("CreateFirst() error = %v", err)
	}
	if created || got != want {
		t.Fatalf("CreateFirst() = %+v, %v; want %+v, false", got, created, want)
	}
}

func TestService_CreateFirst_CreatedConcurrently(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	want := testUser(true)
	gomock.InOrder(
		d.users.EXPECT().GetByEmail(gomock.Any(), domain.Email("admin@example.com")).
			Return(domain.User{}, "", domain.ErrUserNotFound),
		d.hasher.EXPECT().Hash(testPassword).Return(testHash, nil),
		d.users.EXPECT().CreateActive(gomock.Any(), domain.Email("admin@example.com"), domain.UserName("Админ"), testHash).
			Return(domain.User{}, domain.ErrEmailTaken),
		d.users.EXPECT().GetByEmail(gomock.Any(), domain.Email("admin@example.com")).Return(want, testHash, nil),
	)

	got, created, err := svc.CreateFirst(t.Context(), "admin@example.com", "Админ", testPassword)
	if err != nil {
		t.Fatalf("CreateFirst() error = %v", err)
	}
	if created || got != want {
		t.Fatalf("CreateFirst() = %+v, %v; want %+v, false", got, created, want)
	}
}

func TestService_CreateFirst_DependencyErrors(t *testing.T) {
	t.Parallel()

	errDB := errors.New("db down")
	tests := map[string]func(d deps){
		"lookup fails": func(d deps) {
			d.users.EXPECT().GetByEmail(gomock.Any(), domain.Email("admin@example.com")).Return(domain.User{}, "", errDB)
		},
		"hash fails": func(d deps) {
			d.users.EXPECT().GetByEmail(gomock.Any(), domain.Email("admin@example.com")).
				Return(domain.User{}, "", domain.ErrUserNotFound)
			d.hasher.EXPECT().Hash(testPassword).Return("", errDB)
		},
		"insert fails": func(d deps) {
			d.users.EXPECT().GetByEmail(gomock.Any(), domain.Email("admin@example.com")).
				Return(domain.User{}, "", domain.ErrUserNotFound)
			d.hasher.EXPECT().Hash(testPassword).Return(testHash, nil)
			d.users.EXPECT().CreateActive(gomock.Any(), domain.Email("admin@example.com"), domain.UserName("Админ"), testHash).
				Return(domain.User{}, errDB)
		},
	}
	for name, expect := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			expect(d)
			_, created, err := svc.CreateFirst(t.Context(), "admin@example.com", "Админ", testPassword)
			if !errors.Is(err, errDB) || created {
				t.Fatalf("CreateFirst() = %v, %v; want %v, false", created, err, errDB)
			}
		})
	}
}
