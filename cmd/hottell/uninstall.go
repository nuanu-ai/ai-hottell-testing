package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/claude"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/codex"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/backup"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/launchd"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/mcpserver"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/skillinstall"
	"git.alva.dev/alva/harness-telemetry/skills"
)

// runUninstall removes everything install set up.
func runUninstall(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, usage)
		return exitUsage
	}
	cfg, err := installConfigFrom(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "hottell uninstall: %v\n", err)
		return exitFailure
	}
	return uninstall(context.Background(), cfg, stdout, stderr)
}

// uninstall stops the daemon, takes hottell's entries out of both agents' configs, then
// removes the state, the logs and the installed binary, printing each outcome. The
// configs are copied into a backup set first, and the backups stay, for hottell restore. Only
// hottell's own entries go, hottell-local among them, and the skills it laid out unless they
// were edited since: those of others and the service's MCP server hottell stay. A folder denial
// never wrote anything into a folder (native-otel.md, section 4), so there is nothing to
// take out of one. What is already gone is no error, so a repeated run is safe.
//
// The order matters. The daemon goes first, or it would apply the settings again. The
// state goes only once both agents are clean: it remembers which Claude Code env keys
// hottell wrote and the user's own Codex [otel] family, which a later run still needs.
// The binary goes last, while nothing runs it any more.
func uninstall(ctx context.Context, cfg installConfig, stdout, stderr io.Writer) int {
	r := &report{out: stdout}
	d := cfg.daemon

	agent := launchd.Agent{Home: d.home, Binary: d.binary, UID: cfg.uid, Runner: cfg.launchd}
	if err := agent.Uninstall(ctx); err != nil {
		return r.stop(stderr, "launchd", err)
	}
	r.ok("launchd", "removed "+agent.PlistPath())

	// The saved outcome of the last application no longer tells what the agents hold, even
	// when a step below fails and the state is kept.
	if err := os.Remove(d.paths.ApplyFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return r.stop(stderr, "state", fmt.Errorf("remove %s: %w", d.paths.ApplyFile(), err))
	}

	backups := agentBackups(d)
	if _, err := backups.Take(backup.ReasonUninstall); err != nil {
		return r.stop(stderr, "backup", err)
	}

	claudeSettings := filepath.Join(d.claudeDir, "settings.json")
	otel := claude.OTel{SettingsPath: claudeSettings, RecordPath: d.paths.ClaudeOTelFile()}
	r.step("claude", "hooks, native OTel and "+mcpserver.Name+" removed",
		func() error { return claude.RemoveHooks(claudeSettings) },
		otel.Remove,
		func() error { return claude.RemoveMCPLocal(claudeJSONOf(d), mcpserver.Name) },
	)
	r.step("codex", "hooks, their trust, native OTel and "+mcpserver.Name+" removed",
		func() error { return codex.Uninstall(d.codexHome) },
		func() error { return codex.RemoveOTel(d.codexHome, d.paths) },
		func() error { return codex.RemoveMCPLocal(d.codexHome, mcpserver.Name) },
	)
	if err := backups.Record(); err != nil {
		r.failed("backup", err)
	}
	// Only the skills hottell laid out and nobody changed since go; the others stay and are named.
	reportSkills(r, skillinstall.Uninstall(skills.FS, skillTargets(d)))
	if r.anyFailed {
		fmt.Fprintln(stderr, "hottell uninstall: stopped, see above; the state is kept for the next run, run it again once it is fixed")
		return exitFailure
	}

	if err := removeState(d.paths.Root, backups.Dir); err != nil {
		return r.stop(stderr, "state", err)
	}
	for _, dir := range uniq(d.paths.Logs, agent.LogDir()) {
		if err := os.RemoveAll(dir); err != nil {
			return r.stop(stderr, "state", err)
		}
	}
	r.ok("state", "removed "+d.paths.Root+" but the backups in "+backups.Dir+", and the logs")

	if err := os.Remove(d.binary); err != nil && !errors.Is(err, os.ErrNotExist) {
		return r.stop(stderr, "binary", fmt.Errorf("remove %s: %w", d.binary, err))
	}
	r.ok("binary", "removed "+d.binary)
	fmt.Fprintln(stdout, "The MCP server hottell stays in the agents' configs; remove it there if it is no longer needed.")
	return exitOK
}

// Running agent sessions keep writing events into the queue, and a file written while the
// queue is being removed leaves it not empty. A moment later the removal goes through.
const (
	removeStateTries = 5
	removeStatePause = 200 * time.Millisecond
)

// removeState removes everything in root but keep, trying again while something writes
// into it.
func removeState(root, keep string) error {
	for try := 1; ; try++ {
		err := removeStateOnce(root, keep)
		if !errors.Is(err, syscall.ENOTEMPTY) {
			return err
		}
		if try == removeStateTries {
			return fmt.Errorf("%w: agent sessions are still running and write into it; "+
				"close them and run hottell uninstall again", err)
		}
		time.Sleep(removeStatePause)
	}
}

// removeStateOnce removes everything in root but keep.
func removeStateOnce(root, keep string) error {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", root, err)
	}
	for _, e := range entries {
		if path := filepath.Join(root, e.Name()); path != keep {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
		}
	}
	return nil
}

// step runs every do, even after one fails, and reports them as one item.
func (r *report) step(item, done string, do ...func() error) {
	var errs []error
	for _, f := range do {
		if err := f(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		r.failed(item, err)
		return
	}
	r.ok(item, done)
}

// stop reports a step that stops the uninstallation.
func (r *report) stop(stderr io.Writer, item string, err error) int {
	r.failed(item, err)
	fmt.Fprintln(stderr, "hottell uninstall: stopped, see above; run it again once it is fixed")
	return exitFailure
}

// uniq returns paths without repeats, in order.
func uniq(paths ...string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, p := range paths {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}
