package queue_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/queue"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

// Environment of the child process that TestPutFromProcesses starts.
const (
	childDirEnv   = "HOTTELL_QUEUE_TEST_DIR"
	childIndexEnv = "HOTTELL_QUEUE_TEST_INDEX"
	childCountEnv = "HOTTELL_QUEUE_TEST_COUNT"
)

// TestMain turns the test binary into a writer when TestPutFromProcesses starts it.
func TestMain(m *testing.M) {
	if dir := os.Getenv(childDirEnv); dir != "" {
		if err := childPut(dir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func childPut(dir string) error {
	count, err := strconv.Atoi(os.Getenv(childCountEnv))
	if err != nil {
		return err
	}
	q := queue.New(filepath.Join(dir, "queue"), filepath.Join(dir, "rejected"), 0, nil)
	index := os.Getenv(childIndexEnv)
	for i := range count {
		if _, err := q.Put([]byte("proc"), fmt.Appendf(nil, "%s-%d", index, i)); err != nil {
			return err
		}
	}
	return nil
}

type dirs struct {
	queue, rejected string
}

func newDirs(t *testing.T) dirs {
	t.Helper()
	root := t.TempDir()
	return dirs{queue: filepath.Join(root, "queue"), rejected: filepath.Join(root, "rejected")}
}

func (d dirs) open(maxBytes int64, log *slog.Logger) *queue.Queue {
	return queue.New(d.queue, d.rejected, maxBytes, log)
}

func mustPut(t *testing.T, q *queue.Queue, kind, payload string) string {
	t.Helper()
	id, err := q.Put([]byte(kind), []byte(payload))
	if err != nil {
		t.Fatalf("Put(%q): %v", payload, err)
	}
	return id
}

func mustNext(t *testing.T, q *queue.Queue, maxBytes int64) []queue.Record {
	t.Helper()
	recs, err := q.Next(maxBytes)
	if err != nil {
		t.Fatalf("Next(%d): %v", maxBytes, err)
	}
	return recs
}

func payloads(recs []queue.Record) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = string(r.Payload)
	}
	return out
}

func ids(recs []queue.Record) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.ID
	}
	return out
}

func mustStats(t *testing.T, q *queue.Queue) queue.Stats {
	t.Helper()
	s, err := q.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	return s
}

func TestEmptyQueue(t *testing.T) {
	t.Parallel()

	q := newDirs(t).open(0, nil)
	if recs := mustNext(t, q, 1<<20); len(recs) != 0 {
		t.Errorf("Next on an empty queue = %v", payloads(recs))
	}
	if s := mustStats(t, q); s != (queue.Stats{}) {
		t.Errorf("Stats = %+v, want zero", s)
	}
}

