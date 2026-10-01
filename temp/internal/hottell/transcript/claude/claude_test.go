package claude_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/queue"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/transcript"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/transcript/claude"
)

const (
	project = "-work-demo"
	session = "00000000-0000-4000-8000-000000000001"
	mainRel = project + "/" + session + ".jsonl"
)

type env struct {
	t      *testing.T
	root   string
	state  string
	queue  *queue.Queue
	log    *bytes.Buffer
	reader *claude.Reader
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
		root:  filepath.Join(dir, "projects"),
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
	e.reader = claude.New(e.queue, transcript.NewOffsets(filepath.Join(e.state, "offsets")), log)
}

func (e *env) write(rel, data string) {
	e.t.Helper()
	path := filepath.Join(e.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) append(rel, data string) {
	e.t.Helper()
	f, err := os.OpenFile(filepath.Join(e.root, filepath.FromSlash(rel)), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(data); err != nil {
		e.t.Fatal(err)
	}
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

func rec(cwd, text string) string {
	return `{"type":"user","cwd":"` + cwd + `","text":"` + text + `"}` + "\n"
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
	first, second := rec("/work/demo", "one"), rec("/work/demo", "two")
	e.write(mainRel, first+second)
	e.scan(policy.Settings{})

	got := e.taken()
	equal(t, payloads(got), []string{strings.TrimSuffix(first, "\n"), strings.TrimSuffix(second, "\n")})
	for i, l := range got {
		if l.meta.Source != "transcript" || l.meta.Agent != "claude" || l.meta.SessionID != session ||
			l.meta.Kind != "main" || l.meta.Path != mainRel || l.meta.Line != int64(i+1) ||
			l.meta.Backfill || l.meta.ObservedUnixNano == 0 {
			t.Fatalf("line %d: meta %+v", i+1, l.meta)
		}
	}
	if got[0].meta.Offset != 0 || got[1].meta.Offset != int64(len(first)) {
		t.Fatalf("offsets %d, %d, want 0, %d", got[0].meta.Offset, got[1].meta.Offset, len(first))
	}

	third := `{"type":"assistant"}` + "\r\n"
	e.append(mainRel, third)
	e.scan(policy.Settings{})
	got = e.taken()
	equal(t, payloads(got), []string{`{"type":"assistant"}`})
	if m := got[0].meta; m.Line != 3 || m.Offset != int64(len(first+second)) {
		t.Fatalf("appended line meta %+v", m)
	}
}

func TestScanWaitsForIncompleteLastLine(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	first := rec("/work/demo", "one")
	e.write(mainRel, first+`{"type":"assistant","te`)
	e.scan(policy.Settings{})
	equal(t, payloads(e.taken()), []string{strings.TrimSuffix(first, "\n")})

	e.scan(policy.Settings{})
	equal(t, payloads(e.taken()), nil)

	e.append(mainRel, `xt":"done"}`+"\n")
	e.scan(policy.Settings{})
	got := e.taken()
	equal(t, payloads(got), []string{`{"type":"assistant","text":"done"}`})
	if m := got[0].meta; m.Line != 2 || m.Offset != int64(len(first)) {
		t.Fatalf("completed line meta %+v", m)
	}
}

func TestScanSubagent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	subRel := project + "/" + session + "/subagents/agent-a1b2c3.jsonl"
	e.write(subRel, rec("/work/demo", "sub"))
	// Files beside the transcripts are not transcript lines.
	e.write(project+"/"+session+"/subagents/agent-a1b2c3.meta.json", `{"agentType":"x"}`+"\n")
	e.write(project+"/"+session+"/tool-results/toolu_1.txt", "output\n")
	e.write(project+"/notes.txt", "x\n")
	e.scan(policy.Settings{})

	got := e.taken()
	if len(got) != 1 {
		t.Fatalf("queued %d lines, want 1: %q", len(got), payloads(got))
	}
	if m := got[0].meta; m.Kind != "subagent" || m.SessionID != session || m.Path != subRel || m.Line != 1 {
		t.Fatalf("subagent meta %+v", m)
	}
}

func TestScanDeniedFolderIsNotSent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	settings := policy.Settings{
		Home:    "/Users/dev",
		Folders: policy.Folders{Denied: []string{"~/secret/**"}},
	}
	deniedRel := "-Users-dev-secret-x/00000000-0000-4000-8000-000000000002.jsonl"
	deniedSub := "-Users-dev-secret-x/00000000-0000-4000-8000-000000000002/subagents/agent-1.jsonl"
	// The first line has no cwd: the session's cwd still denies it.
	e.write(deniedRel, `{"type":"summary"}`+"\n"+rec("/Users/dev/secret/x", "private"))
	e.write(deniedSub, rec("/Users/dev/secret/x", "private sub"))
	e.write(mainRel, rec("/Users/dev/open", "public"))
	e.scan(settings)
	equal(t, payloads(e.taken()), []string{strings.TrimSuffix(rec("/Users/dev/open", "public"), "\n")})

	// Lines appended later in the denied session are not sent either.
	e.append(deniedRel, `{"type":"assistant"}`+"\n")
	e.scan(settings)
	equal(t, payloads(e.taken()), nil)
}

func TestScanDisabledTranscriptsAreNotSent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	off := false
	settings := policy.Settings{}
	settings.Agents.Claude.Sources.Transcripts = &off
	e.write(mainRel, rec("/work/demo", "one"))
	e.scan(settings)
	equal(t, payloads(e.taken()), nil)
}

