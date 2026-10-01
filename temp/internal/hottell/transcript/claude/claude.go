// Package claude reads the transcripts of Claude Code: it finds the transcript files
// that grew since the last scan, applies the deny settings, and queues every complete
// line appended since the saved read position. It also queues the history of the
// transcripts, as package transcript describes it.
//
// The files are the ones ingest.md lists under the agent's root, ~/.claude/projects:
// <project>/<session-id>.jsonl for a session and
// <project>/<session-id>/subagents/agent-<id>.jsonl for its subagents. Everything else
// under the root is skipped.
package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/transcript"
)

const (
	ext            = ".jsonl"
	subagentsDir   = "subagents"
	subagentPrefix = "agent-"
	// readBufSize is the read buffer; longer lines grow past it.
	readBufSize = 64 << 10
)

// Queue takes a record; *queue.Queue is one.
type Queue interface {
	Put(kind, payload []byte) (string, error)
}

// Reader queues the new lines of the transcripts. The daemon keeps one and calls Scan.
type Reader struct {
	queue   Queue
	offsets *transcript.Offsets
	log     *slog.Logger
	now     func() time.Time
}

// New returns a reader that queues into q and keeps the read positions in offsets. A
// nil log discards the messages.
func New(q Queue, offsets *transcript.Offsets, log *slog.Logger) *Reader {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Reader{queue: q, offsets: offsets, log: log, now: time.Now}
}

// file is one transcript file under the root.
type file struct {
	rel       string
	sessionID string
	kind      string
}

// Scan queues the complete lines appended to the transcripts under root since the
// last scan and saves the new read positions. A line is queued only when the settings
// let the session's transcripts through; a denied line is skipped for good. A missing
// root has nothing to scan. An error in one file does not stop the others.
//
// The first scan queues nothing: it saves every file at the end of its complete lines
// and keeps what is before as the file's history. A file it fails on is counted as
// history by the next scans until one reads it to its end.
func (r *Reader) Scan(root string, settings policy.Settings) error {
	files, err := list(root)
	if err != nil {
		return err
	}
	baselined, err := r.offsets.Baselined(string(policy.Claude))
	if err != nil {
		return err
	}
	var errs []error
	for _, f := range files {
		if err := r.scanFile(root, f, settings, baselined); err != nil {
			errs = append(errs, fmt.Errorf("transcript %s: %w", f.rel, err))
		}
	}
	if !baselined {
		if err := r.offsets.SaveBaseline(string(policy.Claude), r.now()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// scanFile reads the file from its saved position on. A file being counted is only
// read to its end, and its lines become its history.
func (r *Reader) scanFile(root string, f file, settings policy.Settings, baselined bool) error {
	path := filepath.Join(root, filepath.FromSlash(f.rel))
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	pos, err := r.offsets.Get(string(policy.Claude), f.rel)
	if err != nil {
		return err
	}
	counting := pos.Counting || !baselined && pos.Fresh()
	if counting && !pos.Counting {
		// Saved before reading: a file the first scan fails on is still history later.
		pos.Counting = true
		if err := r.offsets.Put(pos); err != nil {
			return err
		}
	}
	live := !counting
	size := info.Size()
	if size == pos.Offset {
		if counting {
			pos.Counted(pos.Cwd)
			return r.offsets.Put(pos)
		}
		return nil
	}
	if size < pos.Offset {
		r.log.Warn("transcript is shorter than its read position, reading it from the start",
			"path", f.rel, "size", size, "offset", pos.Offset)
		pos.Handled = max(pos.Handled, pos.Line)
		pos.Offset, pos.Line = 0, 0
	}

	fh, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open: %w", err)
	}
	defer fh.Close()

	if pos.Cwd == "" {
		// The folder denial needs the session's cwd before any line goes out; until a
		// complete line carries it, the file waits.
		cwd, err := findCwd(fh, pos.Offset)
		if err != nil || (cwd == "" && live) {
			return err
		}
		pos.Cwd = cwd
	}

	if _, err := fh.Seek(pos.Offset, io.SeekStart); err != nil {
		return fmt.Errorf("seek: %w", err)
	}
	br := bufio.NewReaderSize(fh, readBufSize)
	for {
		raw, err := br.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			// An incomplete last line waits until the agent finishes it.
			break
		}
		if err != nil {
			return errors.Join(fmt.Errorf("read: %w", err), r.offsets.Put(pos))
		}
		next := pos
		next.Offset += int64(len(raw))
		next.Line++
		line := trimEOL(raw)
		if live && next.Line > pos.Handled && policy.Transcript(settings, policy.Claude, pos.Cwd, line) {
			if err := r.put(f, pos.Offset, next.Line, line, false); err != nil {
				return errors.Join(err, r.offsets.Put(pos))
			}
		}
		next.Handled = max(next.Handled, next.Line)
		pos = next
	}
	if counting {
		pos.Counted(pos.Cwd)
	}
	return r.offsets.Put(pos)
}

// Backfill queues the history of the transcripts under root, marked as history: the
// lines of the newest file first, until the pass has read limit bytes. A line is
// queued only when the settings let the session's transcripts through; a denied line
// is skipped for good. It does nothing while the settings do not ask for the history,
// and the next pass goes on where the last one stopped, after a restart too. It
// reports whether the pass stopped at its limit, so that history is left.
//
// The daemon calls it between scans, never alongside one: both save the positions.
func (r *Reader) Backfill(root string, settings policy.Settings, limit int64) (bool, error) {
	if !settings.BackfillHistory {
		return false, nil
	}
	files, err := list(root)
	if err != nil {
		return false, err
	}
	type pending struct {
		file
		mod time.Time
	}
	var todo []pending
	var errs []error
	for _, f := range files {
		pos, err := r.offsets.Get(string(policy.Claude), f.rel)
		if err != nil {
			errs = append(errs, fmt.Errorf("transcript %s: %w", f.rel, err))
			continue
		}
		if pos.History == nil {
			continue
		}
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(f.rel)))
		if err != nil {
			continue
		}
		todo = append(todo, pending{file: f, mod: info.ModTime()})
	}
	slices.SortStableFunc(todo, func(a, b pending) int { return b.mod.Compare(a.mod) })

	started := false
	for _, p := range todo {
		used, err := r.backfillFile(root, p.file, settings, limit, started)
		if err != nil {
			errs = append(errs, fmt.Errorf("transcript %s: %w", p.rel, err))
		}
		if used < 0 {
			return true, errors.Join(errs...)
		}
		limit -= used
		started = started || used > 0
	}
	return false, errors.Join(errs...)
}

