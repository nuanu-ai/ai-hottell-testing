package codex_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/queue"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/transcript"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/transcript/codex"
)

const (
	thread   = "00000000-0000-4000-8000-000000000001"
	child    = "00000000-0000-4000-8000-000000000002"
	mainRel  = "sessions/2026/09/30/rollout-2026-09-30T10-00-00-" + thread + ".jsonl"
	childRel = "sessions/2026/09/30/rollout-2026-09-30T10-05-00-" + child + ".jsonl"
	archRel  = "archived_sessions/rollout-2026-09-30T10-00-00-" + thread + ".jsonl.zst"
)

type env struct {
	t      *testing.T
	root   string
	state  string
	queue  *queue.Queue
	log    *bytes.Buffer
	reader *codex.Reader
}

// newEnv returns an environment whose first scan is done: every file written in it is
// new, not history.
func newEnv(t *testing.T) *env {
	t.Helper()
	e := newBareEnv(t)
	e.scan(policy.Settings{})
	return e
}

// newBareEnv returns an environment before the first scan: the files written before
// it are history.
func newBareEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	e := &env{
		t:     t,
		root:  filepath.Join(dir, "codex"),
		state: filepath.Join(dir, "state"),
		log:   &bytes.Buffer{},
	}
	e.queue = queue.New(filepath.Join(e.state, "queue"), filepath.Join(e.state, "rejected"), 0, nil)
	e.restart()
	return e
}

// restart replaces the reader, as a new daemon process does.
func (e *env) restart() {
	log := slog.New(slog.NewTextHandler(e.log, nil))
	e.reader = codex.New(e.queue, transcript.NewOffsets(filepath.Join(e.state, "offsets")), log)
}

func (e *env) path(rel string) string {
	return filepath.Join(e.root, filepath.FromSlash(rel))
}

func (e *env) write(rel, data string) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(e.path(rel)), 0o700); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(e.path(rel), []byte(data), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) append(rel, data string) {
	e.t.Helper()
	f, err := os.OpenFile(e.path(rel), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(data); err != nil {
		e.t.Fatal(err)
	}
}

// compress moves the rollout at rel to zstRel compressed, as Codex does with cold
// files.
func (e *env) compress(rel, zstRel string) {
	e.t.Helper()
	data, err := os.ReadFile(e.path(rel))
	if err != nil {
		e.t.Fatal(err)
	}
	e.writeZst(zstRel, string(data))
	if err := os.Remove(e.path(rel)); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) writeZst(rel, data string) {
	e.t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		e.t.Fatal(err)
	}
	e.write(rel, string(enc.EncodeAll([]byte(data), nil)))
}

func (e *env) scan(settings policy.Settings) {
	e.t.Helper()
	if err := e.reader.Scan(e.root, settings); err != nil {
		e.t.Fatalf("Scan: %v", err)
	}
}

func (e *env) backfill(settings policy.Settings, limit int64) bool {
	e.t.Helper()
	more, err := e.reader.Backfill(e.root, settings, limit)
	if err != nil {
		e.t.Fatalf("Backfill: %v", err)
	}
	return more
}

// age sets the modification time of the file at rel to d before now.
func (e *env) age(rel string, d time.Duration) {
	e.t.Helper()
	at := time.Now().Add(-d)
	if err := os.Chtimes(filepath.Join(e.root, filepath.FromSlash(rel)), at, at); err != nil {
		e.t.Fatal(err)
	}
}

type line struct {
	meta    transcript.Meta
	payload string
}

// taken returns the records queued since the last call and acknowledges them.
func (e *env) taken() []line {
	e.t.Helper()
	recs, err := e.queue.Next(1 << 40)
	if err != nil {
		e.t.Fatal(err)
	}
	out := make([]line, 0, len(recs))
	ids := make([]string, 0, len(recs))
	for _, r := range recs {
		var m transcript.Meta
		if err := json.Unmarshal(r.Kind, &m); err != nil {
			e.t.Fatalf("decode kind %q: %v", r.Kind, err)
		}
		out = append(out, line{meta: m, payload: string(r.Payload)})
		ids = append(ids, r.ID)
	}
	if err := e.queue.Ack(ids); err != nil {
		e.t.Fatal(err)
	}
	return out
}

