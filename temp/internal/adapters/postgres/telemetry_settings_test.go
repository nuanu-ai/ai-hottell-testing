package postgres_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

const (
	// testSettings and otherSettings are synthetic settings documents; the repository stores them
	// as opaque JSON objects.
	testSettings  = `{"backfill_history": true}`
	otherSettings = `{"folders": {"denied": ["~/work/**"], "allowed": []}}`
)

func TestTelemetrySettings_GetWithoutRow(t *testing.T) {
	t.Parallel()

	document, version, err := postgres.NewTelemetrySettings(testDB.Pool()).Get(t.Context(), createActive(t).ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	assertSettings(t, document, version, `{}`, 0)
}

func TestTelemetrySettings_FirstSave(t *testing.T) {
	t.Parallel()

	repo := postgres.NewTelemetrySettings(testDB.Pool())
	user := createActive(t)

	if err := repo.Save(t.Context(), user.ID, json.RawMessage(testSettings), 0); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	document, version, err := repo.Get(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	assertSettings(t, document, version, testSettings, 1)
}

func TestTelemetrySettings_Update(t *testing.T) {
	t.Parallel()

	repo := postgres.NewTelemetrySettings(testDB.Pool())
	user := createActive(t)
	saveSettings(t, user.ID, testSettings, 0)

	if err := repo.Save(t.Context(), user.ID, json.RawMessage(otherSettings), 1); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	document, version, err := repo.Get(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	assertSettings(t, document, version, otherSettings, 2)
}

func TestTelemetrySettings_VersionConflict(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		saved    int // how many times the settings are saved before the conflicting save
		expected int64
	}{
		"stale version":            {saved: 2, expected: 1},
		"version ahead":            {saved: 1, expected: 2},
		"first save not from zero": {saved: 0, expected: 3},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			repo := postgres.NewTelemetrySettings(testDB.Pool())
			user := createActive(t)
			for i := range tc.saved {
				saveSettings(t, user.ID, testSettings, int64(i))
			}

			err := repo.Save(t.Context(), user.ID, json.RawMessage(otherSettings), tc.expected)
			if !errors.Is(err, domain.ErrSettingsVersionConflict) {
				t.Fatalf("Save() error = %v, want %v", err, domain.ErrSettingsVersionConflict)
			}

			document, version, err := repo.Get(t.Context(), user.ID)
			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			want := testSettings
			if tc.saved == 0 {
				want = `{}`
			}
			assertSettings(t, document, version, want, int64(tc.saved))
		})
	}
}

func TestTelemetrySettings_SaveUnknownUser(t *testing.T) {
	t.Parallel()

	err := postgres.NewTelemetrySettings(testDB.Pool()).Save(t.Context(), uuid.New(), json.RawMessage(testSettings), 0)
	if !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("Save() error = %v, want %v", err, domain.ErrUserNotFound)
	}
}

func TestTelemetrySettings_UserDeleteCascades(t *testing.T) {
	t.Parallel()

	user := createInvited(t)
	saveSettings(t, user.ID, testSettings, 0)

	if err := postgres.NewUsers(testDB.Pool()).DeleteInvited(t.Context(), user.ID); err != nil {
		t.Fatalf("DeleteInvited() error = %v", err)
	}
	var count int
	err := testDB.Pool().QueryRow(t.Context(),
		"SELECT count(*) FROM telemetry_settings WHERE user_id = $1", user.ID).Scan(&count)
	if err != nil {
		t.Fatalf("count telemetry_settings: %v", err)
	}
	if count != 0 {
		t.Errorf("telemetry_settings rows of the deleted user = %d, want 0", count)
	}
}

// saveSettings saves document as the settings of the user with userID from expectedVersion.
func saveSettings(t *testing.T, userID uuid.UUID, document string, expectedVersion int64) {
	t.Helper()

	err := postgres.NewTelemetrySettings(testDB.Pool()).Save(t.Context(), userID, json.RawMessage(document), expectedVersion)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
}

// assertSettings fails t unless document is the JSON of want and version is wantVersion.
func assertSettings(t *testing.T, document json.RawMessage, version int64, want string, wantVersion int64) {
	t.Helper()

	var got, wantValue any
	if err := json.Unmarshal(document, &got); err != nil {
		t.Fatalf("document %q is not JSON: %v", document, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("want %q is not JSON: %v", want, err)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(wantValue)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("document = %s, want %s", gotJSON, wantJSON)
	}
	if version != wantVersion {
		t.Errorf("version = %d, want %d", version, wantVersion)
	}
}
