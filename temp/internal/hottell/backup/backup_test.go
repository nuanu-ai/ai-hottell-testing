package backup_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/backup"
)

// setLayout is how the store names a set, by its UTC time.
const setLayout = "20060102T150405.000000000Z"

// fixture is a home with two config files, the first present and the second absent, and
// a store whose clock moves a second per set.
type fixture struct {
	present, absent string
	store           *backup.Store
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	home := t.TempDir()
	f := fixture{
		present: filepath.Join(home, ".claude", "settings.json"),
		absent:  filepath.Join(home, ".codex", "hooks.json"),
	}
	if err := os.MkdirAll(filepath.Dir(f.present), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.present, []byte("{\"model\": \"opus\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	f.store = &backup.Store{
		Dir: filepath.Join(home, "state", "backup"),
		Files: []backup.File{
			{Name: "claude-settings.json", Path: f.present},
			{Name: "codex-hooks.json", Path: f.absent},
		},
		Now: func() time.Time {
			clock = clock.Add(time.Second)
			return clock
		},
	}
	return f
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func sets(t *testing.T, s *backup.Store) []backup.Set {
	t.Helper()
	got, err := s.Sets()
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestTake(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	set, err := f.store.Take(backup.ReasonInstall)
	if err != nil {
		t.Fatal(err)
	}
	if set.Name != "20261001T120001.000000000Z" || set.Reason != backup.ReasonInstall {
		t.Errorf("set %s reason %s", set.Name, set.Reason)
	}
	dir := filepath.Join(f.store.Dir, set.Name)
	for path, want := range map[string]os.FileMode{f.store.Dir: 0o700 | os.ModeDir, dir: 0o700 | os.ModeDir, filepath.Join(dir, "claude-settings.json"): 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode() != want {
			t.Errorf("mode of %s = %v, want %v", path, info.Mode(), want)
		}
	}
	if got := read(t, filepath.Join(dir, "claude-settings.json")); got != read(t, f.present) {
		t.Errorf("copy = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "codex-hooks.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a copy of the absent file: %v", err)
	}

	listed := sets(t, f.store)
	if len(listed) != 1 || listed[0].Name != set.Name {
		t.Fatalf("sets = %+v", listed)
	}
	files := listed[0].Files
	if len(files) != 2 || files[0].Path != f.present || files[0].Absent || len(files[0].SHA256) != 64 || files[0].Mode != 0o644 ||
		files[1].Path != f.absent || !files[1].Absent {
		t.Errorf("manifest files = %+v", files)
	}
}

func TestTakeKeepsTheNewestSets(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	install, err := f.store.Take(backup.ReasonInstall)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for range backup.Keep + 3 {
		set, err := f.store.Take(backup.ReasonApply)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, set.Name)
	}

	got := sets(t, f.store)
	// The newest Keep sets, newest first, and the set of the last install, which restore
	// takes by default.
	if len(got) != backup.Keep+1 {
		t.Fatalf("%d sets kept, want %d", len(got), backup.Keep+1)
	}
	for i := range backup.Keep {
		if want := names[len(names)-1-i]; got[i].Name != want {
			t.Errorf("set %d = %s, want %s", i, got[i].Name, want)
		}
	}
	if last := got[backup.Keep]; last.Name != install.Name {
		t.Errorf("oldest kept set %s, want the install set %s", last.Name, install.Name)
	}
	entries, err := os.ReadDir(f.store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirs := len(entries) - 1; dirs != backup.Keep+1 { // and the record of the fingerprints
		t.Errorf("%d entries in %s", len(entries), f.store.Dir)
	}
}

// TestTakeDropsOlderIncompleteSets: interrupted takes left set directories without a
// manifest; they count for nothing, and the rotation removes those older than the newest
// complete set but leaves a newer one, which may be a take in progress.
func TestTakeDropsOlderIncompleteSets(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	var names []string
	for range backup.Keep {
		set, err := f.store.Take(backup.ReasonApply)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, set.Name)
	}
	// Ten incomplete sets between the last take, at 12:00:10, and the next, at 12:00:11.
	var incomplete []string
	for i := range 10 {
		at := time.Date(2026, 10, 1, 12, 0, 10, (i+1)*int(10*time.Millisecond), time.UTC)
		incomplete = append(incomplete, at.Format(setLayout))
	}
	inProgress := time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC).Format(setLayout)
	for _, name := range append(incomplete, inProgress) {
		write(t, filepath.Join(f.store.Dir, name, "claude-settings.json"), "partial\n")
	}

	set, err := f.store.Take(backup.ReasonApply)
	if err != nil {
		t.Fatal(err)
	}
	names = append(names, set.Name)

	got := sets(t, f.store)
	if len(got) != backup.Keep {
		t.Fatalf("%d complete sets kept, want %d", len(got), backup.Keep)
	}
	for i := range backup.Keep {
		if want := names[len(names)-1-i]; got[i].Name != want {
			t.Errorf("set %d = %s, want %s", i, got[i].Name, want)
		}
	}
	for _, name := range incomplete {
		if _, err := os.Stat(filepath.Join(f.store.Dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("incomplete set %s older than the newest complete set is left: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(f.store.Dir, inProgress, "claude-settings.json")); err != nil {
		t.Errorf("incomplete set %s newer than every complete set is removed: %v", inProgress, err)
	}
}

func TestRestore(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	original := read(t, f.present)
	set, err := f.store.Take(backup.ReasonInstall)
	if err != nil {
		t.Fatal(err)
	}
	// hottell writes both files.
	write(t, f.present, "{\"hooks\": {}}\n")
	write(t, f.absent, "{}\n")

	outcomes, err := f.store.Restore(set)
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, f.present); got != original {
		t.Errorf("restored %q, want %q", got, original)
	}
	if info, err := os.Stat(f.present); err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("restored mode: %v %v", info, err)
	}
	if _, err := os.Stat(f.absent); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the file absent at the backup is left: %v", err)
	}
	want := []backup.Outcome{
		{Path: f.present, Done: backup.DoneRestored, Copy: filepath.Join(f.store.Dir, set.Name, "claude-settings.json")},
		{Path: f.absent, Done: backup.DoneRemoved},
	}
	if len(outcomes) != len(want) {
		t.Fatalf("outcomes %+v", outcomes)
	}
	for i := range want {
		if outcomes[i] != want[i] {
			t.Errorf("outcome %d = %+v, want %+v", i, outcomes[i], want[i])
		}
	}

	// Restored again: the absent file is still absent.
	outcomes, err = f.store.Restore(set)
	if err != nil {
		t.Fatal(err)
	}
	if outcomes[1].Done != backup.DoneAbsent {
		t.Errorf("second restore outcomes %+v", outcomes)
	}
}

// TestRestoreLeavesADirectory: a path absent at the backup is removed only when it is a
// regular file now.
func TestRestoreLeavesADirectory(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	set, err := f.store.Take(backup.ReasonInstall)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.absent, "inside"), "kept\n")

	outcomes, err := f.store.Restore(set)
	if err == nil || outcomes[1].Done != backup.DoneFailed {
		t.Errorf("restore over a directory: %+v, %v", outcomes, err)
	}
	if got := read(t, filepath.Join(f.absent, "inside")); got != "kept\n" {
		t.Errorf("the directory's file %q", got)
	}
}