// sessionMeta is the first line of a main rollout.
func sessionMeta(id, cwd string) string {
	return `{"timestamp":"2026-09-30T10:00:00.000Z","ordinal":0,"type":"session_meta","payload":{"id":"` + id +
		`","cwd":"` + cwd + `","originator":"codex_cli_rs","cli_version":"0.159.0"}}` + "\n"
}

// event is a rollout line with the given ordinal.
func event(ordinal int, text string) string {
	return `{"timestamp":"2026-09-30T10:00:01.000Z","ordinal":` + strconv.Itoa(ordinal) +
		`,"type":"event_msg","payload":{"type":"agent_message","message":"` + text + `"}}` + "\n"
}

func turnContext(ordinal int, cwd string) string {
	return `{"timestamp":"2026-09-30T10:00:02.000Z","ordinal":` + strconv.Itoa(ordinal) +
		`,"type":"turn_context","payload":{"cwd":"` + cwd + `","model":"gpt-5.5-codex"}}` + "\n"
}

func trim(lines ...string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, strings.TrimSuffix(l, "\n"))
	}
	return out
}

func payloads(lines []line) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.payload)
	}
	return out
}

func equal(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("queued lines\n got %q\nwant %q", got, want)
	}
}

func TestScanAppendedLines(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	head, first := sessionMeta(thread, "/work/demo"), event(1, "one")
	e.write(mainRel, head+first)
	e.scan(policy.Settings{})

	got := e.taken()
	equal(t, payloads(got), trim(head, first))
	for i, l := range got {
		if l.meta.Source != "transcript" || l.meta.Agent != "codex" || l.meta.SessionID != thread ||
			l.meta.Kind != "main" || l.meta.Path != mainRel || l.meta.Line != int64(i+1) ||
			l.meta.Backfill || l.meta.ObservedUnixNano == 0 {
			t.Fatalf("line %d: meta %+v", i+1, l.meta)
		}
	}
	if got[0].meta.Offset != 0 || got[1].meta.Offset != int64(len(head)) {
		t.Fatalf("offsets %d, %d, want 0, %d", got[0].meta.Offset, got[1].meta.Offset, len(head))
	}

	second := event(2, "two")
	e.append(mainRel, second)
	e.scan(policy.Settings{})
	got = e.taken()
	equal(t, payloads(got), trim(second))
	if m := got[0].meta; m.Line != 3 || m.Offset != int64(len(head+first)) {
		t.Fatalf("appended line meta %+v", m)
	}
}

func TestScanWaitsForIncompleteLines(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	head := sessionMeta(thread, "/work/demo")
	// Codex is still writing the first line: nothing is known about the rollout yet.
	e.write(mainRel, head[:20])
	e.scan(policy.Settings{})
	equal(t, payloads(e.taken()), nil)

	first := event(1, "one")
	e.write(mainRel, head+first+`{"timestamp":"2026-09-30T10:00:03.000Z","ord`)
	e.scan(policy.Settings{})
	equal(t, payloads(e.taken()), trim(head, first))

	e.append(mainRel, `inal":2,"type":"event_msg","payload":{}}`+"\n")
	e.scan(policy.Settings{})
	got := e.taken()
	equal(t, payloads(got), []string{`{"timestamp":"2026-09-30T10:00:03.000Z","ordinal":2,"type":"event_msg","payload":{}}`})
	if m := got[0].meta; m.Line != 3 || m.Offset != int64(len(head+first)) {
		t.Fatalf("completed line meta %+v", m)
	}
}

func TestScanCompressedRollout(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	head, first := sessionMeta(thread, "/work/demo"), event(1, "one")
	e.writeZst(archRel, head+first)
	// Files beside the rollouts are not transcript lines.
	e.write("sessions/2026/09/30/notes.jsonl", `{"x":1}`+"\n")
	e.write("history.jsonl", `{"x":1}`+"\n")
	e.write("archived_sessions/rollout-short.jsonl", `{"x":1}`+"\n")
	e.scan(policy.Settings{})

	got := e.taken()
	equal(t, payloads(got), trim(head, first))
	if m := got[1].meta; m.Path != archRel || m.SessionID != thread || m.Line != 2 || m.Offset != int64(len(head)) {
		t.Fatalf("compressed line meta %+v", m)
	}

	e.scan(policy.Settings{})
	e.restart()
	e.scan(policy.Settings{})
	equal(t, payloads(e.taken()), nil)
}

