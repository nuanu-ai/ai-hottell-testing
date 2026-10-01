package passkeys_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/application/passkeys"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// userAgent is the User-Agent the signed-in browser sends.
const userAgent = "Mozilla/5.0"

func TestService_BeginLogin(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	ceremonyID := uuid.New()
	d.webauthn.EXPECT().BeginLogin().Return(optionsJSON, sessionData, nil)
	// A login ceremony names no user and lasts five minutes.
	d.ceremonies.EXPECT().
		Save(gomock.Any(), domain.CeremonyKindLogin, nil, nil, sessionData, now.Add(5*time.Minute)).
		Return(ceremonyID, nil)

	gotID, gotOptions, err := svc.BeginLogin(t.Context())
	if err != nil {
		t.Fatalf("BeginLogin() error = %v", err)
	}
	if gotID != ceremonyID {
		t.Errorf("BeginLogin() ceremony = %v, want %v", gotID, ceremonyID)
	}
	if string(gotOptions) != string(optionsJSON) {
		t.Errorf("BeginLogin() options = %s, want %s", gotOptions, optionsJSON)
	}
}

// signIn expects the adapter to call the lookup with the user handle and credential ID
// of the browser response and, when it finds them, to verify the response as signed by
// the passkey with credentialID with signature counter 7 and backup state set.
func signIn(d deps, userHandle, credentialID []byte) {
	d.webauthn.EXPECT().FinishLogin(sessionData, responseJSON, gomock.Any()).DoAndReturn(
		func(_, _ []byte, lookup passkeys.PasskeyLookup) (uuid.UUID, []byte, uint32, bool, error) {
			user, _, _, err := lookup(userHandle, credentialID)
			if err != nil {
				return uuid.Nil, nil, 0, false, err
			}
			return user.ID, credentialID, 7, true, nil
		})
}

func TestService_FinishLogin(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	user := testUser()
	used := testPasskey(user.ID, "Телефон")
	existing := []domain.Passkey{testPasskey(user.ID, "Ноутбук"), used}
	ceremonyID := uuid.New()
	expiresAt := now.Add(domain.SessionTTL)

	d.ceremonies.EXPECT().Take(gomock.Any(), ceremonyID, domain.CeremonyKindLogin).
		Return(nil, nil, sessionData, nil)
	d.users.EXPECT().GetByWebAuthnID(gomock.Any(), webAuthnID).Return(user, nil)
	d.passkeys.EXPECT().ListByUser(gomock.Any(), user.ID).Return(existing, nil)
	signIn(d, webAuthnID, used.CredentialID)
	// The sign-in is recorded on the passkey used and the user, then a session opens.
	gomock.InOrder(
		d.passkeys.EXPECT().RecordUse(gomock.Any(), used.ID, uint32(7), true, now).Return(nil),
		d.users.EXPECT().TouchLogin(gomock.Any(), user.ID, now).Return(nil),
		d.sessions.EXPECT().Open(gomock.Any(), user.ID, userAgent).Return("session token", expiresAt, nil),
	)

	token, gotExpiry, err := svc.FinishLogin(t.Context(), ceremonyID, responseJSON, userAgent)
	if err != nil {
		t.Fatalf("FinishLogin() error = %v", err)
	}
	if token != "session token" {
		t.Errorf("FinishLogin() token = %q, want %q", token, "session token")
	}
	if !gotExpiry.Equal(expiresAt) {
		t.Errorf("FinishLogin() expiresAt = %v, want %v", gotExpiry, expiresAt)
	}
}

