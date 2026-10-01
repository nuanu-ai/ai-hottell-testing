package sessions_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/sessions"
)

// The fixtures follow the structure of real transcripts (keys and nesting); the content
// is synthetic: raw sessions are never committed.

const claudeFixture = `{"type":"queue-operation","operation":"enqueue","timestamp":"2026-08-27T14:07:42.800Z","sessionId":"c1"}
{"type":"attachment","attachment":{"type":"hook_success","hookName":"SessionStart:startup"},"timestamp":"2026-08-27T14:07:15.552Z","cwd":"/Users/a/proj.x","sessionId":"c1","version":"2.1.247","gitBranch":"main"}
{"type":"user","message":{"role":"user","content":"найди башни wifi"},"timestamp":"2026-08-27T14:07:42.813Z","cwd":"/Users/a/proj.x","sessionId":"c1"}
{"type":"assistant","message":{"model":"claude-opus-5","id":"msg_1","role":"assistant","content":[{"type":"thinking","thinking":"думаю"}],"usage":{"input_tokens":2,"cache_creation_input_tokens":100,"cache_read_input_tokens":1000,"output_tokens":50}},"timestamp":"2026-08-27T14:07:46.494Z"}
{"type":"assistant","message":{"model":"claude-opus-5","id":"msg_1","role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"Bash","input":{"command":"ls"}}],"usage":{"input_tokens":2,"cache_creation_input_tokens":100,"cache_read_input_tokens":1000,"output_tokens":50}},"timestamp":"2026-08-27T14:07:47.845Z"}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"нет такого","is_error":true}]},"timestamp":"2026-08-27T14:07:47.893Z"}
{"type":"file-history-snapshot","messageId":"x","snapshot":{}}
{"type":"ai-title","sessionId":"c1","aiTitle":"Башни wifi"}
{"type":"assistant","message":{"model":"claude-opus-5","id":"msg_2","role":"assistant","content":[{"type":"text","text":"Готово"}],"usage":{"input_tokens":3,"cache_creation_input_tokens":0,"cache_read_input_tokens":1200,"output_tokens":20}},"timestamp":"2026-08-27T14:08:14.643Z"}
{"type":"system","subtype":"stop_hook_summary","timestamp":"2026-08-27T14:08:16.273Z"}
{broken
`

const codexFixture = `{"timestamp":"2026-09-23T20:47:36.239Z","type":"session_meta","payload":{"id":"019e-codex-1","cwd":"/Users/a/3d","cli_version":"0.155.0","git":{}}}
{"timestamp":"2026-09-23T20:47:36.240Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t1"}}
{"timestamp":"2026-09-23T20:47:37.406Z","type":"turn_context","payload":{"turn_id":"t1","cwd":"/Users/a/3d","model":"gpt-6-astra"}}
{"timestamp":"2026-09-23T20:47:37.391Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"<app-context>"}]}}
{"timestamp":"2026-09-23T20:47:37.392Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"настрой принтер"}]}}
{"timestamp":"2026-09-23T20:47:37.766Z","type":"event_msg","payload":{"type":"item_completed","item":{}}}
{"timestamp":"2026-09-23T20:47:48.819Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"call_1","name":"exec","input":"ls"}}
{"timestamp":"2026-09-23T20:47:48.855Z","type":"token_usage_record","payload":{"turn_id":"t1","usage":{"input_tokens":38994,"cached_input_tokens":19584,"cache_write_input_tokens":0,"output_tokens":192,"reasoning_output_tokens":10,"total_tokens":39186}}}
{"timestamp":"2026-09-23T20:47:50.831Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_1","output":[{"type":"input_text","text":"a.txt"}]}}
{"timestamp":"2026-09-23T20:47:50.837Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1,"output_tokens":1}}}}
{"timestamp":"2026-09-23T20:47:53.743Z","type":"response_item","payload":{"type":"reasoning","summary":[],"encrypted_content":"gAAA"}}
{"timestamp":"2026-09-23T20:47:54.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Готово"}]}}
{"timestamp":"2026-09-23T20:50:13.615Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","duration_ms":17485}}
`

// codexOldFixture is the old format: no token_usage_record, the tokens are in token_count only.
const codexOldFixture = `{"timestamp":"2026-03-17T05:38:00.000Z","type":"session_meta","payload":{"id":"old","cwd":"/Users/a/old"}}
{"timestamp":"2026-03-17T05:38:44.911Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10672,"cached_input_tokens":7552,"output_tokens":169,"reasoning_output_tokens":49}}}}
`

