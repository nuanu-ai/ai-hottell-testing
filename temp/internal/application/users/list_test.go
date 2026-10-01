package users_test

import (
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestService_List(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	withLink, withoutLink, active := invitedUser(), invitedUser(), activeUser()
	expiresAt := now.Add(domain.LinkTTL)

	d.users.EXPECT().List(gomock.Any()).Return([]domain.User{withLink, withoutLink, active}, nil)
	d.links.EXPECT().GetActiveForUser(gomock.Any(), withLink.ID, domain.LinkKindInvite).
		Return(domain.Link{UserID: withLink.ID, Kind: domain.LinkKindInvite, ExpiresAt: expiresAt}, nil)
	d.links.EXPECT().GetActiveForUser(gomock.Any(), withoutLink.ID, domain.LinkKindInvite).
		Return(domain.Link{}, domain.ErrLinkNotFound)

	views, err := svc.List(t.Context())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(views) != 3 {
		t.Fatalf("List() returned %d views, want 3", len(views))
	}
	if got := views[0].InviteExpiresAt; got == nil || !got.Equal(expiresAt) {
		t.Errorf("invited with link: InviteExpiresAt = %v, want %v", got, expiresAt)
	}
	if got := views[1].InviteExpiresAt; got != nil {
		t.Errorf("invited without link: InviteExpiresAt = %v, want nil", got)
	}
	if views[2].Status != domain.UserStatusActive || views[2].InviteExpiresAt != nil {
		t.Errorf("active: view = %+v, want active without invitation expiry", views[2])
	}
}

func TestService_List_LinkError(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	boom := errors.New("boom")
	d.users.EXPECT().List(gomock.Any()).Return([]domain.User{invitedUser()}, nil)
	d.links.EXPECT().GetActiveForUser(gomock.Any(), gomock.Any(), gomock.Any()).Return(domain.Link{}, boom)

	if _, err := svc.List(t.Context()); !errors.Is(err, boom) {
		t.Fatalf("List() error = %v, want %v", err, boom)
	}
}
