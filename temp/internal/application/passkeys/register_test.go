package passkeys_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

//nolint:gochecknoglobals // fixed test values
var (
	webAuthnID   = []byte("webauthn user handle")
	sessionData  = []byte(`{"challenge":"c"}`)
	optionsJSON  = []byte(`{"challenge":"c"}`)
	responseJSON = []byte(`{"id":"r"}`)
)

func TestService_BeginRegistration(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	user := testUser()
	existing := []domain.Passkey{testPasskey(user.ID, "Ноутбук")}
	ceremonyID := uuid.New()
	d.users.EXPECT().GetByID(gomock.Any(), user.ID).Return(user, "$argon2id$stored", nil)
	d.users.EXPECT().GetWebAuthnID(gomock.Any(), user.ID).Return(webAuthnID, nil)
	d.passkeys.EXPECT().ListByUser(gomock.Any(), user.ID).Return(existing, nil)
	d.webauthn.EXPECT().BeginRegistration(user, webAuthnID, existing).Return(optionsJSON, sessionData, nil)
	// The trimmed name is kept with the ceremony for five minutes.
	name := "Телефон"
	d.ceremonies.EXPECT().
		Save(gomock.Any(), domain.CeremonyKindRegister, &user.ID, &name, sessionData, now.Add(5*time.Minute)).
		Return(ceremonyID, nil)

	gotID, gotOptions, err := svc.BeginRegistration(t.Context(), user.ID, "  Телефон ")
	if err != nil {
		t.Fatalf("BeginRegistration() error = %v", err)
	}
	if gotID != ceremonyID {
		t.Errorf("BeginRegistration() ceremony = %v, want %v", gotID, ceremonyID)
	}
	if string(gotOptions) != string(optionsJSON) {
		t.Errorf("BeginRegistration() options = %s, want %s", gotOptions, optionsJSON)
	}
}

func TestService_BeginRegistration_InvalidName(t *testing.T) {
	t.Parallel()

	for name, passkeyName := range map[string]string{
		"empty":    "",
		"blank":    "   ",
		"too long": strings.Repeat("я", 65),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// No port is reached: the mocks fail on any call.
			svc, _ := newService(t)
			_, _, err := svc.BeginRegistration(t.Context(), uuid.New(), passkeyName)
			if !errors.Is(err, domain.ErrInvalidPasskeyName) {
				t.Fatalf("BeginRegistration() error = %v, want %v", err, domain.ErrInvalidPasskeyName)
			}
		})
	}
}

func TestService_FinishRegistration(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	user := testUser()
	existing := []domain.Passkey{testPasskey(user.ID, "Ноутбук")}
	ceremonyID, name := uuid.New(), "Телефон"
	verified := domain.Passkey{
		CredentialID:    []byte("new credential"),
		PublicKey:       []byte("new public key"),
		AttestationType: "none",
		SignCount:       1,
		Transports:      []string{"internal"},
		BackupEligible:  true,
	}
	want := verified
	want.UserID, want.Name = user.ID, domain.PasskeyName(name)
	stored := want
	stored.ID, stored.CreatedAt = uuid.New(), now

	d.ceremonies.EXPECT().Take(gomock.Any(), ceremonyID, domain.CeremonyKindRegister).
		Return(&user.ID, &name, sessionData, nil)
	d.users.EXPECT().GetByID(gomock.Any(), user.ID).Return(user, "$argon2id$stored", nil)
	d.users.EXPECT().GetWebAuthnID(gomock.Any(), user.ID).Return(webAuthnID, nil)
	d.passkeys.EXPECT().ListByUser(gomock.Any(), user.ID).Return(existing, nil)
	d.webauthn.EXPECT().FinishRegistration(user, webAuthnID, existing, sessionData, responseJSON).Return(verified, nil)
	// The passkey is stored for the user under the name kept with the ceremony.
	d.passkeys.EXPECT().Create(gomock.Any(), want).Return(stored, nil)

	got, err := svc.FinishRegistration(t.Context(), user.ID, ceremonyID, responseJSON)
	if err != nil {
		t.Fatalf("FinishRegistration() error = %v", err)
	}
	if !reflect.DeepEqual(got, stored) {
		t.Fatalf("FinishRegistration() = %+v, want %+v", got, stored)
	}
}

func TestService_FinishRegistration_Rejected(t *testing.T) {
	t.Parallel()

	user := testUser()
	name := "Телефон"
	tests := map[string]struct {
		expect func(d deps, ceremonyID uuid.UUID)
		want   error
	}{
		"expired or unknown ceremony": {
			expect: func(d deps, ceremonyID uuid.UUID) {
				d.ceremonies.EXPECT().Take(gomock.Any(), ceremonyID, domain.CeremonyKindRegister).
					Return(nil, nil, nil, domain.ErrCeremonyNotFound)
			},
			want: domain.ErrCeremonyNotFound,
		},
		"ceremony of another user": {
			expect: func(d deps, ceremonyID uuid.UUID) {
				other := uuid.New()
				d.ceremonies.EXPECT().Take(gomock.Any(), ceremonyID, domain.CeremonyKindRegister).
					Return(&other, &name, sessionData, nil)
			},
			want: domain.ErrCeremonyNotFound,
		},
		"ceremony without a user": {
			expect: func(d deps, ceremonyID uuid.UUID) {
				d.ceremonies.EXPECT().Take(gomock.Any(), ceremonyID, domain.CeremonyKindRegister).
					Return(nil, &name, sessionData, nil)
			},
			want: domain.ErrCeremonyNotFound,
		},
		"response fails verification": {
			expect: func(d deps, ceremonyID uuid.UUID) {
				d.ceremonies.EXPECT().Take(gomock.Any(), ceremonyID, domain.CeremonyKindRegister).
					Return(&user.ID, &name, sessionData, nil)
				d.users.EXPECT().GetByID(gomock.Any(), user.ID).Return(user, "$argon2id$stored", nil)
				d.users.EXPECT().GetWebAuthnID(gomock.Any(), user.ID).Return(webAuthnID, nil)
				d.passkeys.EXPECT().ListByUser(gomock.Any(), user.ID).Return(nil, nil)
				d.webauthn.EXPECT().FinishRegistration(user, webAuthnID, nil, sessionData, responseJSON).
					Return(domain.Passkey{}, domain.ErrPasskeyVerificationFailed)
				// No Create is expected: the mock fails the test if a passkey is stored.
			},
			want: domain.ErrPasskeyVerificationFailed,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			ceremonyID := uuid.New()
			tc.expect(d, ceremonyID)
			_, err := svc.FinishRegistration(t.Context(), user.ID, ceremonyID, responseJSON)
			if !errors.Is(err, tc.want) {
				t.Fatalf("FinishRegistration() error = %v, want %v", err, tc.want)
			}
		})
	}
}
