package domain_test

import (
	"errors"
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestLink_CheckUsable(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	usedAt := now.Add(-time.Hour)

	tests := map[string]struct {
		link    domain.Link
		wantErr error
	}{
		"fresh invite":     {link: domain.Link{Kind: domain.LinkKindInvite, ExpiresAt: now.Add(domain.LinkTTL)}},
		"fresh reset":      {link: domain.Link{Kind: domain.LinkKindReset, ExpiresAt: now.Add(domain.LinkTTL)}},
		"one second left":  {link: domain.Link{ExpiresAt: now.Add(time.Second)}},
		"expires at now":   {link: domain.Link{ExpiresAt: now}, wantErr: domain.ErrLinkExpired},
		"expired":          {link: domain.Link{ExpiresAt: now.Add(-time.Second)}, wantErr: domain.ErrLinkExpired},
		"used":             {link: domain.Link{ExpiresAt: now.Add(time.Hour), UsedAt: &usedAt}, wantErr: domain.ErrLinkUsed},
		"used and expired": {link: domain.Link{ExpiresAt: now.Add(-time.Hour), UsedAt: &usedAt}, wantErr: domain.ErrLinkUsed},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := tc.link.CheckUsable(now); !errors.Is(err, tc.wantErr) {
				t.Fatalf("CheckUsable() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestLinkTTL(t *testing.T) {
	t.Parallel()

	if domain.LinkTTL != 7*24*time.Hour {
		t.Fatalf("LinkTTL = %v, want 7 days", domain.LinkTTL)
	}
}

func TestLinkKind(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		kind domain.LinkKind
		want string
	}{
		"invite": {kind: domain.LinkKindInvite, want: "invite"},
		"reset":  {kind: domain.LinkKindReset, want: "reset"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if string(tc.kind) != tc.want {
				t.Fatalf("LinkKind = %q, want %q", tc.kind, tc.want)
			}
		})
	}
}
