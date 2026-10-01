package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// Синтетические rollout: поля пишутся в порядке Codex ("type" раньше
// "payload"), содержимое выдумано.

func obj(kv ...any) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i := 0; i < len(kv); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(kv[i].(string))
		v, err := json.Marshal(kv[i+1])
		if err != nil {
			panic(err)
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes()
}

func rline(typ string, payload json.RawMessage) json.RawMessage {
	return obj("timestamp", "2026-01-01T00:00:00.000Z", "type", typ, "payload", payload)
}

func evMsg(kv ...any) json.RawMessage { return rline("event_msg", obj(kv...)) }
func respItem(kv ...any) json.RawMessage {
	return rline("response_item", obj(kv...))
}

func taskStarted(turn string) json.RawMessage {
	return evMsg("type", "task_started", "turn_id", turn, "started_at", 1)
}
func taskComplete(turn string, ms int) json.RawMessage {
	return evMsg("type", "task_complete", "turn_id", turn, "last_agent_message", "invented final answer", "duration_ms", ms)
}
func turnContext(turn, model string) json.RawMessage {
	if turn == "" {
		return rline("turn_context", obj("cwd", "/work", "model", model))
	}
	return rline("turn_context", obj("turn_id", turn, "cwd", "/work", "model", model))
}
func itemDone(turn string, item json.RawMessage) json.RawMessage {
	return evMsg("type", "item_completed", "turn_id", turn, "item", item, "started_at_ms", 1000, "completed_at_ms", 1007)
}
func tokens(in, cached, out, reasoning int) json.RawMessage {
	return obj("input_tokens", in, "cached_input_tokens", cached, "cache_write_input_tokens", 0,
		"output_tokens", out, "reasoning_output_tokens", reasoning, "total_tokens", in+out)
}
func usageRecord(turn, resp string, usage, turnUsage json.RawMessage) json.RawMessage {
	return rline("token_usage_record", obj("thread_id", "th", "turn_id", turn, "response_id", resp,
		"usage", usage, "turn_token_usage", turnUsage, "thread_token_usage", turnUsage))
}
func tokenCountLine(total, last json.RawMessage) json.RawMessage {
	if total == nil {
		return evMsg("type", "token_count", "info", nil)
	}
	return evMsg("type", "token_count", "info", obj("total_token_usage", total, "last_token_usage", last))
}