func TestOrderAndRoundTrip(t *testing.T) {
	t.Parallel()

	d := newDirs(t)
	q := d.open(0, nil)
	var want []string
	for i := range 50 {
		p := fmt.Sprintf("event-%02d", i)
		mustPut(t, q, `{"kind":"hook","agent":"claude"}`, p)
		want = append(want, p)
	}

	recs := mustNext(t, q, 1<<20)
	if got := payloads(recs); !slices.Equal(got, want) {
		t.Fatalf("payloads = %v, want %v", got, want)
	}
	for _, r := range recs {
		if string(r.Kind) != `{"kind":"hook","agent":"claude"}` {
			t.Fatalf("kind = %q", r.Kind)
		}
	}
	if !slices.IsSorted(ids(recs)) {
		t.Error("ids are not in ascending order")
	}

	info, err := os.Stat(filepath.Join(d.queue, recs[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("record mode = %o, want 600", info.Mode().Perm())
	}
}

func TestEmptyKindAndPayload(t *testing.T) {
	t.Parallel()

	q := newDirs(t).open(0, nil)
	mustPut(t, q, "", "")
	recs := mustNext(t, q, 1<<20)
	if len(recs) != 1 || len(recs[0].Kind) != 0 || len(recs[0].Payload) != 0 {
		t.Fatalf("records = %+v, want one empty record", recs)
	}
}

func TestNextBatchesBySize(t *testing.T) {
	t.Parallel()

	q := newDirs(t).open(0, nil)
	for i := range 10 {
		mustPut(t, q, "k", fmt.Sprintf("%095d", i))
	}
	size := mustNext(t, q, 1<<20)[0].Size // 4 + 1 + 95 = 100 bytes

	recs := mustNext(t, q, 3*size+size/2)
	if len(recs) != 3 {
		t.Fatalf("batch of 3.5 records' size holds %d records, want 3", len(recs))
	}
	var total int64
	for _, r := range recs {
		total += r.Size
	}
	if total > 3*size+size/2 {
		t.Errorf("batch is %d bytes, over the limit", total)
	}

	// Next does not remove; Ack does, and the next batch continues after it.
	if again := mustNext(t, q, 3*size); !slices.Equal(ids(again), ids(recs)) {
		t.Fatalf("Next before Ack = %v, want %v", ids(again), ids(recs))
	}
	if err := q.Ack(ids(recs)); err != nil {
		t.Fatal(err)
	}
	rest := mustNext(t, q, 1<<20)
	if len(rest) != 7 || string(rest[0].Payload) != fmt.Sprintf("%095d", 3) {
		t.Fatalf("after Ack = %v", payloads(rest))
	}
}

func TestNextReturnsOversizedRecordAlone(t *testing.T) {
	t.Parallel()

	q := newDirs(t).open(0, nil)
	big := strings.Repeat("x", 1000)
	mustPut(t, q, "k", big)
	mustPut(t, q, "k", "small")

	recs := mustNext(t, q, 100)
	if len(recs) != 1 || string(recs[0].Payload) != big {
		t.Fatalf("Next(100) = %d records, want the big one alone", len(recs))
	}
	if err := q.Ack(ids(recs)); err != nil {
		t.Fatal(err)
	}

	// An oversized record behind small ones waits for its own batch.
	mustPut(t, q, "k", big)
	recs = mustNext(t, q, 100)
	if got := payloads(recs); !slices.Equal(got, []string{"small"}) {
		t.Fatalf("Next(100) = %v, want [small]", got)
	}
}

func TestPutEvictsOldest(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	q := newDirs(t).open(1000, log) // ten records of 100 bytes
	for i := range 10 {
		mustPut(t, q, "k", fmt.Sprintf("%095d", i))
	}
	if s := mustStats(t, q); s.Queued != 10 || s.QueuedBytes != 1000 {
		t.Fatalf("at the limit: %+v", s)
	}
	if logs.Len() != 0 {
		t.Fatalf("logged at the limit: %s", logs.String())
	}

	// A 250-byte record needs three of the oldest to go.
	mustPut(t, q, "k", strings.Repeat("n", 245))
	s := mustStats(t, q)
	if s.Queued != 8 || s.QueuedBytes != 950 {
		t.Fatalf("after eviction: %+v, want 8 records of 950 bytes", s)
	}
	recs := mustNext(t, q, 1<<20)
	if string(recs[0].Payload) != fmt.Sprintf("%095d", 3) {
		t.Errorf("oldest left = %q, want record 3", recs[0].Payload)
	}
	if string(recs[len(recs)-1].Payload) != strings.Repeat("n", 245) {
		t.Error("the new record was evicted")
	}
	if !strings.Contains(logs.String(), "removed=3") {
		t.Errorf("log = %q, want the number removed", logs.String())
	}
}

func TestPutKeepsNewRecordOverLimit(t *testing.T) {
	t.Parallel()

	q := newDirs(t).open(100, nil)
	mustPut(t, q, "k", "old")
	mustPut(t, q, "k", strings.Repeat("x", 500))
	recs := mustNext(t, q, 1<<20)
	if len(recs) != 1 || len(recs[0].Payload) != 500 {
		t.Fatalf("records = %v, want the new big one alone", payloads(recs))
	}
}

func TestReject(t *testing.T) {
	t.Parallel()

	d := newDirs(t)
	q := d.open(0, nil)
	mustPut(t, q, "k", "good")
	bad := mustPut(t, q, "k", "bad")
	mustPut(t, q, "k", "later")

	if err := q.Reject([]string{bad}, "413 Payload Too Large"); err != nil {
		t.Fatal(err)
	}
	if got := payloads(mustNext(t, q, 1<<20)); !slices.Equal(got, []string{"good", "later"}) {
		t.Fatalf("queue after Reject = %v", got)
	}

	data, err := os.ReadFile(filepath.Join(d.rejected, bad))
	if err != nil {
		t.Fatalf("rejected record: %v", err)
	}
	if !bytes.HasSuffix(data, []byte("bad")) {
		t.Errorf("rejected record = %q", data)
	}
	reason, err := os.ReadFile(filepath.Join(d.rejected, bad+".reason"))
	if err != nil {
		t.Fatalf("reason: %v", err)
	}
	if string(reason) != "413 Payload Too Large\n" {
		t.Errorf("reason = %q", reason)
	}

	s := mustStats(t, q)
	want := queue.Stats{
		Queued: 2, QueuedBytes: int64(2*(4+1) + len("good") + len("later")),
		Rejected: 1, RejectedBytes: int64(len(data) + len(reason)),
	}
	if s != want {
		t.Errorf("Stats = %+v, want %+v", s, want)
	}

	// A record that is already gone is not an error.
	if err := q.Reject([]string{bad}, "again"); err != nil {
		t.Errorf("Reject of a gone record: %v", err)
	}
	if err := q.Ack([]string{bad}); err != nil {
		t.Errorf("Ack of a gone record: %v", err)
	}
}

func TestInvalidIDs(t *testing.T) {
	t.Parallel()

	q := newDirs(t).open(0, nil)
	for _, id := range []string{"", "../state.json", ".tmp", "1-2"} {
		if err := q.Ack([]string{id}); !errors.Is(err, queue.ErrInvalidID) {
			t.Errorf("Ack(%q) = %v, want ErrInvalidID", id, err)
		}
		if err := q.Reject([]string{id}, "r"); !errors.Is(err, queue.ErrInvalidID) {
			t.Errorf("Reject(%q) = %v, want ErrInvalidID", id, err)
		}
	}
}

func TestNextSkipsForeignAndRejectsCorrupt(t *testing.T) {
	t.Parallel()

	d := newDirs(t)
	q := d.open(0, nil)
	mustPut(t, q, "k", "ok")
	if err := os.WriteFile(filepath.Join(d.queue, ".tmp-leftover"), []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.queue, "README"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	corrupt := "0000000000000000001-0123456789abcdef"
	if err := os.WriteFile(filepath.Join(d.queue, corrupt), []byte{0, 0, 1, 0, 'x'}, 0o600); err != nil {
		t.Fatal(err)
	}

	if got := payloads(mustNext(t, q, 1<<20)); !slices.Equal(got, []string{"ok"}) {
		t.Fatalf("Next = %v, want [ok]", got)
	}
	if _, err := os.Stat(filepath.Join(d.rejected, corrupt)); err != nil {
		t.Errorf("corrupt record not moved to rejected: %v", err)
	}
}

func TestOpenUsesStatePaths(t *testing.T) {
	t.Parallel()

	paths := state.PathsIn(filepath.Join(t.TempDir(), "hottell"))
	q := queue.Open(paths, 0, nil)
	id := mustPut(t, q, "k", "p")
	if _, err := os.Stat(filepath.Join(paths.QueueDir(), id)); err != nil {
		t.Fatal(err)
	}
	if err := q.Reject([]string{id}, "r"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(paths.RejectedDir(), id)); err != nil {
		t.Fatal(err)
	}
}

func TestPutFromGoroutines(t *testing.T) {
	t.Parallel()

	q := newDirs(t).open(0, nil)
	const writers, each = 16, 25
	var wg sync.WaitGroup
	errs := make(chan error, writers*each)
	for w := range writers {
		wg.Go(func() {
			for i := range each {
				if _, err := q.Put([]byte("g"), fmt.Appendf(nil, "%d-%d", w, i)); err != nil {
					errs <- err
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	checkAllWritten(t, mustNext(t, q, 1<<30), writers, each)
}

func TestPutFromProcesses(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	const procs, each = 6, 30
	var wg sync.WaitGroup
	errs := make(chan error, procs)
	for p := range procs {
		wg.Go(func() {
			cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^$")
			cmd.Env = append(os.Environ(),
				childDirEnv+"="+root, childIndexEnv+"="+strconv.Itoa(p), childCountEnv+"="+strconv.Itoa(each))
			if out, err := cmd.CombinedOutput(); err != nil {
				errs <- fmt.Errorf("writer %d: %w: %s", p, err, out)
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	q := queue.New(filepath.Join(root, "queue"), filepath.Join(root, "rejected"), 0, nil)
	checkAllWritten(t, mustNext(t, q, 1<<30), procs, each)
}

// checkAllWritten checks that every writer's records are present once, in each
// writer's own order.
func checkAllWritten(t *testing.T, recs []queue.Record, writers, each int) {
	t.Helper()
	if len(recs) != writers*each {
		t.Fatalf("%d records, want %d", len(recs), writers*each)
	}
	next := make(map[string]int)
	for _, r := range recs {
		writer, i, ok := strings.Cut(string(r.Payload), "-")
		if !ok {
			t.Fatalf("payload %q", r.Payload)
		}
		if strconv.Itoa(next[writer]) != i {
			t.Fatalf("writer %s: record %s, want %d", writer, i, next[writer])
		}
		next[writer]++
	}
	if len(next) != writers {
		t.Fatalf("%d writers seen, want %d", len(next), writers)
	}
}

// BenchmarkPut measures what a hook spends on queueing one event.
func BenchmarkPut(b *testing.B) {
	root := b.TempDir()
	q := queue.New(filepath.Join(root, "queue"), filepath.Join(root, "rejected"), 0, nil)
	payload := bytes.Repeat([]byte("x"), 4<<10)
	for b.Loop() {
		if _, err := q.Put([]byte("hook"), payload); err != nil {
			b.Fatal(err)
		}
	}
}
