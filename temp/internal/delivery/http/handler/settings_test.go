package handler_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"testing"

	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/handler/mock"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/openapi"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// settingsServer serves the contract on settings.
func settingsServer(t *testing.T, settings *mock.MockSettings) http.Handler {
	t.Helper()
	ctrl := gomock.NewController(t)
	return serve(mock.NewMockAuth(ctrl), mock.NewMockSessions(ctrl), mock.NewMockUsers(ctrl),
		mock.NewMockPasskeys(ctrl), mock.NewMockKeys(ctrl), settings, slog.New(slog.DiscardHandler))
}

// versioned returns the body the settings API answers settings at version with.
func versioned(t *testing.T, settings domain.TelemetrySettings, version int64) string {
	t.Helper()
	document, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	return fmt.Sprintf(`{"settings":%s,"version":%d}`, document, version)
}

func TestGetMyTelemetrySettings(t *testing.T) {
	t.Parallel()

	stored := domain.DefaultTelemetrySettings()
	stored.Agents.Codex.Enabled = false
	stored.Folders.Denied = []string{"~/work/secret/**"}
	tests := []struct {
		name     string
		settings domain.TelemetrySettings
		version  int64
	}{
		{name: "never saved", settings: domain.DefaultTelemetrySettings(), version: 0},
		{name: "saved", settings: stored, version: 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := mock.NewMockSettings(gomock.NewController(t))
			settings.EXPECT().Get(gomock.Any(), userID).Return(tt.settings, tt.version, nil)

			rec := do(t, settingsServer(t, settings), http.MethodGet, "/me/telemetry-settings", "", true)

			wantBody(t, rec, http.StatusOK, versioned(t, tt.settings, tt.version))
		})
	}
}

func TestGetMyTelemetrySettingsFullForm(t *testing.T) {
	t.Parallel()

	settings := mock.NewMockSettings(gomock.NewController(t))
	settings.EXPECT().Get(gomock.Any(), userID).Return(domain.DefaultTelemetrySettings(), int64(0), nil)

	rec := do(t, settingsServer(t, settings), http.MethodGet, "/me/telemetry-settings", "", true)

	// Every field of the schema is answered, defaults included, so the page shows them all.
	agent := `{"enabled":true,"sources":{"hooks":true,"transcripts":true,"native_metrics":true,` +
		`"native_logs":true,"native_traces":true},"hook_events":{"denied":[]},"hook_fields":{"denied":[]},` +
		`"native_content":{"denied":[]}}`
	wantBody(t, rec, http.StatusOK, `{"settings":{"agents":{"claude":`+agent+`,"codex":`+agent+`},`+
		`"folders":{"denied":[],"allowed":[]},"backfill_history":false},"version":0}`)
}

func TestUpdateMyTelemetrySettings(t *testing.T) {
	t.Parallel()

	document := `{"agents":{"claude":{"hook_events":{"denied":["UserPromptSubmit"]}}},"backfill_history":true}`
	settings := mock.NewMockSettings(gomock.NewController(t))
	settings.EXPECT().Update(gomock.Any(), userID, json.RawMessage(document), int64(2)).Return(int64(3), nil)

	rec := do(t, settingsServer(t, settings), http.MethodPut, "/me/telemetry-settings",
		`{"settings":`+document+`,"expectedVersion":2}`, true)

	// The saved settings are answered in full form: the absent fields with their defaults.
	want := domain.DefaultTelemetrySettings()
	want.Agents.Claude.HookEvents.Denied = []string{"UserPromptSubmit"}
	want.BackfillHistory = true
	wantBody(t, rec, http.StatusOK, versioned(t, want, 3))
}

func TestUpdateMyTelemetrySettingsConflict(t *testing.T) {
	t.Parallel()

	settings := mock.NewMockSettings(gomock.NewController(t))
	settings.EXPECT().Update(gomock.Any(), userID, json.RawMessage(`{}`), int64(1)).
		Return(int64(0), domain.ErrSettingsVersionConflict)

	rec := do(t, settingsServer(t, settings), http.MethodPut, "/me/telemetry-settings",
		`{"settings":{},"expectedVersion":1}`, true)

	wantError(t, rec, http.StatusConflict, domain.ErrSettingsVersionConflict)
}

func TestUpdateMyTelemetrySettingsInvalid(t *testing.T) {
	t.Parallel()

	document := `{"agents":{"claude":{"hook_events":{"denied":["NoSuchEvent"]}}}}`
	_, invalid := domain.ParseTelemetrySettings([]byte(document))
	if !errors.Is(invalid, domain.ErrInvalidTelemetrySettings) {
		t.Fatalf("got parse error %v, want %v", invalid, domain.ErrInvalidTelemetrySettings)
	}
	settings := mock.NewMockSettings(gomock.NewController(t))
	settings.EXPECT().Update(gomock.Any(), userID, json.RawMessage(document), int64(0)).Return(int64(0), invalid)

	rec := do(t, settingsServer(t, settings), http.MethodPut, "/me/telemetry-settings",
		`{"settings":`+document+`,"expectedVersion":0}`, true)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	var got openapi.InvalidTelemetrySettingsError
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	want := openapi.InvalidTelemetrySettingsError{
		Code:    domain.ErrInvalidTelemetrySettings.Code,
		Message: domain.ErrInvalidTelemetrySettings.Message,
		Detail:  `agents.claude.hook_events.denied[0]: unknown hook event "NoSuchEvent"`,
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestTelemetrySettingsFail(t *testing.T) {
	t.Parallel()

	failure := errors.New("database down")
	tests := []struct {
		name   string
		method string
		body   string
		expect func(s *mock.MockSettingsMockRecorder)
	}{
		{
			name: "get", method: http.MethodGet,
			expect: func(s *mock.MockSettingsMockRecorder) {
				s.Get(gomock.Any(), userID).Return(domain.TelemetrySettings{}, int64(0), failure)
			},
		},
		{
			name: "update", method: http.MethodPut, body: `{"settings":{},"expectedVersion":0}`,
			expect: func(s *mock.MockSettingsMockRecorder) {
				s.Update(gomock.Any(), userID, gomock.Any(), int64(0)).Return(int64(0), failure)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := mock.NewMockSettings(gomock.NewController(t))
			tt.expect(settings.EXPECT())

			rec := do(t, settingsServer(t, settings), tt.method, "/me/telemetry-settings", tt.body, true)

			wantBody(t, rec, http.StatusInternalServerError,
				`{"code":"internal","message":"Что-то пошло не так. Попробуйте ещё раз"}`)
		})
	}
}

func TestTelemetrySettingsWithoutSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method string
		body   string
	}{
		{method: http.MethodGet},
		{method: http.MethodPut, body: `{"settings":{},"expectedVersion":0}`},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			t.Parallel()

			// The mock expects no call: a request without a session never reaches the settings.
			rec := do(t, settingsServer(t, mock.NewMockSettings(gomock.NewController(t))), tt.method,
				"/me/telemetry-settings", tt.body, false)

			wantError(t, rec, http.StatusUnauthorized, domain.ErrUnauthenticated)
		})
	}
}
