package claude

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// setServer is a change of mcpServers that sets name to an empty server.
func setServer(name string) func(*object) (bool, error) {
	return func(servers *object) (bool, error) {
		servers.set(name, json.RawMessage(`{}`))
		return true, nil
	}
}

// filesIn lists the names in dir.
func filesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestEditSectionRetriesAfterConcurrentChange: when Claude Code rewrites ~/.claude.json
// between hottell's read and its write, the edit is made again on the new contents, so
// Claude Code's change is not lost.
func TestEditSectionRetriesAfterConcurrentChange(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	if err := os.WriteFile(path, []byte(`{"numStartups":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	concurrent := func() {
		calls++
		if calls == 1 {
			if err := os.WriteFile(path, []byte(`{"numStartups":2}`), 0o600); err != nil {
				t.Error(err)
			}
		}
	}
	if err := editSectionWith(path, "mcpServers", setServer("hottell-local"), concurrent); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		NumStartups int                        `json:"numStartups"`
		Servers     map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("%v:\n%s", err, b)
	}
	if doc.NumStartups != 2 || doc.Servers["hottell-local"] == nil {
		t.Fatalf("the concurrent change or the edit is lost:\n%s", b)
	}
	if calls != 2 {
		t.Fatalf("checked %d times, want 2", calls)
	}
	if names := filesIn(t, dir); len(names) != 1 {
		t.Fatalf("temporary files are left: %q", names)
	}
}

// TestEditSectionGivesUpOnAFileThatKeepsChanging: after editTries attempts the edit fails
// with errChanged and leaves the file as the other process wrote it.
func TestEditSectionGivesUpOnAFileThatKeepsChanging(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	if err := os.WriteFile(path, []byte(`{"numStartups":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	concurrent := func() {
		calls++
		if err := os.WriteFile(path, []byte(`{"numStartups":`+strconv.Itoa(calls)+`}`), 0o600); err != nil {
			t.Error(err)
		}
	}
	err := editSectionWith(path, "mcpServers", setServer("hottell-local"), concurrent)
	if !errors.Is(err, errChanged) {
		t.Fatalf("err = %v, want errChanged", err)
	}
	if calls != editTries {
		t.Fatalf("tried %d times, want %d", calls, editTries)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"numStartups":` + strconv.Itoa(editTries) + `}`; string(b) != want {
		t.Fatalf("file = %s, want the other process's %s", b, want)
	}
	if names := filesIn(t, dir); len(names) != 1 {
		t.Fatalf("temporary files are left: %q", names)
	}
}

// TestEditSectionCreatesAMissingFile: a file that is still missing at the check is no
// concurrent change.
func TestEditSectionCreatesAMissingFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".claude.json")
	calls := 0
	if err := editSectionWith(path, "mcpServers", setServer("hottell-local"), func() { calls++ }); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("checked %d times, want 1", calls)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