// backfillFile queues the file's history from where the backfill got, reading at most
// limit bytes; a longer line goes out only when the pass has not started yet. It
// returns the bytes read, or -1 when the limit stopped it.
func (r *Reader) backfillFile(root string, f file, settings policy.Settings, limit int64, started bool) (int64, error) {
	pos, err := r.offsets.Get(string(policy.Claude), f.rel)
	if err != nil || pos.History == nil {
		return 0, err
	}
	h := pos.History
	fh, err := os.Open(filepath.Join(root, filepath.FromSlash(f.rel)))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("open: %w", err)
	}
	defer fh.Close()

	if h.Cwd == "" {
		// As in Scan, no line goes out before the session's cwd is known.
		cwd, err := findCwd(fh, 0)
		if err != nil || cwd == "" {
			return 0, err
		}
		h.Cwd = cwd
	}
	if _, err := fh.Seek(h.Offset, io.SeekStart); err != nil {
		return 0, fmt.Errorf("seek: %w", err)
	}
	br := bufio.NewReaderSize(fh, readBufSize)
	var used int64
	for h.Line < h.Lines {
		raw, err := br.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			r.log.Warn("transcript is shorter than its history, dropping the rest of the history",
				"path", f.rel, "line", h.Line, "lines", h.Lines)
			h.Line = h.Lines
			break
		}
		if err != nil {
			return used, errors.Join(fmt.Errorf("read: %w", err), r.offsets.Put(pos))
		}
		size := int64(len(raw))
		if (started || used > 0) && used+size > limit {
			return -1, r.offsets.Put(pos)
		}
		line := trimEOL(raw)
		if policy.Transcript(settings, policy.Claude, h.Cwd, line) {
			if err := r.put(f, h.Offset, h.Line+1, line, true); err != nil {
				return used, errors.Join(err, r.offsets.Put(pos))
			}
		}
		h.Offset += size
		h.Line++
		used += size
	}
	pos.History = nil
	return used, r.offsets.Put(pos)
}

func (r *Reader) put(f file, offset, line int64, payload []byte, backfill bool) error {
	kind, err := transcript.Meta{
		Source:           transcript.Source,
		Agent:            string(policy.Claude),
		SessionID:        f.sessionID,
		Kind:             f.kind,
		Path:             f.rel,
		Offset:           offset,
		Line:             line,
		Backfill:         backfill,
		ObservedUnixNano: r.now().UnixNano(),
	}.Encode()
	if err != nil {
		return err
	}
	if _, err := r.queue.Put(kind, payload); err != nil {
		return fmt.Errorf("queue line %d: %w", line, err)
	}
	return nil
}

// findCwd returns the cwd of the first complete line from offset on that has one, or
// an empty string when none does yet.
func findCwd(fh *os.File, offset int64) (string, error) {
	if _, err := fh.Seek(offset, io.SeekStart); err != nil {
		return "", fmt.Errorf("seek: %w", err)
	}
	br := bufio.NewReaderSize(fh, readBufSize)
	for {
		raw, err := br.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("read: %w", err)
		}
		var rec struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(raw, &rec) == nil && rec.Cwd != "" {
			return rec.Cwd, nil
		}
	}
}

// trimEOL cuts the line's \n and a \r before it.
func trimEOL(raw []byte) []byte {
	raw = bytes.TrimSuffix(raw, []byte("\n"))
	return bytes.TrimSuffix(raw, []byte("\r"))
}

// list finds the transcript files under root; a missing root holds none.
func list(root string) ([]file, error) {
	projects, err := readDir(root)
	if err != nil {
		return nil, err
	}
	var files []file
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		entries, err := readDir(filepath.Join(root, p.Name()))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			switch {
			case e.Type().IsRegular() && strings.HasSuffix(e.Name(), ext):
				files = append(files, file{
					rel:       p.Name() + "/" + e.Name(),
					sessionID: strings.TrimSuffix(e.Name(), ext),
					kind:      transcript.KindMain,
				})
			case e.IsDir():
				subs, err := readDir(filepath.Join(root, p.Name(), e.Name(), subagentsDir))
				if err != nil {
					return nil, err
				}
				for _, s := range subs {
					if s.Type().IsRegular() && strings.HasPrefix(s.Name(), subagentPrefix) && strings.HasSuffix(s.Name(), ext) {
						files = append(files, file{
							rel:       p.Name() + "/" + e.Name() + "/" + subagentsDir + "/" + s.Name(),
							sessionID: e.Name(),
							kind:      transcript.KindSubagent,
						})
					}
				}
			}
		}
	}
	return files, nil
}

// readDir lists dir; a missing directory, or a file where a directory was expected, is
// empty.
func readDir(dir string) ([]fs.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", dir, err)
	}
	return entries, nil
}
