// Package codex writes the hottell binary's entries into the Codex configuration under
// CODEX_HOME: the [otel] family and the hook trust in config.toml, and the hook commands
// in hooks.json. It edits both files surgically: only hottell's keys change, and the rest
// of each file, its order and comments stay byte for byte; a file it cannot parse is not
// written.
package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

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
	data, err := readConfig(path)
	if err != nil {
		return err
	}
	rest, fam, err := cutOTel(data)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	if err := saveBackup(paths, fam); err != nil {
		return err
	}

	out := appendBlock(rest, block)
	if string(out) == string(data) {
		return nil
	}
	return writeConfig(path, out)
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
	data, err := readConfig(path)
	if err != nil {
		return err
	}
	rest, fam, err := cutOTel(data)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if !fam.empty() {
		// The family was at the end, after a blank line of its own.
		rest = trimEnd(rest)
	}

	switch {
	case saved:
		out, err := restore(rest, backup)
		if err != nil {
			return fmt.Errorf("restore [otel] in %s: %w", path, err)
		}
		if string(out) != string(data) {
			if err := writeConfig(path, out); err != nil {
				return err
			}
		}
	case strings.Contains(fam.text(), otelMarker):
		if err := writeConfig(path, rest); err != nil {
			return err
		}
	}

	if err := os.Remove(otelBackupFile(paths)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", otelBackupFile(paths), err)
	}
	return nil
}

// saveBackup keeps the family cut from config.toml, unless a copy is already saved or
// the family is hottell's own.
func saveBackup(paths state.Paths, fam family) error {
	if _, saved, err := readBackup(paths); err != nil || saved {
		return err
	}
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

// writeConfig replaces config.toml atomically. A symlinked config.toml is written
// through to its target, and an existing file keeps its mode; a new one gets 0600.
func writeConfig(path string, data []byte) (err error) {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
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
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmp.Name(), path, err)
	}
	return nil
}
