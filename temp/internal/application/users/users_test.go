package users_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/application/users"
	"git.alva.dev/alva/harness-telemetry/internal/application/users/mock"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

const origin = "http://localhost:8080"

// now is the time the mocked clock tells.
var now = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // a fixed test time

// deps are the mocked collaborators of a Service under test, and the Service itself.
type deps struct {
	svc      *users.Service
	users    *mock.MockUsers
	links    *mock.MockLinks
	sessions *mock.MocksessionRepository
	opener   *mock.MocksessionOpener
	hasher   *mock.MockpasswordHasher
	tokens   *mock.MocktokenIssuer
	clock    *mock.Mockclock
	tx       *mock.MocktxManager
}

// newService returns a Service on fresh mocks; the transaction manager runs fn in place.
func newService(t *testing.T) (*users.Service, deps) {
	t.Helper()

	ctrl := gomock.NewController(t)
	d := deps{
		users:    mock.NewMockUsers(ctrl),
		links:    mock.NewMockLinks(ctrl),
		sessions: mock.NewMocksessionRepository(ctrl),
		opener:   mock.NewMocksessionOpener(ctrl),
		hasher:   mock.NewMockpasswordHasher(ctrl),
		tokens:   mock.NewMocktokenIssuer(ctrl),
		clock:    mock.NewMockclock(ctrl),
		tx:       mock.NewMocktxManager(ctrl),
	}
	d.clock.EXPECT().Now().Return(now).AnyTimes()
	d.tx.EXPECT().WithinTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, fn func(ctx context.Context) error) error {
			return fn(ctx)
		}).AnyTimes()
	d.svc = users.NewService(d.users, d.links, d.sessions, d.opener, d.hasher, d.tokens, d.clock, d.tx, origin)
	return d.svc, d
}

func invitedUser() domain.User {
	return domain.User{ID: uuid.New(), Email: "b@example.com", Name: "Борис", CreatedAt: now.Add(-time.Hour)}
}

func activeUser() domain.User {
	u := invitedUser()
	u.HasPassword = true
	return u
}
