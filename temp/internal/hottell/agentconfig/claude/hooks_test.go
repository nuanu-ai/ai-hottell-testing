package claude_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/claude"
)

const (
	binary = "/Users/me/.local/bin/hottell"
	// silenced follows the hook arguments: whatever the binary does, the command prints
	// nothing and exits 0.
	silenced = " >/dev/null 2>&1 || true"
)

// handler and group mirror the hooks section of the settings file.
type handler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout *int   `json:"timeout"`
	Async   *bool  `json:"async"`
}

type group struct {
	Matcher *string   `json:"matcher"`
	Hooks   []handler `json:"hooks"`
}

// fixture copies testdata/name into a temporary directory and returns its path there.
func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // the mode an agent's settings file usually has
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func hooksOf(t *testing.T, path string) map[string][]group {
	t.Helper()
	var doc struct {
		Hooks map[string][]group `json:"hooks"`
	}
	if err := json.Unmarshal(readFile(t, path), &doc); err != nil {
		t.Fatalf("settings are not JSON: %v", err)
	}
	return doc.Hooks
}

func topKeys(t *testing.T, path string) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(readFile(t, path)))
	var keys []string
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return keys
		}
		switch v := tok.(type) {
		case json.Delim:
			if v == '{' || v == '[' {
				depth++
			} else {
				depth--
			}
		case string:
			if depth == 1 {
				keys = append(keys, v)
				// Skip the value; a string value is consumed by the next Token.
				var skip json.RawMessage
				if err := dec.Decode(&skip); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

// isOurs recognizes a hottell hook the way the package does: an absolute path to a
// hottell binary, possibly quoted, followed by "hook claude" and nothing else but the
// optional wrapper.
func isOurs(h handler) bool {
	command, _ := strings.CutSuffix(h.Command, silenced)
	path, ok := strings.CutSuffix(command, " hook claude")
	if !ok {
		return false
	}
	path = strings.TrimSuffix(strings.TrimPrefix(path, "'"), "'")
	return h.Type == "command" && filepath.IsAbs(path) && filepath.Base(path) == "hottell"
}

// ours returns the hottell handlers of the event with their groups.
func ours(groups []group) ([]handler, []group) {
	var hs []handler
	var gs []group
	for _, g := range groups {
		for _, h := range g.Hooks {
			if isOurs(h) {
				hs = append(hs, h)
				gs = append(gs, g)
			}
		}
	}
	return hs, gs
}

// checkInstalled asserts one synchronous hottell hook without a matcher on every event.
func checkInstalled(t *testing.T, path, command string) {
	t.Helper()
	hooks := hooksOf(t, path)
	for _, event := range claude.Events() {
		hs, gs := ours(hooks[event])
		if len(hs) != 1 {
			t.Errorf("%s: %d hottell hooks, want 1", event, len(hs))
			continue
		}
		h, g := hs[0], gs[0]
		if h.Type != "command" || h.Command != command || h.Async != nil || g.Matcher != nil || len(g.Hooks) != 1 {
			t.Errorf("%s: hook %+v in group %+v, want a lone synchronous command %q without matcher", event, h, g, command)
		}
		switch {
		case event == "SessionEnd" && (h.Timeout == nil || *h.Timeout != 1):
			t.Errorf("SessionEnd timeout = %v, want 1 s within the 1.5 s budget", h.Timeout)
		case event != "SessionEnd" && (h.Timeout == nil || *h.Timeout != 10):
			t.Errorf("%s timeout = %v, want 10 s instead of the 600 s default", event, h.Timeout)
		}
	}
	for event, groups := range hooks {
		if hs, _ := ours(groups); len(hs) > 0 && !slices.Contains(claude.Events(), event) {
			t.Errorf("hottell hook on %s, which is not subscribed", event)
		}
	}
}

func TestEvents(t *testing.T) {
	t.Parallel()
	events := claude.Events()
	if len(events) != 31 {
		t.Errorf("%d events, want the 33 of HT-50 minus WorktreeCreate and WorktreeRemove", len(events))
	}
	for _, e := range []string{"PostToolUseFailure", "SessionEnd", "PreToolUse", "MessageDisplay"} {
		if !slices.Contains(events, e) {
			t.Errorf("%s is missing", e)
		}
	}
	for _, e := range []string{"WorktreeCreate", "WorktreeRemove"} {
		if slices.Contains(events, e) {
			t.Errorf("%s would replace git worktrees with a hook that prints no path", e)
		}
	}
}

func TestEnsureHooksEmptyFile(t *testing.T) {
	t.Parallel()
	path := fixture(t, "empty.json")

	if err := claude.EnsureHooks(path, binary); err != nil {
		t.Fatalf("EnsureHooks() = %v", err)
	}
	checkInstalled(t, path, binary+" hook claude"+silenced)
	if keys := topKeys(t, path); !slices.Equal(keys, []string{"hooks"}) {
		t.Errorf("keys = %v, want [hooks]", keys)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, %v; want the file's 0644 kept", info.Mode(), err)
	}
}

func TestEnsureHooksMissingFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")

	if err := claude.EnsureHooks(path, binary); err != nil {
		t.Fatalf("EnsureHooks() = %v", err)
	}
	checkInstalled(t, path, binary+" hook claude"+silenced)
	if !bytes.HasPrefix(readFile(t, path), []byte("{\n  \"hooks\": {\n")) {
		t.Errorf("new file is not indented like Claude Code's:\n%s", readFile(t, path))
	}
}

func TestEnsureHooksKeepsForeign(t *testing.T) {
	t.Parallel()
	path := fixture(t, "foreign.json")
	before := hooksOf(t, path)
	keysBefore := topKeys(t, path)

	if err := claude.EnsureHooks(path, binary); err != nil {
		t.Fatalf("EnsureHooks() = %v", err)
	}
	checkInstalled(t, path, binary+" hook claude"+silenced)

	if keys := topKeys(t, path); !slices.Equal(keys, keysBefore) {
		t.Errorf("keys = %v, want the order %v", keys, keysBefore)
	}
	after := hooksOf(t, path)
	for event, groups := range before {
		if got := after[event][:len(groups)]; !jsonEqual(t, got, groups) {
			t.Errorf("%s: foreign groups %+v changed to %+v", event, groups, got)
		}
	}
	// The foreign WorktreeCreate hook stays, and hottell adds none next to it.
	if hs, _ := ours(after["WorktreeCreate"]); len(hs) != 0 || len(after["WorktreeCreate"]) != 1 {
		t.Errorf("WorktreeCreate = %+v, want only the foreign hook", after["WorktreeCreate"])
	}
	data := readFile(t, path)
	for _, raw := range []string{`"LIMIT": 1.50`, `"big": 12345678901234567890`, `"caf\u00e9 --line"`, `"bar & <baz>"`} {
		if !bytes.Contains(data, []byte(raw)) {
			t.Errorf("foreign value %s was rewritten:\n%s", raw, data)
		}
	}
}

func TestEnsureHooksTwiceChangesNothing(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"empty.json", "foreign.json", "stale.json"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := fixture(t, name)
			if err := claude.EnsureHooks(path, binary); err != nil {
				t.Fatalf("first EnsureHooks() = %v", err)
			}
			data := readFile(t, path)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}

			if err := claude.EnsureHooks(path, binary); err != nil {
				t.Fatalf("second EnsureHooks() = %v", err)
			}
			if again := readFile(t, path); !bytes.Equal(again, data) {
				t.Errorf("second call changed the file:\n%s\nto\n%s", data, again)
			}
			infoAgain, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(info, infoAgain) {
				t.Error("second call rewrote the file")
			}
		})
	}
}

