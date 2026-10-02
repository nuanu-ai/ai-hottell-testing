package skills

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

// KindSkills is the Kind of a skills snapshot record.
const KindSkills = "skills"

// sessionStart is the hook event after which the snapshot is taken.
const sessionStart = "SessionStart"

// requestExt is the extension of a request file; the temporary files of an atomic write
// start with a dot and do not have it last.
const requestExt = ".json"

// Bounds of the request queue: requests pile up while the daemon is down or the settings
// are unavailable, and the hooks keep firing.
const (
	// MaxRequestAge is the age past which a request is removed without a snapshot.
	MaxRequestAge = 24 * time.Hour
	// MaxRequests is how many of the latest requests one round takes; the older ones are
	// removed without a snapshot.
	MaxRequests = 32
	// RoundBudget bounds the time of one round of Process, collections included, so that a
	// project on a slow volume does not hold the daemon's scan of the transcripts: no request
	// is started after it, and the requests not taken by then stay for the next round. A
	// single call into a hung volume can still outlast it.
	RoundBudget = CollectBudget
	// MaxRequestBytes bounds a request file; a larger one is removed as damaged. A request is
	// an agent, a session id, a working directory and a time, well under it.
	MaxRequestBytes = 8 << 10
)

// Request asks the daemon for a snapshot: the hook writes one for a SessionStart that
// passed the settings, without reading the disk itself.
type Request struct {
	Agent     policy.Agent `json:"agent"`
	SessionID string       `json:"session_id"`
	Cwd       string       `json:"cwd"`
	At        time.Time    `json:"at"`
}

// Meta is the record's Kind in the queue.
type Meta struct {
	Kind      string       `json:"kind"`
	Agent     policy.Agent `json:"agent"`
	SessionID string       `json:"session_id"`
	Cwd       string       `json:"cwd"`
	// CapturedUnixNano is when the snapshot was collected: the record's time.
	CapturedUnixNano int64 `json:"captured_unix_nano"`
}

// Queue is where the snapshots go.
type Queue interface {
	Put(kind, payload []byte) (string, error)
}

// IsRequested reports whether event, as the settings let it be sent, asks for a
// snapshot: it is a SessionStart.
func IsRequested(event []byte) (Request, bool) {
	var fields struct {
		SessionID     *string `json:"session_id"`
		HookEventName *string `json:"hook_event_name"`
		Cwd           *string `json:"cwd"`
	}
	if json.Unmarshal(event, &fields) != nil || fields.HookEventName == nil || *fields.HookEventName != sessionStart ||
		fields.SessionID == nil || fields.Cwd == nil {
		return Request{}, false
	}
	return Request{SessionID: *fields.SessionID, Cwd: *fields.Cwd}, true
}

// WriteRequest writes r into dir as a file of its own.
func WriteRequest(dir string, r Request) error {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode the skills request: %w", err)
	}
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Errorf("name the skills request: %w", err)
	}
	name := fmt.Sprintf("%020d-%s%s", r.At.UnixNano(), hex.EncodeToString(suffix[:]), requestExt)
	if err := state.WriteFileAtomic(filepath.Join(dir, name), data); err != nil {
		return fmt.Errorf("write the skills request: %w", err)
	}
	return nil
}

// Process takes the latest MaxRequests requests in dir within RoundBudget, as now reads
// the time: it collects the snapshot of a request the current settings still allow, puts it
// into q and removes the request. An older request, one older than MaxRequestAge, and an
// unreadable, damaged or denied one are removed without a snapshot. A request that fails, or
// that the budget leaves untaken, stays for the next round, and the others are still taken.
func Process(dir string, settings policy.Settings, home string, now func() time.Time, q Queue, log *slog.Logger) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("list the skills requests: %w", err)
	}
	// ReadDir sorts by name, and a name starts with the request's time: the latest last.
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && !strings.HasPrefix(e.Name(), ".") && strings.HasSuffix(e.Name(), requestExt) {
			names = append(names, e.Name())
		}
	}
	var errs []error
	if over := len(names) - MaxRequests; over > 0 {
		log.Warn("drop the oldest skills requests over the limit", "count", over)
		for _, name := range names[:over] {
			if err := remove(filepath.Join(dir, name)); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", name, err))
			}
		}
		names = names[over:]
	}
	deadline := now().Add(RoundBudget)
	for i, name := range names {
		left := deadline.Sub(now())
		if left <= 0 {
			log.Warn("leave the skills requests over the round's time to the next round", "count", len(names)-i)
			break
		}
		if err := process(filepath.Join(dir, name), settings, home, now, min(left, CollectBudget), q, log); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// readRequest reads the request file path: opened without following a link and without
// blocking on a FIFO swapped in for it, only a regular file, at most MaxRequestBytes.
func readRequest(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errNotRegular
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxRequestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	return data, nil
}

var errNotRegular = errors.New("not a regular file")

func process(
	path string, settings policy.Settings, home string, now func() time.Time, budget time.Duration, q Queue, log *slog.Logger,
) error {
	data, err := readRequest(path)
	if err != nil {
		// Left in place it would fail every round.
		log.Warn("drop an unreadable skills request", "file", filepath.Base(path), "error", err)
		return remove(path)
	}
	var r Request
	if len(data) > MaxRequestBytes || json.Unmarshal(data, &r) != nil || r.SessionID == "" ||
		(r.Agent != policy.Claude && r.Agent != policy.Codex) {
		log.Warn("drop a damaged skills request", "file", filepath.Base(path))
		return remove(path)
	}
	if now().Sub(r.At) > MaxRequestAge {
		log.Warn("drop a stale skills request", "file", filepath.Base(path))
		return remove(path)
	}
	if !allowed(settings, r) {
		return remove(path)
	}
	captured := now()
	body, err := collect(r.Agent, home, r.Cwd, captured, budget).Encode()
	if err != nil {
		return err
	}
	kind, err := json.Marshal(Meta{Kind: KindSkills, Agent: r.Agent, SessionID: r.SessionID, Cwd: r.Cwd, CapturedUnixNano: captured.UnixNano()})
	if err != nil {
		return fmt.Errorf("encode the skills record kind: %w", err)
	}
	if _, err := q.Put(kind, body); err != nil {
		return fmt.Errorf("queue the skills snapshot: %w", err)
	}
	return remove(path)
}

// allowed applies the settings to the SessionStart the request came from, as the hook
// did: the agent, the hooks source, the folder and the event.
func allowed(settings policy.Settings, r Request) bool {
	event, err := json.Marshal(map[string]string{"session_id": r.SessionID, "hook_event_name": sessionStart, "cwd": r.Cwd})
	if err != nil {
		return false
	}
	_, send := policy.Hook(settings, r.Agent, event)
	return send
}

func remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove the skills request: %w", err)
	}
	return nil
}
