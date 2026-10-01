package users_test

import (
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// TestService_CompleteReset checks that the reset sets the password, uses the link up and
// closes every session of the user before it opens the new one.
func TestService_CompleteReset(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	link := usableLink(domain.LinkKindReset)
	sessionExpires := now.Add(domain.SessionTTL)
	gomock.InOrder(
		d.tokens.EXPECT().Hash(linkToken).Return(linkHash, nil),
		d.links.EXPECT().GetByTokenHash(gomock.Any(), linkHash).Return(link, nil),
		d.hasher.EXPECT().Hash(testPassword).Return(testHash, nil),
		d.links.EXPECT().MarkUsed(gomock.Any(), link.ID, now).Return(nil),
		d.users.EXPECT().SetPassword(gomock.Any(), link.UserID, testHash).Return(nil),
		d.sessions.EXPECT().DeleteAllForUser(gomock.Any(), link.UserID).Return(nil),
		d.opener.EXPECT().Open(gomock.Any(), link.UserID, "UA").Return("session", sessionExpires, nil),
	)

	token, expiresAt, err := svc.CompleteReset(t.Context(), linkToken, testPassword, "UA")
	if err != nil {
		t.Fatalf("CompleteReset() error = %v", err)
	}
	if token != "session" || !expiresAt.Equal(sessionExpires) {
		t.Errorf("CompleteReset() = %q, %v, want %q, %v", token, expiresAt, "session", sessionExpires)
	}
}

// TestService_CompleteReset_CloseSessionsFails checks that a reset whose sessions could
// not be closed fails and opens no session, so the transaction rolls the password back.
func TestService_CompleteReset_CloseSessionsFails(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	errDB := errors.New("db down")
	link := usableLink(domain.LinkKindReset)
	d.tokens.EXPECT().Hash(linkToken).Return(linkHash, nil)
	d.links.EXPECT().GetByTokenHash(gomock.Any(), linkHash).Return(link, nil)
	d.hasher.EXPECT().Hash(testPassword).Return(testHash, nil)
	d.links.EXPECT().MarkUsed(gomock.Any(), link.ID, now).Return(nil)
	d.users.EXPECT().SetPassword(gomock.Any(), link.UserID, testHash).Return(nil)
	d.sessions.EXPECT().DeleteAllForUser(gomock.Any(), link.UserID).Return(errDB)

	if _, _, err := svc.CompleteReset(t.Context(), linkToken, testPassword, "UA"); !errors.Is(err, errDB) {
		t.Fatalf("CompleteReset() error = %v, want %v", err, errDB)
	}
}
