package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/codextrust"
)

// ErrMalformed means a file is not what Codex reads: hooks.json that is not a JSON object
// or has hooks of the wrong shape, or config.toml that is not TOML. The file is left as
// it is.
var ErrMalformed = errors.New("malformed Codex config")

// HooksFile returns the path of hooks.json under codexHome, which is also the source part
// of the trust keys of its hooks.
func HooksFile(codexHome string) string { return filepath.Join(codexHome, "hooks.json") }

const (
	// homeEnv overrides the Codex home, ~/.codex by default.
	homeEnv = "CODEX_HOME"

	// binaryName is the file name the hottell binary must have for its command to be
	// recognized as ours.
	binaryName = "hottell"
	// commandArgs follow the binary path in the hook command.
	commandArgs = " hook codex"
	// silenced follows commandArgs. Codex acts on what a hook does: exit 2 blocks the
	// tool call, the prompt or the stop, and stdout is read as a decision. Whatever
	// happens to the binary — missing, replaced, failing — the command prints nothing and
	// exits 0, so it can never block or steer the agent. Earlier versions wrote the
	// command without it.
	silenced = " >/dev/null 2>&1 || true"

	// hookTimeoutSec bounds every hottell hook instead of Codex's 600 s default, so a
	// binary that hangs holds the agent up for 10 s at most; exitTimeoutSec keeps the
	// one-second default of the events Codex waits on at exit.
	hookTimeoutSec = 10
	exitTimeoutSec = 1
)

// Events are the Codex 0.159.0 hook events hottell subscribes to: all of HT-50. A command
// hook that prints nothing and exits 0 changes no decision on any of them; on Interrupt
// and SessionEnd its presence also makes Codex flush the rollout and wait for it, at most
// the one-second default timeout.
func Events() []string {
	return []string{
		codextrust.SessionStart,
		codextrust.SessionEnd,
		codextrust.UserPromptSubmit,
		codextrust.PreToolUse,
		codextrust.PermissionRequest,
		codextrust.PostToolUse,
		codextrust.PreCompact,
		codextrust.PostCompact,
		codextrust.SubagentStart,
		codextrust.SubagentStop,
		codextrust.Stop,
		codextrust.Interrupt,
	}
}

// Home returns the Codex home as Codex resolves it, which is the directory the trust keys
// are built from: CODEX_HOME with symlinks resolved when it is set, ~/.codex otherwise.
func Home(getenv func(string) string) (string, error) {
	if dir := getenv(homeEnv); dir != "" {
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", homeEnv, err)
		}
		return filepath.Abs(resolved)
	}
	home := getenv("HOME")
	if home == "" {
		return "", errors.New("HOME is not set")
	}
	return filepath.Join(home, ".codex"), nil
}

// Command returns the hook command for the binary at binaryPath, quoted for the shell
// Codex runs it with, with its output dropped and its exit status ignored.
func Command(binaryPath string) (string, error) {
	if !filepath.IsAbs(binaryPath) {
		return "", fmt.Errorf("hottell binary path %q is not absolute", binaryPath)
	}
	if filepath.Base(binaryPath) != binaryName {
		return "", fmt.Errorf("hottell binary path %q does not end in %s", binaryPath, binaryName)
	}
	return shellQuote(binaryPath) + commandArgs + silenced, nil
}

// Install puts the hottell hooks into hooks.json and trusts them in config.toml.
func Install(codexHome, binaryPath string) error {
	if err := EnsureHooks(codexHome, binaryPath); err != nil {
		return err
	}
	return EnsureTrust(codexHome, binaryPath)
}

// Uninstall removes the trust of the hottell hooks and then the hooks, in that order,
// because the hooks tell which trust entries are ours.
func Uninstall(codexHome string) error {
	if err := RemoveTrust(codexHome); err != nil {
		return err
	}
	return RemoveHooks(codexHome)
}

