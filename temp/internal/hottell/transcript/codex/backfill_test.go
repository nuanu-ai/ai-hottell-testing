package codex_test

import (
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
)

// backfillOn returns settings that ask for the history.
func backfillOn() policy.Settings { return policy.Settings{BackfillHistory: true} }

func TestFirstScanKeepsHistoryAndReadsNewLines(t *testing.T) {
	t.Parallel()
	e := newBareEnv(t)
	head, one := sessionMeta(thread, "/work/demo"), event(1, "one")
	e.write(mainRel, head+one)
	e.scan(backfillOn())
	equal(t, payloads(e.taken()), nil)

	two := event(2, "two")
	e.append(mainRel, two)
	childHead := sessionMeta(child, "/work/demo")
	e.write(childRel, childHead)
	e.scan(backfillOn())
	got := e.taken()
	equal(t, payloads(got), trim(two, childHead))
	for _, l := range got {
		if l.meta.Backfill {
			t.Fatalf("line after the first scan marked as history: %+v", l.meta)
		}
	}
	if m := got[0].meta; m.Line != 3 || m.Offset != int64(len(head+one)) {
		t.Fatalf("appended line meta %+v", m)
	}
	if m := got[1].meta; m.SessionID != child || m.Line != 1 {
		t.Fatalf("new rollout meta %+v", m)
	}

	if e.backfill(policy.Settings{}, 1<<30) {
		t.Fatal("Backfill with the flag off reported history left")
	}
	equal(t, payloads(e.taken()), nil)
	e.backfill(backfillOn(), 1<<30)
	got = e.taken()
	equal(t, payloads(got), trim(head, one))
	for i, l := range got {
		if m := l.meta; !m.Backfill || m.SessionID != thread || m.Path != mainRel || m.Line != int64(i+1) {
			t.Fatalf("history line %d: meta %+v", i+1, m)
		}
	}
}

func TestBackfillStopsAndGoesOnAfterCompression(t *testing.T) {
	t.Parallel()
	e := newBareEnv(t)
	head, one, two := sessionMeta(thread, "/work/demo"), event(1, "one"), event(2, "two")
	childHead, c1 := sessionMeta(child, "/work/demo"), event(1, "c1")
	e.write(mainRel, head+one+two)
	e.write(childRel, childHead+c1)
	e.age(mainRel, time.Hour)
	e.scan(policy.Settings{})

	// The newer rollout goes first; the pass stops before the line past its limit.
	if !e.backfill(backfillOn(), int64(len(childHead+c1+head))) {
		t.Fatal("Backfill stopped at its limit without reporting history left")
	}
	equal(t, payloads(e.taken()), trim(childHead, c1, head))

	e.backfill(policy.Settings{}, 1<<30)
	equal(t, payloads(e.taken()), nil)

	// Codex archives and compresses the rollout; the backfill goes on by thread.
	e.compress(mainRel, archRel)
	e.restart()
	e.scan(backfillOn())
	equal(t, payloads(e.taken()), nil)
	if e.backfill(backfillOn(), 1<<30) {
		t.Fatal("Backfill reported history left after sending all of it")
	}
	got := e.taken()
	equal(t, payloads(got), trim(one, two))
	if m := got[0].meta; !m.Backfill || m.Path != archRel || m.Line != 2 || m.Offset != int64(len(head)) {
		t.Fatalf("resumed line meta %+v", m)
	}

	e.backfill(backfillOn(), 1<<30)
	e.scan(backfillOn())
	equal(t, payloads(e.taken()), nil)
}

func TestBackfillSubagentSkipsParentCopy(t *testing.T) {
	t.Parallel()
	e := newBareEnv(t)
	head := `{"timestamp":"2026-09-30T10:05:00.000Z","ordinal":0,"type":"session_meta","payload":{"id":"` + child +
		`","cwd":"/work/demo","thread_source":"subagent","subagent_history_start_ordinal":3}}` + "\n"
	own := event(3, "own")
	e.write(childRel, head+event(1, "parent one")+event(2, "parent two")+own)
	e.scan(policy.Settings{})
	e.backfill(backfillOn(), 1<<30)
	got := e.taken()
	equal(t, payloads(got), trim(head, own))
	if m := got[1].meta; m.Kind != "subagent" || m.Line != 4 || !m.Backfill {
		t.Fatalf("own line meta %+v", m)
	}
}

func TestBackfillDeniedFolderIsNotSent(t *testing.T) {
	t.Parallel()
	e := newBareEnv(t)
	settings := policy.Settings{
		Home:            "/Users/dev",
		Folders:         policy.Folders{Denied: []string{"~/secret/**"}},
		BackfillHistory: true,
	}
	head, one := sessionMeta(thread, "/Users/dev/open"), event(1, "public")
	// The turn moves the session into a denied folder: its lines stay home.
	e.write(mainRel, head+one+turnContext(2, "/Users/dev/secret/x")+event(3, "private"))
	e.write(childRel, sessionMeta(child, "/Users/dev/secret/y")+event(1, "private"))
	e.scan(settings)
	e.backfill(settings, 1<<30)
	equal(t, payloads(e.taken()), trim(head, one))

	// A denied line is skipped for good: allowing the folder later does not send it.
	e.backfill(backfillOn(), 1<<30)
	equal(t, payloads(e.taken()), nil)
}

func TestFirstScanFailureKeepsRolloutAsHistory(t *testing.T) {
	t.Parallel()
	e := newBareEnv(t)
	head, one := sessionMeta(thread, "/work/demo"), event(1, "one")
	e.write(archRel, "not zstd")
	childHead := sessionMeta(child, "/work/demo")
	e.write(childRel, childHead)
	if err := e.reader.Scan(e.root, policy.Settings{}); err == nil {
		t.Fatal("Scan of a corrupt rollout returned no error")
	}

	// The rollout that failed does not keep the others from being read live.
	c1 := event(1, "c1")
	e.append(childRel, c1)
	_ = e.reader.Scan(e.root, policy.Settings{})
	equal(t, payloads(e.taken()), trim(c1))

	// Readable again, the rollout is still history: not sent by the scan, sent by the
	// backfill.
	e.writeZst(archRel, head+one)
	e.scan(policy.Settings{})
	equal(t, payloads(e.taken()), nil)
	e.backfill(backfillOn(), 1<<30)
	got := e.taken()
	equal(t, payloads(got), trim(head, one, childHead))
	for _, l := range got {
		if !l.meta.Backfill {
			t.Fatalf("history line not marked: %+v", l.meta)
		}
	}
}
