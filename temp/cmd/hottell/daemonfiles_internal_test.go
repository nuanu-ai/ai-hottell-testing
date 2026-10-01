package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogRotatesBySize(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "logs", daemonLog)
	log, err := openRotating(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"first\n", "second\n", "third\n"} {
		if _, err := log.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	for file, want := range map[string]string{path: "third\n", path + ".1": "second\n"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Errorf("%s = %q, want %q", filepath.Base(file), data, want)
		}
	}

	// A reopened log counts what it already holds.
	log, err = openRotating(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.Write([]byte("fourth\n")); err != nil {
		t.Fatal(err)
	}
	_ = log.Close()
	if data, _ := os.ReadFile(path + ".1"); !strings.HasPrefix(string(data), "third") {
		t.Errorf("daemon.log.1 = %q, want the third line", data)
	}
}
