package users

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/application/auth"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// linkPaths are the paths of the pages a link of each kind opens, below the origin.
var linkPaths = map[domain.LinkKind]string{ //nolint:gochecknoglobals // a fixed lookup table
	domain.LinkKindInvite: "/invite/",
	domain.LinkKindReset:  "/reset/",
}

// issueInvite issues a fresh invitation link of the user with userID on behalf of
// actorID, replacing the unused one, and returns its URL and expiry.
func (s *Service) issueInvite(ctx context.Context, actorID, userID uuid.UUID) (string, time.Time, error) {
	return s.issueLink(ctx, domain.LinkKindInvite, actorID, userID)
}

// issueLink issues a fresh link of kind to the user with userID on behalf of actorID,
// replacing the unused one of that kind, and returns its URL and expiry.
func (s *Service) issueLink(ctx context.Context, kind domain.LinkKind, actorID, userID uuid.UUID) (string, time.Time, error) {
	token, hash, err := s.tokens.New()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("issue %s token: %w", kind, err)
	}
	link, err := s.links.Replace(ctx, userID, kind, hash, actorID, s.clock.Now().Add(domain.LinkTTL))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("replace %s link: %w", kind, err)
	}
	return string(s.origin) + linkPaths[kind] + token, link.ExpiresAt, nil
}

// usableLink returns the link of kind that token opens; domain.ErrLinkNotFound for a
// malformed or unknown token or a link of another kind, domain.ErrLinkUsed or
// domain.ErrLinkExpired for a link that cannot be used any more.
func (s *Service) usableLink(ctx context.Context, token string, kind domain.LinkKind) (domain.Link, error) {
	hash, err := s.tokens.Hash(token)
	if errors.Is(err, auth.ErrMalformedToken) {
		return domain.Link{}, domain.ErrLinkNotFound
	}
	if err != nil {
		return domain.Link{}, fmt.Errorf("hash link token: %w", err)
	}
	link, err := s.links.GetByTokenHash(ctx, hash)
	if err != nil {
		return domain.Link{}, fmt.Errorf("get link: %w", err)
	}
	if link.Kind != kind {
		return domain.Link{}, domain.ErrLinkNotFound
	}
	if err := link.CheckUsable(s.clock.Now()); err != nil {
		return domain.Link{}, err
	}
	return link, nil
}

// spendLink sets password for the user of the link of kind that token opens and uses the
// link up, then runs then with the user's id and the time of use, and returns the user's
// id. A bad link or a password out of bounds is refused before anything is written; the
// writes share one transaction, and a link used, reissued or revoked concurrently fails
// it with domain.ErrLinkUsed or domain.ErrLinkNotFound.
func (s *Service) spendLink(
	ctx context.Context, token, password string, kind domain.LinkKind,
	then func(ctx context.Context, userID uuid.UUID, now time.Time) error,
) (uuid.UUID, error) {
	link, err := s.usableLink(ctx, token, kind)
	if err != nil {
		return uuid.Nil, err
	}
	if err := domain.ValidatePassword(password); err != nil {
		return uuid.Nil, err
	}
	// Hashed before the transaction: argon2id is slow on purpose.
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return uuid.Nil, fmt.Errorf("hash password: %w", err)
	}

	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		now := s.clock.Now()
		// The conditional update checks the link again under its row lock: of two
		// concurrent uses only one wins, and a link a reissue or a revocation deleted
		// since the read is not found.
		if err := s.links.MarkUsed(ctx, link.ID, now); err != nil {
			return fmt.Errorf("mark link used: %w", err)
		}
		if err := s.users.SetPassword(ctx, link.UserID, hash); err != nil {
			return fmt.Errorf("set password: %w", err)
		}
		return then(ctx, link.UserID, now)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return link.UserID, nil
}
