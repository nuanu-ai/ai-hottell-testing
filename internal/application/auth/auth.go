package auth

import (
	"fmt"
	"sync"
)

// dummyPassword is hashed once into the hash that a sign-in without a stored hash is
// checked against, so it takes as long as one with a wrong password.
const dummyPassword = "dummy password of no user"

// Service runs the sign-in use cases.
type Service struct {
	users    Users
	hasher   PasswordHasher
	sessions SessionService
	clock    Clock
	tx       TxManager

	dummyMu   sync.Mutex
	dummyHash string
}

// NewService returns a Service on its ports.
func NewService(users Users, hasher PasswordHasher, sessions SessionService, clock Clock, tx TxManager) *Service {
	return &Service{users: users, hasher: hasher, sessions: sessions, clock: clock, tx: tx}
}

// dummy returns the hash of dummyPassword, made by the hasher on first use so that it
// costs what the hasher's own hashes cost; a failure is not kept and is retried next time.
func (s *Service) dummy() (string, error) {
	s.dummyMu.Lock()
	defer s.dummyMu.Unlock()
	if s.dummyHash == "" {
		hash, err := s.hasher.Hash(dummyPassword)
		if err != nil {
			return "", fmt.Errorf("hash dummy password: %w", err)
		}
		s.dummyHash = hash
	}
	return s.dummyHash, nil
}
