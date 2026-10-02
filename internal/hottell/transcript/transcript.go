// Package transcript holds what the transcript readers of both agents share: the
// metadata a transcript line carries in the queue and the saved read positions of the
// transcript files.
//
// The history of an agent is what its transcripts held when the daemon first scanned
// them, as ingest.md defines it. That first scan saves the position of every file at
// the end of its complete lines and keeps the part before it as the file's History;
// later scans read only what is appended, and a file that appears later is read from
// its start. A file the first scan failed to read to its end stays Counting: later
// scans go on counting it as history instead of sending it. The readers' Backfill queues the history, marked as such, while the
// backfill_history setting is on.
package transcript

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

// Source is the Meta.Source of a transcript line.
const Source = "transcript"

// PassBytes is how many bytes of history one backfill pass reads at most, so that the
// history does not crowd the queue and the network. A single longer line still goes
// out, alone in its pass.
const PassBytes = 4 << 20

// Kinds of transcript file, as the transcript.kind attribute of ingest.md names them.
const (
	KindMain     = "main"
	KindSubagent = "subagent"
)

// Meta describes a transcript line in the queue; it is the record's kind, and the line
// itself is the payload. The fields are what ingest.md puts in the line's attributes.
type Meta struct {
	// Source is always "transcript".
	Source string `json:"source"`
	// Agent is claude or codex.
	Agent string `json:"agent"`
	// SessionID is the session.id attribute.
	SessionID string `json:"session_id"`
	// Kind is main or subagent.
	Kind string `json:"kind"`
	// Path is the file's path relative to the agent's root, with / as the separator.
	Path string `json:"path"`
	// Offset is the byte position of the line's start in the file.
	Offset int64 `json:"offset"`
	// Line is the line's number in the file, from 1.
	Line int64 `json:"line"`
	// Backfill marks a line of the history.
	Backfill bool `json:"backfill"`
	// ObservedUnixNano is when the line was read from the file.
	ObservedUnixNano int64 `json:"observed_unix_nano"`
	// RepoRoot and RepoRemote mark the git repository of the session's cwd at the
	// line; empty outside a repository.
	RepoRoot   string `json:"repo_root,omitempty"`
	RepoRemote string `json:"repo_remote,omitempty"`
}

// Encode returns the metadata as the queue record's kind.
func (m Meta) Encode() ([]byte, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("encode transcript line metadata: %w", err)
	}
	return data, nil
}

// Position is how far a transcript file was read.
type Position struct {
	// Agent and Path name the file as Meta does. Codex moves rollouts to its archive
	// and compresses them, so there Path is the thread id from the file name.
	Agent string `json:"agent"`
	Path  string `json:"path"`
	// Offset is the byte position right after the last complete line read.
	Offset int64 `json:"offset"`
	// Line is the number of complete lines up to Offset.
	Line int64 `json:"line"`
	// Handled is the highest line number already queued or denied. It exceeds Line
	// after the file got shorter: lines up to it are not queued again.
	Handled int64 `json:"handled"`
	// Cwd is the session's cwd once a line carried it.
	Cwd string `json:"cwd,omitempty"`
	// Compressed is the size and modification time of the compressed file read to its
	// end; while they stay the same, the file has nothing new and is not unpacked again.
	Compressed string `json:"compressed,omitempty"`
	// History is the part of the file the backfill has yet to queue; nil for a file
	// that appeared after the first scan or whose history is done.
	History *History `json:"history,omitempty"`
	// Counting marks a file that was there at the first scan and is not read to its
	// end yet: what is read of it is history, not sent by the scans.
	Counting bool `json:"counting,omitempty"`
}

// Counted ends the counting of a file read to its end: the lines up to the position
// are its history, which starts in the session's cwd.
func (p *Position) Counted(cwd string) {
	p.Counting = false
	if p.Line > 0 {
		p.History = &History{End: p.Offset, Lines: p.Line, Cwd: cwd}
	}
}

// History is the part of a transcript file that was there at the first scan.
type History struct {
	// End and Lines are the byte position and the number of the complete lines the
	// file held at the first scan.
	End   int64 `json:"end"`
	Lines int64 `json:"lines"`
	// Offset and Line are how far the backfill got: the lines up to them are queued or
	// denied.
	Offset int64 `json:"offset"`
	Line   int64 `json:"line"`
	// Cwd is the session's cwd at Offset.
	Cwd string `json:"cwd,omitempty"`
}

// Fresh reports whether p is the position of a file never read.
func (p Position) Fresh() bool {
	return p.Offset == 0 && p.Line == 0 && p.Handled == 0 && p.Cwd == "" && p.Compressed == "" && p.History == nil &&
		!p.Counting
}

// Offsets stores one Position per transcript file in a directory, the offsets
// directory of the local state.
type Offsets struct {
	dir string
}

// NewOffsets returns the positions stored in dir.
func NewOffsets(dir string) *Offsets { return &Offsets{dir: dir} }

// Get returns the saved position of agent's file at path; a file never read is at zero.
func (o *Offsets) Get(agent, path string) (Position, error) {
	data, err := os.ReadFile(o.file(agent, path))
	if errors.Is(err, fs.ErrNotExist) {
		return Position{Agent: agent, Path: path}, nil
	}
	if err != nil {
		return Position{}, fmt.Errorf("read transcript position: %w", err)
	}
	var p Position
	if err := json.Unmarshal(data, &p); err != nil {
		return Position{}, fmt.Errorf("decode transcript position of %s: %w", path, err)
	}
	return p, nil
}

// Put saves the position atomically.
func (o *Offsets) Put(p Position) error {
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode transcript position: %w", err)
	}
	return state.WriteFileAtomic(o.file(p.Agent, p.Path), data)
}

// Baselined reports whether agent's transcripts had their first scan: from then on a
// file never read is new, not history.
func (o *Offsets) Baselined(agent string) (bool, error) {
	_, err := os.Stat(o.baseline(agent))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read transcript baseline: %w", err)
	}
	return true, nil
}

// SaveBaseline records that agent's transcripts had their first scan.
func (o *Offsets) SaveBaseline(agent string, at time.Time) error {
	return state.WriteFileAtomic(o.baseline(agent), []byte(at.UTC().Format(time.RFC3339)+"\n"))
}

func (o *Offsets) baseline(agent string) string {
	return filepath.Join(o.dir, agent+"-baseline")
}

// file names the position of a transcript by a hash, since paths nest and may be long.
func (o *Offsets) file(agent, path string) string {
	sum := sha256.Sum256([]byte(path))
	return filepath.Join(o.dir, agent+"-"+hex.EncodeToString(sum[:16])+".json")
}
