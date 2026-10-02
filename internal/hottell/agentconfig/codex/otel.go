// Package codex writes the hottell binary's entries into the Codex configuration under
// CODEX_HOME: the [otel] family and the hook trust in config.toml, and the hook commands
// in hooks.json. It edits both files surgically: only hottell's keys change, and the rest
// of each file, its order and comments stay byte for byte; a file it cannot parse is not
// written.
package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

// Values of the [otel] family, docs/specs/hottell-contract/native-otel.md section 2.
const (
	environment = "hottell"
	// maxToolResultBytes is the largest integer TOML holds; Codex 0.159 loads it.
	maxToolResultBytes = "9223372036854775807"
	exporterOff        = "none"
	otelMarker         = "# Written by hottell, which owns the whole [otel] family; hottell uninstall restores the previous one."
)

// Native content categories of the settings document that change Codex keys.
const (
	categoryPrompts            = "prompts"
	categoryAssistantResponses = "assistant_responses"
	categoryToolContent        = "tool_content"
)

// ConfigFile returns the path of config.toml under codexHome.
func ConfigFile(codexHome string) string { return filepath.Join(codexHome, "config.toml") }

// otelBackupFile keeps the user's own [otel] family while hottell owns it.
func otelBackupFile(paths state.Paths) string {
	return filepath.Join(paths.Root, "codex-otel-backup.json")
}

// otelBackup is the [otel] family config.toml held before hottell first wrote its own.
type otelBackup struct {
	// Had is false when config.toml had no [otel] family.
	Had bool `json:"had"`
	// Root holds the family's root-level pairs, otel.x = … and otel = { … }.
	Root string `json:"root,omitempty"`
	// Tables holds the family's tables as they stood in the file, comments included.
	Tables string `json:"tables,omitempty"`
}

// ApplyOTel writes hottell's [otel] family into config.toml under codexHome: the three
// OTLP/HTTP exporters to the service with the collector token, and the content switches
// the Codex deny settings leave on. A folder denial does not apply: Codex ignores a
// project [otel]. The first time, the user's own [otel] family is kept in the state
// under paths so that RemoveOTel can restore it. Applying the same inputs again leaves
// the file unchanged.
func ApplyOTel(codexHome string, paths state.Paths, creds state.Credentials, settings policy.Settings) error {
	block, err := otelBlock(creds, settings.Agents.Codex)
	if err != nil {
		return err
	}

	path := ConfigFile(codexHome)
	return editConfig(path, func(data []byte) ([]byte, bool, error) {
		rest, fam, err := cutOTel(data)
		if err != nil {
			return nil, false, fmt.Errorf("parse %s: %w", path, err)
		}
		if err := saveBackup(paths, fam); err != nil {
			return nil, false, err
		}
		out := appendBlock(rest, block)
		return out, !bytes.Equal(out, data), nil
	})
}

// RemoveOTel takes hottell's [otel] family out of config.toml under codexHome and puts
// back the family the user had before ApplyOTel, then forgets the saved copy. Without a
// saved copy it removes only a family hottell wrote, and leaves a user's family alone.
func RemoveOTel(codexHome string, paths state.Paths) error {
	backup, saved, err := readBackup(paths)
	if err != nil {
		return err
	}

	path := ConfigFile(codexHome)
	err = editConfig(path, func(data []byte) ([]byte, bool, error) {
		rest, fam, err := cutOTel(data)
		if err != nil {
			return nil, false, fmt.Errorf("parse %s: %w", path, err)
		}
		if !fam.empty() {
			// The family was at the end, after a blank line of its own.
			rest = trimEnd(rest)
		}
		switch {
		case saved:
			out, err := restore(rest, backup)
			if err != nil {
				return nil, false, fmt.Errorf("restore [otel] in %s: %w", path, err)
			}
			return out, !bytes.Equal(out, data), nil
		case strings.Contains(fam.text(), otelMarker):
			return rest, true, nil
		}
		return nil, false, nil
	})
	if err != nil {
		return err
	}

	if err := os.Remove(otelBackupFile(paths)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", otelBackupFile(paths), err)
	}
	return nil
}