// codexHome — временный HOME с ~/.codex/sessions.
func codexHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".codex", "sessions", "2026", "01", "01")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeLines(t *testing.T, path string, lines ...any) string {
	t.Helper()
	var b bytes.Buffer
	for _, l := range lines {
		switch x := l.(type) {
		case json.RawMessage:
			b.Write(x)
		case string:
			b.WriteString(x) // намеренно битые строки
		default:
			t.Fatalf("line type %T", l)
		}
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// appendLines дописывает строки по одной, как Codex; вызывается из горутины,
// поэтому возвращает ошибку, а не валит тест.
func appendLines(path string, lines ...json.RawMessage) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.Write(append(l, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func codexEvent(name, transcript string, ts time.Time, kv ...any) event {
	p := map[string]any{"hook_event_name": name, "session_id": "s-enrich", "cwd": "/work"}
	if transcript != "" {
		p["transcript_path"] = transcript
	}
	for i := 0; i < len(kv); i += 2 {
		p[kv[i].(string)] = kv[i+1]
	}
	return event{TS: ts.UnixNano(), Agent: "codex", Event: name, Payload: p}
}

var longAgo = time.Now().Add(-time.Hour)

func enrichCfg(t *testing.T) Config {
	cfg := testConfig(t)
	cfg.EnrichTranscript = true
	return cfg
}

func hottellKeys(p map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range p {
		if strings.HasPrefix(k, "hottell.") {
			out[k] = v
		}
	}
	return out
}

func wantAttrs(t *testing.T, what string, p map[string]any, want map[string]any) {
	t.Helper()
	if got := hottellKeys(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s:\n got  %#v\n want %#v", what, got, want)
	}
}

func TestEnrichExitCodeFromItemCompleted(t *testing.T) {
	dir := codexHome(t)
	path := writeLines(t, filepath.Join(dir, "rollout-a.jsonl"),
		rline("session_meta", obj("id", "s-enrich")),
		taskStarted("T1"),
		turnContext("T1", "gpt-test"),
		itemDone("T1", obj("type", "CommandExecution", "id", "exec-ok", "command", []string{"/bin/zsh", "-lc", "echo SECRET-COMMAND"},
			"status", "completed", "stdout", "SECRET-OUTPUT", "exit_code", 0, "duration", obj("secs", 1, "nanos", 250_000_000))),
		"{not json at all",
		itemDone("T1", obj("type", "CommandExecution", "id", "exec-fail", "command", []string{"false"},
			"status", "failed", "stdout", "", "exit_code", 2, "duration", obj("secs", 0, "nanos", 5_000_000))),
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"event_msg","payload":{"type":"item_completed","item":{"id":"exec-trunc`,
		taskComplete("T1", 900),
	)
	events := []event{
		codexEvent("PostToolUse", path, longAgo, "tool_use_id", "exec-ok", "turn_id", "T1"),
		codexEvent("PostToolUse", path, longAgo, "tool_use_id", "exec-fail", "turn_id", "T1"),
	}
	out := enrichEvents(enrichCfg(t), events, time.Now())
	wantAttrs(t, "exit 0", events[0].Payload, map[string]any{
		"hottell.enrich_status": "found", "hottell.tool_status": "completed",
		"hottell.exit_code": int64(0), "hottell.tool_duration_ms": int64(1250),
	})
	wantAttrs(t, "exit 2", events[1].Payload, map[string]any{
		"hottell.enrich_status": "found", "hottell.tool_status": "failed",
		"hottell.exit_code": int64(2), "hottell.tool_duration_ms": int64(5),
	})
	if out[0].hold || out[1].hold || !out[0].changed {
		t.Fatalf("outcomes: %+v", out)
	}
	for _, ev := range events {
		raw, _ := json.Marshal(ev.Payload)
		if bytes.Contains(raw, []byte("SECRET")) {
			t.Fatalf("command text or output copied: %s", raw)
		}
	}
	// Атрибуты уходят в OTLP целыми числами.
	attrs := attrMap(t, toAny(t, buildLogRecord(enrichCfg(t), events[1])["attributes"]))
	if v := attrs["hottell.exit_code"].(map[string]any)["intValue"]; v != "2" {
		t.Fatalf("exit_code OTLP value: %#v", attrs["hottell.exit_code"])
	}
}

func TestEnrichMcpFileChangeAndExtensionStatus(t *testing.T) {
	dir := codexHome(t)
	path := writeLines(t, filepath.Join(dir, "rollout-b.jsonl"),
		taskStarted("T1"),
		respItem("type", "function_call", "name", "lookup", "namespace", "mcp__demo", "arguments", "{}", "call_id", "call_mcp"),
		itemDone("T1", obj("type", "McpToolCall", "id", "call_mcp", "server", "demo", "tool", "lookup",
			"status", "failed", "error", obj("message", "invented failure"), "duration", obj("secs", 2, "nanos", 0))),
		respItem("type", "function_call_output", "call_id", "call_mcp", "output", "Wall time: 2.0 seconds\nOutput:\nExit code: 9"),
		itemDone("T1", obj("type", "FileChange", "id", "exec-patch", "changes", obj(), "status", "declined")),
		itemDone("T1", obj("type", "Extension", "kind", "web.search", "id", "exec-web", "durationMs", 1234)),
		itemDone("T1", obj("type", "ImageView", "id", "exec-img", "path", "file:///x.png")),
	)
	events := []event{
		codexEvent("PostToolUse", path, longAgo, "tool_use_id", "call_mcp", "turn_id", "T1"),
		codexEvent("PostToolUse", path, longAgo, "tool_use_id", "exec-patch", "turn_id", "T1"),
		codexEvent("PostToolUse", path, longAgo, "tool_use_id", "exec-web", "turn_id", "T1"),
		codexEvent("PostToolUse", path, longAgo, "tool_use_id", "exec-img", "turn_id", "T1"),
	}
	enrichEvents(enrichCfg(t), events, time.Now())
	// item_completed важнее текста вывода: "Exit code: 9" в теле не код выхода.
	wantAttrs(t, "mcp", events[0].Payload, map[string]any{
		"hottell.enrich_status": "found", "hottell.tool_status": "failed", "hottell.tool_duration_ms": int64(2000),
	})
	wantAttrs(t, "file change", events[1].Payload, map[string]any{
		"hottell.enrich_status": "found", "hottell.tool_status": "declined", "hottell.tool_duration_ms": int64(7),
	})
	wantAttrs(t, "extension", events[2].Payload, map[string]any{
		"hottell.enrich_status": "found", "hottell.tool_duration_ms": int64(1234),
	})
	wantAttrs(t, "image", events[3].Payload, map[string]any{
		"hottell.enrich_status": "found", "hottell.tool_duration_ms": int64(7),
	})
}

func TestEnrichFallbackFromCallOutput(t *testing.T) {
	dir := codexHome(t)
	path := writeLines(t, filepath.Join(dir, "rollout-old.jsonl"),
		taskStarted("T1"),
		respItem("type", "function_call", "name", "shell", "arguments", "{}", "call_id", "call_a"),
		respItem("type", "function_call_output", "call_id", "call_a",
			"output", "Exit code: 3\nWall time: 1.5 seconds\nOutput:\nExit code: 9\n"),
		respItem("type", "custom_tool_call", "name", "exec_command", "input", "ls", "call_id", "call_b"),
		respItem("type", "custom_tool_call_output", "call_id", "call_b",
			"output", "Chunk ID: ab12\nWall time: 0.2500 seconds\nProcess exited with code 0\nOriginal token count: 3\nOutput:\nok\n"),
		respItem("type", "function_call", "name", "apply_patch", "arguments", "{}", "call_id", "call_c"),
		respItem("type", "function_call_output", "call_id", "call_c",
			"output", `{"output":"invented","metadata":{"exit_code":1,"duration_seconds":0.4}}`),
		respItem("type", "function_call", "name", "exec", "arguments", "{}", "call_id", "call_d"),
		respItem("type", "function_call_output", "call_id", "call_d",
			"output", []any{obj("type", "input_text", "text", "Script completed\nWall time 0.1 seconds\nOutput:\nProcess exited with code 7\n")}),
		respItem("type", "function_call", "name", "shell", "arguments", "{}", "call_id", "call_e"),
		respItem("type", "function_call_output", "call_id", "call_e",
			"output", "Chunk ID: cd34\nWall time: 30.0 seconds\nProcess running with session ID 7\nOutput:\n"),
	)
	ids := []string{"call_a", "call_b", "call_c", "call_d", "call_e"}
	var events []event
	for _, id := range ids {
		events = append(events, codexEvent("PostToolUse", path, longAgo, "tool_use_id", id, "turn_id", "T1"))
	}
	enrichEvents(enrichCfg(t), events, time.Now())
	want := []map[string]any{
		{"hottell.enrich_status": "found", "hottell.exit_code": int64(3), "hottell.tool_duration_ms": int64(1500)},
		{"hottell.enrich_status": "found", "hottell.exit_code": int64(0), "hottell.tool_duration_ms": int64(250)},
		{"hottell.enrich_status": "found", "hottell.exit_code": int64(1), "hottell.tool_duration_ms": int64(400)},
		{"hottell.enrich_status": "found"}, // код в теле вывода не считается
		{"hottell.enrich_status": "found"}, // процесс ещё работает
	}
	for i := range ids {
		wantAttrs(t, ids[i], events[i].Payload, want[i])
	}
}

func TestEnrichStopTurnTotals(t *testing.T) {
	dir := codexHome(t)
	path := writeLines(t, filepath.Join(dir, "rollout-turn.jsonl"),
		taskStarted("T0"),
		turnContext("T0", "older-model"),
		usageRecord("T0", "r0", tokens(999, 0, 9, 0), tokens(999, 0, 9, 0)),
		taskComplete("T0", 100),
		taskStarted("T1"),
		turnContext("T1", "gpt-test"),
		usageRecord("T1", "r1", tokens(100, 40, 10, 3), tokens(100, 40, 10, 3)),
		tokenCountLine(tokens(1099, 40, 19, 3), tokens(100, 40, 10, 3)),
		itemDone("T1", obj("type", "CommandExecution", "id", "exec-1", "status", "completed", "exit_code", 0)),
		usageRecord("T1", "r2", tokens(200, 150, 20, 5), tokens(300, 190, 30, 8)),
		usageRecord("T1", "r2", tokens(200, 150, 20, 5), tokens(300, 190, 30, 8)), // повтор записи
		tokenCountLine(tokens(1299, 190, 39, 8), tokens(200, 150, 20, 5)),
		taskComplete("T1", 4321),
		taskStarted("T2"),
		turnContext("T2", "gpt-next"),
		usageRecord("T2", "r3", tokens(50, 0, 5, 0), tokens(50, 0, 5, 0)),
	)
	events := []event{
		codexEvent("Stop", path, longAgo, "turn_id", "T1"),
		codexEvent("Stop", path, time.Now(), "turn_id", "T2"), // свежий, task_complete ещё нет
		codexEvent("Stop", path, longAgo, "turn_id", "T2"),
		codexEvent("Stop", path, longAgo, "turn_id", "T-missing"),
		codexEvent("Stop", path, time.Now(), "turn_id", "T-young-missing"),
	}
	out := enrichEvents(enrichCfg(t), events, time.Now())
	wantAttrs(t, "T1", events[0].Payload, map[string]any{
		"hottell.enrich_status": "found", "hottell.turn.responses": int64(2),
		"hottell.turn.input_tokens": int64(300), "hottell.turn.cached_input_tokens": int64(190),
		"hottell.turn.output_tokens": int64(30), "hottell.turn.reasoning_output_tokens": int64(8),
		"hottell.turn.model": "gpt-test", "hottell.turn.duration_ms": int64(4321),
	})
	wantT2 := map[string]any{
		"hottell.enrich_status": "found", "hottell.turn.responses": int64(1),
		"hottell.turn.input_tokens": int64(50), "hottell.turn.cached_input_tokens": int64(0),
		"hottell.turn.output_tokens": int64(5), "hottell.turn.reasoning_output_tokens": int64(0),
		"hottell.turn.model": "gpt-next",
	}
	wantAttrs(t, "T2 young", events[1].Payload, wantT2)
	wantAttrs(t, "T2 old", events[2].Payload, wantT2)
	wantAttrs(t, "missing", events[3].Payload, map[string]any{"hottell.enrich_status": "not_found"})
	if out[0].hold || !out[1].hold || out[2].hold || out[3].hold || !out[4].hold {
		t.Fatalf("hold decisions: %+v", out)
	}
}

func TestEnrichStopOldFormatTokenCount(t *testing.T) {
	dir := codexHome(t)
	path := writeLines(t, filepath.Join(dir, "rollout-old-turn.jsonl"),
		taskStarted("A"),
		turnContext("A", "gpt-old"),
		tokenCountLine(nil, nil),
		tokenCountLine(tokens(100, 0, 0, 0), tokens(100, 0, 0, 0)),
		taskComplete("A", 10),
		taskStarted("B"),
		turnContext("", "gpt-old-b"), // без turn_id, как в ранних rollout
		tokenCountLine(tokens(100, 0, 0, 0), tokens(100, 0, 0, 0)), // опрос: повтор прошлого total
		tokenCountLine(tokens(160, 20, 10, 2), tokens(50, 20, 10, 2)),
		tokenCountLine(tokens(160, 20, 10, 2), tokens(50, 20, 10, 2)), // опрос
		tokenCountLine(nil, nil),
		tokenCountLine(tokens(250, 50, 20, 3), tokens(80, 30, 10, 1)),
		taskComplete("B", 999),
		taskStarted("C"),
		tokenCountLine(tokens(250, 50, 20, 3), tokens(80, 30, 10, 1)),
		tokenCountLine(tokens(300, 50, 25, 3), tokens(45, 0, 5, 0)),
	)
	events := []event{codexEvent("Stop", path, longAgo, "turn_id", "B")}
	enrichEvents(enrichCfg(t), events, time.Now())
	wantAttrs(t, "B", events[0].Payload, map[string]any{
		"hottell.enrich_status": "found", "hottell.turn.responses": int64(2),
		"hottell.turn.input_tokens": int64(130), "hottell.turn.cached_input_tokens": int64(50),
		"hottell.turn.output_tokens": int64(20), "hottell.turn.reasoning_output_tokens": int64(3),
		"hottell.turn.model": "gpt-old-b", "hottell.turn.duration_ms": int64(999),
	})
}

func withEnrichWindows(t *testing.T, first, maxChunk, readCap int64) {
	t.Helper()
	f, m, c := enrichFirstChunk, enrichMaxChunk, enrichReadCap
	enrichFirstChunk, enrichMaxChunk, enrichReadCap = first, maxChunk, readCap
	t.Cleanup(func() { enrichFirstChunk, enrichMaxChunk, enrichReadCap = f, m, c })
}

func TestEnrichTailCapPartial(t *testing.T) {
	withEnrichWindows(t, 1024, 2048, 8*1024)
	dir := codexHome(t)
	filler := respItem("type", "reasoning", "summary", []any{obj("type", "summary_text", "text", strings.Repeat("x", 600))})
	lines := []any{
		taskStarted("T1"),
		turnContext("T1", "gpt-test"),
		usageRecord("T1", "r1", tokens(100, 0, 10, 0), tokens(100, 0, 10, 0)),
	}
	for i := 0; i < 40; i++ {
		lines = append(lines, filler)
	}
	lines = append(lines,
		usageRecord("T1", "r2", tokens(200, 100, 20, 2), tokens(300, 100, 30, 2)),
		usageRecord("T1", "r3", tokens(300, 250, 30, 3), tokens(600, 350, 60, 5)),
		taskComplete("T1", 777),
	)
	path := writeLines(t, filepath.Join(dir, "rollout-big.jsonl"), lines...)
	events := []event{
		codexEvent("Stop", path, longAgo, "turn_id", "T1"),
		codexEvent("PostToolUse", path, longAgo, "tool_use_id", "exec-early", "turn_id", "T1"),
	}
	enrichEvents(enrichCfg(t), events, time.Now())
	// Начало хода за потолком: ответы посчитаны частично, суммы — из
	// накопительного turn_token_usage последней записи, модели нет.
	wantAttrs(t, "partial", events[0].Payload, map[string]any{
		"hottell.enrich_status": "partial", "hottell.turn.responses": int64(2),
		"hottell.turn.input_tokens": int64(600), "hottell.turn.cached_input_tokens": int64(350),
		"hottell.turn.output_tokens": int64(60), "hottell.turn.reasoning_output_tokens": int64(5),
		"hottell.turn.duration_ms": int64(777),
	})
	wantAttrs(t, "tool beyond cap", events[1].Payload, map[string]any{"hottell.enrich_status": "not_found"})
}

func TestEnrichPathValidation(t *testing.T) {
	dir := codexHome(t)
	home := os.Getenv("HOME")
	content := []any{taskStarted("T1"), itemDone("T1", obj("type", "CommandExecution", "id", "exec-x", "status", "completed", "exit_code", 0))}
	inside := writeLines(t, filepath.Join(dir, "rollout-ok.jsonl"), content...)
	outside := writeLines(t, filepath.Join(t.TempDir(), "rollout-out.jsonl"), content...)
	link := filepath.Join(dir, "rollout-link.jsonl")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	notJSONL := writeLines(t, filepath.Join(dir, "rollout.txt"), content...)
	dirPath := filepath.Join(dir, "rollout-dir.jsonl")
	if err := os.Mkdir(dirPath, 0o700); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ path, want string }{
		{inside, "found"},
		{outside, "error"},
		{link, "error"},
		{"sessions/rollout-ok.jsonl", "error"},
		{filepath.Join(home, ".codex", "sessions", "..", "..", "outside.jsonl"), "error"},
		{home + "/.codex/sessions//2026/01/01/rollout-ok.jsonl", "error"},
		{notJSONL, "error"},
		{dirPath, "error"},
		{filepath.Join(dir, "rollout-missing.jsonl"), "no_transcript"},
		{"", "no_transcript"},
	}
	for _, c := range cases {
		events := []event{codexEvent("PostToolUse", c.path, time.Now(), "tool_use_id", "exec-x", "turn_id", "T1")}
		out := enrichEvents(enrichCfg(t), events, time.Now())
		if got := events[0].Payload[keyEnrichStatus]; got != c.want {
			t.Errorf("%q: enrich_status=%v, want %s", c.path, got, c.want)
		}
		if out[0].hold {
			t.Errorf("%q: held", c.path)
		}
		if c.want != "found" && len(hottellKeys(events[0].Payload)) != 1 {
			t.Errorf("%q: leaked attributes %v", c.path, hottellKeys(events[0].Payload))
		}
	}
}

func TestEnrichLeavesOtherEventsUntouched(t *testing.T) {
	dir := codexHome(t)
	path := writeLines(t, filepath.Join(dir, "rollout-c.jsonl"), taskStarted("T1"))
	events := []event{
		{TS: 1, Agent: "claude", Event: "PostToolUse", Payload: map[string]any{"transcript_path": path, "tool_use_id": "toolu_1"}},
		{TS: 1, Agent: "claude", Event: "Stop", Payload: map[string]any{"transcript_path": path, "turn_id": "T1"}},
		codexEvent("PreToolUse", path, longAgo, "tool_use_id", "exec-1", "turn_id", "T1"),
		codexEvent("SessionStart", path, longAgo),
		{TS: 1, Agent: "codex", Event: "Stop"}, // payload nil
	}
	before, _ := json.Marshal(events)
	out := enrichEvents(enrichCfg(t), events, time.Now())
	after, _ := json.Marshal(events)
	if !bytes.Equal(before, after) {
		t.Fatalf("payload changed:\n%s\n%s", before, after)
	}
	for i, o := range out {
		if o.hold || o.changed {
			t.Fatalf("event %d: %+v", i, o)
		}
	}
}

func TestEnrichDisabledByConfig(t *testing.T) {
	dir := codexHome(t)
	path := writeLines(t, filepath.Join(dir, "rollout-d.jsonl"),
		taskStarted("T1"), itemDone("T1", obj("type", "CommandExecution", "id", "exec-1", "status", "failed", "exit_code", 1)))
	cfg := enrichCfg(t)
	cfg.EnrichTranscript = false
	events := []event{
		codexEvent("PostToolUse", path, time.Now(), "tool_use_id", "exec-1", "turn_id", "T1"),
		codexEvent("Stop", path, time.Now(), "turn_id", "T1"),
	}
	out := enrichEvents(cfg, events, time.Now())
	for i, ev := range events {
		wantAttrs(t, ev.Event, ev.Payload, map[string]any{"hottell.enrich_status": "disabled"})
		if out[i].hold {
			t.Fatal("disabled enrichment must not hold events")
		}
	}
}

func TestEnrichConfigDefaultAndOverride(t *testing.T) {
	if !loadConfig(filepath.Join(t.TempDir(), "missing.json")).EnrichTranscript {
		t.Fatal("enrich_transcript must default to true")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"enrich_transcript": false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if loadConfig(path).EnrichTranscript {
		t.Fatal("enrich_transcript=false ignored")
	}
}

// Байт-в-байт: обратное чтение мелкими кусками отдаёт те же строки, что и
// прямое, включая строки длиннее куска и файл без финального перевода строки.
func TestScanLinesBackwardChunkBoundaries(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 50; trial++ {
		var lines []string
		for i := 0; i < 1+rng.Intn(40); i++ {
			n := rng.Intn(90)
			if rng.Intn(10) == 0 {
				n = 200 + rng.Intn(300) // длиннее любого куска
			}
			lines = append(lines, fmt.Sprintf("%d:%s", i, strings.Repeat(string(rune('a'+i%26)), n)))
		}
		data := strings.Join(lines, "\n")
		if trial%2 == 0 {
			data += "\n"
		}
		withEnrichWindows(t, int64(1+rng.Intn(16)), int64(16+rng.Intn(64)), 1<<30)
		var got []string
		capHit, err := scanLinesBackward(strings.NewReader(data), int64(len(data)), func(l []byte) bool {
			got = append(got, string(l))
			return true
		})
		if err != nil || capHit {
			t.Fatalf("trial %d: cap=%v err=%v", trial, capHit, err)
		}
		for i, j := 0, len(got)-1; i < j; i, j = i+1, j-1 {
			got[i], got[j] = got[j], got[i]
		}
		if !reflect.DeepEqual(got, lines) {
			t.Fatalf("trial %d: lines differ\n got %q\nwant %q", trial, got, lines)
		}
	}

	// Потолок: отдаются только целые строки с конца, capHit=true.
	withEnrichWindows(t, 8, 8, 20)
	data := "first-line-long\nsecond\nthird\nfourth\n"
	var got []string
	capHit, err := scanLinesBackward(strings.NewReader(data), int64(len(data)), func(l []byte) bool {
		got = append(got, string(l))
		return true
	})
	if err != nil || !capHit || !reflect.DeepEqual(got, []string{"fourth", "third"}) {
		t.Fatalf("cap: got %q cap=%v err=%v", got, capHit, err)
	}

	// Укороченный под нами файл — ошибка, а не мусор.
	_, err = scanLinesBackward(strings.NewReader("short\n"), 1000, func([]byte) bool { return true })
	if err == nil {
		t.Fatal("truncated file must report an error")
	}
}

// ---------- через дрейнер: гонка с записью rollout ----------

type capturedRecord struct {
	at    time.Time
	body  string
	attrs map[string]any
}

type collector struct {
	mu      sync.Mutex
	records []capturedRecord
}

func (c *collector) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var otlp map[string]any
		if err := json.Unmarshal(raw, &otlp); err != nil {
			t.Errorf("collector: %v", err)
			return
		}
		now := time.Now()
		rl := otlp["resourceLogs"].([]any)[0].(map[string]any)
		for _, rec := range rl["scopeLogs"].([]any)[0].(map[string]any)["logRecords"].([]any) {
			m := rec.(map[string]any)
			c.mu.Lock()
			c.records = append(c.records, capturedRecord{at: now, body: m["body"].(map[string]any)["stringValue"].(string), attrs: attrMap(t, m["attributes"].([]any))})
			c.mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}
}

func (c *collector) find(body string) (capturedRecord, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.records {
		if r.body == body {
			return r, true
		}
	}
	return capturedRecord{}, false
}

func withGrace(t *testing.T, grace, retry time.Duration) {
	t.Helper()
	g, r := enrichGrace, enrichRetryBase
	enrichGrace, enrichRetryBase = grace, retry
	t.Cleanup(func() { enrichGrace, enrichRetryBase = g, r })
}

func spoolEvent(t *testing.T, cfg Config, ev event) {
	t.Helper()
	raw, _ := json.Marshal(ev)
	if err := spoolWrite(cfg, raw); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
}

func drainAndWait(t *testing.T, cfg Config, timeout time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() { runDrain(cfg); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatal("drainer did not finish")
	}
}

func attrString(a any) string {
	m, _ := a.(map[string]any)
	if s, ok := m["stringValue"].(string); ok {
		return s
	}
	if s, ok := m["intValue"].(string); ok {
		return s
	}
	return fmt.Sprint(a)
}

func TestDrainHoldsYoungNotFoundThenShipsIt(t *testing.T) {
	withGrace(t, 600*time.Millisecond, 50*time.Millisecond)
	dir := codexHome(t)
	path := writeLines(t, filepath.Join(dir, "rollout-race.jsonl"), taskStarted("T1"))
	col := &collector{}
	srv := httptest.NewServer(col.handler(t))
	defer srv.Close()
	cfg := enrichCfg(t)
	cfg.Endpoint, cfg.LingerSec = srv.URL, 0

	start := time.Now()
	spoolEvent(t, cfg, codexEvent("PostToolUse", path, start, "tool_use_id", "exec-never", "turn_id", "T1"))
	spoolEvent(t, cfg, event{TS: start.UnixNano(), Agent: "claude", Event: "Stop", Payload: map[string]any{"session_id": "c"}})
	drainAndWait(t, cfg, 5*time.Second)

	claude, ok1 := col.find("agent.hook.Stop")
	codex, ok2 := col.find("agent.hook.PostToolUse")
	if !ok1 || !ok2 {
		t.Fatalf("delivered: %+v", col.records)
	}
	if claude.at.Sub(start) > 400*time.Millisecond {
		t.Fatalf("held entry blocked the rest of the spool: %s", claude.at.Sub(start))
	}
	if codex.at.Sub(start) < 600*time.Millisecond {
		t.Fatalf("young event shipped before the grace period: %s", codex.at.Sub(start))
	}
	if s := attrString(codex.attrs["hottell.enrich_status"]); s != "not_found" {
		t.Fatalf("enrich_status: %s", s)
	}
	if left := spoolList(cfg); len(left) != 0 {
		t.Fatalf("spool not empty: %d", len(left))
	}
	state, err := loadDeliveryState(cfg)
	if err != nil || state.Sessions["s-enrich"].LastAcceptedAt == "" || state.Sessions["s-enrich"].Dropped != 0 {
		t.Fatalf("delivery state: %+v %v", state, err)
	}
}

func TestDrainHeldEventPicksUpLateRecord(t *testing.T) {
	withGrace(t, 3*time.Second, 50*time.Millisecond)
	dir := codexHome(t)
	path := writeLines(t, filepath.Join(dir, "rollout-late.jsonl"), taskStarted("T1"))
	col := &collector{}
	srv := httptest.NewServer(col.handler(t))
	defer srv.Close()
	cfg := enrichCfg(t)
	cfg.Endpoint, cfg.LingerSec = srv.URL, 0

	start := time.Now()
	spoolEvent(t, cfg, codexEvent("PostToolUse", path, start, "tool_use_id", "exec-late", "turn_id", "T1"))
	spoolEvent(t, cfg, codexEvent("Stop", path, start, "turn_id", "T1"))
	go func() {
		time.Sleep(150 * time.Millisecond)
		if err := appendLines(path,
			itemDone("T1", obj("type", "CommandExecution", "id", "exec-late", "status", "failed", "exit_code", 5)),
			usageRecord("T1", "r1", tokens(10, 0, 1, 0), tokens(10, 0, 1, 0)),
			taskComplete("T1", 321)); err != nil {
			t.Error(err)
		}
	}()
	drainAndWait(t, cfg, 5*time.Second)

	tool, ok1 := col.find("agent.hook.PostToolUse")
	stop, ok2 := col.find("agent.hook.Stop")
	if !ok1 || !ok2 {
		t.Fatalf("delivered: %+v", col.records)
	}
	if attrString(tool.attrs["hottell.enrich_status"]) != "found" || attrString(tool.attrs["hottell.exit_code"]) != "5" {
		t.Fatalf("tool attrs: %v", tool.attrs)
	}
	if attrString(stop.attrs["hottell.turn.duration_ms"]) != "321" || attrString(stop.attrs["hottell.turn.responses"]) != "1" {
		t.Fatalf("stop attrs: %v", stop.attrs)
	}
	if tool.at.Sub(start) > 2*time.Second {
		t.Fatalf("late record picked up too late: %s", tool.at.Sub(start))
	}
}

func TestDrainKeepsEnrichmentAcrossSendFailure(t *testing.T) {
	dir := codexHome(t)
	path := writeLines(t, filepath.Join(dir, "rollout-fail.jsonl"),
		taskStarted("T1"), itemDone("T1", obj("type", "CommandExecution", "id", "exec-1", "status", "failed", "exit_code", 4)))
	cfg := enrichCfg(t)
	cfg.Endpoint = "http://127.0.0.1:1"
	cfg.LingerSec, cfg.BackoffMaxSec = 0, 0
	spoolEvent(t, cfg, codexEvent("PostToolUse", path, longAgo, "tool_use_id", "exec-1", "turn_id", "T1"))

	done := make(chan struct{})
	go func() { runDrain(cfg); close(done) }()
	time.Sleep(300 * time.Millisecond)
	entries := spoolList(cfg)
	if len(entries) != 1 {
		t.Fatalf("spool: %d", len(entries))
	}
	raw, _ := os.ReadFile(entries[0].path)
	var ev event
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Payload[keyEnrichStatus] != "found" || ev.Payload["hottell.exit_code"] != float64(4) {
		t.Fatalf("enrichment not persisted: %v", hottellKeys(ev.Payload))
	}
	// Уже обогащённое событие повторно не трогается.
	if out := enrichEvents(cfg, []event{ev}, time.Now()); out[0].changed || out[0].hold {
		t.Fatalf("re-enriched: %+v", out)
	}
	for _, e := range spoolList(cfg) {
		_ = os.Remove(e.path)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("drainer did not finish")
	}
}

type countingReader struct {
	r    io.ReaderAt
	read int64
}

func (c *countingReader) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.r.ReadAt(p, off)
	c.read += int64(n)
	return n, err
}

// Файл не читается целиком: поиск останавливается на начале хода события,
// даже если нужный вызов так и не нашёлся.
func TestEnrichReadsOnlyTheTurnTail(t *testing.T) {
	withEnrichWindows(t, 4<<10, 16<<10, 1<<30)
	var b bytes.Buffer
	add := func(l json.RawMessage) { b.Write(l); b.WriteByte('\n') }
	add(taskStarted("T0"))
	filler := respItem("type", "reasoning", "summary", []any{obj("type", "summary_text", "text", strings.Repeat("y", 1000))})
	for i := 0; i < 400; i++ {
		add(filler)
	}
	add(itemDone("T0", obj("type", "CommandExecution", "id", "exec-old", "status", "completed", "exit_code", 0)))
	add(taskComplete("T0", 1))
	add(taskStarted("T1"))
	add(turnContext("T1", "gpt-test"))
	add(usageRecord("T1", "r1", tokens(10, 0, 1, 0), tokens(10, 0, 1, 0)))
	add(itemDone("T1", obj("type", "CommandExecution", "id", "exec-1", "status", "completed", "exit_code", 0)))
	data := b.Bytes()

	scan := newRolloutScan()
	scan.wantTool("exec-1", "T1")
	scan.wantTool("exec-missing", "T1")
	scan.wantTurn("T1")
	r := &countingReader{r: bytes.NewReader(data)}
	capHit, err := scanLinesBackward(r, int64(len(data)), scan.handle)
	if err != nil || capHit {
		t.Fatalf("cap=%v err=%v", capHit, err)
	}
	if r.read > 8<<10 {
		t.Fatalf("read %d of %d bytes: the scan did not stop at the turn start", r.read, len(data))
	}
	if !scan.tools["exec-1"].found || scan.tools["exec-missing"].found || !scan.turns["T1"].started {
		t.Fatalf("scan state: %+v %+v", scan.tools, scan.turns)
	}
}
