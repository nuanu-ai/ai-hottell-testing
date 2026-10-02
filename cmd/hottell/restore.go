package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/backup"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/launchd"
)

// runRestore puts the agents' configs back from a backup set.
func runRestore(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	cfg, err := restoreConfigFrom(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "hottell restore: %v\n", err)
		return exitFailure
	}
	return restore(context.Background(), cfg, args, stdout, stderr)
}

// restoreConfigFrom is what restore needs: the state, the installed binary and launchd. It
// resolves neither agent's dir, which may be gone by now; the paths to restore come from
// the set.
func restoreConfigFrom(getenv func(string) string) (installConfig, error) {
	paths, home, err := hookPaths(getenv)
	if err != nil {
		return installConfig{}, err
	}
	if home == "" {
		return installConfig{}, errors.New("HOME is not set")
	}
	return installConfig{
		daemon:  daemonConfig{paths: paths, home: home, binary: installBinary(home)},
		launchd: launchd.ExecRunner{},
		uid:     os.Getuid(),
	}, nil
}

// restore lists the backup sets with --list, or puts back the agents' configs of the set
// args name, by default the one the last install took, and removes the launchd agent the
// way uninstall does. It needs neither the network nor the MCP server, so the user can run
// it when every agent is broken. The installed binary stays.
//
// The set is checked first: a damaged copy, a path that is not absolute or an incomplete
// manifest entry refuses it before the daemon is stopped, and nothing changes. The daemon
// goes next, or it would apply the settings again over the restored files within a round;
// when it cannot be stopped, restore touches nothing and tells how to stop it by hand.
// Before writing, restore copies the files as they are into a restore set, so that
// restoring that set undoes it. The writes are not atomic: an error midway leaves the
// files restored before it, and that restore set, whose undo command restore prints, is
// the way back. A config that is a symbolic link, or was one at the backup, is skipped
// and left to the user, and restore then exits 1.
func restore(ctx context.Context, cfg installConfig, args []string, stdout, stderr io.Writer) int {
	var name string
	list := false
	switch {
	case len(args) == 0:
	case len(args) == 1 && args[0] == "--list":
		list = true
	case len(args) == 1 && !strings.HasPrefix(args[0], "-"):
		name = args[0]
	default:
		fmt.Fprintln(stderr, usage)
		return exitUsage
	}
	store := &backup.Store{Dir: cfg.daemon.paths.BackupDir()}
	sets, err := store.Sets()
	if err != nil {
		fmt.Fprintf(stderr, "hottell restore: %v\n", err)
		return exitFailure
	}
	if list {
		listSets(stdout, sets)
		if len(sets) == 0 {
			return exitFailure
		}
		return exitOK
	}
	set, err := pickSet(sets, name)
	if err != nil {
		fmt.Fprintf(stderr, "hottell restore: %v (backups are kept in %s)\n", err, store.Dir)
		return exitFailure
	}
	if err := store.Check(set); err != nil {
		fmt.Fprintf(stderr, "hottell restore: the set %s cannot be restored, nothing was changed: %v\n", set.Name, err)
		return exitFailure
	}

	r := &report{out: stdout}
	d := cfg.daemon
	agent := launchd.Agent{Home: d.home, Binary: d.binary, UID: cfg.uid, Runner: cfg.launchd}
	if err := agent.Uninstall(ctx); err != nil {
		r.failed("launchd", err)
		fmt.Fprintf(stderr, "hottell restore: the daemon is not stopped, so nothing is restored; stop it with\n  launchctl bootout gui/%d/%s\nand run hottell restore again\n", cfg.uid, launchd.Label)
		return exitFailure
	}
	r.ok("launchd", "removed "+agent.PlistPath())
	undo, err := store.TakeBeforeRestore(set)
	if err != nil {
		r.failed("backup", err)
		fmt.Fprintln(stderr, "hottell restore: could not copy the current configs, so nothing is restored")
		return exitFailure
	}
	r.ok("backup", fmt.Sprintf("%s (the state before this restore; undo with: %s restore %s)", undo.Name, d.binary, undo.Name))
	// The saved outcome of the last application no longer tells what the agents hold.
	if err := os.Remove(d.paths.ApplyFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
		r.failed("state", fmt.Errorf("remove %s: %w", d.paths.ApplyFile(), err))
	}

	outcomes, err := store.Restore(set)
	skipped := false
	for _, out := range outcomes {
		switch {
		case out.Done != backup.DoneSkipped:
			fmt.Fprintf(stdout, "  %-9s %s\n", out.Done, out.Path)
		case out.Copy == "":
			skipped = true
			fmt.Fprintf(stdout, "  %-9s %s: a symbolic link; it was absent before\n", out.Done, out.Path)
		default:
			skipped = true
			fmt.Fprintf(stdout, "  %-9s %s: a symbolic link; restore it by hand from %s\n", out.Done, out.Path, out.Copy)
		}
	}
	if err != nil {
		r.failed("restore", err)
	}
	if r.anyFailed {
		fmt.Fprintln(stderr, "hottell restore: some steps failed, see above; run it again once they are fixed")
		return exitFailure
	}
	if skipped {
		fmt.Fprintln(stderr, "hottell restore: some configs are symbolic links and were left as they are, see above")
		return exitFailure
	}
	fmt.Fprintf(stdout, "Restored the set %s. The binary %s stays; start new agent sessions.\n", set.Name, d.binary)
	return exitOK
}

// pickSet returns the set named name, or the newest install set when name is empty.
func pickSet(sets []backup.Set, name string) (backup.Set, error) {
	if len(sets) == 0 {
		return backup.Set{}, errors.New("no backups")
	}
	for _, set := range sets {
		if (name == "" && set.Reason == backup.ReasonInstall) || (name != "" && set.Name == name) {
			return set, nil
		}
	}
	if name != "" {
		return backup.Set{}, fmt.Errorf("no backup set %s; see hottell restore --list", name)
	}
	return backup.Set{}, errors.New("no backup set taken by install; name one of hottell restore --list")
}

// listSets prints the sets, newest first, and marks the one restore takes by default.
func listSets(stdout io.Writer, sets []backup.Set) {
	if len(sets) == 0 {
		fmt.Fprintln(stdout, "no backups")
		return
	}
	def, _ := pickSet(sets, "")
	for _, set := range sets {
		absent := 0
		for _, f := range set.Files {
			if f.Absent {
				absent++
			}
		}
		mark := ""
		if set.Name == def.Name {
			mark = "  (default)"
		}
		fmt.Fprintf(stdout, "%s  %-9s %d files, %d absent%s\n", set.Name, set.Reason, len(set.Files)-absent, absent, mark)
	}
}