func TestService_FinishLogin_Rejected(t *testing.T) {
	t.Parallel()

	user := testUser()
	invited := testUser()
	invited.HasPassword = false
	kept := testPasskey(user.ID, "Ноутбук")
	deleted := testPasskey(user.ID, "Телефон")
	tests := map[string]struct {
		expect func(d deps, ceremonyID uuid.UUID)
		want   error
	}{
		"expired or unknown ceremony": {
			expect: func(d deps, ceremonyID uuid.UUID) {
				d.ceremonies.EXPECT().Take(gomock.Any(), ceremonyID, domain.CeremonyKindLogin).
					Return(nil, nil, nil, domain.ErrCeremonyNotFound)
			},
			want: domain.ErrCeremonyNotFound,
		},
		"deleted passkey": {
			expect: func(d deps, ceremonyID uuid.UUID) {
				d.ceremonies.EXPECT().Take(gomock.Any(), ceremonyID, domain.CeremonyKindLogin).
					Return(nil, nil, sessionData, nil)
				d.users.EXPECT().GetByWebAuthnID(gomock.Any(), webAuthnID).Return(user, nil)
				d.passkeys.EXPECT().ListByUser(gomock.Any(), user.ID).Return([]domain.Passkey{kept}, nil)
				signIn(d, webAuthnID, deleted.CredentialID)
			},
			want: domain.ErrPasskeyVerificationFailed,
		},
		"unknown user": {
			expect: func(d deps, ceremonyID uuid.UUID) {
				d.ceremonies.EXPECT().Take(gomock.Any(), ceremonyID, domain.CeremonyKindLogin).
					Return(nil, nil, sessionData, nil)
				d.users.EXPECT().GetByWebAuthnID(gomock.Any(), webAuthnID).
					Return(domain.User{}, domain.ErrUserNotFound)
				signIn(d, webAuthnID, kept.CredentialID)
			},
			want: domain.ErrPasskeyVerificationFailed,
		},
		"invited user": {
			expect: func(d deps, ceremonyID uuid.UUID) {
				d.ceremonies.EXPECT().Take(gomock.Any(), ceremonyID, domain.CeremonyKindLogin).
					Return(nil, nil, sessionData, nil)
				d.users.EXPECT().GetByWebAuthnID(gomock.Any(), webAuthnID).Return(invited, nil)
				signIn(d, webAuthnID, kept.CredentialID)
			},
			want: domain.ErrPasskeyVerificationFailed,
		},
		"response fails verification": {
			expect: func(d deps, ceremonyID uuid.UUID) {
				d.ceremonies.EXPECT().Take(gomock.Any(), ceremonyID, domain.CeremonyKindLogin).
					Return(nil, nil, sessionData, nil)
				d.webauthn.EXPECT().FinishLogin(sessionData, responseJSON, gomock.Any()).
					Return(uuid.Nil, nil, uint32(0), false, domain.ErrPasskeyVerificationFailed)
			},
			want: domain.ErrPasskeyVerificationFailed,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// No RecordUse, TouchLogin or Open is expected: the mocks fail the test on any.
			svc, d := newService(t)
			ceremonyID := uuid.New()
			tc.expect(d, ceremonyID)
			_, _, err := svc.FinishLogin(t.Context(), ceremonyID, responseJSON, userAgent)
			if !errors.Is(err, tc.want) {
				t.Fatalf("FinishLogin() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestService_FinishLogin_CeremonyReplayed(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	user := testUser()
	used := testPasskey(user.ID, "Телефон")
	ceremonyID := uuid.New()

	// Take removes the ceremony, so the store finds it only the first time.
	gomock.InOrder(
		d.ceremonies.EXPECT().Take(gomock.Any(), ceremonyID, domain.CeremonyKindLogin).
			Return(nil, nil, sessionData, nil),
		d.ceremonies.EXPECT().Take(gomock.Any(), ceremonyID, domain.CeremonyKindLogin).
			Return(nil, nil, nil, domain.ErrCeremonyNotFound),
	)
	d.users.EXPECT().GetByWebAuthnID(gomock.Any(), webAuthnID).Return(user, nil)
	d.passkeys.EXPECT().ListByUser(gomock.Any(), user.ID).Return([]domain.Passkey{used}, nil)
	signIn(d, webAuthnID, used.CredentialID)
	d.passkeys.EXPECT().RecordUse(gomock.Any(), used.ID, uint32(7), true, now).Return(nil)
	d.users.EXPECT().TouchLogin(gomock.Any(), user.ID, now).Return(nil)
	d.sessions.EXPECT().Open(gomock.Any(), user.ID, userAgent).Return("session token", now.Add(domain.SessionTTL), nil)

	if _, _, err := svc.FinishLogin(t.Context(), ceremonyID, responseJSON, userAgent); err != nil {
		t.Fatalf("first FinishLogin() error = %v", err)
	}
	// The same browser response sent again opens no second session.
	_, _, err := svc.FinishLogin(t.Context(), ceremonyID, responseJSON, userAgent)
	if !errors.Is(err, domain.ErrCeremonyNotFound) {
		t.Fatalf("second FinishLogin() error = %v, want %v", err, domain.ErrCeremonyNotFound)
	}
}
