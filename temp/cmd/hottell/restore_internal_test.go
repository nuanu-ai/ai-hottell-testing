package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/backup"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/launchd"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/mcpconfig"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

func (f *installFixture) restore(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = restore(t.Context(), f.cfg, args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func (f *installFixture) sets(t *testing.T) []backup.Set {
	t.Helper()
	sets, err := agentBackups(f.cfg.daemon).Sets()
	if err != nil {
		t.Fatal(err)
	}
	return sets
}

// TestInstallBacksUpTheAgentsConfigs: install copies the agents' configs as they were
// before it wrote them, and ends with the command that puts them back.
func TestInstallBacksUpTheAgentsConfigs(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	f.withAgentFixtures(t)
	if err := os.Remove(filepath.Join(f.home, ".codex", "hooks.json")); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := f.install(t)
	if code != exitOK {
		t.Fatalf("install exit code %d\n%s\n%s", code, stdout, stderr)
	}

	sets := f.sets(t)
	if len(sets) != 1 || sets[0].Reason != backup.ReasonInstall {
		t.Fatalf("sets %+v, want one install set", sets)
	}
	set := sets[0]
	dir := filepath.Join(f.cfg.daemon.paths.BackupDir(), set.Name)
	for _, file := range set.Files {
		switch filepath.Base(file.Path) {
		case "hooks.json":
			if !file.Absent {
				t.Errorf("%s not recorded as absent", file.Path)
			}
		default:
			rel, err := filepath.Rel(f.home, file.Path)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(filepath.Join(dir, file.Name)); err != nil || string(got) != f.fixture(t, rel) {
				t.Errorf("copy of %s = %q, %v; want the config before install", rel, got, err)
			}
		}
	}
	if len(set.Files) != 3 {
		t.Errorf("set files %+v, want settings.json, config.toml and hooks.json", set.Files)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if want := "rollback: " + f.cfg.daemon.binary + " restore " + set.Name; lines[len(lines)-1] != want {
		t.Errorf("install output ends with %q, want %q", lines[len(lines)-1], want)
	}
}

func TestUninstallKeepsTheBackups(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	f.withAgentFixtures(t)
	mustInstall(t, f)
	if code, stdout, stderr := f.uninstall(t); code != exitOK {
		t.Fatalf("uninstall exit code %d\n%s\n%s", code, stdout, stderr)
	}

	sets := f.sets(t)
	if len(sets) != 2 || sets[0].Reason != backup.ReasonUninstall || sets[1].Reason != backup.ReasonInstall {
		t.Fatalf("sets %+v, want the uninstall and the install sets", sets)
	}
	entries, err := os.ReadDir(f.cfg.daemon.paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(f.cfg.daemon.paths.BackupDir()) {
		t.Errorf("the state holds %v after uninstall, want only the backups", entries)
	}

	// The install set still puts back the configs install found.
	if code, stdout, stderr := f.restore(t, sets[1].Name); code != exitOK {
		t.Fatalf("restore exit code %d\n%s\n%s", code, stdout, stderr)
	}
	for _, rel := range agentFixtures {
		if got := f.read(t, rel); got != f.fixture(t, rel) {
			t.Errorf("%s after restore:\n%s\nwant the fixture", rel, got)
		}
	}
}

func TestRestore(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	f.withAgentFixtures(t)
	if err := os.Remove(filepath.Join(f.home, ".codex", "hooks.json")); err != nil {
		t.Fatal(err)
	}
	mustInstall(t, f)
	// The user edits a config after install; the daemon, which would take the edit into a
	// set of its own, is stopped first.
	f.launchd.freeze()
	settings := filepath.Join(f.home, ".claude", "settings.json")
	edited := f.read(t, ".claude/settings.json") + " "
	if err := os.WriteFile(settings, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := f.restore(t)
	if code != exitOK {
		t.Fatalf("restore exit code %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, rel := range []string{".claude/settings.json", ".codex/config.toml"} {
		if got := f.read(t, rel); got != f.fixture(t, rel) {
			t.Errorf("%s after restore:\n%s\nwant the fixture byte for byte", rel, got)
		}
	}
	if exists(t, filepath.Join(f.home, ".codex", "hooks.json")) {
		t.Errorf("hooks.json, absent before install, is left")
	}
	agent := launchd.Agent{Home: f.home}
	if exists(t, agent.PlistPath()) {
		t.Errorf("the launchd agent is left")
	}
	if got := f.launchd.runs(); !slices.Equal(got, []string{"bootout", "bootstrap", "bootout"}) {
		t.Errorf("launchctl calls %q, want the daemon booted out", got)
	}
	if !exists(t, f.cfg.daemon.binary) {
		t.Errorf("restore removed the installed binary")
	}
	for _, want := range []string{
		"launchd     ok", "restored  " + settings, "removed   " + filepath.Join(f.home, ".codex", "hooks.json"),
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}

	// Restore first copied the configs as they were, the user's edit included.
	sets := f.sets(t)
	if len(sets) != 2 || sets[0].Reason != backup.ReasonRestore || sets[1].Reason != backup.ReasonInstall {
		t.Fatalf("sets %+v, want the restore and the install sets", sets)
	}
	undo := sets[0]
	if want := "backup      ok            " + undo.Name + " (the state before this restore; undo with: " + f.cfg.daemon.binary + " restore " + undo.Name + ")"; !strings.Contains(stdout, want) {
		t.Errorf("stdout lacks %q:\n%s", want, stdout)
	}
	if def, err := pickSet(sets, ""); err != nil || def.Name != sets[1].Name {
		t.Errorf("default set %s, %v; want the install set %s, never a restore set", def.Name, err, sets[1].Name)
	}
	// Restoring that set undoes the restore.
	if code, stdout, stderr := f.restore(t, undo.Name); code != exitOK {
		t.Fatalf("restore of the restore set: exit code %d\n%s\n%s", code, stdout, stderr)
	}
	if got, want := f.read(t, ".claude/settings.json"), edited; got != want {
		t.Errorf("settings.json after undoing the restore:\n%s\nwant the content before the restore:\n%s", got, want)
	}
}

// TestRestoreSkipsALink: hooks.json, absent at install, is a link to the user's file
// now; restore leaves the link and the file, puts back the rest and exits 1.
func TestRestoreSkipsALink(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	f.withAgentFixtures(t)
	hooks := filepath.Join(f.home, ".codex", "hooks.json")
	if err := os.Remove(hooks); err != nil {
		t.Fatal(err)
	}
	mustInstall(t, f)
	f.launchd.freeze()
	target := filepath.Join(f.home, "dotfiles", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("the user's hooks\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(hooks); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, hooks); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := f.restore(t)
	if code != exitFailure {
		t.Fatalf("exit code %d, want %d\nstdout:\n%s\nstderr:\n%s", code, exitFailure, stdout, stderr)
	}
	if want := "skipped   " + hooks + ": a symbolic link; it was absent before"; !strings.Contains(stdout, want) {
		t.Errorf("stdout lacks %q:\n%s", want, stdout)
	}
	if got, err := os.Readlink(hooks); err != nil || got != target {
		t.Errorf("the link is touched: %q, %v", got, err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "the user's hooks\n" {
		t.Errorf("the user's file is touched: %q, %v", got, err)
	}
	for _, rel := range []string{".claude/settings.json", ".codex/config.toml"} {
		if got := f.read(t, rel); got != f.fixture(t, rel) {
			t.Errorf("%s after restore:\n%s\nwant the fixture byte for byte", rel, got)
		}
	}
}

// TestRestoreWithTheAgentsElsewhere: restore runs with the Claude dir set differently
// than at install; it backs up and puts back the files of the set, not those the
// environment names now.
func TestRestoreWithTheAgentsElsewhere(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	f.withAgentFixtures(t)
	mustInstall(t, f)
	f.launchd.freeze()
	settings := filepath.Join(f.home, ".claude", "settings.json")
	installed := f.read(t, ".claude/settings.json")
	elsewhere := filepath.Join(f.home, "claude-elsewhere")
	if err := os.MkdirAll(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, "settings.json"), []byte("another config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.cfg.daemon.claudeDir = elsewhere

	if code, stdout, stderr := f.restore(t); code != exitOK {
		t.Fatalf("restore exit code %d\n%s\n%s", code, stdout, stderr)
	}
	if got := f.read(t, ".claude/settings.json"); got != f.fixture(t, ".claude/settings.json") {
		t.Errorf("settings.json after restore:\n%s\nwant the fixture", got)
	}
	undo := f.sets(t)[0]
	if undo.Reason != backup.ReasonRestore || undo.Files[0].Path != settings {
		t.Fatalf("restore set %+v, want the paths of the install set", undo)
	}
	if code, stdout, stderr := f.restore(t, undo.Name); code != exitOK {
		t.Fatalf("restore of the restore set: exit code %d\n%s\n%s", code, stdout, stderr)
	}
	if got := f.read(t, ".claude/settings.json"); got != installed {
		t.Errorf("settings.json after undoing the restore:\n%s\nwant the content before the restore:\n%s", got, installed)
	}
	if got := f.read(t, "claude-elsewhere/settings.json"); got != "another config\n" {
		t.Errorf("the other config %q, want it untouched", got)
	}
}

// TestRestoreWithCodexHomeGone: restore takes the paths from the set, not from the agents'
// dirs, so a CODEX_HOME that no longer exists keeps neither --list nor the restore from
// working.
func TestRestoreWithCodexHomeGone(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	f.withAgentFixtures(t)
	mustInstall(t, f)
	f.launchd.freeze()
	env := lookup(map[string]string{
		"HOME":                       f.home,
		state.HomeEnv:                f.cfg.daemon.paths.Root,
		mcpconfig.CodexHomeEnv:       filepath.Join(f.home, "codex-gone"),
		mcpconfig.ClaudeConfigDirEnv: filepath.Join(f.home, "claude-gone"),
	})

	var out, errOut bytes.Buffer
	if code := runRestore([]string{"--list"}, &out, &errOut, env); code != exitOK || !strings.Contains(out.String(), "install") {
		t.Fatalf("--list: exit code %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	cfg, err := restoreConfigFrom(env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.daemon.binary != f.cfg.daemon.binary {
		t.Errorf("binary %s, want the installed one %s", cfg.daemon.binary, f.cfg.daemon.binary)
	}
	cfg.launchd = f.launchd
	out.Reset()
	errOut.Reset()
	if code := restore(t.Context(), cfg, nil, &out, &errOut); code != exitOK {
		t.Fatalf("restore exit code %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	for _, rel := range agentFixtures {
		if got := f.read(t, rel); got != f.fixture(t, rel) {
			t.Errorf("%s after restore:\n%s\nwant the fixture", rel, got)
		}
	}
}

// TestRestoreWhenTheDaemonDoesNotStop: with the daemon still loaded the restore would be
// applied over within a round, so restore touches nothing and tells how to stop it.
func TestRestoreWhenTheDaemonDoesNotStop(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	f.withAgentFixtures(t)
	mustInstall(t, f)
	f.launchd.freeze()
	f.launchd.failBootout()
	before := map[string]string{}
	for _, rel := range agentFixtures {
		before[rel] = f.read(t, rel)
	}
	setsBefore := f.sets(t)

	code, stdout, stderr := f.restore(t)
	if code != exitFailure {
		t.Fatalf("exit code %d, want %d\nstdout:\n%s\nstderr:\n%s", code, exitFailure, stdout, stderr)
	}
	for _, rel := range agentFixtures {
		if got := f.read(t, rel); got != before[rel] {
			t.Errorf("%s changed though the daemon was not stopped", rel)
		}
	}
	if !exists(t, f.cfg.daemon.paths.ApplyFile()) {
		t.Errorf("apply.json removed though the daemon was not stopped")
	}
	if got := f.sets(t); len(got) != len(setsBefore) {
		t.Errorf("sets %+v, want no new set", got)
	}
	if want := fmt.Sprintf("launchctl bootout gui/%d/%s", f.cfg.uid, launchd.Label); !strings.Contains(stderr, want) {
		t.Errorf("stderr lacks the manual command %q:\n%s", want, stderr)
	}
}

func TestRestoreWithoutBackups(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	code, stdout, stderr := f.restore(t)
	if code != exitFailure || !strings.Contains(stderr, "no backups") {
		t.Errorf("exit code %d, want %d\nstdout:\n%s\nstderr:\n%s", code, exitFailure, stdout, stderr)
	}
	code, stdout, stderr = f.restore(t, "--list")
	if code != exitFailure || !strings.Contains(stdout, "no backups") {
		t.Errorf("--list: exit code %d, want %d\nstdout:\n%s\nstderr:\n%s", code, exitFailure, stdout, stderr)
	}
	if calls := f.launchd.runs(); len(calls) != 0 {
		t.Errorf("launchctl called: %q", calls)
	}
}

func TestRestoreList(t *testing.T) {
	t.Parallel()

	f := newInstallFixture(t, true)
	mustInstall(t, f)
	sets := f.sets(t)
	if len(sets) != 1 {
		t.Fatalf("sets %+v, want the install set", sets)
	}
	code, stdout, stderr := f.restore(t, "--list")
	if code != exitOK || !strings.Contains(stdout, sets[0].Name+"  install") {
		t.Errorf("exit code %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if calls := f.launchd.runs(); !slices.Equal(calls, []string{"bootout", "bootstrap"}) {
		t.Errorf("--list called launchctl: %q", calls)
	}

	if code, _, stderr := f.restore(t, "20000101T000000.000000000Z"); code != exitFailure || !strings.Contains(stderr, "no backup set") {
		t.Errorf("an unknown set: exit code %d, stderr %s", code, stderr)
	}
	if code, _, _ := f.restore(t, "--list", "extra"); code != exitUsage {
		t.Errorf("--list with a set: exit code %d, want %d", code, exitUsage)
	}
}