// EnsureHooks makes hooks.json in codexHome hold exactly one hottell hook for every event
// of Events and none elsewhere: synchronous, without a matcher, with its timeout, running
// the binary at binaryPath, alone in its matcher group. The trust key of
// a hook is its position (see codextrust), so a group of others must never move because
// of hottell: the group goes last in an event that has none, and a hottell group already
// there is rewritten in place. Hooks and keys of others are left alone. A file that already holds that is not
// written, so a repeated call changes nothing; a missing file is created.
func EnsureHooks(codexHome, binaryPath string) error {
	command, err := Command(binaryPath)
	if err != nil {
		return err
	}
	return editHooks(codexHome, func(hooks *object) (bool, error) {
		changed := false
		want := Events()
		for _, event := range want {
			ok, err := ensureEvent(hooks, event, command)
			if err != nil {
				return false, err
			}
			changed = changed || ok
		}
		for _, event := range slices.Clone(hooks.keys()) {
			if slices.Contains(want, event) {
				continue
			}
			ok, err := removeFromEvent(hooks, event)
			if err != nil {
				return false, err
			}
			changed = changed || ok
		}
		return changed, nil
	})
}

// RemoveHooks takes every hottell hook out of hooks.json in codexHome, dropping a matcher
// group, an event and the hooks section that are left empty by it. Hooks and keys of
// others are left alone. A file without hottell hooks, or no file, is not written.
func RemoveHooks(codexHome string) error {
	return editHooks(codexHome, func(hooks *object) (bool, error) {
		changed := false
		for _, event := range slices.Clone(hooks.keys()) {
			ok, err := removeFromEvent(hooks, event)
			if err != nil {
				return false, err
			}
			changed = changed || ok
		}
		return changed, nil
	})
}

// handler is a command hook as hottell writes it. Its timeout is part of the trust hash,
// which hottell computes from the handler as written.
type handler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

// group is a matcher group; ours has no matcher.
type group struct {
	Hooks []handler `json:"hooks"`
}

func wantGroup(event, command string) group {
	timeout := hookTimeoutSec
	if event == codextrust.SessionEnd || event == codextrust.Interrupt {
		timeout = exitTimeoutSec
	}
	return group{Hooks: []handler{{Type: "command", Command: command, Timeout: timeout}}}
}

// ensureEvent writes the wanted group over the first group of the event made only of
// hottell hooks, or appends it when there is none, and takes every other hottell hook out
// of the event.
func ensureEvent(hooks *object, event, command string) (bool, error) {
	want, err := marshal(wantGroup(event, command))
	if err != nil {
		return false, err
	}
	groups, err := eventGroups(hooks, event)
	if err != nil {
		return false, err
	}

	out := make([]json.RawMessage, 0, len(groups)+1)
	placed := false
	for _, raw := range groups {
		_, handlers, ok, err := groupHandlers(raw)
		if err != nil {
			return false, err
		}
		if ok && !placed && len(handlers) > 0 && !slices.ContainsFunc(handlers, notOurs) {
			out = append(out, want)
			placed = true
			continue
		}
		kept, err := withoutOurs(raw)
		if err != nil {
			return false, err
		}
		if kept != nil {
			out = append(out, kept)
		}
	}
	if !placed {
		out = append(out, want)
	}
	if sameGroups(groups, out) {
		return false, nil
	}
	hooks.set(event, encodeArray(out))
	return true, nil
}

// removeFromEvent removes the hottell hooks of the event and drops the event when that
// leaves it empty.
func removeFromEvent(hooks *object, event string) (bool, error) {
	n, err := removeMatching(hooks, event, isOurs)
	return n > 0, err
}

// removeMatching removes the handlers of the event that drop matches and drops the event
// when that leaves it empty. It returns how many it removed.
func removeMatching(hooks *object, event string, drop func(json.RawMessage) bool) (int, error) {
	groups, err := eventGroups(hooks, event)
	if err != nil {
		return 0, err
	}
	out := make([]json.RawMessage, 0, len(groups))
	removed := 0
	for _, raw := range groups {
		kept, n, err := without(raw, drop)
		if err != nil {
			return 0, err
		}
		removed += n
		if kept != nil {
			out = append(out, kept)
		}
	}
	if removed == 0 {
		return 0, nil
	}
	if len(out) == 0 {
		hooks.remove(event)
		return removed, nil
	}
	hooks.set(event, encodeArray(out))
	return removed, nil
}

