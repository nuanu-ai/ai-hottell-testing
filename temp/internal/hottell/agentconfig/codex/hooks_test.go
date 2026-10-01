package codex_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/codex"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/codextrust"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

const (
	binary     = "/Users/dev/.local/bin/hottell"
	hooksName  = "hooks.json"
	configName = "config.toml"
	// homeMark stands for the temporary Codex home in fixtures and golden files.
	homeMark = "{{CODEX_HOME}}"
	// updateEnv set to 1 rewrites the golden files from the current output.
	updateEnv = "HOTTELL_UPDATE_GOLDEN"
)

// newHome copies the fixture directory into a temporary Codex home; an empty name gives
// an empty home.
func newHome(t *testing.T, fixture string) string {
	t.Helper()
	home := t.TempDir()
	if fixture == "" {
		return home
	}
	for _, name := range []string{hooksName, configName} {
		data, err := os.ReadFile(filepath.Join("testdata", fixture, name))
		if err != nil {
			t.Fatal(err)
		}
		writeBytes(t, filepath.Join(home, name), bytes.ReplaceAll(data, []byte(homeMark), []byte(home)))
	}
	return home
}

func writeBytes(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// checkGolden compares a file of the home with testdata/<dir>/<name>.golden.
func checkGolden(t *testing.T, home, dir, name string) {
	t.Helper()
	got := bytes.ReplaceAll(readBytes(t, filepath.Join(home, name)), []byte(home), []byte(homeMark))
	golden := filepath.Join("testdata", dir, name+".golden")
	if os.Getenv(updateEnv) == "1" {
		if err := os.MkdirAll(filepath.Dir(golden), 0o750); err != nil {
			t.Fatal(err)
		}
		writeBytes(t, golden, got)
	}
	if want := readBytes(t, golden); !bytes.Equal(got, want) {
		t.Errorf("%s differs from %s:\n%s", name, golden, got)
	}
}

func TestInstall(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ name, fixture string }{
		{name: "empty home", fixture: ""},
		{name: "hooks and trust of others", fixture: "foreign"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			home := newHome(t, tt.fixture)
			if err := codex.Install(home, binary); err != nil {
				t.Fatalf("Install() = %v", err)
			}
			dir := tt.fixture
			if dir == "" {
				dir = "empty"
			}
			checkGolden(t, home, dir, hooksName)
			checkGolden(t, home, dir, configName)
			checkTrust(t, home)
		})
	}
}