func TestEnsureHooksRepairsStale(t *testing.T) {
	t.Parallel()
	path := fixture(t, "stale.json")

	if err := claude.EnsureHooks(path, binary); err != nil {
		t.Fatalf("EnsureHooks() = %v", err)
	}
	checkInstalled(t, path, binary+" hook claude"+silenced)

	hooks := hooksOf(t, path)
	// The foreign handler that shared a group with an old hottell hook keeps its matcher.
	pre := hooks["PreToolUse"]
	if len(pre) != 2 || pre[0].Matcher == nil || *pre[0].Matcher != "*" ||
		len(pre[0].Hooks) != 1 || pre[0].Hooks[0].Command != "~/bin/guard.sh" {
		t.Errorf("PreToolUse = %+v, want the foreign handler in its group, then ours", pre)
	}
	if _, found := hooks["WorktreeCreate"]; found {
		t.Errorf("WorktreeCreate = %+v, want the old hottell hook removed with the event", hooks["WorktreeCreate"])
	}
	if keys := topKeys(t, path); !slices.Equal(keys, []string{"hooks", "model"}) {
		t.Errorf("keys = %v, want [hooks model]", keys)
	}
	if !bytes.Contains(readFile(t, path), []byte("\n\t\t\"PreToolUse\": [")) {
		t.Errorf("the file's tab indentation was not kept:\n%s", readFile(t, path))
	}
}