func TestScanWaitsForSessionCwd(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	settings := policy.Settings{Folders: policy.Folders{Denied: []string{"/secret/**"}}}
	summary := `{"type":"summary"}` + "\n"
	e.write(mainRel, summary)
	e.scan(settings)
	equal(t, payloads(e.taken()), nil)

	e.append(mainRel, rec("/work/demo", "one"))
	e.scan(settings)
	equal(t, payloads(e.taken()), []string{strings.TrimSuffix(summary, "\n"), strings.TrimSuffix(rec("/work/demo", "one"), "\n")})
}

func TestScanTenMegabyteLine(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	big := `{"type":"user","cwd":"/work/demo","text":"` + strings.Repeat("x", 10<<20) + `"}`
	e.write(mainRel, big+"\n"+rec("/work/demo", "after"))
	e.scan(policy.Settings{})
	got := e.taken()
	if len(got) != 2 || got[0].payload != big {
		t.Fatalf("queued %d lines, first %d bytes, want 2 lines, first %d bytes", len(got), len(got[0].payload), len(big))
	}
	if m := got[1].meta; m.Line != 2 || m.Offset != int64(len(big)+1) {
		t.Fatalf("line after the big one: meta %+v", m)
	}
}

func TestScanAgainDoesNotDuplicate(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	subRel := project + "/" + session + "/subagents/agent-1.jsonl"
	e.write(mainRel, rec("/work/demo", "one")+rec("/work/demo", "two"))
	e.write(subRel, rec("/work/demo", "sub"))
	e.scan(policy.Settings{})
	if got := e.taken(); len(got) != 3 {
		t.Fatalf("first scan queued %d lines, want 3", len(got))
	}

	e.scan(policy.Settings{})
	e.restart()
	e.scan(policy.Settings{})
	equal(t, payloads(e.taken()), nil)
}

func TestScanShorterFileRereadsWithoutResending(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.write(mainRel, rec("/work/demo", "one")+rec("/work/demo", "two")+rec("/work/demo", "three"))
	e.scan(policy.Settings{})
	if got := e.taken(); len(got) != 3 {
		t.Fatalf("first scan queued %d lines, want 3", len(got))
	}

	// Rewritten shorter: lines 1–3 were sent already, only line 4 is new.
	e.write(mainRel, rec("/work/demo", "a")+rec("/work/demo", "b"))
	e.scan(policy.Settings{})
	equal(t, payloads(e.taken()), nil)
	if !strings.Contains(e.log.String(), "level=WARN") || !strings.Contains(e.log.String(), mainRel) {
		t.Fatalf("no warning about the shorter file in the log: %q", e.log.String())
	}

	e.append(mainRel, rec("/work/demo", "c")+rec("/work/demo", "d"))
	e.scan(policy.Settings{})
	got := e.taken()
	equal(t, payloads(got), []string{strings.TrimSuffix(rec("/work/demo", "d"), "\n")})
	if got[0].meta.Line != 4 {
		t.Fatalf("new line numbered %d, want 4", got[0].meta.Line)
	}
}

func TestScanMissingRoot(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.scan(policy.Settings{})
	equal(t, payloads(e.taken()), nil)
}