func TestScanRolloutCompressedAfterSending(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	head, first, second := sessionMeta(thread, "/work/demo"), event(1, "one"), event(2, "two")
	e.write(mainRel, head+first)
	e.scan(policy.Settings{})
	if got := e.taken(); len(got) != 2 {
		t.Fatalf("first scan queued %d lines, want 2", len(got))
	}

	// Codex appends a line and archives the rollout compressed before the next scan:
	// only the new line goes out, numbered as in the unpacked file.
	e.append(mainRel, second)
	e.compress(mainRel, archRel)
	e.scan(policy.Settings{})
	got := e.taken()
	equal(t, payloads(got), trim(second))
	if m := got[0].meta; m.Path != archRel || m.Line != 3 || m.Offset != int64(len(head+first)) {
		t.Fatalf("line of the compressed rollout: meta %+v", m)
	}

	e.restart()
	e.scan(policy.Settings{})
	equal(t, payloads(e.taken()), nil)
}

func TestScanSubagentSkipsParentCopy(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	settings := policy.Settings{Folders: policy.Folders{Denied: []string{"/parent-only/**"}}}
	// The child's session_meta, the parent's records copied as ordinals 1–3, and the
	// child's own records from subagent_history_start_ordinal 4 on.
	head := `{"timestamp":"2026-09-30T10:05:00.000Z","ordinal":0,"type":"session_meta","payload":{"id":"` + child +
		`","cwd":"/work/demo","thread_source":"subagent","parent_thread_id":"` + thread +
		`","history_mode":"paginated","subagent_history_start_ordinal":4}}` + "\n"
	copied := event(1, "parent one") + turnContext(2, "/parent-only/x") + event(3, "parent two")
	own := event(4, "child one") + event(5, "child two")
	e.write(childRel, head+copied+own)
	e.scan(settings)

	got := e.taken()
	equal(t, payloads(got), trim(head, event(4, "child one"), event(5, "child two")))
	for _, l := range got {
		if l.meta.Kind != "subagent" || l.meta.SessionID != child || l.meta.Path != childRel {
			t.Fatalf("subagent meta %+v", l.meta)
		}
	}
	if got[1].meta.Line != 5 || got[2].meta.Line != 6 {
		t.Fatalf("own lines numbered %d, %d, want 5, 6", got[1].meta.Line, got[2].meta.Line)
	}

	third := event(6, "child three")
	e.append(childRel, third)
	e.scan(settings)
	equal(t, payloads(e.taken()), trim(third))
}

func TestScanSubagentWithoutBoundaryIsSentWhole(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	head := `{"timestamp":"2026-09-30T10:05:00.000Z","type":"session_meta","payload":{"id":"` + child +
		`","cwd":"/work/demo","thread_source":"subagent"}}` + "\n"
	rest := `{"timestamp":"2026-09-30T10:05:01.000Z","type":"event_msg","payload":{}}` + "\n"
	e.write(childRel, head+rest)
	e.scan(policy.Settings{})
	equal(t, payloads(e.taken()), trim(head, rest))
}