// writeFile writes body to dir/rel, creating the directories.
func writeFile(t *testing.T, dir, rel string, body []byte) string {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// writeRoots writes the transcripts of both agents into a temporary home: a Claude Code
// session with a subagent, a Codex session and an old-format Codex session.
func writeRoots(t *testing.T) sessions.Roots {
	t.Helper()
	home := t.TempDir()
	writeFile(t, home, ".claude/projects/-Users-a-proj-x/c1.jsonl", []byte(claudeFixture))
	writeFile(t, home, ".claude/projects/-Users-a-proj-x/c1/subagents/agent-s1.jsonl", []byte(claudeFixture))
	writeFile(t, home, ".codex/sessions/2026/09/23/rollout-2026-09-23T23-47-35-019e-codex-1.jsonl", []byte(codexFixture))
	writeFile(t, home, ".codex/sessions/2026/03/17/rollout-2026-03-17T13-37-38-old.jsonl", []byte(codexOldFixture))
	return sessions.Roots{
		ClaudeProjects: filepath.Join(home, ".claude", "projects"),
		CodexHome:      filepath.Join(home, ".codex"),
	}
}

func TestSessionsList(t *testing.T) {
	t.Parallel()
	roots, now := writeRoots(t), time.Now()
	list := func(f sessions.Filter) ([]sessions.Info, int) {
		t.Helper()
		out, total, err := sessions.List(roots, f, now)
		if err != nil {
			t.Fatal(err)
		}
		return out, total
	}
	if out, total := list(sessions.Filter{}); total != 3 {
		t.Fatalf("total %d, want 3 (without subagents): %+v", total, out)
	}
	if _, total := list(sessions.Filter{IncludeSubagents: true}); total != 4 {
		t.Fatalf("with subagents %d", total)
	}
	if out, total := list(sessions.Filter{Agent: "codex", Project: "/Users/a/3d"}); total != 1 ||
		out[0].ID != "019e-codex-1" || out[0].Started.IsZero() || out[0].Project != "/Users/a/3d" {
		t.Fatalf("codex+project filter: %+v", out)
	}
	if out, total := list(sessions.Filter{Project: "/Users/a/proj.x"}); total != 1 || out[0].Agent != "claude" {
		t.Fatalf("claude project filter: %+v", out)
	}
	if _, total := list(sessions.Filter{Since: "1h"}); total != 3 {
		t.Fatalf("since 1h: %d", total)
	}
	if _, total := list(sessions.Filter{Until: "2000-01-01"}); total != 0 {
		t.Fatalf("until 2000: %d", total)
	}
	if _, _, err := sessions.List(roots, sessions.Filter{Agent: "gemini"}, now); err == nil {
		t.Fatal("an unknown agent is an error")
	}
}

func TestSessionsListOffset(t *testing.T) {
	t.Parallel()
	roots, now := writeRoots(t), time.Now()
	all, _, err := sessions.List(roots, sessions.Filter{}, now)
	if err != nil {
		t.Fatal(err)
	}
	page, total, err := sessions.List(roots, sessions.Filter{Offset: 1, Limit: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(page) != 1 || page[0].Path != all[1].Path {
		t.Fatalf("offset 1, limit 1: total=%d %+v", total, page)
	}
	if past, total, _ := sessions.List(roots, sessions.Filter{Offset: 10}, now); total != 3 || len(past) != 0 {
		t.Fatalf("offset past the end: total=%d %+v", total, past)
	}
}

// TestSessionsListStableOrder: files changed at the same moment keep one order from call
// to call, so the pages neither repeat a session nor skip one.
func TestSessionsListStableOrder(t *testing.T) {
	t.Parallel()
	roots, now := writeRoots(t), time.Now()
	same := now.Add(-time.Hour).Truncate(time.Second)
	for _, root := range []string{roots.ClaudeProjects, roots.CodexHome} {
		if err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			return os.Chtimes(p, same, same)
		}); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for off := range 3 {
		page, total, err := sessions.List(roots, sessions.Filter{Limit: 1, Offset: off}, now)
		if err != nil || total != 3 || len(page) != 1 {
			t.Fatalf("offset %d: total=%d %+v %v", off, total, page, err)
		}
		if seen[page[0].Path] {
			t.Fatalf("offset %d repeats %s", off, page[0].Path)
		}
		seen[page[0].Path] = true
	}
}

// TestListReadsNoBodyItSkips: a rollout that since, min_size or the page leave out is never
// opened. The rollout is a named pipe: opening it for reading blocks until a writer comes.
func TestListReadsNoBodyItSkips(t *testing.T) {
	t.Parallel()
	roots, now := writeRoots(t), time.Now()
	dir := filepath.Join(roots.CodexHome, "sessions", "2026", "01", "01")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	pipe := filepath.Join(dir, "rollout-2026-01-01T00-00-00-pipe.jsonl")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(pipe, old, old); err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"since": func() error {
			_, _, err := sessions.List(roots, sessions.Filter{Since: "24h"}, now)
			return err
		},
		"min_size": func() error {
			_, _, err := sessions.List(roots, sessions.Filter{MinSize: 1}, now)
			return err
		},
		"outside the page": func() error {
			_, _, err := sessions.List(roots, sessions.Filter{Limit: 1}, now)
			return err
		},
		"find another id": func() error {
			_, err := sessions.Find(roots, "", "019e-codex-1", now)
			return err
		},
	} {
		done := make(chan error, 1)
		go func() { done <- call() }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		case <-time.After(5 * time.Second):
			releasePipe(pipe, done)
			t.Fatalf("%s: List opened a rollout it leaves out", name)
		}
	}
}