// saveBackup keeps the family cut from config.toml, unless it is hottell's own. The copy
// matters only while hottell's family stands in the file; while it does not, the family in
// the file is the user's latest, so each attempt saves it over any earlier copy: a retried
// attempt does not keep what a stale read saw, and a copy left by an apply that gave up is
// replaced. Nothing deletes the copy here, so an apply that runs at the same time and
// relies on it keeps it.
func saveBackup(paths state.Paths, fam family) error {
	if strings.Contains(fam.text(), otelMarker) {
		return nil
	}
	data, err := json.Marshal(otelBackup{Had: !fam.empty(), Root: fam.Root, Tables: fam.Tables})
	if err != nil {
		return fmt.Errorf("encode [otel] backup: %w", err)
	}
	return state.WriteFileAtomic(otelBackupFile(paths), data)
}

// restore puts the saved family back: its root pairs before the first table header, its
// tables at the end.
func restore(rest []byte, backup otelBackup) ([]byte, error) {
	if !backup.Had {
		return rest, nil
	}
	out, err := insertRoot(rest, backup.Root)
	if err != nil {
		return nil, err
	}
	if backup.Tables != "" {
		out = appendBlock(out, backup.Tables)
	}
	return out, nil
}

func readBackup(paths state.Paths) (otelBackup, bool, error) {
	var backup otelBackup
	data, err := os.ReadFile(otelBackupFile(paths))
	if errors.Is(err, os.ErrNotExist) {
		return backup, false, nil
	}
	if err != nil {
		return backup, false, fmt.Errorf("read %s: %w", otelBackupFile(paths), err)
	}
	if err := json.Unmarshal(data, &backup); err != nil {
		return backup, false, fmt.Errorf("decode %s: %w", otelBackupFile(paths), err)
	}
	return backup, true, nil
}

// otelBlock renders the [otel] family for the Codex deny settings, per the mapping
// table of native-otel.md section 3: a denial writes the switching-off value, and the
// denials add up.
func otelBlock(creds state.Credentials, codex policy.AgentSettings) (string, error) {
	base := serviceURL(creds.IngestURL)
	if base == "" {
		return "", errors.New("no ingest URL in the collector credentials")
	}
	if creds.CollectorToken == "" {
		return "", errors.New("no collector token in the collector credentials")
	}

	src := codex.Sources
	agentOff := !on(codex.Enabled) || (!on(src.NativeMetrics) && !on(src.NativeLogs) && !on(src.NativeTraces))
	denied := func(category string) bool { return slices.Contains(codex.NativeContent.Denied, category) }

	logs := !agentOff && on(src.NativeLogs)
	traces := !agentOff && on(src.NativeTraces)
	metrics := !agentOff && on(src.NativeMetrics)
	maxBytes := maxToolResultBytes
	if denied(categoryToolContent) {
		maxBytes = "0"
	}

	var b strings.Builder
	b.WriteString("[otel]\n")
	b.WriteString(otelMarker + "\n")
	fmt.Fprintf(&b, "environment = %s\n", quote(environment))
	for _, e := range []struct {
		key string
		on  bool
	}{{"exporter", logs}, {"trace_exporter", traces}, {"metrics_exporter", metrics}} {
		if !e.on {
			fmt.Fprintf(&b, "%s = %s\n", e.key, quote(exporterOff))
		}
	}
	fmt.Fprintf(&b, "log_user_prompt = %t\n", !agentOff && !denied(categoryPrompts))
	fmt.Fprintf(&b, "log_agent_responses = %t\n", !agentOff && !denied(categoryAssistantResponses))
	fmt.Fprintf(&b, "tool_result.max_bytes = %s\n", maxBytes)

	header := fmt.Sprintf("{ %s = %s }", quote("Authorization"), quote("Bearer "+creds.CollectorToken))
	for _, e := range []struct {
		key, signal string
		on          bool
	}{{"exporter", "logs", logs}, {"trace_exporter", "traces", traces}, {"metrics_exporter", "metrics", metrics}} {
		if !e.on {
			continue
		}
		fmt.Fprintf(&b, "\n[otel.%s.otlp-http]\n", e.key)
		fmt.Fprintf(&b, "endpoint = %s\n", quote(base+"/v1/"+e.signal))
		fmt.Fprintf(&b, "protocol = %s\n", quote("binary"))
		fmt.Fprintf(&b, "headers = %s\n", header)
	}
	return b.String(), nil
}

