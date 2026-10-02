package state

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomic replaces path with data so that a reader sees either the old
// contents or the new ones, never a mix: the data goes to a temporary file in the same
// directory, is synced, and is renamed over path. The directory is created with mode
// 0700 when missing, and the file gets mode 0600. An error from syncing the directory
// after the rename means path may already hold the new data.
func WriteFileAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	if err := ensureDir(dir); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", path, err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()

	if err := tmp.Chmod(filePerm); err != nil {
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
	return syncDir(dir)
}

// syncDir makes the rename durable.
func syncDir(dir string) error {
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
