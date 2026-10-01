package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// lockDaemon takes the daemon's lock at path and returns the file that holds it until it
// is closed or the process ends; errAlreadyRunning when another process holds it.
func lockDaemon(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the lock: %w", err)
	}
	//nolint:gosec // a file descriptor fits an int
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errAlreadyRunning
		}
		return nil, fmt.Errorf("take the lock %s: %w", path, err)
	}
	return f, nil
}

// rotatingFile is a log file that is moved to <path>.1 once a write would take it over
// maxBytes, replacing the previous one, and started anew.
type rotatingFile struct {
	path     string
	maxBytes int64

	mu   sync.Mutex
	f    *os.File
	size int64
}

func openRotating(path string, maxBytes int64) (*rotatingFile, error) {
	r := &rotatingFile{path: path, maxBytes: maxBytes}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create the log directory: %w", err)
	}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *rotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open the log: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("stat the log: %w", err)
	}
	r.f, r.size = f, info.Size()
	return nil
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f != nil && r.size > 0 && r.size+int64(len(p)) > r.maxBytes {
		// A log that cannot be moved goes on growing rather than losing lines.
		_ = r.rotate()
	}
	if r.f == nil {
		if err := r.open(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotatingFile) rotate() error {
	if err := os.Rename(r.path, r.path+".1"); err != nil {
		return fmt.Errorf("rotate the log: %w", err)
	}
	_ = r.f.Close()
	r.f = nil
	return nil
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	return r.f.Close()
}