// symlinkState is what a link and its target hold, to tell that restore left them alone.
func symlinkState(t *testing.T, link string) string {
	t.Helper()
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("%s is no longer a link: %v", link, err)
	}
	data, err := os.ReadFile(link)
	if errors.Is(err, os.ErrNotExist) {
		return target + " -> (missing)"
	}
	if err != nil {
		t.Fatal(err)
	}
	return target + " -> " + string(data)
}

// TestRestoreSkipsASymlink: a config that was a link at the backup is left to the user,
// who gets the copy, taken through the link.
func TestRestoreSkipsASymlink(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	target := filepath.Join(filepath.Dir(f.present), "dotfiles-settings.json")
	if err := os.Rename(f.present, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, f.present); err != nil {
		t.Fatal(err)
	}
	original := read(t, target)
	set, err := f.store.Take(backup.ReasonInstall)
	if err != nil {
		t.Fatal(err)
	}
	if !set.Files[0].Symlink || set.Files[1].Symlink {
		t.Fatalf("manifest files %+v, want only the first a link", set.Files)
	}
	write(t, target, "changed\n")
	before := symlinkState(t, f.present)

	outcomes, err := f.store.Restore(set)
	if err != nil {
		t.Fatal(err)
	}
	if outcomes[0].Done != backup.DoneSkipped {
		t.Errorf("outcome %+v, want skipped", outcomes[0])
	}
	if got := symlinkState(t, f.present); got != before {
		t.Errorf("the link is touched: %s, want %s", got, before)
	}
	if got := read(t, outcomes[0].Copy); got != original {
		t.Errorf("the copy for the user holds %q, want %q", got, original)
	}
}

