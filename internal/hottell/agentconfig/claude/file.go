package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	// newFilePerm and newDirPerm are the modes of a settings file and directory hottell
	// creates; an existing file keeps its mode.
	newFilePerm fs.FileMode = 0o600
	newDirPerm  fs.FileMode = 0o700
	// defaultIndent is what Claude Code writes its settings with.
	defaultIndent = "  "
	// editTries bounds the attempts of an edit whose file another process keeps changing.
	editTries = 3
)

// errChanged: the settings file changed between hottell's read and its write. Claude Code
// rewrites ~/.claude.json all the time, and writing over its change would lose it.
var errChanged = errors.New("changed by another process while it was being edited")

// edit reads the settings file at path, lets change edit its hooks section and, when
// change reports a change, writes the file back atomically. A file that cannot be parsed
// is never written.
func edit(path string, change func(hooks *object) (bool, error)) error {
	return editSection(path, "hooks", change)
}

// editSection is edit for the top-level object section named key. A section that change
// leaves empty is dropped from the file. When another process changes the file while it is
// being edited, the edit is made again from a fresh read, up to editTries times, so that
// its change is kept; change must therefore not depend on an earlier call.
func editSection(path, key string, change func(section *object) (bool, error)) error {
	return editSectionWith(path, key, change, nil)
}

// editSectionWith is editSection with beforeCheck run after each write of the temporary
// file and before the file is compared with what was read: a test changes the file there
// the way Claude Code would.
func editSectionWith(path, key string, change func(section *object) (bool, error), beforeCheck func()) error {
	var err error
	for range editTries {
		if err = editOnce(path, key, change, beforeCheck); !errors.Is(err, errChanged) {
			return err
		}
	}
	return err
}

// editOnce is one attempt of editSectionWith.
func editOnce(path, key string, change func(section *object) (bool, error), beforeCheck func()) error {
	target, err := resolve(path)
	if err != nil {
		return err
	}
	data, mode, err := read(target)
	if err != nil {
		return err
	}

	doc := &object{}
	if len(bytes.TrimSpace(data)) > 0 {
		if !isKind(data, '{') {
			return fmt.Errorf("%s: %w: not a JSON object", target, ErrMalformed)
		}
		if doc, err = parseObject(data); err != nil {
			return fmt.Errorf("%s: %w", target, err)
		}
	}

	section := &object{}
	raw, found := doc.get(key)
	if found {
		if !isKind(raw, '{') {
			return fmt.Errorf("%s: %w: %s is not an object", target, ErrMalformed, key)
		}
		if section, err = parseObject(raw); err != nil {
			return fmt.Errorf("%s: %w", target, err)
		}
	}

	changed, err := change(section)
	if err != nil {
		return fmt.Errorf("%s: %w", target, err)
	}
	if !changed {
		return nil
	}

	if len(section.members) == 0 {
		doc.remove(key)
	} else {
		encoded, err := section.encode()
		if err != nil {
			return err
		}
		doc.set(key, encoded)
	}
	out, err := format(doc, data)
	if err != nil {
		return err
	}
	return writeAtomic(target, out, mode, data, beforeCheck)
}

// resolve follows a symlinked settings file, as dotfile managers make them, so that the
// link stays and its target is edited, including a target not created yet.
func resolve(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return path, nil
	}
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		return path, nil
	}
	target, err := filepath.EvalSymlinks(path)
	if errors.Is(err, fs.ErrNotExist) {
		link, err := os.Readlink(path)
		if err != nil {
			return "", fmt.Errorf("read link %s: %w", path, err)
		}
		if !filepath.IsAbs(link) {
			link = filepath.Join(filepath.Dir(path), link)
		}
		return resolve(link)
	}
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	return target, nil
}

// read returns the file's contents and mode; a missing file reads as empty with the mode
// of a new file.
func read(path string) ([]byte, fs.FileMode, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, newFilePerm, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("read %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, fmt.Errorf("stat %s: %w", path, err)
	}
	return data, info.Mode().Perm(), nil
}

// format indents doc the way the original file was indented, and ends it with a newline
// when the original did or there was none.
func format(doc *object, original []byte) ([]byte, error) {
	compact, err := doc.encode()
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compact, "", indentOf(original)); err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(original)) == 0 || bytes.HasSuffix(original, []byte("\n")) {
		out.WriteByte('\n')
	}
	return out.Bytes(), nil
}

// indentOf returns the whitespace that starts the first indented line of data.
func indentOf(data []byte) string {
	for line := range bytes.Lines(data) {
		trimmed := bytes.TrimLeft(line, " \t")
		if n := len(line) - len(trimmed); n > 0 && len(bytes.TrimSpace(trimmed)) > 0 {
			return string(line[:n])
		}
	}
	return defaultIndent
}

// writeAtomic replaces path with data through a synced temporary file in the same
// directory, so that Claude Code reads either the old settings or the new ones. The file
// gets mode; a missing directory is created, an existing one keeps its mode. Just before
// the replacement path is read again: when it no longer holds original, another process
// has written it since, and the temporary file goes with errChanged instead. beforeCheck,
// when set, runs just before that read.
func writeAtomic(path string, data []byte, mode fs.FileMode, original []byte, beforeCheck func()) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, newDirPerm); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".hottell-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", path, err)
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
	if beforeCheck != nil {
		beforeCheck()
	}
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if !bytes.Equal(current, original) {
		return fmt.Errorf("%s: %w", path, errChanged)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmp.Name(), path, err)
	}
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open %s: %w", dir, err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", dir, err)
	}
	return nil
}