// checkTrust checks that every hottell handler of hooks.json has the hash Codex computes
// for it at the key of its position, and that no other trust entry holds a hottell hash.
func checkTrust(t *testing.T, home string) {
	t.Helper()
	var hooksFile struct {
		Hooks map[string][]struct {
			Matcher *string `json:"matcher"`
			Hooks   []struct {
				Command string  `json:"command"`
				Timeout *uint64 `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(readBytes(t, filepath.Join(home, hooksName)), &hooksFile); err != nil {
		t.Fatal(err)
	}
	state := trustState(t, home)
	command, _ := codex.Command(binary)
	ours := map[string]bool{}
	for _, event := range codex.Events() {
		found := 0
		for gi, g := range hooksFile.Hooks[event] {
			for hi, h := range g.Hooks {
				if h.Command != command {
					continue
				}
				found++
				if h.Timeout == nil || *h.Timeout != hookTimeout(event) {
					t.Errorf("%s timeout = %v, want %d s", event, h.Timeout, hookTimeout(event))
				}
				key, _ := codextrust.Key(filepath.Join(home, hooksName), event, gi, hi)
				want, _ := codextrust.Hash(event, g.Matcher, codextrust.Handler{Command: command, TimeoutSec: h.Timeout})
				if state[key] != want {
					t.Errorf("trust of %s = %q, want %q", key, state[key], want)
				}
				ours[want] = true
			}
		}
		if found != 1 {
			t.Errorf("%s has %d hottell hooks, want 1", event, found)
		}
	}
	trusted := 0
	for _, hash := range state {
		if ours[hash] {
			trusted++
		}
	}
	if trusted != len(codex.Events()) {
		t.Errorf("%d trust entries hold a hottell hash, want %d", trusted, len(codex.Events()))
	}
}

// hookTimeout is the timeout of the hottell hook of event: the one-second default of the
// events Codex waits on at exit, 10 s instead of the 600 s default elsewhere.
func hookTimeout(event string) uint64 {
	if event == codextrust.SessionEnd || event == codextrust.Interrupt {
		return 1
	}
	return 10
}

// trustLine matches a trust table as hottell and Codex write it, with trusted_hash as
// its first pair.
var trustLine = regexp.MustCompile(`(?m)^\[hooks\.state\."([^"]+)"\]\n(?:[a-z_]+ = .*\n)*?trusted_hash = "([^"]+)"`) //nolint:gochecknoglobals // compiled once

// trustState reads the trusted_hash values of config.toml by key.
func trustState(t *testing.T, home string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, configName))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, m := range trustLine.FindAllStringSubmatch(string(data), -1) {
		out[m[1]] = m[2]
	}
	return out
}

func TestInstallAgainWritesNothing(t *testing.T) {
	t.Parallel()

	for _, fixture := range []string{"", "foreign"} {
		home := newHome(t, fixture)
		if err := codex.Install(home, binary); err != nil {
			t.Fatalf("Install() = %v", err)
		}
		before := statFiles(t, home)
		if err := codex.Install(home, binary); err != nil {
			t.Fatalf("second Install() = %v", err)
		}
		for i, info := range statFiles(t, home) {
			if !os.SameFile(before[i], info) {
				t.Errorf("fixture %q: second Install() rewrote %s", fixture, info.Name())
			}
		}
	}
}

func statFiles(t *testing.T, home string) []os.FileInfo {
	t.Helper()
	var out []os.FileInfo
	for _, name := range []string{hooksName, configName} {
		info, err := os.Stat(filepath.Join(home, name))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, info)
	}
	return out
}

func TestUninstallLeavesOthers(t *testing.T) {
	t.Parallel()

	home := newHome(t, "foreign")
	hooksBefore := readBytes(t, filepath.Join(home, hooksName))
	configBefore := readBytes(t, filepath.Join(home, configName))
	if err := codex.Install(home, binary); err != nil {
		t.Fatalf("Install() = %v", err)
	}
	if err := codex.Uninstall(home); err != nil {
		t.Fatalf("Uninstall() = %v", err)
	}
	if got := readBytes(t, filepath.Join(home, hooksName)); !bytes.Equal(got, hooksBefore) {
		t.Errorf("hooks.json after Uninstall:\n%s\nwant:\n%s", got, hooksBefore)
	}
	if got := readBytes(t, filepath.Join(home, configName)); !bytes.Equal(got, configBefore) {
		t.Errorf("config.toml after Uninstall:\n%s\nwant:\n%s", got, configBefore)
	}

	// Nothing of hottell is left, so another Uninstall writes nothing.
	before := statFiles(t, home)
	if err := codex.Uninstall(home); err != nil {
		t.Fatalf("second Uninstall() = %v", err)
	}
	for i, info := range statFiles(t, home) {
		if !os.SameFile(before[i], info) {
			t.Errorf("second Uninstall() rewrote %s", info.Name())
		}
	}
}

func TestUninstallFromEmptyHome(t *testing.T) {
	t.Parallel()

	home := newHome(t, "")
	if err := codex.Install(home, binary); err != nil {
		t.Fatalf("Install() = %v", err)
	}
	if err := codex.Uninstall(home); err != nil {
		t.Fatalf("Uninstall() = %v", err)
	}
	if got := string(readBytes(t, filepath.Join(home, hooksName))); got != "{}\n" {
		t.Errorf("hooks.json = %q, want {}", got)
	}
	if got := string(readBytes(t, filepath.Join(home, configName))); got != "" {
		t.Errorf("config.toml = %q, want empty", got)
	}
}

// A group others insert before ours moves our trust key; the next install trusts the new
// position and leaves the old entry, which now names someone else's hook, alone.
func TestTrustFollowsPosition(t *testing.T) {
	t.Parallel()

	home := newHome(t, "foreign")
	if err := codex.Install(home, binary); err != nil {
		t.Fatalf("Install() = %v", err)
	}
	source := filepath.Join(home, hooksName)
	oldKey, _ := codextrust.Key(source, codextrust.PreToolUse, 1, 0)
	newKey, _ := codextrust.Key(source, codextrust.PreToolUse, 2, 0)
	oldHash := trustState(t, home)[oldKey]

	hooksPath := filepath.Join(home, hooksName)
	inserted := strings.Replace(string(readBytes(t, hooksPath)),
		`"PreToolUse": [`,
		`"PreToolUse": [{"matcher": "Edit", "hooks": [{"type": "command", "command": "lint-edit"}]},`, 1)
	writeBytes(t, hooksPath, []byte(inserted))

	if err := codex.Install(home, binary); err != nil {
		t.Fatalf("Install() = %v", err)
	}
	var doc struct {
		Hooks map[string][]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(readBytes(t, hooksPath), &doc); err != nil {
		t.Fatal(err)
	}
	if n := len(doc.Hooks[codextrust.PreToolUse]); n != 3 {
		t.Fatalf("PreToolUse has %d groups, want 3: the hottell group stays where it is", n)
	}
	state := trustState(t, home)
	if state[newKey] != oldHash {
		t.Errorf("trust of %s = %q, want %q", newKey, state[newKey], oldHash)
	}
	if state[oldKey] != oldHash {
		t.Errorf("trust of %s = %q, want it left as %q", oldKey, state[oldKey], oldHash)
	}
}

func TestInstallOtherBinaryReplacesInPlace(t *testing.T) {
	t.Parallel()

	home := newHome(t, "foreign")
	if err := codex.Install(home, "/opt/old dir/hottell"); err != nil {
		t.Fatalf("Install() = %v", err)
	}
	if err := codex.Install(home, binary); err != nil {
		t.Fatalf("Install() = %v", err)
	}
	checkGolden(t, home, "foreign", hooksName)
	checkTrust(t, home)
	if data := readBytes(t, filepath.Join(home, hooksName)); bytes.Contains(data, []byte("old dir")) {
		t.Errorf("hooks.json still runs the old binary:\n%s", data)
	}
}

func TestRefusesWhatItCannotEdit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		hooks  string
		config string
		want   error
		// kept is the file the error is about, which must stay as it was.
		kept string
	}{
		{name: "hooks.json is not JSON", hooks: `{"hooks": `, want: codex.ErrMalformed, kept: hooksName},
		{name: "hooks.json is an array", hooks: `[]`, want: codex.ErrMalformed, kept: hooksName},
		{name: "an event is not an array", hooks: `{"hooks": {"Stop": {}}}`, want: codex.ErrMalformed, kept: hooksName},
		{name: "config.toml is not TOML", config: "[features\nhooks = true\n", want: codex.ErrMalformed, kept: configName},
		{
			name:   "trust kept in an inline table",
			config: "[hooks]\nstate = { \"x\" = { trusted_hash = \"sha256:0\" } }\n",
			want:   codex.ErrLayout,
			kept:   configName,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			home := newHome(t, "")
			files := map[string]string{hooksName: tt.hooks, configName: tt.config}
			for name, data := range files {
				writeBytes(t, filepath.Join(home, name), []byte(data))
			}
			if err := codex.Install(home, binary); !errors.Is(err, tt.want) {
				t.Fatalf("Install() = %v, want %v", err, tt.want)
			}
			if got := string(readBytes(t, filepath.Join(home, tt.kept))); got != files[tt.kept] {
				t.Errorf("%s was written: %q", tt.kept, got)
			}
		})
	}
}

func TestEditsTheTargetOfASymlink(t *testing.T) {
	t.Parallel()

	home := newHome(t, "")
	dotfiles := t.TempDir()
	for _, name := range []string{hooksName, configName} {
		target := filepath.Join(dotfiles, name)
		writeBytes(t, target, nil)
		if err := os.Symlink(target, filepath.Join(home, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := codex.Install(home, binary); err != nil {
		t.Fatalf("Install() = %v", err)
	}
	for _, name := range []string{hooksName, configName} {
		if info, err := os.Lstat(filepath.Join(home, name)); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s is no longer a symlink (%v)", name, err)
		}
		if len(readBytes(t, filepath.Join(dotfiles, name))) == 0 {
			t.Errorf("the target of %s was not written", name)
		}
	}
}

func TestHome(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "codex-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}

	env := map[string]string{"CODEX_HOME": link, "HOME": "/Users/dev"}
	got, err := codex.Home(func(k string) string { return env[k] })
	if err != nil || got != resolved {
		t.Errorf("Home() with CODEX_HOME = %q, %v; want %q", got, err, resolved)
	}
	delete(env, "CODEX_HOME")
	got, err = codex.Home(func(k string) string { return env[k] })
	if err != nil || got != "/Users/dev/.codex" {
		t.Errorf("Home() = %q, %v; want /Users/dev/.codex", got, err)
	}
}

func TestCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path    string
		want    string
		wantErr bool
	}{
		{path: "/Users/dev/.local/bin/hottell", want: "/Users/dev/.local/bin/hottell hook codex >/dev/null 2>&1 || true"},
		{path: "/Users/d'ev/my bin/hottell", want: `'/Users/d'\''ev/my bin/hottell' hook codex >/dev/null 2>&1 || true`},
		{path: "bin/hottell", wantErr: true},
		{path: "/usr/local/bin/other", wantErr: true},
	}
	for _, tt := range tests {
		got, err := codex.Command(tt.path)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("Command(%q) = %q, %v; want %q, error %v", tt.path, got, err, tt.want, tt.wantErr)
		}
	}
}

// Entries Codex already keeps at the keys of hottell hooks are updated in place: the hash
// is set next to what else the table holds, a stale one is replaced with its comment
// kept, and removal takes only the hash, or the table it was alone in.
func TestTrustEditsExistingEntries(t *testing.T) {
	t.Parallel()

	home := newHome(t, "existing-state")
	if err := codex.Install(home, binary); err != nil {
		t.Fatalf("Install() = %v", err)
	}
	checkGolden(t, home, "existing-state", configName)
	checkTrust(t, home)

	if err := codex.Uninstall(home); err != nil {
		t.Fatalf("Uninstall() = %v", err)
	}
	got := strings.ReplaceAll(string(readBytes(t, filepath.Join(home, configName))), home, homeMark)
	want := "[hooks.state.\"" + homeMark + "/hooks.json:user_prompt_submit:0:0\"]\n" +
		"enabled = false # the user turned hottell off here\n"
	if got != want {
		t.Errorf("config.toml after Uninstall:\n%s\nwant:\n%s", got, want)
	}
}

// The hook trust and the [otel] family share config.toml: writing and removing either
// leaves the other as it is, in any order.
func TestTrustAndOTelShareConfig(t *testing.T) {
	t.Parallel()

	home := newHome(t, "foreign")
	paths := state.PathsIn(t.TempDir())
	configBefore := readBytes(t, filepath.Join(home, configName))

	if err := codex.Install(home, binary); err != nil {
		t.Fatalf("Install() = %v", err)
	}
	trusted := trustState(t, home)
	if err := codex.ApplyOTel(home, paths, creds, policy.Settings{}); err != nil {
		t.Fatalf("ApplyOTel() = %v", err)
	}
	if got := trustState(t, home); !maps.Equal(got, trusted) {
		t.Errorf("trust after ApplyOTel = %v, want %v", got, trusted)
	}
	withOTel := readBytes(t, filepath.Join(home, configName))
	if err := codex.Install(home, binary); err != nil {
		t.Fatalf("Install() = %v", err)
	}
	if got := readBytes(t, filepath.Join(home, configName)); !bytes.Equal(got, withOTel) {
		t.Errorf("Install() over the [otel] family changed config.toml:\n%s", got)
	}

	if err := codex.Uninstall(home); err != nil {
		t.Fatalf("Uninstall() = %v", err)
	}
	if err := codex.RemoveOTel(home, paths); err != nil {
		t.Fatalf("RemoveOTel() = %v", err)
	}
	if got := readBytes(t, filepath.Join(home, configName)); !bytes.Equal(got, configBefore) {
		t.Errorf("config.toml after Uninstall and RemoveOTel:\n%s\nwant:\n%s", got, configBefore)
	}
}

// An install of an earlier version wrote the unwrapped command without a timeout; the
// next install rewrites it in place to the wrapped one with its trust, and removal takes
// both forms and their trust out while a command that merely mentions hottell stays.
func TestOldUnwrappedFormIsReplaced(t *testing.T) {
	t.Parallel()

	const old = binary + " hook codex"
	foreign := []string{"echo hottell hook codex is someone else's", binary + " hook codex --dry-run"}
	home := newHome(t, "")
	hooksPath := filepath.Join(home, hooksName)
	content := `{"hooks": {
  "Stop": [
    {"hooks": [{"type": "command", "command": "` + old + `"}]},
    {"hooks": [{"type": "command", "command": "` + foreign[0] + `"}, {"type": "command", "command": "` + foreign[1] + `"}]},
    {"hooks": [{"type": "command", "command": "` + old + ` >/dev/null 2>&1 || true", "timeout": 10}]}
  ],
  "SessionEnd": [{"hooks": [{"type": "command", "command": "` + old + `"}]}]
}}`
	writeBytes(t, hooksPath, []byte(content))
	// Trust the old form the way an earlier version did, at its positions.
	var oldTrust strings.Builder
	for _, at := range []struct {
		event string
		group int
	}{{codextrust.Stop, 0}, {codextrust.SessionEnd, 0}} {
		key, _ := codextrust.Key(hooksPath, at.event, at.group, 0)
		hash, _ := codextrust.Hash(at.event, nil, codextrust.Handler{Command: old})
		oldTrust.WriteString("[hooks.state.\"" + key + "\"]\ntrusted_hash = \"" + hash + "\"\n\n")
	}
	writeBytes(t, filepath.Join(home, configName), []byte(oldTrust.String()))

	if err := codex.Install(home, binary); err != nil {
		t.Fatalf("Install() = %v", err)
	}
	checkTrust(t, home)
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(readBytes(t, hooksPath), &doc); err != nil {
		t.Fatal(err)
	}
	stop := doc.Hooks[codextrust.Stop]
	if len(stop) != 2 || len(stop[1].Hooks) != 2 || stop[1].Hooks[0].Command != foreign[0] || stop[1].Hooks[1].Command != foreign[1] {
		t.Errorf("Stop = %+v, want the wrapped hottell group in place of the old one, then the foreign group", stop)
	}
	if bytes.Contains(readBytes(t, hooksPath), []byte(old+`"`)) {
		t.Errorf("hooks.json still holds the unwrapped command:\n%s", readBytes(t, hooksPath))
	}

	writeBytes(t, hooksPath, []byte(content))
	writeBytes(t, filepath.Join(home, configName), []byte(oldTrust.String()))
	if err := codex.Uninstall(home); err != nil {
		t.Fatalf("Uninstall() = %v", err)
	}
	doc.Hooks = nil
	if err := json.Unmarshal(readBytes(t, hooksPath), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Hooks) != 1 || len(doc.Hooks[codextrust.Stop]) != 1 || len(doc.Hooks[codextrust.Stop][0].Hooks) != 2 {
		t.Errorf("hooks.json after Uninstall:\n%s\nwant only the foreign Stop group", readBytes(t, hooksPath))
	}
	if got := trustState(t, home); len(got) != 0 {
		t.Errorf("trust after Uninstall = %v, want none", got)
	}
}
