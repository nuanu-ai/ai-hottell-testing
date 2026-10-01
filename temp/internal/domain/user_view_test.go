package domain_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestNewUserView(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	login := created.Add(time.Hour)
	expires := created.Add(domain.LinkTTL)

	tests := map[string]struct {
		user        domain.User
		wantStatus  domain.UserStatus
		wantExpires *time.Time
	}{
		"invited keeps the invitation expiry": {
			user:        domain.User{HasPassword: false},
			wantStatus:  domain.UserStatusInvited,
			wantExpires: &expires,
		},
		"active drops the invitation expiry": {
			user:        domain.User{HasPassword: true, LastLoginAt: &login},
			wantStatus:  domain.UserStatusActive,
			wantExpires: nil,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tc.user.ID = uuid.New()
			tc.user.Email = "a@example.com"
			tc.user.Name = "Анна"
			tc.user.CreatedAt = created

			got := domain.NewUserView(tc.user, &expires)

			want := domain.UserView{
				ID:              tc.user.ID,
				Email:           tc.user.Email,
				Name:            tc.user.Name,
				Status:          tc.wantStatus,
				CreatedAt:       created,
				LastLoginAt:     tc.user.LastLoginAt,
				InviteExpiresAt: tc.wantExpires,
			}
			if got != want {
				t.Fatalf("NewUserView() = %+v, want %+v", got, want)
			}
		})
	}
}
