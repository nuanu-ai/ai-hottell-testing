// Package session opens, recognizes with extension, and closes the sessions of signed-in
// browsers.
package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/application/auth"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// maxUserAgent is how many characters of the User-Agent header a session keeps.
const maxUserAgent = 255

// Service manages sessions.
type Service struct {
	sessions Sessions
	tokens   TokenIssuer
	clock    Clock
}

// NewService returns a Service on its ports.
func NewService(sessions Sessions, tokens TokenIssuer, clock Clock) *Service {
	return &Service{sessions: sessions, tokens: tokens, clock: clock}
}

// Open opens a session of the user with userID for 30 days and returns its token for
// the client and its expiry; userAgent is kept up to 255 characters.
func (s *Service) Open(ctx context.Context, userID uuid.UUID, userAgent string) (string, time.Time, error) {
	token, hash, err := s.tokens.New()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("issue session token: %w", err)
	}
	expiresAt := s.clock.Now().Add(domain.SessionTTL)
	if _, err := s.sessions.Create(ctx, userID, hash, expiresAt, truncate(userAgent, maxUserAgent)); err != nil {
		return "", time.Time{}, fmt.Errorf("create session: %w", err)
	}
	return token, expiresAt, nil
}

// Resolve returns the live session of token, extending it when activity moves its
// expiry; the new expiry is returned so the client's cookie can follow it, nil when it
// did not move. A malformed, unknown or expired token is domain.ErrUnauthenticated.
func (s *Service) Resolve(ctx context.Context, token string) (domain.Session, *time.Time, error) {
	hash, err := s.tokens.Hash(token)
	if errors.Is(err, auth.ErrMalformedToken) {
		return domain.Session{}, nil, domain.ErrUnauthenticated
	}
	if err != nil {
		return domain.Session{}, nil, fmt.Errorf("hash session token: %w", err)
	}
	session, err := s.sessions.GetByTokenHash(ctx, hash)
	if err != nil {
		return domain.Session{}, nil, fmt.Errorf("get session: %w", err)
	}

	// The store filters by the database clock; a session expired by the application
	// clock is refused too, before NextExpiry would extend it.
	now := s.clock.Now()
	if !now.Before(session.ExpiresAt) {
		return domain.Session{}, nil, domain.ErrSessionNotFound
	}
	expiresAt, moved := session.NextExpiry(now)
	if !moved {
		return session, nil, nil
	}
	if err := s.sessions.Extend(ctx, session.ID, expiresAt, now); err != nil {
		return domain.Session{}, nil, fmt.Errorf("extend session: %w", err)
	}
	session.ExpiresAt = expiresAt
	session.LastSeenAt = now
	return session, &expiresAt, nil
}

// Close closes the session with sessionID.
func (s *Service) Close(ctx context.Context, sessionID uuid.UUID) error {
	if err := s.sessions.Delete(ctx, sessionID); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// CloseAll closes every session of the user with userID.
func (s *Service) CloseAll(ctx context.Context, userID uuid.UUID) error {
	if err := s.sessions.DeleteAllForUser(ctx, userID); err != nil {
		return fmt.Errorf("delete user sessions: %w", err)
	}
	return nil
}

// CloseOthers closes every session of the user with userID but keepID.
func (s *Service) CloseOthers(ctx context.Context, userID, keepID uuid.UUID) error {
	if err := s.sessions.DeleteAllForUserExcept(ctx, userID, keepID); err != nil {
		return fmt.Errorf("delete other user sessions: %w", err)
	}
	return nil
}

// truncate returns s cut to at most n characters.
func truncate(s string, n int) string {
	i := 0
	for pos := range s {
		if i == n {
			return s[:pos]
		}
		i++
	}
	return s
}
