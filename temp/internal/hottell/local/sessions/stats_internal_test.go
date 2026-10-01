package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

// TestStatsPageByBytes: a page of a period stops at the byte cap of the transcripts it
// parses, yet always reads one session; the coverage and the note count what was read.
func TestStatsPageByBytes(t *testing.T) {
	t.Parallel()
	roots := Roots{ClaudeProjects: filepath.Join(t.TempDir(), "projects")}
	const line = `{"type":"user","message":{"role":"user","content":"реплика"},"timestamp":"2026-09-30T08:00:00Z"}` + "\n"
	base := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	for i, id := range []string{"a", "b", "c"} { // a is the newest
		p := filepath.Join(roots.ClaudeProjects, "-w", id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
		mt := base.Add(-time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	size, now := int64(len(line)), base.Add(time.Hour)
	page := func(f Filter, capBytes int64) (*Stats, string) {
		t.Helper()
		st, note, err := statsOf(roots, StatsIn{Filter: f}, now, capBytes)
		if err != nil {
			t.Fatal(err)
		}
		return st, note
	}
	ids := func(st *Stats) string {
		var s string
		for _, b := range st.PerSession {
			s += b.ID
		}
		return s
	}

	st, note := page(Filter{}, 2*size)
	if c := st.Coverage; c.SessionsFound != 3 || c.SessionsRead != 2 || c.NextOffset != 2 || st.Sessions != 2 || ids(st) != "ab" {
		t.Fatalf("two fit under the cap: %+v %q", c, ids(st))
	}
	if want := "sessions 1–2 of 3 read; next page: offset=2"; note != want {
		t.Fatalf("note %q, want %q", note, want)
	}
	st, note = page(Filter{Offset: 2}, 2*size)
	if c := st.Coverage; c.SessionsRead != 1 || c.NextOffset != 0 || note != "" || ids(st) != "c" {
		t.Fatalf("the last page: %+v %q", c, note)
	}
	st, note = page(Filter{}, 1)
	if c := st.Coverage; c.SessionsRead != 1 || c.NextOffset != 1 || ids(st) != "a" {
		t.Fatalf("one session above the cap is read anyway: %+v", c)
	}
	if want := "sessions 1–1 of 3 read; next page: offset=1"; note != want {
		t.Fatalf("note %q, want %q", note, want)
	}
	st, _ = page(Filter{Offset: 1}, 1<<40)
	if c := st.Coverage; c.SessionsRead != 2 || c.NextOffset != 0 || ids(st) != "bc" {
		t.Fatalf("a large cap reads the rest: %+v", c)
	}
	st, _ = page(Filter{Limit: 1}, 1<<40)
	if c := st.Coverage; c.SessionsRead != 1 || c.NextOffset != 1 {
		t.Fatalf("the limit still holds: %+v", c)
	}
}

// TestStatsPageCountsCompressedSessionsUnpacked: a compressed Codex rollout is parsed
// unpacked, so the page's byte cap counts it at compressedRatio times its size.
func TestStatsPageCountsCompressedSessionsUnpacked(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	roots := Roots{ClaudeProjects: filepath.Join(dir, "projects"), CodexHome: filepath.Join(dir, "codex")}
	base := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	write := func(path string, data []byte, age time.Duration) int64 {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		mt := base.Add(-age)
		if err := os.Chtimes(path, mt, mt); err != nil {
			t.Fatal(err)
		}
		return int64(len(data))
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	rollout := `{"timestamp":"2026-09-30T08:00:00Z","type":"session_meta","payload":{"id":"z","cwd":"/w"}}` + "\n" +
		strings.Repeat(`{"timestamp":"2026-09-30T08:00:01Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t1"}}`+"\n", 50)
	// The newest session is the compressed rollout, then a Claude session.
	packed := write(filepath.Join(roots.CodexHome, "archived_sessions", "rollout-2026-09-30T08-00-00-z.jsonl.zst"), enc.EncodeAll([]byte(rollout), nil), 0)
	plain := write(filepath.Join(roots.ClaudeProjects, "-w", "c.jsonl"),
		[]byte(`{"type":"user","message":{"role":"user","content":"реплика"},"timestamp":"2026-09-30T08:00:00Z"}`+"\n"), time.Minute)
	now := base.Add(time.Hour)
	read := func(capBytes int64) int {
		t.Helper()
		st, _, err := statsOf(roots, StatsIn{}, now, capBytes)
		if err != nil {
			t.Fatal(err)
		}
		return st.Coverage.SessionsRead
	}
	if n := read(compressedRatio*packed + plain - 1); n != 1 {
		t.Fatalf("under the cap counted unpacked: %d sessions read, want 1", n)
	}
	if n := read(compressedRatio*packed + plain); n != 2 {
		t.Fatalf("at the cap counted unpacked: %d sessions read, want 2", n)
	}
}
