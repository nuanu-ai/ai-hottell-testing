package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Фикстуры повторяют структуру реальных транскриптов (ключи и вложенность),
// содержимое синтетическое: сырые сессии в репозиторий не коммитятся.

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

// старый формат: без token_usage_record, токены только в token_count
const codexOldFixture = `{"timestamp":"2026-03-17T05:38:00.000Z","type":"session_meta","payload":{"id":"old","cwd":"/Users/a/old"}}
{"timestamp":"2026-03-17T05:38:44.911Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10672,"cached_input_tokens":7552,"output_tokens":169,"reasoning_output_tokens":49}}}}
`

// fakeHome — HOME с транскриптами обоих агентов.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	write := func(rel, body string) {
		p := filepath.Join(home, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".claude/projects/-Users-a-proj-x/c1.jsonl", claudeFixture)
	write(".claude/projects/-Users-a-proj-x/c1/subagents/agent-s1.jsonl", claudeFixture)
	write(".codex/sessions/2026/09/23/rollout-2026-09-23T23-47-35-019e-codex-1.jsonl", codexFixture)
	write(".codex/sessions/2026/03/17/rollout-2026-03-17T13-37-38-old.jsonl", codexOldFixture)
	return home
}

func TestSessionsList(t *testing.T) {
	fakeHome(t)
	var out sessionsListOut
	callTool(t, "sessions_list", nil, &out)
	if out.Total != 3 {
		t.Fatalf("всего %d, ожидалось 3 (без субагентов): %+v", out.Total, out.Sessions)
	}
	callTool(t, "sessions_list", map[string]any{"include_subagents": true}, &out)
	if out.Total != 4 {
		t.Fatalf("с субагентами %d", out.Total)
	}
	callTool(t, "sessions_list", map[string]any{"agent": "codex", "project": "/Users/a/3d"}, &out)
	if out.Total != 1 || out.Sessions[0].ID != "019e-codex-1" || out.Sessions[0].Started.IsZero() {
		t.Fatalf("фильтр codex+project: %+v", out.Sessions)
	}
	callTool(t, "sessions_list", map[string]any{"project": "/Users/a/proj.x"}, &out)
	if out.Total != 1 || out.Sessions[0].Agent != "claude" {
		t.Fatalf("фильтр по проекту claude: %+v", out.Sessions)
	}
	callTool(t, "sessions_list", map[string]any{"since": "1h"}, &out)
	if out.Total != 3 {
		t.Fatalf("since 1h: %d", out.Total)
	}
	callTool(t, "sessions_list", map[string]any{"until": "2000-01-01"}, &out)
	if out.Total != 0 {
		t.Fatalf("until 2000: %d", out.Total)
	}
}

func kinds(evs []sessEvent) string {
	var ks []string
	for _, e := range evs {
		ks = append(ks, e.Kind)
	}
	return strings.Join(ks, ",")
}

func TestParseClaude(t *testing.T) {
	tr := parseClaude(strings.NewReader(claudeFixture))
	want := "attachment,user_message,reasoning,token_usage,tool_call,tool_result,assistant_message,token_usage,system"
	if got := kinds(tr.Events); got != want {
		t.Fatalf("виды:\n%s\n%s", got, want)
	}
	if tr.Cwd != "/Users/a/proj.x" || tr.Title != "Башни wifi" || tr.Broken != 1 || tr.Version != "2.1.247" {
		t.Fatalf("мета: %+v", tr)
	}
	// usage msg_1 записан дважды (по блоку) — учтён один раз
	var tok tokenUsage
	for _, e := range tr.Events {
		if e.Tokens != nil {
			tok.add(*e.Tokens)
		}
	}
	if tok.Input != 1102+1203 || tok.CacheRead != 2200 || tok.Output != 70 {
		t.Fatalf("токены: %+v", tok)
	}
	if e := tr.Events[5]; !e.IsError || e.CallID != "tu_1" || e.Text != "нет такого" {
		t.Fatalf("tool_result: %+v", e)
	}
}

func TestParseCodex(t *testing.T) {
	tr := parseCodex(strings.NewReader(codexFixture))
	want := "turn_start,system,user_message,tool_call,token_usage,tool_result,reasoning,assistant_message,turn_end"
	if got := kinds(tr.Events); got != want {
		t.Fatalf("виды:\n%s\n%s", got, want)
	}
	u := tr.Events[4].Tokens
	if u.Input != 38994 || u.CacheRead != 19584 || u.Output != 192 || u.Reasoning != 10 {
		t.Fatalf("usage из token_usage_record, token_count не должен удваивать: %+v", u)
	}
	if tr.Events[5].Text != "a.txt" || tr.Events[7].Model != "gpt-6-astra" || tr.Cwd != "/Users/a/3d" {
		t.Fatalf("события: %+v", tr.Events)
	}
	for i, e := range tr.Events {
		if e.Seq != i {
			t.Fatalf("seq %d != %d", e.Seq, i)
		}
	}

	old := parseCodex(strings.NewReader(codexOldFixture))
	if len(old.Events) != 1 || old.Events[0].Tokens.Input != 10672 {
		t.Fatalf("старый формат: %+v", old.Events)
	}
}