// serviceURL returns the service's base address without a trailing slash; an address
// saved with the /v1/logs path of the binary's own records is cut back to the base.
func serviceURL(ingestURL string) string {
	base := strings.TrimRight(ingestURL, "/")
	base = strings.TrimSuffix(base, "/v1/logs")
	return strings.TrimRight(base, "/")
}

// quote writes s as a TOML basic string.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func on(v *bool) bool { return v == nil || *v }

// appendBlock puts block at the end of the file, after one blank line: at the end a
// table header cannot capture the file's root keys.
func appendBlock(rest []byte, block string) []byte {
	trimmed := strings.TrimRight(string(rest), " \t\r\n")
	block = strings.TrimRight(block, " \t\r\n") + "\n"
	if trimmed == "" {
		return []byte(block)
	}
	return []byte(trimmed + "\n\n" + block)
}

// trimEnd leaves a non-empty file with a single newline at its end.
func trimEnd(data []byte) []byte {
	trimmed := strings.TrimRight(string(data), " \t\r\n")
	if trimmed == "" {
		return nil
	}
	return []byte(trimmed + "\n")
}

// readConfig returns config.toml, or nil when it does not exist.
func readConfig(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}

// editTries bounds the attempts of an edit whose file another process keeps changing.
const editTries = 3

// errChanged: the file changed between hottell's read and its replacement. Codex writes
// config.toml itself (project trust), and a daemon of an earlier install may apply
// settings to it; writing over their change would lose it.
var errChanged = errors.New("changed by another process while it was being edited")

// beforeReplace maps a file path to a function run just before the file is compared with
// what was read; a test changes the file there the way Codex would. Production code
// never stores into it.
var beforeReplace sync.Map //nolint:gochecknoglobals // test seam, keyed by path

// editConfig reads the file at path (nil when missing), lets edit compute its new
// contents and, when edit reports a change, replaces the file atomically. When another
// process changes the file between the read and the replacement, the edit is made again
// from a fresh read, up to editTries times, so that both changes are kept; edit must
// therefore not depend on an earlier call. A change made in the instant between the last
// comparison and the rename is the remaining risk (writeConfig).
func editConfig(path string, edit func(data []byte) (out []byte, write bool, err error)) error {
	var err error
	for range editTries {
		if err = editConfigOnce(path, edit); !errors.Is(err, errChanged) {
			return err
		}
	}
	return err
}

// editConfigOnce is one attempt of editConfig.
func editConfigOnce(path string, edit func(data []byte) ([]byte, bool, error)) error {
	data, err := readConfig(path)
	if err != nil {
		return err
	}
	out, write, err := edit(data)
	if err != nil || !write {
		return err
	}
	return writeConfig(path, out, data)
}

// writeConfig replaces the file at path atomically with data, unless it no longer holds
// original: then it returns errChanged and leaves the file alone. A symlinked file is
// written through to its target, and an existing file keeps its mode; a new one gets
// 0600. A file that will carry hottell's [otel] family, and with it the collector token,
// loses the group and other bits.
//
// The comparison and the rename are two steps: a write by Codex that lands between them
// (microseconds) is still lost. Closing that window needs a lock Codex honours too, and
// Codex takes none, so editConfig narrows the race and does not exclude it.
func writeConfig(path string, data, original []byte) (err error) {
	key := path
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if bytes.Contains(data, []byte(otelMarker)) {
		mode &= 0o600
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", path, err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("chmod %s: %w", tmp.Name(), err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", tmp.Name(), err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp.Name(), err)
	}
	if check, ok := beforeReplace.Load(key); ok {
		check.(func())()
	}
	current, err := readConfig(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, original) {
		return fmt.Errorf("%s: %w", path, errChanged)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmp.Name(), path, err)
	}
	return nil
}