func TestEnsureHooksQuotesPath(t *testing.T) {
	t.Parallel()
	path := fixture(t, "foreign.json")
	spaced := "/Users/Jane Doe/.local/bin/hottell"

	if err := claude.EnsureHooks(path, spaced); err != nil {
		t.Fatalf("EnsureHooks() = %v", err)
	}
	checkInstalled(t, path, "'/Users/Jane Doe/.local/bin/hottell' hook claude"+silenced)
	if err := claude.RemoveHooks(path); err != nil {
		t.Fatalf("RemoveHooks() = %v", err)
	}
	if want := readFile(t, filepath.Join("testdata", "foreign.json")); !bytes.Equal(readFile(t, path), want) {
		t.Errorf("quoted hooks were not all removed:\n%s", readFile(t, path))
	}
}

func TestEnsureHooksRejectsBinaryPath(t *testing.T) {
	t.Parallel()
	for _, bin := range []string{"hottell", "bin/hottell", "/usr/local/bin/other"} {
		path := fixture(t, "foreign.json")
		if err := claude.EnsureHooks(path, bin); err == nil {
			t.Errorf("EnsureHooks(%q) = nil, want an error", bin)
		}
		if want := readFile(t, filepath.Join("testdata", "foreign.json")); !bytes.Equal(readFile(t, path), want) {
			t.Errorf("EnsureHooks(%q) changed the file", bin)
		}
	}
}

func TestRemoveHooksKeepsForeign(t *testing.T) {
	t.Parallel()
	path := fixture(t, "foreign.json")
	original := readFile(t, path)

	if err := claude.EnsureHooks(path, binary); err != nil {
		t.Fatalf("EnsureHooks() = %v", err)
	}
	if err := claude.RemoveHooks(path); err != nil {
		t.Fatalf("RemoveHooks() = %v", err)
	}
	if got := readFile(t, path); !bytes.Equal(got, original) {
		t.Errorf("install and removal did not give back the file:\n%s\nwant\n%s", got, original)
	}
}

func TestRemoveHooksFromEmpty(t *testing.T) {
	t.Parallel()
	path := fixture(t, "empty.json")

	if err := claude.EnsureHooks(path, binary); err != nil {
		t.Fatalf("EnsureHooks() = %v", err)
	}
	if err := claude.RemoveHooks(path); err != nil {
		t.Fatalf("RemoveHooks() = %v", err)
	}
	if got := readFile(t, path); string(got) != "{}\n" {
		t.Errorf("after removal = %q, want an empty object", got)
	}
}