func TestRestoreRefusesADamagedCopy(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	set, err := f.store.Take(backup.ReasonInstall)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.store.Dir, set.Name, "claude-settings.json"), "damaged")
	write(t, f.present, "current\n")

	if _, err := f.store.Restore(set); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Errorf("restore of a damaged copy: %v", err)
	}
	if got := read(t, f.present); got != "current\n" {
		t.Errorf("the damaged copy was written: %q", got)
	}
}

func TestChanged(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	// Nothing recorded yet: every file counts as changed.
	if changed, err := f.store.Changed(); err != nil || !changed {
		t.Fatalf("changed %v, %v before any record", changed, err)
	}
	if err := f.store.Record(); err != nil {
		t.Fatal(err)
	}
	if changed, err := f.store.Changed(); err != nil || changed {
		t.Errorf("changed %v, %v right after the record", changed, err)
	}
	write(t, f.absent, "{}\n")
	if changed, err := f.store.Changed(); err != nil || !changed {
		t.Errorf("changed %v, %v after the absent file appeared", changed, err)
	}
	// A set records what it copied.
	if _, err := f.store.Take(backup.ReasonApply); err != nil {
		t.Fatal(err)
	}
	if changed, err := f.store.Changed(); err != nil || changed {
		t.Errorf("changed %v, %v right after a set", changed, err)
	}
}

func TestSetsOfAnEmptyStore(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if got := sets(t, f.store); len(got) != 0 {
		t.Errorf("sets = %+v", got)
	}
}

// TestRestoreOfADanglingSymlink: the config was a link to a missing file and hottell
// created the file through the link; restore leaves both, and so does undoing it.
func TestRestoreOfADanglingSymlink(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	target := filepath.Join(filepath.Dir(f.absent), "dotfiles", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(f.absent), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, f.absent); err != nil {
		t.Fatal(err)
	}
	set, err := f.store.Take(backup.ReasonInstall)
	if err != nil {
		t.Fatal(err)
	}
	if !set.Files[1].Absent || !set.Files[1].Symlink {
		t.Fatalf("a dangling link not recorded as an absent link: %+v", set.Files[1])
	}
	write(t, target, "{\"hooks\": {}}\n")
	before := symlinkState(t, f.absent)

	undo, err := f.store.TakeBeforeRestore(set)
	if err != nil {
		t.Fatal(err)
	}
	for _, restoring := range []backup.Set{set, undo} {
		outcomes, err := f.store.Restore(restoring)
		if err != nil {
			t.Fatal(err)
		}
		if outcomes[1].Done != backup.DoneSkipped {
			t.Errorf("restore of the %s set: outcome %+v, want skipped", restoring.Reason, outcomes[1])
		}
		if got := symlinkState(t, f.absent); got != before {
			t.Errorf("restore of the %s set touched the link: %s, want %s", restoring.Reason, got, before)
		}
	}
}

