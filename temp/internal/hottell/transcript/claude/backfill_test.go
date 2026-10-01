package claude_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
)

// backfillOn returns settings that ask for the history.
func backfillOn() policy.Settings { return policy.Settings{BackfillHistory: true} }

func TestFirstScanKeepsHistoryAndReadsNewLines(t *testing.T) {
	t.Parallel()
	e := newBareEnv(t)
	one, two := rec("/work/demo", "one"), rec("/work/demo", "two")
	// The incomplete last line is not history: it is finished after the first scan.
	e.write(mainRel, one+two+`{"type":"assistant","te`)
	e.scan(backfillOn())
	equal(t, payloads(e.taken()), nil)

	e.append(mainRel, `xt":"done"}`+"\n")
	newRel := project + "/00000000-0000-4000-8000-000000000009.jsonl"
	e.write(newRel, rec("/work/demo", "new"))
	e.scan(backfillOn())
	got := e.taken()
	equal(t, payloads(got), []string{`{"type":"assistant","text":"done"}`, strings.TrimSuffix(rec("/work/demo", "new"), "\n")})
	for _, l := range got {
		if l.meta.Backfill {
			t.Fatalf("line after the first scan marked as history: %+v", l.meta)
		}
	}
	if m := got[0].meta; m.Path != mainRel || m.Line != 3 || m.Offset != int64(len(one+two)) {
		t.Fatalf("appended line meta %+v", m)
	}
	if m := got[1].meta; m.Path != newRel || m.Line != 1 || m.Offset != 0 {
		t.Fatalf("new file meta %+v", m)
	}

	// Without the flag the history stays; the new file has none.
	if e.backfill(policy.Settings{}, 1<<30) {
		t.Fatal("Backfill with the flag off reported history left")
	}
	equal(t, payloads(e.taken()), nil)
	e.backfill(backfillOn(), 1<<30)
	got = e.taken()
	equal(t, payloads(got), []string{strings.TrimSuffix(one, "\n"), strings.TrimSuffix(two, "\n")})
	if got[0].meta.Path != mainRel {
		t.Fatalf("history of %s, want %s", got[0].meta.Path, mainRel)
	}
}

func TestBackfillSendsHistoryOnce(t *testing.T) {
	t.Parallel()
	e := newBareEnv(t)
	one, two := rec("/work/demo", "one"), rec("/work/demo", "two")
	subRel := project + "/" + session + "/subagents/agent-1.jsonl"
	e.write(mainRel, one+two)
	e.write(subRel, rec("/work/demo", "sub"))
	e.age(subRel, time.Hour)
	e.scan(policy.Settings{})
	equal(t, payloads(e.taken()), nil)

	if e.backfill(backfillOn(), 1<<30) {
		t.Fatal("Backfill reported history left after sending all of it")
	}
	got := e.taken()
	equal(t, payloads(got), []string{
		strings.TrimSuffix(one, "\n"), strings.TrimSuffix(two, "\n"), strings.TrimSuffix(rec("/work/demo", "sub"), "\n"),
	})
	for i, want := range []struct {
		path, kind   string
		line, offset int64
	}{{mainRel, "main", 1, 0}, {mainRel, "main", 2, int64(len(one))}, {subRel, "subagent", 1, 0}} {
		m := got[i].meta
		if !m.Backfill || m.Path != want.path || m.Kind != want.kind || m.Line != want.line ||
			m.Offset != want.offset || m.SessionID != session || m.ObservedUnixNano == 0 {
			t.Fatalf("history line %d: meta %+v, want %+v", i+1, m, want)
		}
	}

	e.backfill(backfillOn(), 1<<30)
	e.scan(backfillOn())
	e.restart()
	e.scan(backfillOn())
	e.backfill(backfillOn(), 1<<30)
	equal(t, payloads(e.taken()), nil)
}

