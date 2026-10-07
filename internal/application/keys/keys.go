package keys

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/application/auth"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// touchInterval is how often at most the last use of a key is recorded.
const touchInterval = time.Minute

// Service manages the access keys of users.
type Service struct {
	keys   AccessKeys
	tokens TokenIssuer
	clock  Clock
}

// NewService returns a Service on its ports.
func NewService(keys AccessKeys, tokens TokenIssuer, clock Clock) *Service {
	return &Service{keys: keys, tokens: tokens, clock: clock}
}

// KeyStatus is the state of the key of one kind of a user. CreatedAt and LastUsedAt are
// those of the active key and zero and nil when there is none.
type KeyStatus struct {
	Active     bool
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// Status is the state of the keys of a user, by kind.
type Status struct {
	MCP    KeyStatus
	Ingest KeyStatus
}

// IssueMCPKey issues a new MCP key of the user with userID, revoking the one they had, and
// returns its open value: it is shown once, only its hash is kept.
func (s *Service) IssueMCPKey(ctx context.Context, userID uuid.UUID) (string, error) {
	return s.replace(ctx, userID, domain.AccessKeyKindMCP)
}

// RevokeMCPKey revokes the MCP key of the user with userID; domain.ErrAccessKeyNotFound
// when there is none.
func (s *Service) RevokeMCPKey(ctx context.Context, userID uuid.UUID) error {
	if err := s.keys.Revoke(ctx, userID, domain.AccessKeyKindMCP); err != nil {
		return fmt.Errorf("revoke mcp key: %w", err)
	}
	return nil
}

// KeysStatus returns whether the user with userID has an active key of each kind, when it
// was created and when it was last used.
func (s *Service) KeysStatus(ctx context.Context, userID uuid.UUID) (Status, error) {
	mcp, err := s.status(ctx, userID, domain.AccessKeyKindMCP)
	if err != nil {
		return Status{}, err
	}
	ingest, err := s.status(ctx, userID, domain.AccessKeyKindIngest)
	if err != nil {
		return Status{}, err
	}
	return Status{MCP: mcp, Ingest: ingest}, nil
}

// IngestToken returns the collector token of the user with userID, creating it when they
// have none; the token stays the same across calls, so every machine of the user gets it.
func (s *Service) IngestToken(ctx context.Context, userID uuid.UUID) (string, error) {
	key, err := s.keys.Active(ctx, userID, domain.AccessKeyKindIngest)
	if err == nil {
		return key.Plaintext, nil
	}
	if !errors.Is(err, domain.ErrAccessKeyNotFound) {
		return "", fmt.Errorf("get ingest token: %w", err)
	}
	// Two first calls at once both create a token and the later one revokes the other;
	// the binary that got the revoked one asks again when the collector refuses it.
	return s.replace(ctx, userID, domain.AccessKeyKindIngest)
}

// ReissueIngestToken issues a new collector token of the user with userID and revokes the
// one they had: sending stops on every machine until the binary gets the new token.
func (s *Service) ReissueIngestToken(ctx context.Context, userID uuid.UUID) (string, error) {
	return s.replace(ctx, userID, domain.AccessKeyKindIngest)
}

// Resolve returns the id of the user whose active key of kind plaintext is, recording its
// use at most once a minute. A malformed or unknown key, or a key of another kind, is
// domain.ErrAccessKeyNotFound; a revoked one is domain.ErrAccessKeyRevoked.
func (s *Service) Resolve(ctx context.Context, kind domain.AccessKeyKind, plaintext string) (uuid.UUID, error) {
	hash, err := s.tokens.Hash(plaintext)
	if errors.Is(err, auth.ErrMalformedToken) {
		return uuid.Nil, domain.ErrAccessKeyNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("hash access key: %w", err)
	}
	key, err := s.keys.FindByHash(ctx, hash)
	if err != nil {
		return uuid.Nil, fmt.Errorf("find access key: %w", err)
	}
	if key.Kind != kind {
		return uuid.Nil, domain.ErrAccessKeyNotFound
	}

	now := s.clock.Now()
	if key.LastUsedAt == nil || now.Sub(*key.LastUsedAt) >= touchInterval {
		// A revocation since the read fails the touch, and the key is refused.
		if err := s.keys.Touch(ctx, key.ID, now); err != nil {
			return uuid.Nil, fmt.Errorf("touch access key: %w", err)
		}
	}
	return key.UserID, nil
}

// replace issues a new key of kind of the user with userID in place of the active one and
// returns its open value; the open value is stored only for an ingest key.
func (s *Service) replace(ctx context.Context, userID uuid.UUID, kind domain.AccessKeyKind) (string, error) {
	token, hash, err := s.tokens.New()
	if err != nil {
		return "", fmt.Errorf("issue %s key: %w", kind, err)
	}
	var plaintext string
	if kind == domain.AccessKeyKindIngest {
		plaintext = token
	}
	if _, err := s.keys.Replace(ctx, userID, kind, hash, plaintext); err != nil {
		return "", fmt.Errorf("replace %s key: %w", kind, err)
	}
	return token, nil
}

// status returns the state of the key of kind of the user with userID.
func (s *Service) status(ctx context.Context, userID uuid.UUID, kind domain.AccessKeyKind) (KeyStatus, error) {
	key, err := s.keys.Active(ctx, userID, kind)
	if errors.Is(err, domain.ErrAccessKeyNotFound) {
		return KeyStatus{}, nil
	}
	if err != nil {
		return KeyStatus{}, fmt.Errorf("get %s key: %w", kind, err)
	}
	return KeyStatus{Active: true, CreatedAt: key.CreatedAt, LastUsedAt: key.LastUsedAt}, nil
}
