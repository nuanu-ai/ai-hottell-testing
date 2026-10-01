package sessions_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/sessions"
)

// longClaude is a Claude Code session: one reply from June and 250 from September 30.
func longClaude(t *testing.T) sessions.Roots {
	t.Helper()
	roots := sessions.Roots{ClaudeProjects: filepath.Join(t.TempDir(), "projects"), CodexHome: t.TempDir()}
	var b strings.Builder
	line := func(text string, at time.Time) {
		fmt.Fprintf(&b, `{"type":"user","message":{"role":"user","content":%q},"timestamp":%q,"sessionId":"long"}`+"\n",
			text, at.Format(time.RFC3339Nano))
	}
	line("старая реплика", time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC))
	base := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	for i := range 250 {
		line(fmt.Sprintf("реплика %d", i), base.Add(time.Duration(i)*time.Minute))
	}
	p := filepath.Join(roots.ClaudeProjects, "-w-long", "long.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return roots
}

func TestReadCountersCoverTheWholeSession(t *testing.T) {
	t.Parallel()
	roots, now := longClaude(t), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	in := sessions.ReadIn{ID: "long", Agent: "claude", From: "2026-09-29T00:00:00Z"}
	first, err := sessions.Read(roots, in, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 251 || first.Matched != 250 || first.OutOfPeriod != 1 || len(first.Events) != 100 || first.Next != 101 {
		t.Fatalf("page 1: total=%d matched=%d out=%d n=%d next=%d", first.Total, first.Matched, first.OutOfPeriod, len(first.Events), first.Next)
	}
	in.Offset = 201
	last, err := sessions.Read(roots, in, now)
	if err != nil {
		t.Fatal(err)
	}
	if last.Matched != 250 || last.OutOfPeriod != 1 || len(last.Events) != 50 || last.Next != 0 {
		t.Fatalf("last page: matched=%d out=%d n=%d next=%d", last.Matched, last.OutOfPeriod, len(last.Events), last.Next)
	}
	for _, ev := range append(first.Events, last.Events...) {
		if ev.TS.Before(time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)) {
			t.Fatalf("an event before the window: %+v", ev)
		}
	}
}

func TestReadPagesAndKinds(t *testing.T) {
	t.Parallel()
	roots, now := writeRoots(t), time.Now()
	rd, err := sessions.Read(roots, sessions.ReadIn{ID: "019e", Limit: 3, MaxText: 4}, now)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Total != 9 || len(rd.Events) != 3 || rd.Next != 3 || rd.Meta.Cwd != "/Users/a/3d" || rd.OutOfPeriod != 0 {
		t.Fatalf("read: total=%d n=%d next=%d out=%d", rd.Total, len(rd.Events), rd.Next, rd.OutOfPeriod)
	}
	if txt := rd.Events[1].Text; !strings.HasPrefix(txt, "<app…") {
		t.Fatalf("max_text 4: %q", txt)
	}
	rd, err = sessions.Read(roots, sessions.ReadIn{ID: "c1", Agent: "claude", Kinds: []string{"tool_call", "tool_result"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Matched != 2 || len(rd.Events) != 2 || rd.Events[0].Tool != "Bash" {
		t.Fatalf("read kinds: %+v", rd.Events)
	}
	if _, err := sessions.Read(roots, sessions.ReadIn{ID: "c1", From: "yesterday"}, now); err == nil {
		t.Fatal("an unreadable from is an error")
	}
}
