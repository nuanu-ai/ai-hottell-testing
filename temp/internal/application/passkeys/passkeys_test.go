package passkeys_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/application/passkeys"
	"git.alva.dev/alva/harness-telemetry/internal/application/passkeys/mock"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// now is the time the mocked clock tells.
var now = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // a fixed test time

// deps are the mocked ports of a Service under test.
type deps struct {
	users      *mock.MockUsers
	passkeys   *mock.MockPasskeys
	ceremonies *mock.MockCeremonies
	webauthn   *mock.MockWebAuthn
	sessions   *mock.MockSessionService
	clock      *mock.MockClock
}

// newService returns a Service on fresh mocks; the clock tells now.
func newService(t *testing.T) (*passkeys.Service, deps) {
	t.Helper()

	ctrl := gomock.NewController(t)
	d := deps{
		users:      mock.NewMockUsers(ctrl),
		passkeys:   mock.NewMockPasskeys(ctrl),
		ceremonies: mock.NewMockCeremonies(ctrl),
		webauthn:   mock.NewMockWebAuthn(ctrl),
		sessions:   mock.NewMockSessionService(ctrl),
		clock:      mock.NewMockClock(ctrl),
	}
	d.clock.EXPECT().Now().Return(now).AnyTimes()
	return passkeys.NewService(d.users, d.passkeys, d.ceremonies, d.webauthn, d.sessions, d.clock), d
}

// testUser returns an active user.
func testUser() domain.User {
	return domain.User{
		ID:          uuid.New(),
		Email:       "user@example.com",
		Name:        "Пользователь",
		HasPassword: true,
		CreatedAt:   now.Add(-time.Hour),
	}
}

// testPasskey returns a stored passkey of the user with userID.
func testPasskey(userID uuid.UUID, name domain.PasskeyName) domain.Passkey {
	return domain.Passkey{
		ID:           uuid.New(),
		UserID:       userID,
		CredentialID: []byte("credential " + name.String()),
		PublicKey:    []byte("public key"),
		Name:         name,
		CreatedAt:    now.Add(-time.Minute),
	}
}