func TestBackfillStopsAndGoesOnNewestFirst(t *testing.T) {
	t.Parallel()
	e := newBareEnv(t)
	oldRel := project + "/00000000-0000-4000-8000-000000000002.jsonl"
	a1, a2, a3 := rec("/work/demo", "a1"), rec("/work/demo", "a2"), rec("/work/demo", "a3")
	b1, b2, b3 := rec("/work/demo", "b1"), rec("/work/demo", "b2"), rec("/work/demo", "b3")
	e.write(oldRel, a1+a2+a3)
	e.write(mainRel, b1+b2+b3)
	e.age(oldRel, 2*time.Hour)
	e.age(mainRel, time.Hour)
	e.scan(policy.Settings{})

	// The pass stops before the line that would take it past its limit.
	if !e.backfill(backfillOn(), int64(len(b1+b2)+1)) {
		t.Fatal("Backfill stopped at its limit without reporting history left")
	}
	equal(t, payloads(e.taken()), []string{strings.TrimSuffix(b1, "\n"), strings.TrimSuffix(b2, "\n")})

	// The flag is turned off: nothing more goes out.
	if e.backfill(policy.Settings{}, 1<<30) {
		t.Fatal("Backfill with the flag off reported history left")
	}
	equal(t, payloads(e.taken()), nil)

	// Turned on again after a restart, the backfill goes on where it stopped; a line
	// longer than the limit still goes out when it is the first of its pass.
	e.restart()
	if !e.backfill(backfillOn(), 1) {
		t.Fatal("Backfill stopped at its limit without reporting history left")
	}
	got := e.taken()
	equal(t, payloads(got), []string{strings.TrimSuffix(b3, "\n")})
	if m := got[0].meta; m.Line != 3 || m.Offset != int64(len(b1+b2)) {
		t.Fatalf("resumed line meta %+v", m)
	}
	if e.backfill(backfillOn(), 1<<30) {
		t.Fatal("Backfill reported history left after sending all of it")
	}
	equal(t, payloads(e.taken()), []string{
		strings.TrimSuffix(a1, "\n"), strings.TrimSuffix(a2, "\n"), strings.TrimSuffix(a3, "\n"),
	})
}

func TestBackfillDeniedFolderIsNotSent(t *testing.T) {
	t.Parallel()
	e := newBareEnv(t)
	settings := policy.Settings{
		Home:            "/Users/dev",
		Folders:         policy.Folders{Denied: []string{"~/secret/**"}},
		BackfillHistory: true,
	}
	deniedRel := "-Users-dev-secret-x/00000000-0000-4000-8000-000000000002.jsonl"
	// The first line has no cwd: the session's cwd still denies it.
	e.write(deniedRel, `{"type":"summary"}`+"\n"+rec("/Users/dev/secret/x", "private"))
	e.write(mainRel, rec("/Users/dev/open", "public"))
	e.scan(settings)
	e.backfill(settings, 1<<30)
	equal(t, payloads(e.taken()), []string{strings.TrimSuffix(rec("/Users/dev/open", "public"), "\n")})

	// A denied line is skipped for good: allowing the folder later does not send it.
	e.backfill(backfillOn(), 1<<30)
	equal(t, payloads(e.taken()), nil)
}

func TestBackfillDisabledTranscriptsAreNotSent(t *testing.T) {
	t.Parallel()
	e := newBareEnv(t)
	off := false
	settings := backfillOn()
	settings.Agents.Claude.Sources.Transcripts = &off
	e.write(mainRel, rec("/work/demo", "one"))
	e.scan(settings)
	e.backfill(settings, 1<<30)
	equal(t, payloads(e.taken()), nil)
}

func TestFirstScanFailureKeepsFileAsHistory(t *testing.T) {
	t.Parallel()
	e := newBareEnv(t)
	brokenRel := project + "/00000000-0000-4000-8000-000000000002.jsonl"
	old := rec("/work/demo", "old")
	e.write(brokenRel, old)
	e.write(mainRel, rec("/work/demo", "one"))
	path := filepath.Join(e.root, filepath.FromSlash(brokenRel))
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.reader.Scan(e.root, policy.Settings{}); err == nil {
		t.Fatal("Scan of an unreadable transcript returned no error")
	}

	// The file that failed does not keep the others from being read live.
	two := rec("/work/demo", "two")
	e.append(mainRel, two)
	_ = e.reader.Scan(e.root, policy.Settings{})
	equal(t, payloads(e.taken()), []string{strings.TrimSuffix(two, "\n")})

	// Readable again, the file is still history: not sent by the scan, sent by the
	// backfill.
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	e.scan(policy.Settings{})
	equal(t, payloads(e.taken()), nil)
	e.backfill(backfillOn(), 1<<30)
	got := e.taken()
	// The other file was written to later, so its history goes first.
	equal(t, payloads(got), []string{strings.TrimSuffix(rec("/work/demo", "one"), "\n"), strings.TrimSuffix(old, "\n")})
	for _, l := range got {
		if !l.meta.Backfill {
			t.Fatalf("history line not marked: %+v", l.meta)
		}
	}
}
