// Package backup keeps copies of the agents' config files that hottell writes, taken
// before it changes them, and puts them back for hottell restore. A set is one directory
// under the state named by its UTC time, so that the names sort by age; it holds the
// copies and a manifest with the sha256 of each and the files that did not exist. The
// store also records the fingerprints of the files as hottell last left them, which tells
// a change of someone else from hottell's own writes.
package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

// Reasons a set is taken for.
const (
	ReasonInstall   = "install"
	ReasonUninstall = "uninstall"
	ReasonApply     = "apply"
	// ReasonRestore: the files as they were before a restore, to undo it.
	ReasonRestore = "restore"
)

// Keep is how many of the newest complete sets are kept; the newest install set is kept
// besides.
const Keep = 10

// What Restore did to a file.
const (
	DoneRestored = "restored"
	DoneRemoved  = "removed"
	// DoneAbsent: the file was absent at the backup and is absent now.
	DoneAbsent = "absent"
	// DoneSkipped: the path is a symbolic link, or was one at the backup; restore leaves
	// it to the user.
	DoneSkipped = "skipped"
	DoneFailed  = "failed"
)

const (
	manifestName = "manifest.json"
	recordName   = "written.json"
	// setLayout names a set; fixed width, so that the names sort by time.
	setLayout = "20060102T150405.000000000Z"
	// absent is the fingerprint of a file that does not exist.
	absent = "absent"
	// nameAttempts bounds the search for a free set name within one clock reading.
	nameAttempts = 100
)

const (
	dirPerm  os.FileMode = 0o700
	filePerm os.FileMode = 0o600
)

// File is a config file the store backs up.
type File struct {
	// Name is the file name of the copy in a set; unique within the store.
	Name string
	// Path is the config file; a symlink is followed to copy it.
	Path string
}

// Store keeps the sets in Dir.
type Store struct {
	Dir   string
	Files []File
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

// Manifest describes a set.
type Manifest struct {
	Reason string  `json:"reason"`
	Files  []Entry `json:"files"`
}

// Entry is one file of a set.
type Entry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Absent: the file did not exist, and restore removes it.
	Absent bool `json:"absent,omitempty"`
	// Symlink: the path was a symbolic link, and restore skips it.
	Symlink bool        `json:"symlink,omitempty"`
	SHA256  string      `json:"sha256,omitempty"`
	Mode    os.FileMode `json:"mode,omitempty"`
}

// Set is a set of copies in the store.
type Set struct {
	// Name is the set's directory, its UTC time.
	Name string
	Manifest
}

// Outcome is what Restore did to one file.
type Outcome struct {
	Path string
	// Done is DoneRestored, DoneRemoved, DoneAbsent, DoneSkipped or DoneFailed.
	Done string
	// Copy is the file's copy in the set, empty when the file was absent at the backup.
	Copy string
}

// Take copies the files into a new set, records their fingerprints and drops the sets
// beyond Keep.
func (s *Store) Take(reason string) (Set, error) {
	set, err := s.take(reason, s.Files, "")
	if err != nil {
		return Set{}, err
	}
	return set, s.Record()
}

// TakeBeforeRestore copies the files of restoring, the set about to be restored, as they
// are now into a new restore set, so that restoring that set undoes the restore. It takes
// the paths of restoring's manifest, which are what Restore replaces, but those that are
// not absolute, which Restore refuses; the rotation keeps restoring.
func (s *Store) TakeBeforeRestore(restoring Set) (Set, error) {
	files := make([]File, 0, len(restoring.Files))
	for _, entry := range restoring.Files {
		if filepath.IsAbs(entry.Path) {
			files = append(files, File{Name: entry.Name, Path: entry.Path})
		}
	}
	return s.take(ReasonRestore, files, restoring.Name)
}

// take copies files into a new set, recorded by their absolute paths, and drops the sets
// beyond Keep, except the new one and keep.
func (s *Store) take(reason string, files []File, keep string) (Set, error) {
	set := Set{Manifest: Manifest{Reason: reason}}
	copies := make(map[string][]byte, len(files))
	for _, f := range files {
		path, err := filepath.Abs(f.Path)
		if err != nil {
			return Set{}, fmt.Errorf("resolve %s: %w", f.Path, err)
		}
		data, mode, err := readFile(path)
		if err != nil {
			return Set{}, err
		}
		link, err := isSymlink(path)
		if err != nil {
			return Set{}, err
		}
		entry := Entry{Name: f.Name, Path: path, Absent: data == nil, Symlink: link}
		if data != nil {
			entry.SHA256, entry.Mode = digest(data), mode
			copies[f.Name] = data
		}
		set.Files = append(set.Files, entry)
	}

	name, err := s.newSetDir()
	if err != nil {
		return Set{}, err
	}
	set.Name = name
	dir := filepath.Join(s.Dir, name)
	for file, data := range copies {
		if err := state.WriteFileAtomic(filepath.Join(dir, file), data); err != nil {
			return Set{}, err
		}
	}
	manifest, err := json.MarshalIndent(set.Manifest, "", "  ")
	if err != nil {
		return Set{}, fmt.Errorf("encode the manifest: %w", err)
	}
	// The manifest goes last: a set without one is incomplete and not listed.
	if err := state.WriteFileAtomic(filepath.Join(dir, manifestName), manifest); err != nil {
		return Set{}, err
	}
	if err := s.rotate(name, keep); err != nil {
		return Set{}, err
	}
	return set, nil
}

