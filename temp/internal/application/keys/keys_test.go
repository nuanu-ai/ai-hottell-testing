package keys_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/application/auth"
	"git.alva.dev/alva/harness-telemetry/internal/application/keys"
	"git.alva.dev/alva/harness-telemetry/internal/application/keys/mock"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

//nolint:gochecknoglobals // fixed test time
var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

var errDB = errors.New("db down")

type ports struct {
	keys   *mock.MockAccessKeys
	tokens *mock.MockTokenIssuer
	clock  *mock.MockClock
}

func newService(t *testing.T) (*keys.Service, ports) {
	t.Helper()
	ctrl := gomock.NewController(t)
	p := ports{
		keys:   mock.NewMockAccessKeys(ctrl),
		tokens: mock.NewMockTokenIssuer(ctrl),
		clock:  mock.NewMockClock(ctrl),
	}
	return keys.NewService(p.keys, p.tokens, p.clock), p
}

func TestServiceIssueMCPKey(t *testing.T) {
	t.Parallel()

	svc, p := newService(t)
	userID := uuid.New()
	hash := []byte("hash")
	p.tokens.EXPECT().New().Return("mcp-key", hash, nil)
	// Only the hash is stored: the open value of an MCP key is empty.
	p.keys.EXPECT().Replace(gomock.Any(), userID, domain.AccessKeyKindMCP, hash, "").
		Return(domain.AccessKey{ID: uuid.New(), UserID: userID, Kind: domain.AccessKeyKindMCP}, nil)

	got, err := svc.IssueMCPKey(context.Background(), userID)
	if err != nil {
		t.Fatalf("IssueMCPKey() error = %v", err)
	}
	if got != "mcp-key" {
		t.Fatalf("IssueMCPKey() = %q, want %q", got, "mcp-key")
	}
}