func TestRemoveHooksMixedGroup(t *testing.T) {
	t.Parallel()
	path := fixture(t, "stale.json")

	if err := claude.RemoveHooks(path); err != nil {
		t.Fatalf("RemoveHooks() = %v", err)
	}
	hooks := hooksOf(t, path)
	if len(hooks) != 1 || len(hooks["PreToolUse"]) != 1 {
		t.Fatalf("hooks = %+v, want only the foreign PreToolUse group", hooks)
	}
	g := hooks["PreToolUse"][0]
	if g.Matcher == nil || *g.Matcher != "*" || len(g.Hooks) != 1 || g.Hooks[0].Command != "~/bin/guard.sh" {
		t.Errorf("PreToolUse group = %+v, want the foreign handler with its matcher", g)
	}
}

func TestRemoveHooksWithoutOursDoesNotWrite(t *testing.T) {
	t.Parallel()
	path := fixture(t, "foreign.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := claude.RemoveHooks(path); err != nil {
		t.Fatalf("RemoveHooks() = %v", err)
	}
	infoAfter, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(info, infoAfter) {
		t.Error("RemoveHooks rewrote a file without hottell hooks")
	}

	missing := filepath.Join(t.TempDir(), "settings.json")
	if err := claude.RemoveHooks(missing); err != nil {
		t.Fatalf("RemoveHooks(missing) = %v", err)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("RemoveHooks created %s: %v", missing, err)
	}
}

func TestMalformedIsNotWritten(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"broken":          string(readFile(t, filepath.Join("testdata", "broken.json"))),
		"array":           `[{"hooks": {}}]`,
		"trailing":        `{"model": "opus"} {}`,
		"hooks array":     `{"hooks": []}`,
		"event not array": `{"hooks": {"Stop": {"hooks": []}}}`,
		"duplicate hooks": `{"hooks": {}, "model": "opus", "hooks": {}}`,
		"duplicate event": `{"hooks": {"Stop": [], "Stop": []}}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			for op, call := range map[string]func() error{
				"EnsureHooks": func() error { return claude.EnsureHooks(path, binary) },
				"RemoveHooks": func() error { return claude.RemoveHooks(path) },
			} {
				if err := call(); !errors.Is(err, claude.ErrMalformed) {
					t.Errorf("%s() = %v, want ErrMalformed", op, err)
				}
				if got := readFile(t, path); string(got) != content {
					t.Errorf("%s changed the file to %q", op, got)
				}
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil || len(entries) != 1 {
				t.Errorf("directory holds %v (%v), want only the settings file", entries, err)
			}
		})
	}
}

func TestEnsureHooksFollowsSymlink(t *testing.T) {
	t.Parallel()
	target := fixture(t, "foreign.json")
	link := filepath.Join(t.TempDir(), "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := claude.EnsureHooks(link, binary); err != nil {
		t.Fatalf("EnsureHooks() = %v", err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("settings link replaced by a file: %v, %v", info, err)
	}
	checkInstalled(t, target, binary+" hook claude"+silenced)
}

func TestEnsureHooksDanglingSymlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink(filepath.Join("dotfiles", "claude-settings.json"), link); err != nil {
		t.Fatal(err)
	}

	if err := claude.EnsureHooks(link, binary); err != nil {
		t.Fatalf("EnsureHooks() = %v", err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("settings link replaced by a file: %v, %v", info, err)
	}
	checkInstalled(t, filepath.Join(dir, "dotfiles", "claude-settings.json"), binary+" hook claude"+silenced)
}

func TestForeignCommandLikeOurs(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.json")
	content := `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "~/bin/hottell hooks-audit"}]}]}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := claude.RemoveHooks(path); err != nil {
		t.Fatalf("RemoveHooks() = %v", err)
	}
	if got := readFile(t, path); string(got) != content {
		t.Errorf("RemoveHooks changed a foreign hook: %s", got)
	}
	if err := claude.EnsureHooks(path, binary); err != nil {
		t.Fatalf("EnsureHooks() = %v", err)
	}
	if stop := hooksOf(t, path)["Stop"]; len(stop) != 2 || stop[0].Hooks[0].Command != "~/bin/hottell hooks-audit" {
		t.Errorf("Stop = %+v, want the foreign hook kept before ours", stop)
	}
}