func TestScanDeniedFolderIsNotSent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	settings := policy.Settings{
		Home:    "/Users/dev",
		Folders: policy.Folders{Denied: []string{"~/secret/**"}},
	}
	e.write(childRel, sessionMeta(child, "/Users/dev/secret/x")+event(1, "private"))
	open := sessionMeta(thread, "/Users/dev/open") + event(1, "public")
	e.write(mainRel, open)
	e.scan(settings)
	equal(t, payloads(e.taken()), trim(sessionMeta(thread, "/Users/dev/open"), event(1, "public")))

	// Lines appended later in the denied session are not sent either.
	e.append(childRel, event(2, "private too"))
	e.scan(settings)
	equal(t, payloads(e.taken()), nil)

	// A turn moves the open session into the denied folder: from its turn_context on,
	// its lines stay home, also after a restart.
	moved := turnContext(2, "/Users/dev/secret/y") + event(3, "private"+" later")
	e.append(mainRel, moved)
	e.scan(settings)
	equal(t, payloads(e.taken()), nil)
	e.restart()
	e.append(mainRel, event(4, "still private"))
	e.scan(settings)
	equal(t, payloads(e.taken()), nil)

	back := turnContext(5, "/Users/dev/open")
	e.append(mainRel, back)
	e.scan(settings)
	equal(t, payloads(e.taken()), trim(back))
}

func TestScanDisabledTranscriptsAreNotSent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	off := false
	settings := policy.Settings{}
	settings.Agents.Codex.Sources.Transcripts = &off
	e.write(mainRel, sessionMeta(thread, "/work/demo")+event(1, "one"))
	e.scan(settings)
	equal(t, payloads(e.taken()), nil)
}

func TestScanAgainDoesNotDuplicate(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.write(mainRel, sessionMeta(thread, "/work/demo")+event(1, "one"))
	e.write(childRel, sessionMeta(child, "/work/demo")+event(1, "sub"))
	e.scan(policy.Settings{})
	if got := e.taken(); len(got) != 4 {
		t.Fatalf("first scan queued %d lines, want 4", len(got))
	}

	e.scan(policy.Settings{})
	e.restart()
	e.scan(policy.Settings{})
	equal(t, payloads(e.taken()), nil)

	// Archived as is, without compression: the same thread, nothing new.
	if err := os.MkdirAll(e.path("archived_sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	arch := "archived_sessions/" + filepath.Base(mainRel)
	if err := os.Rename(e.path(mainRel), e.path(arch)); err != nil {
		t.Fatal(err)
	}
	e.scan(policy.Settings{})
	equal(t, payloads(e.taken()), nil)
}

func TestScanShorterFileRereadsWithoutResending(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	head := sessionMeta(thread, "/work/demo")
	e.write(mainRel, head+event(1, "one")+event(2, "two"))
	e.scan(policy.Settings{})
	if got := e.taken(); len(got) != 3 {
		t.Fatalf("first scan queued %d lines, want 3", len(got))
	}

	e.write(mainRel, head+event(1, "a"))
	e.scan(policy.Settings{})
	equal(t, payloads(e.taken()), nil)
	if !strings.Contains(e.log.String(), "level=WARN") || !strings.Contains(e.log.String(), mainRel) {
		t.Fatalf("no warning about the shorter file in the log: %q", e.log.String())
	}

	e.append(mainRel, event(2, "b")+event(3, "c"))
	e.scan(policy.Settings{})
	got := e.taken()
	equal(t, payloads(got), trim(event(3, "c")))
	if got[0].meta.Line != 4 {
		t.Fatalf("new line numbered %d, want 4", got[0].meta.Line)
	}
}

func TestScanShorterCompressedFile(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	head := sessionMeta(thread, "/work/demo")
	e.write(mainRel, head+event(1, "one")+event(2, "two"))
	e.scan(policy.Settings{})
	if got := e.taken(); len(got) != 3 {
		t.Fatalf("first scan queued %d lines, want 3", len(got))
	}

	// Compressed shorter than the part already read: its lines were sent already.
	e.writeZst(archRel, head+event(1, "a"))
	if err := os.Remove(e.path(mainRel)); err != nil {
		t.Fatal(err)
	}
	e.scan(policy.Settings{})
	equal(t, payloads(e.taken()), nil)
	if !strings.Contains(e.log.String(), "level=WARN") || !strings.Contains(e.log.String(), archRel) {
		t.Fatalf("no warning about the shorter file in the log: %q", e.log.String())
	}
	e.restart()
	e.scan(policy.Settings{})
	equal(t, payloads(e.taken()), nil)
}

func TestScanMissingRoot(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.scan(policy.Settings{})
	equal(t, payloads(e.taken()), nil)
}