// newSetDir creates the directory of a new set and returns its name.
func (s *Store) newSetDir() (string, error) {
	if err := os.MkdirAll(s.Dir, dirPerm); err != nil {
		return "", fmt.Errorf("create %s: %w", s.Dir, err)
	}
	if err := os.Chmod(s.Dir, dirPerm); err != nil {
		return "", fmt.Errorf("chmod %s: %w", s.Dir, err)
	}
	at := s.now().UTC()
	for range nameAttempts {
		name := at.Format(setLayout)
		err := os.Mkdir(filepath.Join(s.Dir, name), dirPerm)
		if err == nil {
			return name, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("create a backup set: %w", err)
		}
		at = at.Add(time.Nanosecond)
	}
	return "", fmt.Errorf("create a backup set in %s: every name is taken", s.Dir)
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// rotate removes the complete sets beyond the newest Keep, except the newest install set
// and keep; a set taken with the clock behind sorts beyond them, and keep holds it. It
// removes the incomplete sets older than the newest complete one too, which count for
// nothing; a newer one may be a take in progress and is left.
func (s *Store) rotate(keep ...string) error {
	names, err := s.setNames()
	if err != nil {
		return err
	}
	complete, keptInstall := 0, false
	for _, name := range names {
		set, ok := s.read(name)
		if !ok && complete == 0 {
			continue
		}
		if ok {
			complete++
		}
		isInstall := ok && set.Reason == ReasonInstall && !keptInstall
		keptInstall = keptInstall || isInstall
		if (ok && complete <= Keep) || isInstall || slices.Contains(keep, name) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.Dir, name)); err != nil {
			return fmt.Errorf("remove an old backup set: %w", err)
		}
	}
	return nil
}

