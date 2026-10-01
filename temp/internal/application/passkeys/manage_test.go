package passkeys_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestService_List(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	userID := uuid.New()
	want := []domain.Passkey{testPasskey(userID, "Ноутбук"), testPasskey(userID, "Телефон")}
	d.passkeys.EXPECT().ListByUser(gomock.Any(), userID).Return(want, nil)

	got, err := svc.List(t.Context(), userID)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %+v, want %+v", got, want)
	}
}

func TestService_Delete(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	userID, passkeyID := uuid.New(), uuid.New()
	d.passkeys.EXPECT().Delete(gomock.Any(), passkeyID, userID).Return(nil)

	if err := svc.Delete(t.Context(), userID, passkeyID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
}

func TestService_Delete_OfAnotherUser(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	userID, passkeyID := uuid.New(), uuid.New()
	// The repository deletes only by both ids, so a passkey of another user is not found.
	d.passkeys.EXPECT().Delete(gomock.Any(), passkeyID, userID).Return(domain.ErrPasskeyNotFound)

	err := svc.Delete(t.Context(), userID, passkeyID)
	if !errors.Is(err, domain.ErrPasskeyNotFound) {
		t.Fatalf("Delete() error = %v, want %v", err, domain.ErrPasskeyNotFound)
	}
}
