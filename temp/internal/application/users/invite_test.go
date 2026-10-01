package users_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestService_Invite(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	actorID := uuid.New()
	user := invitedUser()
	hash := []byte("hash")
	expiresAt := now.Add(domain.LinkTTL)

	d.users.EXPECT().CreateInvited(gomock.Any(), domain.Email("b@example.com"), domain.UserName("Борис")).Return(user, nil)
	d.tokens.EXPECT().New().Return("tok", hash, nil)
	d.links.EXPECT().Replace(gomock.Any(), user.ID, domain.LinkKindInvite, hash, actorID, expiresAt).
		Return(domain.Link{ID: uuid.New(), UserID: user.ID, Kind: domain.LinkKindInvite, ExpiresAt: expiresAt}, nil)

	view, url, err := svc.Invite(t.Context(), actorID, " B@Example.com ", " Борис ")
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	if want := origin + "/invite/tok"; url != want {
		t.Errorf("Invite() url = %q, want %q", url, want)
	}
	if view.ID != user.ID || view.Status != domain.UserStatusInvited {
		t.Errorf("Invite() view = %+v, want invited user %s", view, user.ID)
	}
	if view.InviteExpiresAt == nil || !view.InviteExpiresAt.Equal(expiresAt) {
		t.Errorf("Invite() InviteExpiresAt = %v, want %v", view.InviteExpiresAt, expiresAt)
	}
}

func TestService_Invite_EmailTaken(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	d.users.EXPECT().CreateInvited(gomock.Any(), gomock.Any(), gomock.Any()).Return(domain.User{}, domain.ErrEmailTaken)

	_, _, err := svc.Invite(t.Context(), uuid.New(), "b@example.com", "Борис")
	if !errors.Is(err, domain.ErrEmailTaken) {
		t.Fatalf("Invite() error = %v, want %v", err, domain.ErrEmailTaken)
	}
}

func TestService_Invite_InvalidInput(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		email, name string
		want        error
	}{
		"bad email":  {email: "nope", name: "Борис", want: domain.ErrInvalidEmail},
		"empty name": {email: "b@example.com", name: "  ", want: domain.ErrInvalidName},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, _ := newService(t)

			_, _, err := svc.Invite(t.Context(), uuid.New(), tc.email, tc.name)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Invite() error = %v, want %v", err, tc.want)
			}
		})
	}
}
