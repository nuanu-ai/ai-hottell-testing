package domain_test

import (
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestSession_NextExpiry(t *testing.T) {
	t.Parallel()

	const day = 24 * time.Hour
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	tests := map[string]struct {
		expiresAt time.Time
		want      time.Time
		wantMoved bool
	}{
		"just opened":             {expiresAt: now.Add(30 * day), want: now.Add(30 * day), wantMoved: false},
		"exactly 29 days left":    {expiresAt: now.Add(29 * day), want: now.Add(29 * day), wantMoved: false},
		"just under 29 days left": {expiresAt: now.Add(29*day - time.Second), want: now.Add(30 * day), wantMoved: true},
		"one day left":            {expiresAt: now.Add(day), want: now.Add(30 * day), wantMoved: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := domain.Session{ExpiresAt: tc.expiresAt}

			got, moved := s.NextExpiry(now)

			if !got.Equal(tc.want) || moved != tc.wantMoved {
				t.Fatalf("NextExpiry() = (%v, %v), want (%v, %v)", got, moved, tc.want, tc.wantMoved)
			}
		})
	}
}

func TestSessionTTL(t *testing.T) {
	t.Parallel()

	if domain.SessionTTL != 30*24*time.Hour {
		t.Fatalf("SessionTTL = %v, want 30 days", domain.SessionTTL)
	}
}
