package users_test

import (
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestService_AcceptInvite(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	link := usableLink(domain.LinkKindInvite)
	sessionExpires := now.Add(domain.SessionTTL)
	gomock.InOrder(
		d.tokens.EXPECT().Hash(linkToken).Return(linkHash, nil),
		d.links.EXPECT().GetByTokenHash(gomock.Any(), linkHash).Return(link, nil),
		d.hasher.EXPECT().Hash(testPassword).Return(testHash, nil),
		d.links.EXPECT().MarkUsed(gomock.Any(), link.ID, now).Return(nil),
		d.users.EXPECT().SetPassword(gomock.Any(), link.UserID, testHash).Return(nil),
		d.users.EXPECT().TouchLogin(gomock.Any(), link.UserID, now).Return(nil),
		d.opener.EXPECT().Open(gomock.Any(), link.UserID, "UA").Return("session", sessionExpires, nil),
	)

	token, expiresAt, err := svc.AcceptInvite(t.Context(), linkToken, testPassword, "UA")
	if err != nil {
		t.Fatalf("AcceptInvite() error = %v", err)
	}
	if token != "session" || !expiresAt.Equal(sessionExpires) {
		t.Errorf("AcceptInvite() = %q, %v, want %q, %v", token, expiresAt, "session", sessionExpires)
	}
}

// TestService_AcceptInvite_Twice checks that the second acceptance of one link, once the
// first has used it, is refused and changes nothing.
func TestService_AcceptInvite_Twice(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	link := usableLink(domain.LinkKindInvite)
	used := link
	usedAt := now
	used.UsedAt = &usedAt
	d.tokens.EXPECT().Hash(linkToken).Return(linkHash, nil).Times(2)
	gomock.InOrder(
		d.links.EXPECT().GetByTokenHash(gomock.Any(), linkHash).Return(link, nil),
		d.links.EXPECT().GetByTokenHash(gomock.Any(), linkHash).Return(used, nil),
	)
	d.hasher.EXPECT().Hash(testPassword).Return(testHash, nil)
	d.links.EXPECT().MarkUsed(gomock.Any(), link.ID, now).Return(nil)
	d.users.EXPECT().SetPassword(gomock.Any(), link.UserID, testHash).Return(nil)
	d.users.EXPECT().TouchLogin(gomock.Any(), link.UserID, now).Return(nil)
	d.opener.EXPECT().Open(gomock.Any(), link.UserID, "UA").Return("session", now.Add(domain.SessionTTL), nil)

	if _, _, err := svc.AcceptInvite(t.Context(), linkToken, testPassword, "UA"); err != nil {
		t.Fatalf("first AcceptInvite() error = %v", err)
	}
	if _, _, err := svc.AcceptInvite(t.Context(), linkToken, testPassword, "UA"); !errors.Is(err, domain.ErrLinkUsed) {
		t.Fatalf("second AcceptInvite() error = %v, want %v", err, domain.ErrLinkUsed)
	}
}

func TestService_AcceptInvite_DependencyErrors(t *testing.T) {
	t.Parallel()

	errDB := errors.New("db down")
	link := usableLink(domain.LinkKindInvite)
	tests := map[string]func(d deps){
		"touch login fails": func(d deps) {
			d.links.EXPECT().MarkUsed(gomock.Any(), link.ID, now).Return(nil)
			d.users.EXPECT().SetPassword(gomock.Any(), link.UserID, testHash).Return(nil)
			d.users.EXPECT().TouchLogin(gomock.Any(), link.UserID, now).Return(errDB)
		},
		"open session fails": func(d deps) {
			d.links.EXPECT().MarkUsed(gomock.Any(), link.ID, now).Return(nil)
			d.users.EXPECT().SetPassword(gomock.Any(), link.UserID, testHash).Return(nil)
			d.users.EXPECT().TouchLogin(gomock.Any(), link.UserID, now).Return(nil)
			d.opener.EXPECT().Open(gomock.Any(), link.UserID, "UA").Return("", now, errDB)
		},
	}
	for name, expect := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			d.tokens.EXPECT().Hash(linkToken).Return(linkHash, nil)
			d.links.EXPECT().GetByTokenHash(gomock.Any(), linkHash).Return(link, nil)
			d.hasher.EXPECT().Hash(testPassword).Return(testHash, nil)
			expect(d)

			if _, _, err := svc.AcceptInvite(t.Context(), linkToken, testPassword, "UA"); !errors.Is(err, errDB) {
				t.Fatalf("AcceptInvite() error = %v, want %v", err, errDB)
			}
		})
	}
}
