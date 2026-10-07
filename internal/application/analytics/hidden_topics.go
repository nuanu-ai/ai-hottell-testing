package analytics

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// topicKeyMax is the longest topic key, in characters.
const topicKeyMax = 200

// errNoHiddenTopics: the Service was built without WithHiddenTopics.
var errNoHiddenTopics = errors.New("analytics service has no hidden topics store")

// WithHiddenTopics gives the Service the store of the topics each person hid.
func WithHiddenTopics(store HiddenTopicsStore) Option {
	return func(s *Service) { s.hidden = store }
}

// HideTopic hides the topic key, a card id, for the person userID only: «not a problem». Hiding
// a hidden topic again is no error. domain.ErrInvalidTopicKey for an empty key, one longer than
// topicKeyMax characters or one that is not text.
func (s *Service) HideTopic(ctx context.Context, userID uuid.UUID, key string) error {
	if err := s.hiddenReady(key); err != nil {
		return err
	}
	if err := s.hidden.Hide(ctx, userID, key, s.clock.Now()); err != nil {
		return fmt.Errorf("hide topic: %w", err)
	}
	return nil
}

// UnhideTopic shows the topic key to the person userID again; a topic not hidden is no error.
func (s *Service) UnhideTopic(ctx context.Context, userID uuid.UUID, key string) error {
	if err := s.hiddenReady(key); err != nil {
		return err
	}
	if err := s.hidden.Unhide(ctx, userID, key); err != nil {
		return fmt.Errorf("unhide topic: %w", err)
	}
	return nil
}

// UnhideAllTopics shows every topic the person userID hid again.
func (s *Service) UnhideAllTopics(ctx context.Context, userID uuid.UUID) error {
	if s.hidden == nil {
		return errNoHiddenTopics
	}
	if err := s.hidden.UnhideAll(ctx, userID); err != nil {
		return fmt.Errorf("unhide all topics: %w", err)
	}
	return nil
}

// HiddenTopics returns the topics the person userID hid, the earliest hidden first; an empty
// list when none.
func (s *Service) HiddenTopics(ctx context.Context, userID uuid.UUID) ([]string, error) {
	if s.hidden == nil {
		return nil, errNoHiddenTopics
	}
	keys, err := s.hidden.List(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list hidden topics: %w", err)
	}
	if keys == nil {
		keys = []string{}
	}
	return keys, nil
}

// hiddenReady checks the store and the key.
func (s *Service) hiddenReady(key string) error {
	if s.hidden == nil {
		return errNoHiddenTopics
	}
	n := utf8.RuneCountInString(key)
	if n == 0 || n > topicKeyMax || !utf8.ValidString(key) || strings.ContainsRune(key, 0) {
		return domain.ErrInvalidTopicKey
	}
	return nil
}
