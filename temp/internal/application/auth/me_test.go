package auth_test

import (
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestService_Me(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	stored := testUser(true)
	d.users.EXPECT().GetByID(gomock.Any(), stored.ID).Return(stored, testHash, nil)

	got, err := svc.Me(t.Context(), stored.ID)
	if err != nil {
		t.Fatalf("Me() error = %v", err)
	}
	if got != stored {
		t.Fatalf("Me() = %+v, want %+v", got, stored)
	}
}

func TestService_Me_UnknownUser(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	stored := testUser(true)
	d.users.EXPECT().GetByID(gomock.Any(), stored.ID).Return(domain.User{}, "", domain.ErrUserNotFound)

	if _, err := svc.Me(t.Context(), stored.ID); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("Me() error = %v, want %v", err, domain.ErrUserNotFound)
	}
}
