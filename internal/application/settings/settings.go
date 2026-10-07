package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// errStoredSettings: the stored settings no longer pass the schema. It is a server fault
// and deliberately does not wrap domain.ErrInvalidTelemetrySettings, the client's error.
var errStoredSettings = errors.New("stored telemetry settings break the schema")

// Service reads and changes the deny settings of users and tells their subscribers of
// every change.
type Service struct {
	settings Settings
	notifier Notifier
}

// NewService returns a Service on its ports.
func NewService(settings Settings, notifier Notifier) *Service {
	return &Service{settings: settings, notifier: notifier}
}

// Get returns the settings of the user with userID in full form and their version; the
// defaults and version 0 when the user has never saved settings.
func (s *Service) Get(ctx context.Context, userID uuid.UUID) (domain.TelemetrySettings, int64, error) {
	document, version, err := s.settings.Get(ctx, userID)
	if err != nil {
		return domain.TelemetrySettings{}, 0, fmt.Errorf("get telemetry settings: %w", err)
	}
	parsed, err := domain.ParseTelemetrySettings(document)
	if err != nil {
		return domain.TelemetrySettings{}, 0, fmt.Errorf("%w: %s", errStoredSettings, err.Error())
	}
	return parsed, version, nil
}

// Update checks document against the settings schema, stores it as the settings of the
// user with userID in place of version expectedVersion and tells the subscribers of the
// user; it returns the version the settings now have. A document equal to the stored one
// is not stored again and keeps the version. domain.ErrInvalidTelemetrySettings when the
// document breaks the schema, domain.ErrSettingsVersionConflict when the settings are no
// longer at expectedVersion, domain.ErrUserNotFound when there is no such user.
func (s *Service) Update(
	ctx context.Context, userID uuid.UUID, document json.RawMessage, expectedVersion int64,
) (int64, error) {
	parsed, err := domain.ParseTelemetrySettings(document)
	if err != nil {
		return 0, err
	}
	current, version, err := s.Get(ctx, userID)
	if err != nil {
		return 0, err
	}
	if version != expectedVersion {
		return 0, domain.ErrSettingsVersionConflict
	}
	if reflect.DeepEqual(current, parsed) {
		return version, nil
	}

	full, err := json.Marshal(parsed)
	if err != nil {
		return 0, fmt.Errorf("marshal telemetry settings: %w", err)
	}
	if err := s.settings.Save(ctx, userID, full, expectedVersion); err != nil {
		return 0, fmt.Errorf("save telemetry settings: %w", err)
	}
	newVersion := expectedVersion + 1
	s.notifier.Publish(userID, newVersion)
	return newVersion, nil
}

// Subscribe returns the channel the new versions of the settings of the user with userID
// arrive on after every Update; it is closed once ctx is done.
func (s *Service) Subscribe(ctx context.Context, userID uuid.UUID) <-chan int64 {
	return s.notifier.Subscribe(ctx, userID)
}