// setNames returns the names of the set directories, newest first.
func (s *Store) setNames() ([]string, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.Dir, err)
	}
	var names []string
	for _, e := range entries {
		if _, err := time.Parse(setLayout, e.Name()); e.IsDir() && err == nil {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	slices.Reverse(names)
	return names, nil
}

// Sets returns the complete sets, newest first.
func (s *Store) Sets() ([]Set, error) {
	names, err := s.setNames()
	if err != nil {
		return nil, err
	}
	var sets []Set
	for _, name := range names {
		if set, ok := s.read(name); ok {
			sets = append(sets, set)
		}
	}
	return sets, nil
}

// read returns the set name, and false when its manifest is missing or unreadable.
func (s *Store) read(name string) (Set, bool) {
	data, err := os.ReadFile(filepath.Join(s.Dir, name, manifestName))
	if err != nil {
		return Set{}, false
	}
	set := Set{Name: name}
	if json.Unmarshal(data, &set.Manifest) != nil {
		return Set{}, false
	}
	return set, true
}

// Check finds, without writing anything, the problems of set that Restore or the restore
// set before it would hit: a path that is not absolute, an entry without a plain copy
// name, and, for a file that existed and was not a symbolic link at the backup, a missing
// sha256 or a copy that cannot be read or does not match its sha256. The problems of every
// entry are joined. It reads only the set, not the configs as they are now: what is at a
// path now (a link, a directory) and the errors of writing are still Restore's to meet.
func (s *Store) Check(set Set) error {
	var errs []error
	for _, entry := range set.Files {
		if !filepath.IsAbs(entry.Path) {
			errs = append(errs, notAbsolute(entry.Path))
			continue
		}
		// TakeBeforeRestore copies every file under its name, whether absent at the backup
		// or not.
		if entry.Name == "" || entry.Name == "." || entry.Name == ".." || filepath.Base(entry.Name) != entry.Name {
			errs = append(errs, fmt.Errorf("the copy name %q of %s in the manifest is not a file name in the set", entry.Name, entry.Path))
			continue
		}
		if entry.Absent || entry.Symlink {
			continue
		}
		if entry.SHA256 == "" {
			errs = append(errs, fmt.Errorf("%s has no sha256 in the manifest", entry.Path))
			continue
		}
		if _, err := readCopy(filepath.Join(s.Dir, set.Name, entry.Name), entry); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Restore puts every file of set back byte for byte and removes the files that were
// absent. It never writes, removes or replaces a symbolic link: a path that is one now, or
// was one at the backup, is skipped and left to the user. A path that is not absolute (an
// old or a foreign manifest) fails, since it would resolve against the working directory.
// Every file is attempted; the errors are joined. Restore is not atomic: a file that fails,
// on a write error or on what is at its path now, leaves the files restored before it as
// they are. Check, run first, refuses a damaged or incomplete set before anything is
// written; the restore set TakeBeforeRestore takes undoes a restore that failed midway.
func (s *Store) Restore(set Set) ([]Outcome, error) {
	var outcomes []Outcome
	var errs []error
	for _, entry := range set.Files {
		out := Outcome{Path: entry.Path}
		if !entry.Absent {
			out.Copy = filepath.Join(s.Dir, set.Name, entry.Name)
		}
		done, err := s.restore(out.Copy, entry)
		out.Done = done
		if err != nil {
			out.Done = DoneFailed
			errs = append(errs, err)
		}
		outcomes = append(outcomes, out)
	}
	return outcomes, errors.Join(errs...)
}

func (s *Store) restore(copyPath string, entry Entry) (string, error) {
	if !filepath.IsAbs(entry.Path) {
		return "", notAbsolute(entry.Path)
	}
	info, err := os.Lstat(entry.Path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("stat %s: %w", entry.Path, err)
	}
	exists := err == nil
	if entry.Symlink || (exists && info.Mode()&os.ModeSymlink != 0) {
		return DoneSkipped, nil
	}
	if entry.Absent {
		switch {
		case !exists:
			return DoneAbsent, nil
		case !info.Mode().IsRegular():
			return "", fmt.Errorf("%s, absent at the backup, is not a regular file now; it is left", entry.Path)
		}
		if err := os.Remove(entry.Path); err != nil {
			return "", fmt.Errorf("remove %s: %w", entry.Path, err)
		}
		return DoneRemoved, nil
	}
	data, err := readCopy(copyPath, entry)
	if err != nil {
		return "", err
	}
	if err := writeFile(entry.Path, data, entry.Mode); err != nil {
		return "", err
	}
	return DoneRestored, nil
}

// notAbsolute is the error of a manifest path that is not absolute.
func notAbsolute(path string) error {
	return fmt.Errorf("%s in the manifest is not an absolute path; it is left", path)
}

// readCopy returns the copy of entry at copyPath, checked against its sha256.
func readCopy(copyPath string, entry Entry) ([]byte, error) {
	data, err := os.ReadFile(copyPath) //nolint:gosec // a copy in the store
	if err != nil {
		return nil, fmt.Errorf("read the copy of %s: %w", entry.Path, err)
	}
	if digest(data) != entry.SHA256 {
		return nil, fmt.Errorf("the copy of %s does not match its sha256 in the manifest", entry.Path)
	}
	return data, nil
}

// Changed reports whether any file differs from what hottell last recorded; with nothing
// recorded every file counts as changed.
func (s *Store) Changed() (bool, error) {
	recorded, err := s.recorded()
	if err != nil {
		return false, err
	}
	for _, f := range s.Files {
		current, err := fingerprint(f.Path)
		if err != nil {
			return false, err
		}
		if last, ok := recorded[f.Path]; !ok || last != current {
			return true, nil
		}
	}
	return false, nil
}

// Record saves the fingerprints of the files as they are now, as hottell's last write. A
// record that already holds them is not written again.
func (s *Store) Record() error {
	current := make(map[string]string, len(s.Files))
	for _, f := range s.Files {
		fp, err := fingerprint(f.Path)
		if err != nil {
			return err
		}
		current[f.Path] = fp
	}
	data, err := json.Marshal(current)
	if err != nil {
		return fmt.Errorf("encode the fingerprints: %w", err)
	}
	path := filepath.Join(s.Dir, recordName)
	if old, err := os.ReadFile(path); err == nil && string(old) == string(data) {
		return nil
	}
	return state.WriteFileAtomic(path, data)
}

// recorded returns the fingerprints of the last Record, empty when there is none.
func (s *Store) recorded() (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(s.Dir, recordName))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the fingerprints: %w", err)
	}
	var recorded map[string]string
	if err := json.Unmarshal(data, &recorded); err != nil {
		return nil, fmt.Errorf("decode %s: %w", filepath.Join(s.Dir, recordName), err)
	}
	return recorded, nil
}

// fingerprint is the sha256 of the file, or absent.
func fingerprint(path string) (string, error) {
	data, _, err := readFile(path)
	if err != nil || data == nil {
		return absent, err
	}
	return digest(data), nil
}

// readFile returns the file's contents and mode through a symlink, or nil when it does
// not exist.
func readFile(path string) ([]byte, os.FileMode, error) {
	data, err := os.ReadFile(path) //nolint:gosec // an agent config the store is given
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("read %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, fmt.Errorf("stat %s: %w", path, err)
	}
	if data == nil {
		data = []byte{}
	}
	return data, info.Mode().Perm(), nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// isSymlink reports whether path is a symbolic link.
func isSymlink(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	return info.Mode()&os.ModeSymlink != 0, nil
}

// writeFile replaces path with data atomically, with mode. A missing directory is created;
// an existing one keeps its mode, since it is the agent's.
func writeFile(path string, data []byte, mode os.FileMode) (err error) {
	if mode == 0 {
		mode = filePerm
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".restore-*")
	if err != nil {
		return fmt.Errorf("create a temporary file for %s: %w", path, err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("chmod %s: %w", tmp.Name(), err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", tmp.Name(), err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmp.Name(), path, err)
	}
	return nil
}
