// Package claude edits the entries hottell owns in the settings file of Claude Code,
// ~/.claude/settings.json: the hottell hook command on every hook event and the native
// OpenTelemetry keys in env. It touches nothing else in the file, keeps the order of its
// keys, and refuses to write over a file it cannot parse.
package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
)

// ErrMalformed means the settings file is not what Claude Code reads: not JSON, not an
// object, or a hooks section of the wrong shape. The file is left as it is.
var ErrMalformed = errors.New("malformed Claude Code settings")

// commandArgs follows the quoted binary path in the hook command.
const commandArgs = " hook claude"

// silenced follows the hook arguments in the command. Claude Code acts on what a hook
// does: exit 2 blocks the tool call, the prompt or the stop, and stdout at exit 0 is read
// as a decision. Whatever happens to the binary — missing, replaced, failing — the
// command prints nothing and exits 0, so it can never block or steer the agent.
const silenced = " >/dev/null 2>&1 || true"

// hookTimeoutSec bounds every hottell hook but SessionEnd instead of the 600 s default,
// so a binary that hangs holds the agent up for 10 s at most.
const hookTimeoutSec = 10

// binaryName is the file name the hottell binary must have for its command to be
// recognized as ours.
const binaryName = "hottell"

// sessionEndTimeoutSec keeps the SessionEnd hook inside the 1.5 s budget Claude Code gives
// all SessionEnd hooks together: a per-hook timeout above that budget would raise it and
// delay every exit, so ours stays below it.
const sessionEndTimeoutSec = 1

// Events are the Claude Code 2.1.285 hook events hottell subscribes to: every event of
// HT-50 except WorktreeCreate and WorktreeRemove. A command hook on WorktreeCreate
// replaces git worktree creation and must print the worktree path, and one on
// WorktreeRemove replaces the removal of such worktrees, so the silent hottell hook would
// break both.
func Events() []string {
	return []string{
		"SessionStart",
		"Setup",
		"InstructionsLoaded",
		"UserPromptSubmit",
		"UserPromptExpansion",
		"MessageDisplay",
		"PreToolUse",
		"PermissionRequest",
		"PermissionDenied",
		"PostToolUse",
		"PostToolUseFailure",
		"PostToolBatch",
		"Notification",
		"SubagentStart",
		"SubagentStop",
		"TaskCreated",
		"TaskCompleted",
		"Stop",
		"StopFailure",
		"TeammateIdle",
		"ConfigChange",
		"CwdChanged",
		"DirectoryAdded",
		"FileChanged",
		"PreCompact",
		"PostCompact",
		"PreModelSwitch",
		"PostModelSwitch",
		"Elicitation",
		"ElicitationResult",
		"SessionEnd",
	}
}

// Command returns the hook command for the binary at binaryPath, quoted for the shell
// Claude Code runs it with, with its output dropped and its exit status ignored.
func Command(binaryPath string) (string, error) {
	if !filepath.IsAbs(binaryPath) {
		return "", fmt.Errorf("hottell binary path %q is not absolute", binaryPath)
	}
	if filepath.Base(binaryPath) != binaryName {
		return "", fmt.Errorf("hottell binary path %q does not end in %s", binaryPath, binaryName)
	}
	return shellQuote(binaryPath) + commandArgs + silenced, nil
}

