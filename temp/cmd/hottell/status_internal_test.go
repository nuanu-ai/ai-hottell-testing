package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestStatusReportsForeignHooks: hooks of others that run the installed binary are a
// problem, each listed with its file and event.
func TestStatusReportsForeignHooks(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	mustInstall(t, f)
	// The daemon would put the hottell hooks back into the configs written below.
	f.launchd.freeze()
	f.withForeignHooks(t)

	code, rep, text := f.status(t, false)
	if code != exitFailure || rep.OK {
		t.Fatalf("status exit code %d, ok %v, want a problem:\n%s", code, rep.OK, text)
	}
	b := f.cfg.daemon.binary
	codex := filepath.Join(f.home, ".codex", "hooks.json")
	for _, want := range []string{
		"hooks: " + filepath.Join(f.home, ".claude", "settings.json") + " SessionStart runs the hottell binary as a foreign hook: " + b + " -agent claude",
		"hooks: " + codex + " Stop runs the hottell binary as a foreign hook: " + b + " -agent codex",
		"hooks: " + codex + " PreToolUse runs the hottell binary as a foreign hook: " + b + " -agent codex -config x",
	} {
		if !slices.Contains(rep.Problems, want) {
			t.Errorf("problems lack %q:\n%s", want, strings.Join(rep.Problems, "\n"))
		}
	}
	if n := len(slices.DeleteFunc(slices.Clone(rep.Problems), func(p string) bool { return !strings.Contains(p, "foreign hook") })); n != 5 {
		t.Errorf("%d foreign hooks reported, want 5:\n%s", n, text)
	}
}

// TestStatusReportsUnreadableConfig: a config the scan for foreign hooks cannot read is a
// problem, not a config without them.
func TestStatusReportsUnreadableConfig(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	mustInstall(t, f)
	f.launchd.freeze()
	settings := filepath.Join(f.home, ".claude", "settings.json")
	data := strings.ReplaceAll(duplicateKeys, binaryMark, f.cfg.daemon.binary)
	if err := os.WriteFile(settings, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	code, rep, text := f.status(t, false)
	if code != exitFailure || rep.OK {
		t.Fatalf("status exit code %d, ok %v, want a problem:\n%s", code, rep.OK, text)
	}
	if !slices.ContainsFunc(rep.Problems, func(p string) bool {
		return strings.HasPrefix(p, "hooks: ") && strings.Contains(p, settings) && strings.Contains(p, "duplicate key")
	}) {
		t.Errorf("problems lack the unreadable %s:\n%s", settings, strings.Join(rep.Problems, "\n"))
	}
}
