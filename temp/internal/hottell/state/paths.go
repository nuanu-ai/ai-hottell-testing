// Package state holds the local state that the processes of the hottell binary share:
// the hook, the daemon, install and status. It knows where the state lives, reads and
// writes the collector credentials and the settings cache, and writes every file
// atomically so that a reader in another process never sees a half-written one.
package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// HomeEnv overrides the root of the state; tests point it at a temporary directory.
// Under the override the state lives in the root itself and the logs in its logs
// subdirectory.
const HomeEnv = "HOTTELL_HOME"

// Permissions of everything the state creates: only the user reads the token.
const (
	dirPerm  os.FileMode = 0o700
	filePerm os.FileMode = 0o600
)

// Paths are the locations of the local state.
type Paths struct {
	// Root is ~/Library/Application Support/hottell, or HOTTELL_HOME.
	Root string
	// Logs is ~/Library/Logs/hottell, or HOTTELL_HOME/logs.
	Logs string
}

// DefaultPaths resolves the paths from HOTTELL_HOME, or from the user's home directory.
func DefaultPaths() (Paths, error) {
	if root := os.Getenv(HomeEnv); root != "" {
		return PathsIn(root), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve home directory: %w", err)
	}
	return PathsForHome(home), nil
}

// PathsForHome returns the macOS locations under the user's home directory.
func PathsForHome(home string) Paths {
	library := filepath.Join(home, "Library")
	return Paths{
		Root: filepath.Join(library, "Application Support", "hottell"),
		Logs: filepath.Join(library, "Logs", "hottell"),
	}
}

// PathsIn returns the locations under an overridden root.
func PathsIn(root string) Paths {
	return Paths{Root: root, Logs: filepath.Join(root, "logs")}
}

// StateFile holds the collector credentials.
func (p Paths) StateFile() string { return filepath.Join(p.Root, "state.json") }

// SettingsFile holds the cached settings document.
func (p Paths) SettingsFile() string { return filepath.Join(p.Root, "settings.json") }

// QueueDir holds the events waiting to be sent.
func (p Paths) QueueDir() string { return filepath.Join(p.Root, "queue") }

// RejectedDir holds the records the service rejected.
func (p Paths) RejectedDir() string { return filepath.Join(p.Root, "rejected") }

// SenderFile holds what the sender last saw of the service, for hottell status.
func (p Paths) SenderFile() string { return filepath.Join(p.Root, "sender.json") }

// MCPFile holds what the MCP client last saw of the service, for hottell status.
func (p Paths) MCPFile() string { return filepath.Join(p.Root, "mcp.json") }

// OffsetsDir holds the read positions of the transcripts.
func (p Paths) OffsetsDir() string { return filepath.Join(p.Root, "offsets") }

// ClaudeOTelFile remembers the env keys hottell wrote into the Claude Code settings.
func (p Paths) ClaudeOTelFile() string { return filepath.Join(p.Root, "claude-otel.json") }

// ApplyFile holds the outcome of the last application of the settings to the agents,
// which hottell status shows.
func (p Paths) ApplyFile() string { return filepath.Join(p.Root, "apply.json") }

// BackupDir holds the copies of the agents' configs taken before hottell changed them;
// uninstall keeps it, so that hottell restore still has them.
func (p Paths) BackupDir() string { return filepath.Join(p.Root, "backup") }

// LockFile is held by the running daemon, so that a second one exits.
func (p Paths) LockFile() string { return filepath.Join(p.Root, "daemon.lock") }

// Ensure creates every directory of the state with mode 0700 and tightens the mode of
// one that already exists.
func (p Paths) Ensure() error {
	for _, dir := range []string{p.Root, p.QueueDir(), p.RejectedDir(), p.OffsetsDir(), p.Logs} {
		if err := ensureDir(dir); err != nil {
			return err
		}
	}
	return nil
}

func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	// MkdirAll leaves an existing directory's mode alone and is subject to the umask.
	if err := os.Chmod(dir, dirPerm); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}
	return nil
}

// readFile returns the file's contents, or nil when it does not exist.
func readFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}
