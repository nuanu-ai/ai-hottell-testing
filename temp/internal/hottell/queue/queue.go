// Package queue is the on-disk queue between the processes that collect events and the
// daemon that sends them. A record is one file named <unixnano>-<rand>, so the
// lexicographic order of the names is the order of the records. Writers and the reader
// share no locks: a record is written to a temporary file and renamed into place, the
// reader only sees whole files, and a record is removed after the service took it.
//
// The queue directory is bounded in size: a Put over the limit removes the oldest
// records. Records the service refused move to the rejected directory with the reason.
package queue

import (
	"cmp"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

// DefaultMaxBytes bounds the queue directory when no limit is given: 5 GiB.
const DefaultMaxBytes int64 = 5 << 30

// reasonSuffix names the file next to a rejected record that holds why it was rejected.
const reasonSuffix = ".reason"

// headerSize is the length prefix of the kind in a record file.
const headerSize = 4

// Permissions of the queue's directories and files: only the user reads the events.
const (
	dirPerm  os.FileMode = 0o700
	filePerm os.FileMode = 0o600
)

// ErrInvalidID means an id is not the name of a record.
var ErrInvalidID = errors.New("invalid record id")

// errCorrupt means a record file is shorter than its header says.
var errCorrupt = errors.New("record cannot be decoded")

// idPattern matches the name Put gives a record.
var idPattern = regexp.MustCompile(`^[0-9]{19}-[0-9a-f]{16}$`) //nolint:gochecknoglobals // compiled once, read-only

// Record is one queued event.
type Record struct {
	// ID is the record's file name; Ack and Reject take it.
	ID string
	// Kind is what the writer passed to Put: what the payload is, with its metadata.
	Kind []byte
	// Payload is the event as the writer passed it to Put.
	Payload []byte
	// Size is the size of the record on disk.
	Size int64
}

// Stats are the counts that hottell status shows.
type Stats struct {
	Queued        int
	QueuedBytes   int64
	Rejected      int
	RejectedBytes int64
}

// Queue is the queue in one directory, with the rejected records in another.
type Queue struct {
	dir         string
	rejectedDir string
	maxBytes    int64
	log         *slog.Logger
}

// New returns the queue in dir, rejecting into rejectedDir. A maxBytes of zero or less
// means DefaultMaxBytes; a nil log discards the messages. The directories are created
// on first use.
func New(dir, rejectedDir string, maxBytes int64, log *slog.Logger) *Queue {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Queue{dir: dir, rejectedDir: rejectedDir, maxBytes: maxBytes, log: log}
}

// Open returns the queue at the locations of the local state.
func Open(paths state.Paths, maxBytes int64, log *slog.Logger) *Queue {
	return New(paths.QueueDir(), paths.RejectedDir(), maxBytes, log)
}

// Put writes a record atomically and returns its id. When the queue directory then
// exceeds its limit, the oldest records are removed until it fits; the new record
// itself is never removed.
func (q *Queue) Put(kind, payload []byte) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	data := make([]byte, headerSize, headerSize+len(kind)+len(payload))
	binary.BigEndian.PutUint32(data, uint32(len(kind))) //nolint:gosec // a kind is a few bytes of metadata
	data = append(data, kind...)
	data = append(data, payload...)
	if err := writeAtomic(q.dir, id, data); err != nil {
		return "", fmt.Errorf("queue record: %w", err)
	}
	if err := q.evict(id); err != nil {
		return id, err
	}
	return id, nil
}

