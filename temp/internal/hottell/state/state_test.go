package state_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

func newPaths(t *testing.T) state.Paths {
	t.Helper()
	return state.PathsIn(filepath.Join(t.TempDir(), "hottell"))
}

func TestPathsForHome(t *testing.T) {
	t.Parallel()

	p := state.PathsForHome("/Users/u")
	want := map[string]string{
		"root":     "/Users/u/Library/Application Support/hottell",
		"logs":     "/Users/u/Library/Logs/hottell",
		"state":    "/Users/u/Library/Application Support/hottell/state.json",
		"settings": "/Users/u/Library/Application Support/hottell/settings.json",
		"queue":    "/Users/u/Library/Application Support/hottell/queue",
		"rejected": "/Users/u/Library/Application Support/hottell/rejected",
		"offsets":  "/Users/u/Library/Application Support/hottell/offsets",
	}
	got := map[string]string{
		"root": p.Root, "logs": p.Logs, "state": p.StateFile(), "settings": p.SettingsFile(),
		"queue": p.QueueDir(), "rejected": p.RejectedDir(), "offsets": p.OffsetsDir(),
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s = %q, want %q", name, got[name], w)
		}
	}
}

//nolint:paralleltest // sets HOTTELL_HOME
func TestDefaultPathsHonoursOverride(t *testing.T) {
	root := t.TempDir()
	t.Setenv(state.HomeEnv, root)

	p, err := state.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if p != state.PathsIn(root) {
		t.Errorf("paths = %+v, want %+v", p, state.PathsIn(root))
	}
	if p.Logs != filepath.Join(root, "logs") {
		t.Errorf("logs = %q, want under the root", p.Logs)
	}
}

func TestCredentialsRoundTrip(t *testing.T) {
	t.Parallel()
	p := newPaths(t)

	want := state.Credentials{IngestURL: "http://127.0.0.1:18080/v1/logs", CollectorToken: "test-token"}
	if err := p.WriteCredentials(want); err != nil {
		t.Fatal(err)
	}
	got, err := p.ReadCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("credentials = %+v, want %+v", got, want)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	t.Parallel()
	p := newPaths(t)

	doc := json.RawMessage(`{"version":7,"backfill_history":true}`)
	if err := p.WriteSettings(state.SettingsCache{Version: 7, Document: doc}); err != nil {
		t.Fatal(err)
	}
	got, err := p.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 7 || string(got.Document) != string(doc) {
		t.Errorf("settings = {%d %s}, want {7 %s}", got.Version, got.Document, doc)
	}
}

func TestSettingsFromContractExamples(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"settings-empty.json", "settings-full.json"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			doc, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "specs", "hottell-contract", "examples", name))
			if err != nil {
				t.Fatal(err)
			}
			var head struct {
				Version int64 `json:"version"`
			}
			if err := json.Unmarshal(doc, &head); err != nil {
				t.Fatal(err)
			}

			p := newPaths(t)
			if err := p.WriteSettings(state.SettingsCache{Version: head.Version, Document: doc}); err != nil {
				t.Fatal(err)
			}
			got, err := p.ReadSettings()
			if err != nil {
				t.Fatal(err)
			}
			if got.Version != head.Version || string(got.Document) != string(doc) {
				t.Errorf("settings = {%d, %d bytes}, want {%d, %d bytes}", got.Version, len(got.Document), head.Version, len(doc))
			}
		})
	}
}

func TestWriteSettingsRejectsBadDocument(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		cache   state.SettingsCache
		wantErr error
	}{
		"version mismatch": {state.SettingsCache{Version: 3, Document: json.RawMessage(`{"version":2}`)}, state.ErrVersionMismatch},
		"not an object":    {state.SettingsCache{Document: json.RawMessage(`null`)}, nil},
		"not JSON":         {state.SettingsCache{Document: json.RawMessage(`{`)}, nil},
		"negative version": {state.SettingsCache{Version: -1, Document: json.RawMessage(`{"version":-1}`)}, nil},
		"empty":            {state.SettingsCache{}, nil},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := newPaths(t)
			err := p.WriteSettings(tt.cache)
			if err == nil {
				t.Fatal("WriteSettings succeeded, want an error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			}
			if _, statErr := os.Stat(p.SettingsFile()); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("settings file exists after a rejected write: %v", statErr)
			}
		})
	}
}

