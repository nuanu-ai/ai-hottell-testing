// Package skillinstall lays out the skills embedded in the binary for Codex and Claude Code
// (<agent home>/skills/<name>/) and takes them back. A skill directory hottell wrote carries
// a marker with the binary's version and the sha256 of every file it wrote; only such a
// directory, unchanged since, is ever replaced or removed. A directory without the marker is
// someone else's, and one whose files differ from the marker was edited by hand: both stay
// as they are. Every outcome is returned, never raised, so a failure here cannot stop the
// installation or touch the agents.
package skillinstall

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

// MarkerName is the file in a skill directory that says hottell wrote it.
const MarkerName = ".hottell-managed"

// The outcomes of one skill in one agent.
const (
	Installed = "installed"
	Updated   = "updated"
	Unchanged = "unchanged"
	Removed   = "removed"
	// Absent: uninstall found nothing to remove.
	Absent = "absent"
	// Skipped: the agent is not installed on this machine.
	Skipped = "skipped"
	// Modified: hottell's directory whose files someone changed; it is left alone.
	Modified = "изменён вручную"
	// Foreign: a directory, or a link, hottell did not write; it is left alone.
	Foreign = "чужой"
	Failed  = "failed"
)

const (
	dirPerm  = 0o755
	filePerm = 0o644
)

// Target is an agent whose home directory (~/.codex, ~/.claude) gets a skills/ folder.
type Target struct {
	Agent string
	Home  string
}

// Outcome is what happened to one skill in one agent; Err is set when Status is Failed.
type Outcome struct {
	Agent  string
	Skill  string
	Status string
	Err    error
}

type marker struct {
	Version     string            `json:"version"`
	FilesSHA256 map[string]string `json:"files_sha256"`
}

// skill is one top-level directory of the catalogue: its files by slash path, relative to it.
type skill struct {
	name  string
	files map[string][]byte
}

// Install lays out every skill of src for every target that is installed.
func Install(src fs.FS, version string, targets []Target) []Outcome {
	return each(src, targets, func(t Target, s skill) (string, error) { return install(t, s, version) })
}

// Uninstall removes the skills of src that hottell wrote and nobody changed since, and the
// skills folder of an agent when that leaves it empty.
func Uninstall(src fs.FS, targets []Target) []Outcome {
	out := each(src, targets, uninstall)
	for _, t := range targets {
		_ = os.Remove(filepath.Join(t.Home, "skills")) // fails, as it should, unless empty
	}
	return out
}

func each(src fs.FS, targets []Target, do func(Target, skill) (string, error)) []Outcome {
	skills, err := catalogue(src)
	var out []Outcome
	for _, t := range targets {
		if err != nil {
			out = append(out, Outcome{Agent: t.Agent, Skill: "*", Status: Failed, Err: err})
			continue
		}
		installed, err := agentInstalled(t.Home)
		for _, s := range skills {
			o := Outcome{Agent: t.Agent, Skill: s.name}
			switch {
			case err != nil:
				o.Status, o.Err = Failed, err
			case !installed:
				o.Status = Skipped
			default:
				o.Status, o.Err = do(t, s)
				if o.Err != nil {
					o.Status = Failed
				}
			}
			out = append(out, o)
		}
	}
	return out
}

func agentInstalled(home string) (bool, error) {
	info, err := os.Stat(home)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("%s is not a directory", home)
	}
	return true, nil
}

func catalogue(src fs.FS) ([]skill, error) {
	entries, err := fs.ReadDir(src, ".")
	if err != nil {
		return nil, fmt.Errorf("read the embedded skills: %w", err)
	}
	var skills []skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		s := skill{name: e.Name(), files: make(map[string][]byte)}
		err := fs.WalkDir(src, e.Name(), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := fs.ReadFile(src, p)
			if err != nil {
				return err
			}
			rel := p[len(e.Name())+1:]
			if rel == MarkerName {
				return fmt.Errorf("the embedded skill %s holds %s", e.Name(), MarkerName)
			}
			s.files[rel] = data
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("read the embedded skill %s: %w", e.Name(), err)
		}
		skills = append(skills, s)
	}
	return skills, nil
}

func sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func (s skill) marker(version string) marker {
	m := marker{Version: version, FilesSHA256: make(map[string]string, len(s.files))}
	for rel, data := range s.files {
		m.FilesSHA256[rel] = sum(data)
	}
	return m
}

// own reads the state of a skill directory: absent, foreign, or hottell's with its marker
// and whether its files still match it.
func own(dir string) (status string, m marker, err error) {
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return Absent, m, nil
	}
	if err != nil {
		return "", m, err
	}
	if !info.IsDir() {
		return Foreign, m, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, MarkerName))
	if errors.Is(err, fs.ErrNotExist) {
		return Foreign, m, nil
	}
	if err != nil {
		return "", m, err
	}
	// A marker that does not parse is not hottell's to trust: the directory counts as foreign.
	if uerr := json.Unmarshal(data, &m); uerr != nil || m.FilesSHA256 == nil {
		return Foreign, marker{}, nil //nolint:nilerr // an unreadable marker is an outcome, not a failure
	}
	found, err := onDisk(dir)
	if err != nil {
		return "", m, err
	}
	if !maps.Equal(found, m.FilesSHA256) {
		return Modified, m, nil
	}
	return Unchanged, m, nil
}

// onDisk is the sha256 of every file in dir but the marker; anything that is not a regular
// file or a directory makes the hash "" so that it never matches a marker.
func onDisk(dir string) (map[string]string, error) {
	found := make(map[string]string)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case d.IsDir():
			return nil
		case !d.Type().IsRegular():
			found[rel] = ""
			return nil
		case rel == MarkerName:
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		found[rel] = sum(data)
		return nil
	})
	return found, err
}

func install(t Target, s skill, version string) (string, error) {
	root := filepath.Join(t.Home, "skills")
	dir := filepath.Join(root, s.name)
	status, m, err := own(dir)
	if err != nil {
		return "", err
	}
	next := s.marker(version)
	switch status {
	case Foreign, Modified:
		return status, nil
	case Unchanged:
		if m.Version == version && maps.Equal(m.FilesSHA256, next.FilesSHA256) {
			return Unchanged, nil
		}
		status = Updated
	case Absent:
		status = Installed
	}
	if err := os.MkdirAll(root, dirPerm); err != nil {
		return "", err
	}
	if err := place(root, dir, s, next); err != nil {
		return "", err
	}
	return status, nil
}

// place writes the skill into a staging directory next to dir and swaps it in, so an agent
// reading the skill sees the old version or the new one, never a mix.
func place(root, dir string, s skill, m marker) (err error) {
	stage, err := os.MkdirTemp(root, "."+s.name+".hottell-new-")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := os.Chmod(stage, dirPerm); err != nil {
		return err
	}
	for _, rel := range slices.Sorted(maps.Keys(s.files)) {
		p := filepath.Join(stage, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), dirPerm); err != nil {
			return err
		}
		if err := os.WriteFile(p, s.files[rel], filePerm); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, MarkerName), append(data, '\n'), filePerm); err != nil {
		return err
	}

	var old string
	if _, err := os.Lstat(dir); err == nil {
		old = stage + "-old"
		if err := os.Rename(dir, old); err != nil {
			return err
		}
	}
	if err := os.Rename(stage, dir); err != nil {
		if old != "" {
			_ = os.Rename(old, dir)
		}
		return err
	}
	if old != "" {
		_ = os.RemoveAll(old)
	}
	return nil
}

func uninstall(t Target, s skill) (string, error) {
	dir := filepath.Join(t.Home, "skills", s.name)
	status, _, err := own(dir)
	if err != nil {
		return "", err
	}
	if status != Unchanged {
		return status, nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	return Removed, nil
}