func TestServiceIssueMCPKeyErrors(t *testing.T) {
	t.Parallel()

	t.Run("token failure", func(t *testing.T) {
		t.Parallel()

		svc, p := newService(t)
		p.tokens.EXPECT().New().Return("", nil, errDB)

		if _, err := svc.IssueMCPKey(context.Background(), uuid.New()); !errors.Is(err, errDB) {
			t.Fatalf("IssueMCPKey() error = %v, want %v", err, errDB)
		}
	})

	t.Run("unknown user", func(t *testing.T) {
		t.Parallel()

		svc, p := newService(t)
		p.tokens.EXPECT().New().Return("mcp-key", []byte("hash"), nil)
		p.keys.EXPECT().Replace(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(domain.AccessKey{}, domain.ErrUserNotFound)

		if _, err := svc.IssueMCPKey(context.Background(), uuid.New()); !errors.Is(err, domain.ErrUserNotFound) {
			t.Fatalf("IssueMCPKey() error = %v, want %v", err, domain.ErrUserNotFound)
		}
	})
}

func TestServiceRevokeMCPKey(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		repoErr error
		wantErr error
	}{
		"revoked":   {},
		"no key":    {repoErr: domain.ErrAccessKeyNotFound, wantErr: domain.ErrAccessKeyNotFound},
		"db failed": {repoErr: errDB, wantErr: errDB},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, p := newService(t)
			userID := uuid.New()
			p.keys.EXPECT().Revoke(gomock.Any(), userID, domain.AccessKeyKindMCP).Return(tc.repoErr)

			err := svc.RevokeMCPKey(context.Background(), userID)
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Fatalf("RevokeMCPKey() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestServiceKeysStatus(t *testing.T) {
	t.Parallel()

	svc, p := newService(t)
	userID := uuid.New()
	created := now.Add(-time.Hour)
	used := now.Add(-time.Minute)
	p.keys.EXPECT().Active(gomock.Any(), userID, domain.AccessKeyKindMCP).
		Return(domain.AccessKey{Kind: domain.AccessKeyKindMCP, CreatedAt: created, LastUsedAt: &used}, nil)
	p.keys.EXPECT().Active(gomock.Any(), userID, domain.AccessKeyKindIngest).
		Return(domain.AccessKey{}, domain.ErrAccessKeyNotFound)

	got, err := svc.KeysStatus(context.Background(), userID)
	if err != nil {
		t.Fatalf("KeysStatus() error = %v", err)
	}
	if !got.MCP.Active || !got.MCP.CreatedAt.Equal(created) || got.MCP.LastUsedAt == nil ||
		!got.MCP.LastUsedAt.Equal(used) {
		t.Fatalf("KeysStatus().MCP = %+v, want active, created %v, used %v", got.MCP, created, used)
	}
	if got.Ingest != (keys.KeyStatus{}) {
		t.Fatalf("KeysStatus().Ingest = %+v, want no key", got.Ingest)
	}
}

func TestServiceKeysStatusError(t *testing.T) {
	t.Parallel()

	svc, p := newService(t)
	p.keys.EXPECT().Active(gomock.Any(), gomock.Any(), domain.AccessKeyKindMCP).Return(domain.AccessKey{}, errDB)

	if _, err := svc.KeysStatus(context.Background(), uuid.New()); !errors.Is(err, errDB) {
		t.Fatalf("KeysStatus() error = %v, want %v", err, errDB)
	}
}

func TestServiceIngestTokenCreatesWhenNone(t *testing.T) {
	t.Parallel()

	svc, p := newService(t)
	userID := uuid.New()
	hash := []byte("hash")
	p.keys.EXPECT().Active(gomock.Any(), userID, domain.AccessKeyKindIngest).
		Return(domain.AccessKey{}, domain.ErrAccessKeyNotFound)
	p.tokens.EXPECT().New().Return("ingest-token", hash, nil)
	// The open value is stored so that every machine gets the same token.
	p.keys.EXPECT().Replace(gomock.Any(), userID, domain.AccessKeyKindIngest, hash, "ingest-token").
		Return(domain.AccessKey{Kind: domain.AccessKeyKindIngest, Plaintext: "ingest-token"}, nil)

	got, err := svc.IngestToken(context.Background(), userID)
	if err != nil {
		t.Fatalf("IngestToken() error = %v", err)
	}
	if got != "ingest-token" {
		t.Fatalf("IngestToken() = %q, want %q", got, "ingest-token")
	}
}

func TestServiceIngestTokenRepeatedReturnsSame(t *testing.T) {
	t.Parallel()

	svc, p := newService(t)
	userID := uuid.New()
	// A repeated call finds the active token and neither issues nor revokes one: the
	// mocks fail the test on New, Replace or Revoke.
	p.keys.EXPECT().Active(gomock.Any(), userID, domain.AccessKeyKindIngest).
		Return(domain.AccessKey{Kind: domain.AccessKeyKindIngest, Plaintext: "ingest-token"}, nil).Times(2)

	first, err := svc.IngestToken(context.Background(), userID)
	if err != nil {
		t.Fatalf("IngestToken() first error = %v", err)
	}
	second, err := svc.IngestToken(context.Background(), userID)
	if err != nil {
		t.Fatalf("IngestToken() second error = %v", err)
	}
	if first != "ingest-token" || second != first {
		t.Fatalf("IngestToken() = %q then %q, want %q twice", first, second, "ingest-token")
	}
}

func TestServiceIngestTokenError(t *testing.T) {
	t.Parallel()

	svc, p := newService(t)
	p.keys.EXPECT().Active(gomock.Any(), gomock.Any(), domain.AccessKeyKindIngest).Return(domain.AccessKey{}, errDB)

	if _, err := svc.IngestToken(context.Background(), uuid.New()); !errors.Is(err, errDB) {
		t.Fatalf("IngestToken() error = %v, want %v", err, errDB)
	}
}

func TestServiceReissueIngestToken(t *testing.T) {
	t.Parallel()

	svc, p := newService(t)
	userID := uuid.New()
	hash := []byte("new-hash")
	p.tokens.EXPECT().New().Return("new-token", hash, nil)
	// Replace revokes the previous token in the same transaction.
	p.keys.EXPECT().Replace(gomock.Any(), userID, domain.AccessKeyKindIngest, hash, "new-token").
		Return(domain.AccessKey{Kind: domain.AccessKeyKindIngest, Plaintext: "new-token"}, nil)

	got, err := svc.ReissueIngestToken(context.Background(), userID)
	if err != nil {
		t.Fatalf("ReissueIngestToken() error = %v", err)
	}
	if got != "new-token" {
		t.Fatalf("ReissueIngestToken() = %q, want %q", got, "new-token")
	}
}

func TestServiceResolve(t *testing.T) {
	t.Parallel()

	recent := now.Add(-30 * time.Second)
	old := now.Add(-time.Minute)

	tests := map[string]struct {
		lastUsedAt *time.Time
		wantTouch  bool
	}{
		"never used is touched":        {lastUsedAt: nil, wantTouch: true},
		"used a minute ago is touched": {lastUsedAt: &old, wantTouch: true},
		"used 30s ago is not touched":  {lastUsedAt: &recent, wantTouch: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, p := newService(t)
			keyID, userID := uuid.New(), uuid.New()
			hash := []byte("hash")
			p.tokens.EXPECT().Hash("mcp-key").Return(hash, nil)
			p.keys.EXPECT().FindByHash(gomock.Any(), hash).Return(domain.AccessKey{
				ID: keyID, UserID: userID, Kind: domain.AccessKeyKindMCP, LastUsedAt: tc.lastUsedAt,
			}, nil)
			p.clock.EXPECT().Now().Return(now)
			if tc.wantTouch {
				p.keys.EXPECT().Touch(gomock.Any(), keyID, now).Return(nil)
			}

			got, err := svc.Resolve(context.Background(), domain.AccessKeyKindMCP, "mcp-key")
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got != userID {
				t.Fatalf("Resolve() = %v, want %v", got, userID)
			}
		})
	}
}

func TestServiceResolveRefused(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		kind    domain.AccessKeyKind
		hashErr error
		found   domain.AccessKey
		findErr error
		wantErr error
	}{
		"malformed key": {
			kind: domain.AccessKeyKindMCP, hashErr: auth.ErrMalformedToken, wantErr: domain.ErrAccessKeyNotFound,
		},
		"unknown key": {
			kind: domain.AccessKeyKindMCP, findErr: domain.ErrAccessKeyNotFound, wantErr: domain.ErrAccessKeyNotFound,
		},
		"revoked key": {
			kind: domain.AccessKeyKindIngest, findErr: domain.ErrAccessKeyRevoked, wantErr: domain.ErrAccessKeyRevoked,
		},
		"ingest token given as mcp key": {
			kind:    domain.AccessKeyKindMCP,
			found:   domain.AccessKey{ID: uuid.New(), UserID: uuid.New(), Kind: domain.AccessKeyKindIngest},
			wantErr: domain.ErrAccessKeyNotFound,
		},
		"mcp key given as ingest token": {
			kind:    domain.AccessKeyKindIngest,
			found:   domain.AccessKey{ID: uuid.New(), UserID: uuid.New(), Kind: domain.AccessKeyKindMCP},
			wantErr: domain.ErrAccessKeyNotFound,
		},
		"db failed": {kind: domain.AccessKeyKindMCP, findErr: errDB, wantErr: errDB},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, p := newService(t)
			hash := []byte("hash")
			if tc.hashErr != nil {
				p.tokens.EXPECT().Hash("key").Return(nil, tc.hashErr)
			} else {
				p.tokens.EXPECT().Hash("key").Return(hash, nil)
				p.keys.EXPECT().FindByHash(gomock.Any(), hash).Return(tc.found, tc.findErr)
			}
			// A refused key is never touched: the mocks fail the test on Touch.

			got, err := svc.Resolve(context.Background(), tc.kind, "key")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Resolve() error = %v, want %v", err, tc.wantErr)
			}
			if got != uuid.Nil {
				t.Fatalf("Resolve() = %v, want uuid.Nil", got)
			}
		})
	}
}

func TestServiceResolveRevokedWhileTouching(t *testing.T) {
	t.Parallel()

	svc, p := newService(t)
	keyID := uuid.New()
	hash := []byte("hash")
	p.tokens.EXPECT().Hash("mcp-key").Return(hash, nil)
	p.keys.EXPECT().FindByHash(gomock.Any(), hash).
		Return(domain.AccessKey{ID: keyID, UserID: uuid.New(), Kind: domain.AccessKeyKindMCP}, nil)
	p.clock.EXPECT().Now().Return(now)
	p.keys.EXPECT().Touch(gomock.Any(), keyID, now).Return(domain.ErrAccessKeyRevoked)

	got, err := svc.Resolve(context.Background(), domain.AccessKeyKindMCP, "mcp-key")
	if !errors.Is(err, domain.ErrAccessKeyRevoked) {
		t.Fatalf("Resolve() error = %v, want %v", err, domain.ErrAccessKeyRevoked)
	}
	if got != uuid.Nil {
		t.Fatalf("Resolve() = %v, want uuid.Nil", got)
	}
}