func TestMissingFilesReadAsEmpty(t *testing.T) {
	t.Parallel()
	p := newPaths(t)

	creds, err := p.ReadCredentials()
	if err != nil {
		t.Fatalf("ReadCredentials: %v", err)
	}
	if creds != (state.Credentials{}) {
		t.Errorf("credentials = %+v, want zero", creds)
	}

	settings, err := p.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if settings.Version != 0 || settings.Document != nil {
		t.Errorf("settings = {%d %s}, want empty", settings.Version, settings.Document)
	}
}

func TestCorruptFilesAreErrors(t *testing.T) {
	t.Parallel()
	p := newPaths(t)
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{p.StateFile(), p.SettingsFile()} {
		if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := p.ReadCredentials(); err == nil {
		t.Error("ReadCredentials of a broken file succeeded")
	}
	if _, err := p.ReadSettings(); err == nil {
		t.Error("ReadSettings of a broken file succeeded")
	}
}

func TestConcurrentWritesLeaveAWholeFile(t *testing.T) {
	t.Parallel()
	p := newPaths(t)

	const writers = 16
	const rounds = 20
	written := make(map[state.Credentials]bool, writers)
	for i := range writers {
		// Tokens of different lengths: a torn write would mix them.
		written[state.Credentials{IngestURL: fmt.Sprintf("http://h%d/v1/logs", i), CollectorToken: fmt.Sprintf("%0*d", 10+i*50, i)}] = true
	}

	var wg sync.WaitGroup
	errs := make(chan error, writers*rounds*2)
	for creds := range written {
		wg.Go(func() {
			for range rounds {
				if err := p.WriteCredentials(creds); err != nil {
					errs <- err
				}
				// Every read in the middle of the writes sees one whole value.
				got, err := p.ReadCredentials()
				if err != nil {
					errs <- err
				} else if !written[got] {
					errs <- fmt.Errorf("read a value nobody wrote: %+v", got)
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	got, err := p.ReadCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if !written[got] {
		t.Errorf("final credentials = %+v, not one of the written values", got)
	}
	entries, err := os.ReadDir(p.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory holds %v, want only state.json: temporary files were left", names)
	}
}

func TestPermissions(t *testing.T) {
	t.Parallel()
	p := newPaths(t)

	// A directory created earlier with a looser mode is tightened.
	if err := os.MkdirAll(p.QueueDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{p.Root, p.QueueDir(), p.RejectedDir(), p.OffsetsDir(), p.Logs} {
		assertMode(t, dir, 0o700|os.ModeDir)
	}

	// An existing file with a looser mode is replaced by a 0600 one.
	if err := os.WriteFile(p.StateFile(), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.WriteCredentials(state.Credentials{CollectorToken: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := p.WriteSettings(state.SettingsCache{Version: 1, Document: json.RawMessage(`{"version":1}`)}); err != nil {
		t.Fatal(err)
	}
	assertMode(t, p.StateFile(), 0o600)
	assertMode(t, p.SettingsFile(), 0o600)
}

func TestWriteFileAtomicCreatesPrivateDirectory(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "a", "b")
	path := filepath.Join(dir, "offsets.json")

	if err := state.WriteFileAtomic(path, []byte("1")); err != nil {
		t.Fatal(err)
	}
	assertMode(t, dir, 0o700|os.ModeDir)
	assertMode(t, path, 0o600)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode(); got != want {
		t.Errorf("mode of %s = %v, want %v", path, got, want)
	}
}