// withoutOurs returns the group with every hottell handler taken out, the group as it was
// when it has none, or nil when nothing is left of it.
func withoutOurs(raw json.RawMessage) (json.RawMessage, error) {
	kept, _, err := without(raw, isOurs)
	return kept, err
}

// without is withoutOurs for the handlers drop matches, and counts them.
func without(raw json.RawMessage, drop func(json.RawMessage) bool) (json.RawMessage, int, error) {
	g, handlers, ok, err := groupHandlers(raw)
	if err != nil {
		return nil, 0, err
	}
	if !ok {
		return raw, 0, nil
	}
	rest := slices.DeleteFunc(slices.Clone(handlers), drop)
	removed := len(handlers) - len(rest)
	if removed == 0 {
		return raw, 0, nil
	}
	if len(rest) == 0 {
		return nil, removed, nil
	}
	g.set("hooks", encodeArray(rest))
	kept, err := g.encode()
	return kept, removed, err
}

// groupHandlers parses a matcher group. ok is false for a group without a hooks list,
// which cannot hold a hottell hook and is kept as it is.
func groupHandlers(raw json.RawMessage) (*object, []json.RawMessage, bool, error) {
	if !isKind(raw, '{') {
		return nil, nil, false, nil
	}
	g, err := parseObject(raw)
	if err != nil {
		return nil, nil, false, err
	}
	list, found := g.get("hooks")
	if !found || !isKind(list, '[') {
		return nil, nil, false, nil
	}
	handlers, err := parseArray(list)
	if err != nil {
		return nil, nil, false, err
	}
	return g, handlers, true, nil
}

// isOurs reports whether a handler runs a hottell binary, wherever it lies, with
// "hook codex" and nothing else, wrapped as Command writes it or bare as earlier versions
// did: a command that merely mentions hottell or runs it otherwise is someone else's.
func isOurs(raw json.RawMessage) bool {
	var h struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	if !isKind(raw, '{') || json.Unmarshal(raw, &h) != nil || h.Type != "command" {
		return false
	}
	command, _ := strings.CutSuffix(h.Command, silenced)
	path, ok := strings.CutSuffix(command, commandArgs)
	if !ok {
		return false
	}
	path, ok = shellUnquote(path)
	return ok && filepath.IsAbs(path) && filepath.Base(path) == binaryName
}

func notOurs(raw json.RawMessage) bool { return !isOurs(raw) }

func eventGroups(hooks *object, event string) ([]json.RawMessage, error) {
	raw, found := hooks.get(event)
	if !found {
		return nil, nil
	}
	if !isKind(raw, '[') {
		return nil, fmt.Errorf("%w: hooks.%s is not an array", ErrMalformed, event)
	}
	return parseArray(raw)
}

// sameGroups reports whether two lists of groups hold the same JSON values, whatever the
// formatting.
func sameGroups(a, b []json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		var x, y any
		if json.Unmarshal(a[i], &x) != nil || json.Unmarshal(b[i], &y) != nil || !reflect.DeepEqual(x, y) {
			return false
		}
	}
	return true
}

// shellQuote leaves a path of plain characters as it is and single-quotes any other.
func shellQuote(s string) string {
	special := func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("/._-+@%:,=", r)
	}
	if strings.IndexFunc(s, special) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellUnquote reverses shellQuote; ok is false for anything shellQuote does not write.
func shellUnquote(s string) (string, bool) {
	inner, quoted := strings.CutPrefix(s, "'")
	if !quoted {
		return s, s != "" && shellQuote(s) == s
	}
	inner, ok := strings.CutSuffix(inner, "'")
	if !ok {
		return "", false
	}
	path := strings.ReplaceAll(inner, `'\''`, "'")
	return path, shellQuote(path) == s
}