// Commands that mention "hottell hook claude" without being exactly the hook command of
// an absolute hottell binary belong to someone else: install and removal leave them be.
func TestForeignMentionsOfHookClaude(t *testing.T) {
	t.Parallel()
	for name, foreign := range map[string]string{
		"echo":           "echo hottell hook claude",
		"echo wrapped":   "echo hottell hook claude" + silenced,
		"extra argument": binary + " hook claude --debug",
		"relative path":  "hottell hook claude",
		"other binary":   "/usr/local/bin/not-hottell hook claude",
		"prompt type":    "",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			handler := `{"type": "command", "command": "` + foreign + `"}`
			if foreign == "" {
				handler = `{"type": "prompt", "command": "` + binary + ` hook claude"}`
			}
			content := `{"hooks": {"Stop": [{"hooks": [` + handler + `]}]}}`
			path := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}

			if err := claude.RemoveHooks(path); err != nil {
				t.Fatalf("RemoveHooks() = %v", err)
			}
			if got := readFile(t, path); string(got) != content {
				t.Errorf("RemoveHooks changed a foreign hook: %s", got)
			}
			if err := claude.EnsureHooks(path, binary); err != nil {
				t.Fatalf("EnsureHooks() = %v", err)
			}
			var doc struct {
				Hooks map[string][]json.RawMessage `json:"hooks"`
			}
			if err := json.Unmarshal(readFile(t, path), &doc); err != nil {
				t.Fatal(err)
			}
			stop := doc.Hooks["Stop"]
			var first any
			var want any
			if len(stop) == 2 {
				_ = json.Unmarshal(stop[0], &first)
				_ = json.Unmarshal([]byte(`{"hooks": [`+handler+`]}`), &want)
			}
			if len(stop) != 2 || !jsonEqual(t, first, want) {
				t.Errorf("Stop = %s, want the foreign hook kept before ours", stop)
			}
		})
	}
}

// An install of an earlier version wrote the unwrapped command; the next install rewrites
// it to the wrapped one and removal takes both forms out.
func TestOldUnwrappedFormIsReplaced(t *testing.T) {
	t.Parallel()
	const (
		old     = binary + " hook claude"
		foreign = "~/bin/hottell status --json"
	)
	content := `{"hooks": {
		"Stop": [
			{"hooks": [{"type": "command", "command": "` + old + `"}]},
			{"hooks": [{"type": "command", "command": "` + old + silenced + `", "timeout": 10}]},
			{"hooks": [{"type": "command", "command": "` + foreign + `"}]}
		],
		"SessionEnd": [{"hooks": [{"type": "command", "command": "` + old + `", "timeout": 1}]}],
		"PreToolUse": [{"hooks": [{"type": "command", "command": "` + old + `"}]}]
	}}`

	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := claude.EnsureHooks(path, binary); err != nil {
		t.Fatalf("EnsureHooks() = %v", err)
	}
	checkInstalled(t, path, old+silenced)
	if stop := hooksOf(t, path)["Stop"]; len(stop) != 2 || stop[0].Hooks[0].Command != foreign {
		t.Errorf("Stop = %+v, want the foreign hook, then only the wrapped hottell hook", stop)
	}

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := claude.RemoveHooks(path); err != nil {
		t.Fatalf("RemoveHooks() = %v", err)
	}
	want := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"` + foreign + `"}]}]}}`
	var got, wantDoc any
	if err := json.Unmarshal(readFile(t, path), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &wantDoc); err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, got, wantDoc) {
		t.Errorf("after removal:\n%s\nwant only the foreign hook", readFile(t, path))
	}
}

func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	x, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(x, y)
}
