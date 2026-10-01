// Package codex reads the transcripts of Codex: it finds the rollout files that grew
// since the last scan, applies the deny settings, and queues every complete line
// appended since the saved read position. It also queues the history of the rollouts,
// as package transcript describes it.
//
// The files are the ones ingest.md lists under the agent's root, ~/.codex:
// sessions/YYYY/MM/DD/rollout-<time>-<thread-id>.jsonl, the same under
// archived_sessions/, and either of them compressed to .jsonl.zst. A subagent's rollout
// is the same kind of file; its first line says it is one. Everything else under the
// root is skipped.
package codex

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
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/transcript"
)

const (
	ext            = ".jsonl"
	zstExt         = ".zst"
	rolloutPrefix  = "rollout-"
	threadIDLength = 36
	// readBufSize is the read buffer; longer lines grow past it.
	readBufSize = 64 << 10
	// threadSourceSubagent is session_meta.payload.thread_source of a subagent's rollout.
	threadSourceSubagent = "subagent"
	typeSessionMeta      = "session_meta"
	typeTurnContext      = "turn_context"
	// sessionsDir and archivedDir are the directories under the root that hold rollouts.
	sessionsDir = "sessions"
	archivedDir = "archived_sessions"
)

// errShorter reports that a file holds less than its read position.
var errShorter = errors.New("file is shorter than its read position")

// Queue takes a record; *queue.Queue is one.
type Queue interface {
	Put(kind, payload []byte) (string, error)
}

// Reader queues the new lines of the rollouts. The daemon keeps one and calls Scan.
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

// file is one rollout under the root.
type file struct {
	rel      string
	threadID string
	zst      bool
}

