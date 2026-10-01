package users_test

import (
	"testing"

	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestService_LookupLink(t *testing.T) {
	t.Parallel()

	for _, kind := range []domain.LinkKind{domain.LinkKindInvite, domain.LinkKindReset} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			link := usableLink(kind)
			user := invitedUser()
			user.ID = link.UserID
			d.tokens.EXPECT().Hash(linkToken).Return(linkHash, nil)
			d.links.EXPECT().GetByTokenHash(gomock.Any(), linkHash).Return(link, nil)
			d.users.EXPECT().GetByID(gomock.Any(), link.UserID).Return(user, "", nil)

			email, name, err := svc.LookupLink(t.Context(), linkToken, kind)
			if err != nil {
				t.Fatalf("LookupLink() error = %v", err)
			}
			if email != user.Email.String() || name != string(user.Name) {
				t.Errorf("LookupLink() = %q, %q, want %q, %q", email, name, user.Email, user.Name)
			}
		})
	}
}