// Next returns the oldest records whose total size is at most maxBytes, oldest first.
// A record larger than maxBytes is returned alone. The records stay in the queue until
// Ack or Reject; an empty queue returns none. A record that cannot be decoded is moved
// to the rejected directory.
func (q *Queue) Next(maxBytes int64) ([]Record, error) {
	entries, err := list(q.dir)
	if err != nil {
		return nil, err
	}
	var (
		batch []Record
		total int64
	)
	for _, e := range entries {
		if len(batch) > 0 && total+e.size > maxBytes {
			break
		}
		rec, err := q.read(e.name)
		if errors.Is(err, fs.ErrNotExist) {
			// Evicted by a concurrent Put.
			continue
		}
		if errors.Is(err, errCorrupt) {
			if rerr := q.Reject([]string{e.name}, err.Error()); rerr != nil {
				return nil, rerr
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		batch = append(batch, rec)
		total += rec.Size
	}
	return batch, nil
}

// Ack removes the records the service took. An id that is already gone is ignored.
func (q *Queue) Ack(ids []string) error {
	for _, id := range ids {
		if !idPattern.MatchString(id) {
			return fmt.Errorf("%w: %q", ErrInvalidID, id)
		}
		if err := os.Remove(filepath.Join(q.dir, id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove record: %w", err)
		}
	}
	return nil
}

// Reject moves the records the service refused to the rejected directory and writes
// the reason next to each as <id>.reason. An id that is already gone is ignored.
func (q *Queue) Reject(ids []string, reason string) error {
	if err := ensureDir(q.rejectedDir); err != nil {
		return err
	}
	for _, id := range ids {
		if !idPattern.MatchString(id) {
			return fmt.Errorf("%w: %q", ErrInvalidID, id)
		}
		src := filepath.Join(q.dir, id)
		if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		dst := filepath.Join(q.rejectedDir, id)
		if err := writeAtomic(q.rejectedDir, id+reasonSuffix, []byte(reason+"\n")); err != nil {
			return fmt.Errorf("write rejection reason: %w", err)
		}
		if err := os.Rename(src, dst); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				_ = os.Remove(dst + reasonSuffix)
				continue
			}
			return fmt.Errorf("move rejected record: %w", err)
		}
	}
	return nil
}

// Stats counts the records and their bytes in the queue and in the rejected directory;
// the bytes of the rejected directory include the reasons.
func (q *Queue) Stats() (Stats, error) {
	var s Stats
	queued, err := list(q.dir)
	if err != nil {
		return s, err
	}
	for _, e := range queued {
		s.Queued++
		s.QueuedBytes += e.size
	}
	rejected, err := listAll(q.rejectedDir)
	if err != nil {
		return s, err
	}
	for _, e := range rejected {
		if idPattern.MatchString(e.name) {
			s.Rejected++
		}
		s.RejectedBytes += e.size
	}
	return s, nil
}

// evict removes the oldest records, except keep, while the queue exceeds its limit.
func (q *Queue) evict(keep string) error {
	entries, err := list(q.dir)
	if err != nil {
		return err
	}
	var total int64
	for _, e := range entries {
		total += e.size
	}
	var removed int
	var removedBytes int64
	for _, e := range entries {
		if total <= q.maxBytes {
			break
		}
		if e.name == keep {
			continue
		}
		err := os.Remove(filepath.Join(q.dir, e.name))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("evict record: %w", err)
		}
		total -= e.size
		if err == nil {
			removed++
			removedBytes += e.size
		}
	}
	if removed > 0 {
		q.log.Warn("queue over its limit, removed the oldest records",
			"removed", removed, "removed_bytes", removedBytes, "limit_bytes", q.maxBytes)
	}
	return nil
}

func (q *Queue) read(id string) (Record, error) {
	data, err := os.ReadFile(filepath.Join(q.dir, id))
	if err != nil {
		return Record{}, fmt.Errorf("read record: %w", err)
	}
	if len(data) < headerSize {
		return Record{}, errCorrupt
	}
	n := binary.BigEndian.Uint32(data)
	if uint64(n) > uint64(len(data)-headerSize) {
		return Record{}, errCorrupt
	}
	body := data[headerSize:]
	return Record{
		ID:      id,
		Kind:    body[:n:n],
		Payload: body[n:],
		Size:    int64(len(data)),
	}, nil
}

type entry struct {
	name string
	size int64
}

// list returns the records in dir, oldest first; temporary and foreign files are
// skipped, and a missing directory is empty.
func list(dir string) ([]entry, error) {
	all, err := listAll(dir)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(e entry) bool { return !idPattern.MatchString(e.name) }), nil
}

// listAll returns every regular file in dir except the temporary ones, sorted by name.
func listAll(dir string) ([]entry, error) {
	dirents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", dir, err)
	}
	out := make([]entry, 0, len(dirents))
	for _, de := range dirents {
		if !de.Type().IsRegular() || strings.HasPrefix(de.Name(), ".") {
			continue
		}
		info, err := de.Info()
		if err != nil {
			// Removed since the listing.
			continue
		}
		out = append(out, entry{name: de.Name(), size: info.Size()})
	}
	slices.SortFunc(out, func(a, b entry) int { return cmp.Compare(a.name, b.name) })
	return out, nil
}

func newID() (string, error) {
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", fmt.Errorf("random record id: %w", err)
	}
	return fmt.Sprintf("%019d-%s", time.Now().UnixNano(), hex.EncodeToString(rnd[:])), nil
}

// writeAtomic writes dir/name through a temporary file and a rename, so a reader sees
// the whole file or none. Unlike state.WriteFileAtomic it does not sync: a sync costs
// the hook tens of milliseconds on macOS, and a power loss may lose the newest events.
func writeAtomic(dir, name string, data []byte) (err error) {
	if err := ensureDir(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-"+name+"-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", name, err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if err := tmp.Chmod(filePerm); err != nil {
		return fmt.Errorf("chmod %s: %w", tmp.Name(), err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmp.Name(), name, err)
	}
	return nil
}

func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return nil
}
