package users_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestService_ReissueInvite(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	actorID := uuid.New()
	user := invitedUser()
	hash := []byte("hash")
	expiresAt := now.Add(domain.LinkTTL)

	d.users.EXPECT().GetByID(gomock.Any(), user.ID).Return(user, "", nil)
	d.tokens.EXPECT().New().Return("fresh", hash, nil)
	d.links.EXPECT().Replace(gomock.Any(), user.ID, domain.LinkKindInvite, hash, actorID, expiresAt).
		Return(domain.Link{UserID: user.ID, Kind: domain.LinkKindInvite, ExpiresAt: expiresAt}, nil)

	url, gotExpires, err := svc.ReissueInvite(t.Context(), actorID, user.ID)
	if err != nil {
		t.Fatalf("ReissueInvite() error = %v", err)
	}
	if want := origin + "/invite/fresh"; url != want {
		t.Errorf("ReissueInvite() url = %q, want %q", url, want)
	}
	if !gotExpires.Equal(expiresAt) {
		t.Errorf("ReissueInvite() expiresAt = %v, want %v", gotExpires, expiresAt)
	}
}

func TestService_ReissueInvite_Rejected(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		replaceErr error
		want       error
	}{
		"active user":  {want: domain.ErrUserNotInvited},
		"unknown user": {replaceErr: domain.ErrUserNotFound, want: domain.ErrUserNotFound},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			d.tokens.EXPECT().New().Return("fresh", []byte("hash"), nil)
			d.links.EXPECT().Replace(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(domain.Link{}, tc.replaceErr)
			if tc.replaceErr == nil {
				d.users.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(activeUser(), "hash", nil)
			}

			_, _, err := svc.ReissueInvite(t.Context(), uuid.New(), uuid.New())
			if !errors.Is(err, tc.want) {
				t.Fatalf("ReissueInvite() error = %v, want %v", err, tc.want)
			}
		})
	}
}