// EnsureHooks makes the settings file at path hold exactly one hottell hook for every
// event of Events and none elsewhere: synchronous, without a matcher, with its timeout,
// running the binary at binaryPath. Hooks and keys of others are left alone. A file that already holds that
// is not written, so a repeated call changes nothing; a missing file is created.
func EnsureHooks(path, binaryPath string) error {
	command, err := Command(binaryPath)
	if err != nil {
		return err
	}
	return edit(path, func(hooks *object) (bool, error) {
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

// RemoveHooks takes every hottell hook out of the settings file at path, dropping a
// matcher group, an event and the hooks section that are left empty by it. Hooks and
// keys of others are left alone. A file without hottell hooks, or no file, is not
// written.
func RemoveHooks(path string) error {
	return edit(path, func(hooks *object) (bool, error) {
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

// handler is a command hook as hottell writes it.
type handler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// group is a matcher group; ours has no matcher.
type group struct {
	Hooks []handler `json:"hooks"`
}

func wantGroup(event, command string) group {
	h := handler{Type: "command", Command: command, Timeout: hookTimeoutSec}
	if event == "SessionEnd" {
		h.Timeout = sessionEndTimeoutSec
	}
	return group{Hooks: []handler{h}}
}

// ensureEvent leaves the event alone when its only hottell hook is the wanted group;
// otherwise it removes every hottell hook of the event and appends the wanted group.
func ensureEvent(hooks *object, event, command string) (bool, error) {
	want := wantGroup(event, command)
	groups, err := eventGroups(hooks, event)
	if err != nil {
		return false, err
	}
	ours := 0
	exact := false
	for _, raw := range groups {
		n, err := countOurs(raw)
		if err != nil {
			return false, err
		}
		ours += n
		if n > 0 && sameJSON(raw, want) {
			exact = true
		}
	}
	if ours == 1 && exact {
		return false, nil
	}

	kept, _, err := withoutOurs(groups)
	if err != nil {
		return false, err
	}
	raw, err := marshal(want)
	if err != nil {
		return false, err
	}
	hooks.set(event, encodeArray(append(kept, raw)))
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
	kept, removed, err := without(groups, drop)
	if err != nil || removed == 0 {
		return 0, err
	}
	if len(kept) == 0 {
		hooks.remove(event)
		return removed, nil
	}
	hooks.set(event, encodeArray(kept))
	return removed, nil
}

// withoutOurs returns the groups with every hottell handler taken out; a group left
// without handlers is dropped, any other keeps its keys and their order.
func withoutOurs(groups []json.RawMessage) ([]json.RawMessage, bool, error) {
	kept, removed, err := without(groups, isOurs)
	return kept, removed > 0, err
}

// without is withoutOurs for the handlers drop matches, and counts them.
func without(groups []json.RawMessage, drop func(json.RawMessage) bool) ([]json.RawMessage, int, error) {
	kept := make([]json.RawMessage, 0, len(groups))
	removed := 0
	for _, raw := range groups {
		g, handlers, ok, err := groupHandlers(raw)
		if err != nil {
			return nil, 0, err
		}
		if !ok {
			kept = append(kept, raw)
			continue
		}
		rest := slices.DeleteFunc(slices.Clone(handlers), drop)
		if len(rest) == len(handlers) {
			kept = append(kept, raw)
			continue
		}
		removed += len(handlers) - len(rest)
		if len(rest) == 0 {
			continue
		}
		g.set("hooks", encodeArray(rest))
		out, err := g.encode()
		if err != nil {
			return nil, 0, err
		}
		kept = append(kept, out)
	}
	return kept, removed, nil
}

// countOurs counts the hottell handlers of a matcher group.
func countOurs(raw json.RawMessage) (int, error) {
	_, handlers, ok, err := groupHandlers(raw)
	if err != nil || !ok {
		return 0, err
	}
	n := 0
	for _, h := range handlers {
		if isOurs(h) {
			n++
		}
	}
	return n, nil
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
// "hook claude" and nothing else, wrapped as Command writes it or bare as earlier versions
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

// sameJSON reports whether raw holds the same JSON value as v, whatever the formatting
// and key order.
func sameJSON(raw json.RawMessage, v any) bool {
	want, err := marshal(v)
	if err != nil {
		return false
	}
	var a, b any
	if json.Unmarshal(raw, &a) != nil || json.Unmarshal(want, &b) != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
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

func isKind(raw json.RawMessage, open byte) bool {
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	return len(trimmed) > 0 && trimmed[0] == open
}
