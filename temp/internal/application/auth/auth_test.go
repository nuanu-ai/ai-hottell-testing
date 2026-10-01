package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/application/auth"
	"git.alva.dev/alva/harness-telemetry/internal/application/auth/mock"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

const (
	testPassword = "correct horse"
	testHash     = "$argon2id$stored"
	dummyHash    = "$argon2id$dummy"
)

//nolint:gochecknoglobals // fixed test time
var now = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

// deps are the mocked ports of a Service under test.
type deps struct {
	users    *mock.MockUsers
	hasher   *mock.MockPasswordHasher
	sessions *mock.MockSessionService
	clock    *mock.MockClock
	tx       *mock.MockTxManager
}

// newService returns a Service on fresh mocks; the transaction manager runs fn in place.
func newService(t *testing.T) (*auth.Service, deps) {
	t.Helper()

	ctrl := gomock.NewController(t)
	d := deps{
		users:    mock.NewMockUsers(ctrl),
		hasher:   mock.NewMockPasswordHasher(ctrl),
		sessions: mock.NewMockSessionService(ctrl),
		clock:    mock.NewMockClock(ctrl),
		tx:       mock.NewMockTxManager(ctrl),
	}
	d.tx.EXPECT().WithinTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, fn func(ctx context.Context) error) error {
			return fn(ctx)
		}).AnyTimes()
	return auth.NewService(d.users, d.hasher, d.sessions, d.clock, d.tx), d
}

// testUser returns a stored user, active when it has a password.
func testUser(hasPassword bool) domain.User {
	return domain.User{
		ID:          uuid.New(),
		Email:       "user@example.com",
		Name:        "Пользователь",
		HasPassword: hasPassword,
		CreatedAt:   now.Add(-time.Hour),
	}
}
