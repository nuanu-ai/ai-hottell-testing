package domain_test

import (
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestUser_Status(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		user domain.User
		want domain.UserStatus
	}{
		"without password": {user: domain.User{HasPassword: false}, want: domain.UserStatusInvited},
		"with password":    {user: domain.User{HasPassword: true}, want: domain.UserStatusActive},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := tc.user.Status(); got != tc.want {
				t.Fatalf("Status() = %q, want %q", got, tc.want)
			}
		})
	}
}