func TestSessionReadAndStats(t *testing.T) {
	fakeHome(t)
	var rd sessionReadOut
	callTool(t, "session_read", map[string]any{"id": "019e", "limit": 3, "max_text": 4}, &rd)
	if rd.Total != 9 || len(rd.Events) != 3 || rd.Next != 3 || rd.Meta.Cwd != "/Users/a/3d" {
		t.Fatalf("read: total=%d n=%d next=%d", rd.Total, len(rd.Events), rd.Next)
	}
	callTool(t, "session_read", map[string]any{"id": "c1", "agent": "claude", "kinds": []string{"tool_call", "tool_result"}}, &rd)
	if rd.Matched != 2 || len(rd.Events) != 2 || rd.Events[0].Tool != "Bash" {
		t.Fatalf("read kinds: %+v", rd.Events)
	}

	var st sessionStats
	callTool(t, "session_stats", map[string]any{"id": "c1", "agent": "claude"}, &st)
	if st.ToolCalls != 1 || st.ToolErrors != 1 || st.Tools[0].Errors != 1 || st.Turns != 1 || st.Tokens.Output != 70 {
		t.Fatalf("stats c1: %+v", st)
	}
	callTool(t, "session_stats", map[string]any{"agent": "codex"}, &st)
	if st.Sessions != 2 || st.Tokens.Input != 38994+10672 || st.TokensByModel["gpt-6-astra"] == nil || len(st.PerSession) != 2 {
		t.Fatalf("stats period: %+v", st)
	}
}

// Отгрузка: source=backfill, Timestamp = время загрузки, родное — атрибутом;
// повтор ничего не шлёт, дописанная сессия догружается хвостом.
func TestSessionsShipIdempotent(t *testing.T) {
	home := fakeHome(t)
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var doc map[string]any
		json.Unmarshal(raw, &doc)
		mu.Lock()
		bodies = append(bodies, doc)
		mu.Unlock()
	}))
	defer srv.Close()
	cfg := defaultConfig()
	cfg.Endpoint = srv.URL
	cfg.SpoolDir = filepath.Join(home, "state", "spool")
	cfg.BatchSize = 4

	out := shipSessions(cfg, shipIn{IDs: []string{"c1"}, sessionFilter: sessionFilter{Agent: "claude"}})
	if out.Shipped != 9 || len(bodies) != 3 || out.Sessions[0].Error != "" {
		t.Fatalf("первая отгрузка: %+v, POST=%d", out, len(bodies))
	}
	rl := bodies[0]["resourceLogs"].([]any)[0].(map[string]any)
	if !strings.Contains(mustJSON(rl["resource"]), `"agent-backfill"`) {
		t.Fatalf("ресурс: %v", rl["resource"])
	}
	rec := rl["scopeLogs"].([]any)[0].(map[string]any)["logRecords"].([]any)[0].(map[string]any)
	attrs := attrMap(t, rec["attributes"].([]any))
	if attrs["source"].(map[string]any)["stringValue"] != "backfill" ||
		attrs["event.time"].(map[string]any)["stringValue"] != "2026-08-27T14:07:15.552Z" ||
		strings.HasPrefix(rec["timeUnixNano"].(string), "1787") {
		t.Fatalf("запись: %v", rec)
	}

	again := shipSessions(cfg, shipIn{IDs: []string{"c1"}, sessionFilter: sessionFilter{Agent: "claude"}})
	if again.Shipped != 0 || again.Sessions[0].Already != 9 || len(bodies) != 3 {
		t.Fatalf("повтор должен быть пустым: %+v", again)
	}

	p := filepath.Join(home, ".claude/projects/-Users-a-proj-x/c1.jsonl")
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"type":"user","message":{"role":"user","content":"ещё"},"timestamp":"2026-08-27T15:00:00.000Z"}` + "\n")
	f.Close()
	tail := shipSessions(cfg, shipIn{IDs: []string{"c1"}, sessionFilter: sessionFilter{Agent: "claude"}})
	if tail.Shipped != 1 {
		t.Fatalf("хвост: %+v", tail)
	}

	dry := shipSessions(cfg, shipIn{DryRun: true, sessionFilter: sessionFilter{Agent: "codex"}})
	if dry.Shipped != 10 || len(bodies) != 4 {
		t.Fatalf("dry run: %+v POST=%d", dry, len(bodies))
	}
}

// Прогон парсеров по всем реальным транскриптам машины: HOTTELL_REAL_SESSIONS=1.
// Проверяет, что реальный формат разбирается без битых строк и с событиями.
func TestRealSessions(t *testing.T) {
	if os.Getenv("HOTTELL_REAL_SESSIONS") == "" {
		t.Skip("HOTTELL_REAL_SESSIONS не задан")
	}
	list, _, err := listSessions(sessionFilter{IncludeSubagents: true, Limit: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	var events, broken, empty int
	for _, si := range list {
		tr, err := parseTranscriptFile(si)
		if err != nil {
			t.Fatal(err)
		}
		events += len(tr.Events)
		broken += tr.Broken
		if len(tr.Events) == 0 && si.Size > 10000 {
			empty++
			t.Logf("пусто: %s (%d байт)", si.Path, si.Size)
		}
	}
	t.Logf("сессий %d, событий %d, битых строк %d, пустых крупных %d", len(list), events, broken, empty)
	if broken > 0 || empty > 0 {
		t.Fail()
	}
}