// TestRestoreSkipsALinkCreatedLater: a config absent at the backup is a link to someone's
// file now; restore neither removes the link nor the file behind it.
func TestRestoreSkipsALinkCreatedLater(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	set, err := f.store.Take(backup.ReasonInstall)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(filepath.Dir(f.present), "dotfiles", "hooks.json")
	write(t, target, "the user's hooks\n")
	if err := os.MkdirAll(filepath.Dir(f.absent), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, f.absent); err != nil {
		t.Fatal(err)
	}
	before := symlinkState(t, f.absent)

	outcomes, err := f.store.Restore(set)
	if err != nil {
		t.Fatal(err)
	}
	if outcomes[1].Done != backup.DoneSkipped || outcomes[1].Copy != "" {
		t.Errorf("outcome %+v, want skipped without a copy", outcomes[1])
	}
	if got := symlinkState(t, f.absent); got != before {
		t.Errorf("the link is touched: %s, want %s", got, before)
	}
}

// TestTakeBeforeRestore: restore first copies the files as they are, which undoes it,
// without dropping the set it restores.
func TestTakeBeforeRestore(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if _, err := f.store.Take(backup.ReasonInstall); err != nil {
		t.Fatal(err)
	}
	var applied []backup.Set
	for range backup.Keep {
		set, err := f.store.Take(backup.ReasonApply)
		if err != nil {
			t.Fatal(err)
		}
		applied = append(applied, set)
	}
	write(t, f.present, "edited by the user\n")

	// The oldest apply set is the Keep-th newest; one more set would rotate it out.
	restoring := applied[0]
	undo, err := f.store.TakeBeforeRestore(restoring)
	if err != nil {
		t.Fatal(err)
	}
	if undo.Reason != backup.ReasonRestore {
		t.Errorf("reason %s, want %s", undo.Reason, backup.ReasonRestore)
	}
	if got := read(t, filepath.Join(f.store.Dir, undo.Name, "claude-settings.json")); got != "edited by the user\n" {
		t.Errorf("restore set holds %q, want the content before the restore", got)
	}
	if _, err := f.store.Restore(restoring); err != nil {
		t.Fatalf("restore of the set the restore set would rotate out: %v", err)
	}

	// The restore set undoes the restore.
	if _, err := f.store.Restore(undo); err != nil {
		t.Fatal(err)
	}
	if got := read(t, f.present); got != "edited by the user\n" {
		t.Errorf("after restoring the restore set %q, want the user's edit", got)
	}
}

// TestTakeBeforeRestoreTakesTheSetsPaths: the store of the restore names other files (the
// agents' dirs are set differently now); the restore set holds the files the restored set
// names, which are the files the restore replaces.
func TestTakeBeforeRestoreTakesTheSetsPaths(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	set, err := f.store.Take(backup.ReasonInstall)
	if err != nil {
		t.Fatal(err)
	}
	write(t, f.present, "edited by the user\n")
	other := filepath.Join(filepath.Dir(f.present), "elsewhere", "settings.json")
	write(t, other, "another config\n")
	moved := &backup.Store{Dir: f.store.Dir, Files: []backup.File{{Name: "claude-settings.json", Path: other}}, Now: f.store.Now}

	undo, err := moved.TakeBeforeRestore(set)
	if err != nil {
		t.Fatal(err)
	}
	if len(undo.Files) != 2 || undo.Files[0].Path != f.present || undo.Files[1].Path != f.absent || !undo.Files[1].Absent {
		t.Fatalf("restore set files %+v, want the paths of the restored set", undo.Files)
	}
	if got := read(t, filepath.Join(moved.Dir, undo.Name, "claude-settings.json")); got != "edited by the user\n" {
		t.Errorf("restore set holds %q, want the content before the restore", got)
	}
	if _, err := moved.Restore(set); err != nil {
		t.Fatal(err)
	}
	if _, err := moved.Restore(undo); err != nil {
		t.Fatal(err)
	}
	if got := read(t, f.present); got != "edited by the user\n" {
		t.Errorf("after undoing the restore %q, want the user's edit", got)
	}
	if got := read(t, other); got != "another config\n" {
		t.Errorf("the other config %q, want it untouched", got)
	}
}