// Scan queues the complete lines appended to the rollouts under root since the last
// scan and saves the new read positions. A line is queued only when the settings let
// the session's transcripts through; a denied line is skipped for good. The lines a
// subagent's rollout copies from its parent are not queued. A missing root has nothing
// to scan. An error in one file does not stop the others.
//
// The read position belongs to the thread, not to the path, and counts unpacked
// bytes: when Codex archives or compresses a rollout that was already read, its lines
// are not queued again.
//
// The first scan queues nothing: it saves every rollout at the end of its complete
// lines and keeps what is before as the rollout's history. A rollout it fails on is
// counted as history by the next scans until one reads it to its end.
func (r *Reader) Scan(root string, settings policy.Settings) error {
	files, err := list(root)
	if err != nil {
		return err
	}
	baselined, err := r.offsets.Baselined(string(policy.Codex))
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
		if err := r.offsets.SaveBaseline(string(policy.Codex), r.now()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// scanFile reads the rollout from its saved position on. A thread being counted is
// only read to its end, and its lines become its history.
func (r *Reader) scanFile(root string, f file, settings policy.Settings, baselined bool) error {
	path := filepath.Join(root, filepath.FromSlash(f.rel))
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	pos, err := r.offsets.Get(string(policy.Codex), f.threadID)
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
	stamp := ""
	if f.zst {
		// A compressed file is not written to any more; unpacking it again only finds
		// the lines already read.
		stamp = strconv.FormatInt(info.Size(), 10) + ":" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
		if stamp == pos.Compressed {
			return nil
		}
	} else {
		if info.Size() == pos.Offset {
			return r.counted(&pos, counting, "")
		}
		if info.Size() < pos.Offset {
			r.shorter(&pos, f, info.Size())
		}
	}

	head, err := readHead(path, f.zst)
	if err != nil {
		return err
	}
	if head == nil {
		// An incomplete first line waits until Codex finishes it.
		return r.counted(&pos, counting, "")
	}
	if head.cwd == "" {
		r.log.Warn("rollout does not start with the session's cwd, skipping it", "path", f.rel)
		return r.counted(&pos, counting, "")
	}
	if pos.Cwd == "" {
		pos.Cwd = head.cwd
	}

	rd, err := openAt(path, f.zst, pos.Offset)
	if errors.Is(err, errShorter) {
		r.shorter(&pos, f, -1)
		pos.Cwd = head.cwd
		rd, err = openAt(path, f.zst, 0)
	}
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer rd.Close()

	br := bufio.NewReaderSize(rd, readBufSize)
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
		rec := parse(line)
		inherited := next.Line > 1 && head.inherited(rec)
		if !inherited && rec.Type == typeTurnContext && rec.Payload.Cwd != "" {
			// The session's cwd changes with the turn; the folder denial follows it.
			next.Cwd = rec.Payload.Cwd
		}
		if live && next.Line > pos.Handled && !inherited && policy.Transcript(settings, policy.Codex, next.Cwd, line) {
			if err := r.put(f, head.kind, pos.Offset, next.Line, line, false); err != nil {
				return errors.Join(err, r.offsets.Put(pos))
			}
		}
		next.Handled = max(next.Handled, next.Line)
		pos = next
	}
	pos.Compressed = stamp
	if counting {
		pos.Counted(head.cwd)
	}
	return r.offsets.Put(pos)
}

// counted ends the counting of a rollout that has nothing more to read now; a rollout
// not being counted keeps its position unsaved.
func (r *Reader) counted(pos *transcript.Position, counting bool, cwd string) error {
	if !counting {
		return nil
	}
	pos.Counted(cwd)
	return r.offsets.Put(*pos)
}

// Backfill queues the history of the rollouts under root, marked as history: the
// lines of the newest file first, until the pass has read limit unpacked bytes. A line
// is queued only when the settings let the session's transcripts through; a denied
// line is skipped for good, and so are the lines a subagent's rollout copies from its
// parent. It does nothing while the settings do not ask for the history, and the next
// pass goes on where the last one stopped, after a restart and after Codex archives or
// compresses the rollout too. It reports whether the pass stopped at its limit, so
// that history is left.
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
		pos, err := r.offsets.Get(string(policy.Codex), f.threadID)
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

// backfillFile queues the rollout's history from where the backfill got, reading at
// most limit unpacked bytes; a longer line goes out only when the pass has not started
// yet. It returns the bytes read, or -1 when the limit stopped it. The history belongs
// to the thread: once one file of the thread finished it, another one has none.
func (r *Reader) backfillFile(root string, f file, settings policy.Settings, limit int64, started bool) (int64, error) {
	pos, err := r.offsets.Get(string(policy.Codex), f.threadID)
	if err != nil || pos.History == nil {
		return 0, err
	}
	h := pos.History
	path := filepath.Join(root, filepath.FromSlash(f.rel))
	head, err := readHead(path, f.zst)
	if err != nil || head == nil || head.cwd == "" {
		return 0, err
	}
	if h.Line == 0 {
		h.Cwd = head.cwd
	}
	rd, err := openAt(path, f.zst, h.Offset)
	if errors.Is(err, errShorter) {
		r.dropHistory(&pos, f)
		return 0, r.offsets.Put(pos)
	}
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	defer rd.Close()

	br := bufio.NewReaderSize(rd, readBufSize)
	var used int64
	for h.Line < h.Lines {
		raw, err := br.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			r.dropHistory(&pos, f)
			return used, r.offsets.Put(pos)
		}
		if err != nil {
			return used, errors.Join(fmt.Errorf("read: %w", err), r.offsets.Put(pos))
		}
		size := int64(len(raw))
		if (started || used > 0) && used+size > limit {
			return -1, r.offsets.Put(pos)
		}
		line := trimEOL(raw)
		rec := parse(line)
		inherited := h.Line > 0 && head.inherited(rec)
		if !inherited && rec.Type == typeTurnContext && rec.Payload.Cwd != "" {
			h.Cwd = rec.Payload.Cwd
		}
		if !inherited && policy.Transcript(settings, policy.Codex, h.Cwd, line) {
			if err := r.put(f, head.kind, h.Offset, h.Line+1, line, true); err != nil {
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

// dropHistory gives up the rest of the history of a rollout that holds less than it.
func (r *Reader) dropHistory(pos *transcript.Position, f file) {
	r.log.Warn("transcript is shorter than its history, dropping the rest of the history",
		"path", f.rel, "line", pos.History.Line, "lines", pos.History.Lines)
	pos.History = nil
}

// shorter starts pos over from the file's start; the lines already handled are not
// queued again. A negative size is unknown.
func (r *Reader) shorter(pos *transcript.Position, f file, size int64) {
	r.log.Warn("transcript is shorter than its read position, reading it from the start",
		"path", f.rel, "size", size, "offset", pos.Offset)
	pos.Handled = max(pos.Handled, pos.Line)
	pos.Offset, pos.Line, pos.Cwd = 0, 0, ""
}

func (r *Reader) put(f file, kind string, offset, line int64, payload []byte, backfill bool) error {
	meta, err := transcript.Meta{
		Source:           transcript.Source,
		Agent:            string(policy.Codex),
		SessionID:        f.threadID,
		Kind:             kind,
		Path:             f.rel,
		Offset:           offset,
		Line:             line,
		Backfill:         backfill,
		ObservedUnixNano: r.now().UnixNano(),
	}.Encode()
	if err != nil {
		return err
	}
	if _, err := r.queue.Put(meta, payload); err != nil {
		return fmt.Errorf("queue line %d: %w", line, err)
	}
	return nil
}

// record is what the reader needs from a rollout line.
type record struct {
	Type    string  `json:"type"`
	Ordinal *uint64 `json:"ordinal"`
	Payload struct {
		Cwd                         string  `json:"cwd"`
		ThreadSource                string  `json:"thread_source"`
		SubagentHistoryStartOrdinal *uint64 `json:"subagent_history_start_ordinal"`
	} `json:"payload"`
}

// parse reads the fields of a line; a line that is not a JSON object has none.
func parse(line []byte) record {
	var rec record
	_ = json.Unmarshal(line, &rec)
	return rec
}

// head is what the first line, session_meta, says about the whole rollout.
type head struct {
	cwd  string
	kind string
	// start is the first ordinal of the subagent's own records, or nil.
	start *uint64
}

// inherited reports whether a line after the first one is a copy of the parent's
// record.
//
// Codex 0.159.0 (codex-rs, tag rust-v0.159.0) starts a subagent's rollout with its
// own session_meta at ordinal 0 and then copies the parent's records as ordinals
// 1..N: LiveThread::create_with_inherited_model_context in
// thread-store/src/live_thread.rs sets session_meta.subagent_history_start_ordinal to
// N+1 before writing the copy. Codex itself leaves the records below that ordinal out
// of the child's history (read_projection_steps in
// thread-store/src/local/thread_history_materialization.rs), and so does the reader.
// A rollout without the field, legacy ones included, copies nothing that can be told
// apart and is sent whole.
func (h head) inherited(rec record) bool {
	return h.start != nil && rec.Ordinal != nil && *rec.Ordinal < *h.start
}

// readHead reads the first line of the rollout; nil while it is incomplete.
func readHead(path string, zst bool) (*head, error) {
	rd, err := openAt(path, zst, 0)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer rd.Close()
	raw, err := bufio.NewReaderSize(rd, readBufSize).ReadBytes('\n')
	if errors.Is(err, io.EOF) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	rec := parse(trimEOL(raw))
	h := &head{kind: transcript.KindMain}
	if rec.Type != typeSessionMeta {
		return h, nil
	}
	h.cwd = rec.Payload.Cwd
	if rec.Payload.ThreadSource == threadSourceSubagent {
		h.kind = transcript.KindSubagent
	}
	h.start = rec.Payload.SubagentHistoryStartOrdinal
	return h, nil
}

// openAt opens the rollout's unpacked content at offset. errShorter reports that the
// content ends before offset.
func openAt(path string, zst bool, offset int64) (io.ReadCloser, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	if !zst {
		if _, err := fh.Seek(offset, io.SeekStart); err != nil {
			fh.Close()
			return nil, fmt.Errorf("seek: %w", err)
		}
		return fh, nil
	}
	dec, err := zstd.NewReader(fh, zstd.WithDecoderConcurrency(1))
	if err != nil {
		fh.Close()
		return nil, fmt.Errorf("unpack: %w", err)
	}
	rd := &zstReader{dec: dec, fh: fh}
	if _, err := io.CopyN(io.Discard, dec, offset); err != nil {
		rd.Close()
		if errors.Is(err, io.EOF) {
			return nil, errShorter
		}
		return nil, fmt.Errorf("unpack: %w", err)
	}
	return rd, nil
}

// zstReader reads a compressed file unpacked.
type zstReader struct {
	dec *zstd.Decoder
	fh  *os.File
}

func (z *zstReader) Read(p []byte) (int, error) {
	n, err := z.dec.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = fmt.Errorf("unpack: %w", err)
	}
	return n, err
}

func (z *zstReader) Close() error {
	z.dec.Close()
	return z.fh.Close()
}

// trimEOL cuts the line's \n and a \r before it.
func trimEOL(raw []byte) []byte {
	raw = bytes.TrimSuffix(raw, []byte("\n"))
	return bytes.TrimSuffix(raw, []byte("\r"))
}

// list finds the rollouts under root; a missing root or directory holds none.
func list(root string) ([]file, error) {
	var files []file
	for _, dir := range []string{sessionsDir, archivedDir} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if !d.Type().IsRegular() {
				return nil
			}
			f, ok := rollout(d.Name())
			if !ok {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			f.rel = filepath.ToSlash(rel)
			files = append(files, f)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", dir, err)
		}
	}
	return files, nil
}

// rollout tells a rollout's file name; its thread id is the last 36 characters of the
// name without .jsonl and .zst.
func rollout(name string) (file, bool) {
	if !strings.HasPrefix(name, rolloutPrefix) {
		return file{}, false
	}
	base, zst := strings.CutSuffix(name, zstExt)
	base, ok := strings.CutSuffix(base, ext)
	if !ok || len(base) < len(rolloutPrefix)+threadIDLength {
		return file{}, false
	}
	return file{threadID: base[len(base)-threadIDLength:], zst: zst}, true
}
