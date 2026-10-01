package settings_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/application/settings"
	"git.alva.dev/alva/harness-telemetry/internal/application/settings/mock"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

var errDB = errors.New("db down")

type ports struct {
	settings *mock.MockSettings
	notifier *mock.MockNotifier
}

func newService(t *testing.T) (*settings.Service, ports) {
	t.Helper()
	ctrl := gomock.NewController(t)
	p := ports{settings: mock.NewMockSettings(ctrl), notifier: mock.NewMockNotifier(ctrl)}
	return settings.NewService(p.settings, p.notifier), p
}

// fullForm returns the JSON the settings are stored as.
func fullForm(t *testing.T, s domain.TelemetrySettings) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return data
}

func TestServiceGet(t *testing.T) {
	t.Parallel()

	t.Run("never saved", func(t *testing.T) {
		t.Parallel()

		svc, p := newService(t)
		userID := uuid.New()
		p.settings.EXPECT().Get(gomock.Any(), userID).Return(json.RawMessage(`{}`), int64(0), nil)

		got, version, err := svc.Get(context.Background(), userID)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if version != 0 || !reflect.DeepEqual(got, domain.DefaultTelemetrySettings()) {
			t.Fatalf("Get() = %+v, %d, want defaults, 0", got, version)
		}
	})

	t.Run("saved", func(t *testing.T) {
		t.Parallel()

		svc, p := newService(t)
		userID := uuid.New()
		p.settings.EXPECT().Get(gomock.Any(), userID).
			Return(json.RawMessage(`{"backfill_history":true}`), int64(4), nil)

		got, version, err := svc.Get(context.Background(), userID)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		want := domain.DefaultTelemetrySettings()
		want.BackfillHistory = true
		if version != 4 || !reflect.DeepEqual(got, want) {
			t.Fatalf("Get() = %+v, %d, want %+v, 4", got, version, want)
		}
	})

	// Stored settings that no longer pass the schema are a server fault, never the
	// client's invalid input.
	t.Run("stored document breaks the schema", func(t *testing.T) {
		t.Parallel()

		svc, p := newService(t)
		p.settings.EXPECT().Get(gomock.Any(), gomock.Any()).
			Return(json.RawMessage(`{"agents":{"codex":{"hook_events":{"denied":["Gone"]}}}}`), int64(2), nil)

		_, _, err := svc.Get(context.Background(), uuid.New())
		if err == nil || errors.Is(err, domain.ErrInvalidTelemetrySettings) {
			t.Fatalf("Get() error = %v, want an internal error", err)
		}
	})

	t.Run("repository failure", func(t *testing.T) {
		t.Parallel()

		svc, p := newService(t)
		p.settings.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, int64(0), errDB)

		if _, _, err := svc.Get(context.Background(), uuid.New()); !errors.Is(err, errDB) {
			t.Fatalf("Get() error = %v, want %v", err, errDB)
		}
	})
}

func TestServiceUpdate(t *testing.T) {
	t.Parallel()

	svc, p := newService(t)
	userID := uuid.New()
	want := domain.DefaultTelemetrySettings()
	want.Agents.Codex.Enabled = false
	want.Agents.Claude.HookFields.Denied = []string{"prompt"}

	p.settings.EXPECT().Get(gomock.Any(), userID).Return(json.RawMessage(`{}`), int64(3), nil)
	// The full form is stored, so every reader gets every field.
	saved := p.settings.EXPECT().Save(gomock.Any(), userID, fullForm(t, want), int64(3)).Return(nil)
	p.notifier.EXPECT().Publish(userID, int64(4)).After(saved)

	document := json.RawMessage(`{"version":3,"agents":{"codex":{"enabled":false},` +
		`"claude":{"hook_fields":{"denied":["prompt"]}}}}`)
	got, err := svc.Update(context.Background(), userID, document, 3)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got != 4 {
		t.Fatalf("Update() = %d, want 4", got)
	}
}

// Saving the settings the user already has changes neither the version nor the binaries.
func TestServiceUpdateUnchanged(t *testing.T) {
	t.Parallel()

	svc, p := newService(t)
	userID := uuid.New()
	p.settings.EXPECT().Get(gomock.Any(), userID).Return(json.RawMessage(`{}`), int64(2), nil)

	got, err := svc.Update(context.Background(), userID, fullForm(t, domain.DefaultTelemetrySettings()), 2)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got != 2 {
		t.Fatalf("Update() = %d, want 2", got)
	}
}

func TestServiceUpdateErrors(t *testing.T) {
	t.Parallel()

	changed := json.RawMessage(`{"backfill_history":true}`)

	t.Run("invalid document", func(t *testing.T) {
		t.Parallel()

		// Neither stored nor published: the mocks expect no call.
		svc, _ := newService(t)
		document := json.RawMessage(`{"agents":{"claude":{"hook_events":{"denied":["SessionPause"]}}}}`)

		if _, err := svc.Update(context.Background(), uuid.New(), document, 0); !errors.Is(
			err, domain.ErrInvalidTelemetrySettings) {
			t.Fatalf("Update() error = %v, want %v", err, domain.ErrInvalidTelemetrySettings)
		}
	})

	t.Run("stale version", func(t *testing.T) {
		t.Parallel()

		svc, p := newService(t)
		p.settings.EXPECT().Get(gomock.Any(), gomock.Any()).Return(json.RawMessage(`{}`), int64(5), nil)

		if _, err := svc.Update(context.Background(), uuid.New(), changed, 4); !errors.Is(
			err, domain.ErrSettingsVersionConflict) {
			t.Fatalf("Update() error = %v, want %v", err, domain.ErrSettingsVersionConflict)
		}
	})

	t.Run("concurrent save", func(t *testing.T) {
		t.Parallel()

		svc, p := newService(t)
		p.settings.EXPECT().Get(gomock.Any(), gomock.Any()).Return(json.RawMessage(`{}`), int64(1), nil)
		p.settings.EXPECT().Save(gomock.Any(), gomock.Any(), gomock.Any(), int64(1)).
			Return(domain.ErrSettingsVersionConflict)

		if _, err := svc.Update(context.Background(), uuid.New(), changed, 1); !errors.Is(
			err, domain.ErrSettingsVersionConflict) {
			t.Fatalf("Update() error = %v, want %v", err, domain.ErrSettingsVersionConflict)
		}
	})

	t.Run("read failure", func(t *testing.T) {
		t.Parallel()

		svc, p := newService(t)
		p.settings.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, int64(0), errDB)

		if _, err := svc.Update(context.Background(), uuid.New(), changed, 0); !errors.Is(err, errDB) {
			t.Fatalf("Update() error = %v, want %v", err, errDB)
		}
	})

	t.Run("save failure", func(t *testing.T) {
		t.Parallel()

		svc, p := newService(t)
		p.settings.EXPECT().Get(gomock.Any(), gomock.Any()).Return(json.RawMessage(`{}`), int64(0), nil)
		p.settings.EXPECT().Save(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errDB)

		if _, err := svc.Update(context.Background(), uuid.New(), changed, 0); !errors.Is(err, errDB) {
			t.Fatalf("Update() error = %v, want %v", err, errDB)
		}
	})
}

func TestServiceSubscribe(t *testing.T) {
	t.Parallel()

	svc, p := newService(t)
	userID := uuid.New()
	ctx := context.Background()
	events := make(chan int64)
	p.notifier.EXPECT().Subscribe(ctx, userID).Return(events)

	if got := svc.Subscribe(ctx, userID); got != (<-chan int64)(events) {
		t.Fatalf("Subscribe() = %v, want the notifier's channel", got)
	}
}