// TestTakeBeforeRestoreWithTheClockBack: the clock is behind the newest Keep sets, so the
// restore set sorts oldest; the rotation still keeps it, and it undoes the restore.
func TestTakeBeforeRestoreWithTheClockBack(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	var taken []backup.Set
	for range backup.Keep {
		set, err := f.store.Take(backup.ReasonApply)
		if err != nil {
			t.Fatal(err)
		}
		taken = append(taken, set)
	}
	write(t, f.present, "edited by the user\n")
	f.store.Now = func() time.Time { return time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC) }

	undo, err := f.store.TakeBeforeRestore(taken[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.store.Dir, undo.Name, "claude-settings.json")); err != nil {
		t.Fatalf("the restore set just taken is rotated out: %v", err)
	}
	if _, err := f.store.Restore(taken[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Restore(undo); err != nil {
		t.Fatal(err)
	}
	if got := read(t, f.present); got != "edited by the user\n" {
		t.Errorf("after undoing the restore %q, want the user's edit", got)
	}
}

// TestTakeRecordsAbsolutePaths: a config named relative to the working directory is
// recorded by its absolute path, so a restore from another directory puts it back where
// it was and writes nothing in the other directory.
//
//nolint:paralleltest // changes the working directory
func TestTakeRecordsAbsolutePaths(t *testing.T) {
	taken, elsewhere := t.TempDir(), t.TempDir()
	t.Chdir(taken)
	write(t, filepath.Join("claude", "settings.json"), "original\n")
	store := &backup.Store{
		Dir:   filepath.Join(t.TempDir(), "backup"),
		Files: []backup.File{{Name: "claude-settings.json", Path: filepath.Join("claude", "settings.json")}},
	}
	set, err := store.Take(backup.ReasonInstall)
	if err != nil {
		t.Fatal(err)
	}
	if path := set.Files[0].Path; !filepath.IsAbs(path) {
		t.Errorf("manifest path %s, want an absolute one", path)
	}
	write(t, filepath.Join(taken, "claude", "settings.json"), "edited\n")

	t.Chdir(elsewhere)
	if _, err := store.Restore(set); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(taken, "claude", "settings.json")); got != "original\n" {
		t.Errorf("the config after restore %q, want the original", got)
	}
	if entries, err := os.ReadDir(elsewhere); err != nil || len(entries) != 0 {
		t.Errorf("the other directory holds %v, %v; want nothing", entries, err)
	}
}

// TestRestoreRefusesARelativePath: a manifest naming a path that is not absolute (an old
// or a foreign one) has that file left alone, by the restore and by the backup before it.
//
//nolint:paralleltest // changes the working directory
func TestRestoreRefusesARelativePath(t *testing.T) {
	f := newFixture(t)
	set, err := f.store.Take(backup.ReasonInstall)
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	t.Chdir(elsewhere)
	write(t, filepath.Join("claude", "settings.json"), "a file in the working directory\n")
	set.Files[0].Path = filepath.Join("claude", "settings.json")

	undo, err := f.store.TakeBeforeRestore(set)
	if err != nil {
		t.Fatal(err)
	}
	if len(undo.Files) != 1 || undo.Files[0].Path != f.absent {
		t.Errorf("restore set files %+v, want only the absolute path of the set", undo.Files)
	}
	outcomes, err := f.store.Restore(set)
	if err == nil || outcomes[0].Done != backup.DoneFailed {
		t.Errorf("outcomes %+v, %v; want the relative path refused", outcomes, err)
	}
	if got := read(t, filepath.Join(elsewhere, "claude", "settings.json")); got != "a file in the working directory\n" {
		t.Errorf("the file in the working directory %q, want it untouched", got)
	}
}
