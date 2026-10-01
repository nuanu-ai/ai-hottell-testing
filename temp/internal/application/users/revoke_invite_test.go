package users_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestService_RevokeInvite(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		deleteErr error
		want      error
	}{
		"invited user": {},
		"active user":  {deleteErr: domain.ErrUserNotInvited, want: domain.ErrUserNotInvited},
		"unknown user": {deleteErr: domain.ErrUserNotFound, want: domain.ErrUserNotFound},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			userID := uuid.New()
			d.users.EXPECT().DeleteInvited(gomock.Any(), userID).Return(tc.deleteErr)

			err := svc.RevokeInvite(t.Context(), userID)
			if !errors.Is(err, tc.want) {
				t.Fatalf("RevokeInvite() error = %v, want %v", err, tc.want)
			}
		})
	}
}