// releasePipe lets every blocked reader of the pipe through, with an empty write side,
// until the call reports on done.
func releasePipe(pipe string, done <-chan error) {
	for {
		if w, err := os.OpenFile(pipe, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = w.Close()
		}
		select {
		case <-done:
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func TestFind(t *testing.T) {
	t.Parallel()
	roots, now := writeRoots(t), time.Now()
	si, err := sessions.Find(roots, "", "019e", now)
	if err != nil || si.ID != "019e-codex-1" || si.Agent != "codex" {
		t.Fatalf("by prefix: %+v %v", si, err)
	}
	if _, err := sessions.Find(roots, "", "nope", now); err == nil {
		t.Fatal("an unknown id is an error")
	}
	sub, err := sessions.Find(roots, "claude", "agent-s1", now)
	if err != nil || sub.Parent != "c1" {
		t.Fatalf("subagent: %+v %v", sub, err)
	}
	byPath, err := sessions.Find(roots, "", si.Path, now)
	if err != nil || byPath.Agent != "codex" || byPath.ID != "019e-codex-1" {
		t.Fatalf("by path: %+v %v", byPath, err)
	}
}

func TestCodexArchivedAndCompressed(t *testing.T) {
	t.Parallel()
	roots := writeRoots(t)
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	packed := enc.EncodeAll([]byte(codexFixture), nil)
	name := "rollout-2026-09-20T10-00-00-019e-arch.jsonl"
	zst := writeFile(t, roots.CodexHome, filepath.Join("archived_sessions", name+".zst"), packed)
	// The same thread uncompressed under sessions/ as well: one session, not two.
	old := writeFile(t, roots.CodexHome, filepath.Join("sessions", "2026", "09", "20", name), []byte(codexFixture))
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}

	list, _, err := sessions.List(roots, sessions.Filter{Agent: "codex", Limit: 100}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var arch []sessions.Info
	for _, si := range list {
		if si.ID == "019e-arch" {
			arch = append(arch, si)
		}
	}
	if len(arch) != 1 || !arch[0].Compressed || arch[0].Project != "/Users/a/3d" || arch[0].Path != zst {
		t.Fatalf("archived compressed rollout: %+v", arch)
	}
	tr, err := sessions.Parse(arch[0])
	if err != nil || len(tr.Events) == 0 {
		t.Fatalf("parse .zst: %v %+v", err, tr)
	}
	byPath, err := sessions.Find(roots, "", zst, time.Now())
	if err != nil || !byPath.Compressed || byPath.ID != "019e-arch" {
		t.Fatalf("by .jsonl.zst path: %+v %v", byPath, err)
	}
}

// TestRealSessions runs the parsers over every real transcript of this Mac when
// HOTTELL_REAL_SESSIONS is set: the real format parses with no broken lines and with events.
func TestRealSessions(t *testing.T) {
	t.Parallel()
	if os.Getenv("HOTTELL_REAL_SESSIONS") == "" {
		t.Skip("HOTTELL_REAL_SESSIONS is not set")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	claudeDir, codexHome := os.Getenv("CLAUDE_CONFIG_DIR"), os.Getenv("CODEX_HOME")
	if claudeDir == "" {
		claudeDir = filepath.Join(home, ".claude")
	}
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	roots := sessions.Roots{ClaudeProjects: filepath.Join(claudeDir, "projects"), CodexHome: codexHome}
	list, _, err := sessions.List(roots, sessions.Filter{IncludeSubagents: true, Limit: 1 << 30}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var events, broken, empty int
	for _, si := range list {
		tr, err := sessions.Parse(si)
		if err != nil {
			t.Fatal(err)
		}
		events += len(tr.Events)
		broken += tr.Broken
		if len(tr.Events) == 0 && si.Size > 10000 {
			empty++
			t.Logf("empty: %s (%d bytes)", si.Path, si.Size)
		}
	}
	t.Logf("sessions %d, events %d, broken lines %d, large empty %d", len(list), events, broken, empty)
	if broken > 0 || empty > 0 {
		t.Fail()
	}
}
