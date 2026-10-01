package users_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/application/auth"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// linkToken is the token of a link under test, linkHash the hash its issuer returns for it.
const linkToken = "link-token"

var linkHash = []byte("link-hash") //nolint:gochecknoglobals // a fixed test hash

// usableLink returns an unused link of kind that expires after now.
func usableLink(kind domain.LinkKind) domain.Link {
	return domain.Link{ID: uuid.New(), UserID: uuid.New(), Kind: kind, ExpiresAt: now.Add(time.Hour)}
}

// linkUse is a use case that reads a link of kind; spends marks the acceptance of an
// invitation and the completion of a reset, which set the password and use the link up.
type linkUse struct {
	kind   domain.LinkKind
	spends bool
	run    func(ctx context.Context, d deps, token, password string) error
}

// linkUses are the use cases that spend a link, by name.
func linkUses(t *testing.T) map[string]linkUse {
	t.Helper()
	return map[string]linkUse{
		"accept invite": {kind: domain.LinkKindInvite, spends: true, run: func(ctx context.Context, d deps, token, password string) error {
			_, _, err := d.svc.AcceptInvite(ctx, token, password, "UA")
			return err
		}},
		"complete reset": {kind: domain.LinkKindReset, spends: true, run: func(ctx context.Context, d deps, token, password string) error {
			_, _, err := d.svc.CompleteReset(ctx, token, password, "UA")
			return err
		}},
		"lookup invite": {kind: domain.LinkKindInvite, run: func(ctx context.Context, d deps, token, _ string) error {
			_, _, err := d.svc.LookupLink(ctx, token, domain.LinkKindInvite)
			return err
		}},
		"lookup reset": {kind: domain.LinkKindReset, run: func(ctx context.Context, d deps, token, _ string) error {
			_, _, err := d.svc.LookupLink(ctx, token, domain.LinkKindReset)
			return err
		}},
	}
}

// otherKind returns the link kind that is not kind.
func otherKind(kind domain.LinkKind) domain.LinkKind {
	if kind == domain.LinkKindInvite {
		return domain.LinkKindReset
	}
	return domain.LinkKindInvite
}

// TestService_LinkStates checks that every use case of a link refuses a link it cannot use
// and changes nothing: the strict mocks fail on any write.
func TestService_LinkStates(t *testing.T) {
	t.Parallel()

	usedAt := now.Add(-time.Minute)
	tests := map[string]struct {
		hashErr error
		link    func(kind domain.LinkKind) (domain.Link, error)
		want    error
	}{
		"malformed token": {hashErr: auth.ErrMalformedToken, want: domain.ErrLinkNotFound},
		"unknown token": {
			link: func(domain.LinkKind) (domain.Link, error) { return domain.Link{}, domain.ErrLinkNotFound },
			want: domain.ErrLinkNotFound,
		},
		"other kind": {
			link: func(kind domain.LinkKind) (domain.Link, error) { return usableLink(otherKind(kind)), nil },
			want: domain.ErrLinkNotFound,
		},
		"used": {
			link: func(kind domain.LinkKind) (domain.Link, error) {
				l := usableLink(kind)
				l.UsedAt = &usedAt
				return l, nil
			},
			want: domain.ErrLinkUsed,
		},
		"expired": {
			link: func(kind domain.LinkKind) (domain.Link, error) {
				l := usableLink(kind)
				l.ExpiresAt = now
				return l, nil
			},
			want: domain.ErrLinkExpired,
		},
	}

	for useName, use := range linkUses(t) {
		for name, tc := range tests {
			t.Run(useName+"/"+name, func(t *testing.T) {
				t.Parallel()

				_, d := newService(t)
				if tc.hashErr != nil {
					d.tokens.EXPECT().Hash(linkToken).Return(nil, tc.hashErr)
				} else {
					d.tokens.EXPECT().Hash(linkToken).Return(linkHash, nil)
					d.links.EXPECT().GetByTokenHash(gomock.Any(), linkHash).Return(tc.link(use.kind))
				}

				if err := use.run(t.Context(), d, linkToken, testPassword); !errors.Is(err, tc.want) {
					t.Fatalf("error = %v, want %v", err, tc.want)
				}
			})
		}
	}
}

// TestService_LinkShortPassword checks that a password shorter than 8 characters is
// refused before anything is written, so the link stays usable.
func TestService_LinkShortPassword(t *testing.T) {
	t.Parallel()

	for useName, use := range linkUses(t) {
		if !use.spends {
			continue
		}
		t.Run(useName, func(t *testing.T) {
			t.Parallel()

			_, d := newService(t)
			d.tokens.EXPECT().Hash(linkToken).Return(linkHash, nil)
			d.links.EXPECT().GetByTokenHash(gomock.Any(), linkHash).Return(usableLink(use.kind), nil)

			if err := use.run(t.Context(), d, linkToken, "1234567"); !errors.Is(err, domain.ErrPasswordTooShort) {
				t.Fatalf("error = %v, want %v", err, domain.ErrPasswordTooShort)
			}
		})
	}
}

// TestService_LinkSpentConcurrently checks that when another request uses the link, or a
// reissue or a revocation deletes it, between the check and the write, the use case fails
// with the link error before it stores the password and opens no session.
func TestService_LinkSpentConcurrently(t *testing.T) {
	t.Parallel()

	races := map[string]error{
		"used":                domain.ErrLinkUsed,
		"reissued or revoked": domain.ErrLinkNotFound,
	}
	for useName, use := range linkUses(t) {
		if !use.spends {
			continue
		}
		for race, want := range races {
			t.Run(useName+"/"+race, func(t *testing.T) {
				t.Parallel()

				_, d := newService(t)
				link := usableLink(use.kind)
				d.tokens.EXPECT().Hash(linkToken).Return(linkHash, nil)
				d.links.EXPECT().GetByTokenHash(gomock.Any(), linkHash).Return(link, nil)
				d.hasher.EXPECT().Hash(testPassword).Return(testHash, nil)
				d.links.EXPECT().MarkUsed(gomock.Any(), link.ID, now).Return(want)

				if err := use.run(t.Context(), d, linkToken, testPassword); !errors.Is(err, want) {
					t.Fatalf("error = %v, want %v", err, want)
				}
			})
		}
	}
}
