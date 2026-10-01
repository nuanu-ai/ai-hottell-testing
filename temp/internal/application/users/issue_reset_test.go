package users_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestService_IssueReset(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	actorID := uuid.New()
	user := activeUser()
	hash := []byte("hash")
	expiresAt := now.Add(domain.LinkTTL)

	d.users.EXPECT().GetByID(gomock.Any(), user.ID).Return(user, testHash, nil)
	d.tokens.EXPECT().New().Return("tok", hash, nil)
	d.links.EXPECT().Replace(gomock.Any(), user.ID, domain.LinkKindReset, hash, actorID, expiresAt).
		Return(domain.Link{UserID: user.ID, Kind: domain.LinkKindReset, ExpiresAt: expiresAt}, nil)

	url, gotExpires, err := svc.IssueReset(t.Context(), actorID, user.ID)
	if err != nil {
		t.Fatalf("IssueReset() error = %v", err)
	}
	if want := origin + "/reset/tok"; url != want {
		t.Errorf("IssueReset() url = %q, want %q", url, want)
	}
	if !gotExpires.Equal(expiresAt) {
		t.Errorf("IssueReset() expiresAt = %v, want %v", gotExpires, expiresAt)
	}
}

// TestService_IssueReset_Self checks that a user cannot issue a reset link to oneself:
// the own password is changed in the profile.
func TestService_IssueReset_Self(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	id := uuid.New()

	if _, _, err := svc.IssueReset(t.Context(), id, id); !errors.Is(err, domain.ErrCannotResetSelf) {
		t.Fatalf("IssueReset() error = %v, want %v", err, domain.ErrCannotResetSelf)
	}
}

func TestService_IssueReset_Rejected(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		user   domain.User
		getErr error
		want   error
	}{
		"invited user": {user: invitedUser(), want: domain.ErrUserNotActive},
		"unknown user": {getErr: domain.ErrUserNotFound, want: domain.ErrUserNotFound},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			d.users.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(tc.user, "", tc.getErr)

			if _, _, err := svc.IssueReset(t.Context(), uuid.New(), uuid.New()); !errors.Is(err, tc.want) {
				t.Fatalf("IssueReset() error = %v, want %v", err, tc.want)
			}
		})
	}
}
