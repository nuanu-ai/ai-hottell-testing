package otlp_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/hook"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/otlp"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/queue"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/transcript"
)

// contract is the document whose examples the encoder must reproduce.
const contract = "../../../docs/specs/hottell-contract/ingest.md"

var res = otlp.Resource{Version: "0.1.0", HostName: "dev-mac.local"} //nolint:gochecknoglobals // test fixture

// contractExamples returns the requests of ingest.md «Примеры», in the document's order:
// Claude hook, Codex hook, transcript line, history line.
func contractExamples(t *testing.T) []*collogspb.ExportLogsServiceRequest {
	t.Helper()
	doc, err := os.ReadFile(contract)
	if err != nil {
		t.Fatal(err)
	}
	blocks := regexp.MustCompile("(?s)```json\n(.*?)```").FindAllSubmatch(doc, -1)
	if len(blocks) != 4 {
		t.Fatalf("ingest.md has %d JSON examples, want 4", len(blocks))
	}
	out := make([]*collogspb.ExportLogsServiceRequest, len(blocks))
	for i, b := range blocks {
		out[i] = &collogspb.ExportLogsServiceRequest{}
		if err := protojson.Unmarshal(b[1], out[i]); err != nil {
			t.Fatalf("example %d: %v", i+1, err)
		}
	}
	return out
}

// only returns the one LogRecord of an example request.
func only(t *testing.T, req *collogspb.ExportLogsServiceRequest) *logspb.LogRecord {
	t.Helper()
	if len(req.GetResourceLogs()) != 1 || len(req.GetResourceLogs()[0].GetScopeLogs()) != 1 ||
		len(req.GetResourceLogs()[0].GetScopeLogs()[0].GetLogRecords()) != 1 {
		t.Fatalf("request does not hold exactly one record: %v", req)
	}
	return req.GetResourceLogs()[0].GetScopeLogs()[0].GetLogRecords()[0]
}

func attr(lr *logspb.LogRecord, key string) *commonpb.AnyValue {
	for _, kv := range lr.GetAttributes() {
		if kv.GetKey() == key {
			return kv.GetValue()
		}
	}
	return nil
}

// hookRecord is the queue record hottell hook writes for the example's event.
func hookRecord(t *testing.T, agent policy.Agent, lr *logspb.LogRecord) queue.Record {
	t.Helper()
	return hookRecordAt(t, agent, []byte(lr.GetBody().GetStringValue()), int64(lr.GetTimeUnixNano())) //nolint:gosec // example times fit
}

func hookRecordAt(t *testing.T, agent policy.Agent, event []byte, received int64) queue.Record {
	t.Helper()
	kind, err := json.Marshal(hook.Meta{Kind: hook.KindHook, Agent: agent, ReceivedUnixNano: received})
	if err != nil {
		t.Fatal(err)
	}
	return queue.Record{ID: "hook", Kind: kind, Payload: event}
}

// transcriptRecord is the queue record a transcript reader writes for the example's line.
func transcriptRecord(t *testing.T, lr *logspb.LogRecord) queue.Record {
	t.Helper()
	line := []byte(lr.GetBody().GetStringValue())
	return transcriptRecordOf(t, transcript.Meta{
		Source:           transcript.Source,
		Agent:            attr(lr, "agent").GetStringValue(),
		SessionID:        attr(lr, "session.id").GetStringValue(),
		Kind:             attr(lr, "transcript.kind").GetStringValue(),
		Path:             attr(lr, "transcript.path").GetStringValue(),
		Offset:           1234,
		Line:             attr(lr, "transcript.line").GetIntValue(),
		Backfill:         attr(lr, "backfill").GetBoolValue(),
		ObservedUnixNano: int64(lr.GetObservedTimeUnixNano()), //nolint:gosec // example times fit
	}, line)
}

func transcriptRecordOf(t *testing.T, m transcript.Meta, line []byte) queue.Record {
	t.Helper()
	kind, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return queue.Record{ID: "line", Kind: kind, Payload: line}
}

func encode(t *testing.T, recs []queue.Record, observed time.Time) *collogspb.ExportLogsServiceRequest {
	t.Helper()
	data, err := otlp.Encode(recs, res, observed)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	req := &collogspb.ExportLogsServiceRequest{}
	if err := proto.Unmarshal(data, req); err != nil {
		t.Fatalf("decode the encoded request: %v", err)
	}
	return req
}

func TestContractExamples(t *testing.T) {
	t.Parallel()

	examples := contractExamples(t)
	cases := []struct {
		name   string
		record func(*logspb.LogRecord) queue.Record
	}{
		{"claude hook", func(lr *logspb.LogRecord) queue.Record { return hookRecord(t, policy.Claude, lr) }},
		{"codex hook", func(lr *logspb.LogRecord) queue.Record { return hookRecord(t, policy.Codex, lr) }},
		{"transcript line", func(lr *logspb.LogRecord) queue.Record { return transcriptRecord(t, lr) }},
		{"history line", func(lr *logspb.LogRecord) queue.Record { return transcriptRecord(t, lr) }},
	}
	for i, c := range cases {
		want := examples[i]
		lr := only(t, want)
		observed := time.Unix(0, int64(lr.GetObservedTimeUnixNano())) //nolint:gosec // example times fit
		got := encode(t, []queue.Record{c.record(lr)}, observed)

		if !proto.Equal(got, want) {
			t.Errorf("%s:\n got %s\nwant %s", c.name, protojson.Format(got), protojson.Format(want))
			continue
		}
		// The body goes byte for byte as the queue holds it.
		if gotBody := only(t, got).GetBody().GetStringValue(); gotBody != lr.GetBody().GetStringValue() {
			t.Errorf("%s: body changed", c.name)
		}
	}
}

func TestHistoryTimeFromLine(t *testing.T) {
	t.Parallel()

	// The history example's timeUnixNano is the line's own timestamp.
	lr := only(t, contractExamples(t)[3])
	want := time.Date(2026, 9, 21, 8, 2, 11, 500_000_000, time.UTC)
	if lr.GetTimeUnixNano() != uint64(want.UnixNano()) { //nolint:gosec // positive
		t.Fatalf("example time %d, want %d", lr.GetTimeUnixNano(), want.UnixNano())
	}
}

func TestBatchGroupsByServiceAndAgent(t *testing.T) {
	t.Parallel()

	ex := contractExamples(t)
	claudeHook := hookRecord(t, policy.Claude, only(t, ex[0]))
	codexHook := hookRecord(t, policy.Codex, only(t, ex[1]))
	claudeLine := transcriptRecord(t, only(t, ex[2]))
	codexLine := transcriptRecord(t, only(t, ex[3]))

	got := encode(t, []queue.Record{claudeHook, claudeLine, codexHook, claudeHook, codexLine}, time.Unix(1, 0))
	type group struct {
		service, agent string
		n              int
	}
	var groups []group
	for _, rl := range got.GetResourceLogs() {
		var service string
		for _, kv := range rl.GetResource().GetAttributes() {
			if kv.GetKey() == "hottell.user.id" {
				t.Error("resource carries hottell.user.id")
			}
			if kv.GetKey() == "service.name" {
				service = kv.GetValue().GetStringValue()
			}
		}
		if len(rl.GetScopeLogs()) != 1 {
			t.Fatalf("%d ScopeLogs in a ResourceLogs", len(rl.GetScopeLogs()))
		}
		recs := rl.GetScopeLogs()[0].GetLogRecords()
		agent := attr(recs[0], "agent").GetStringValue()
		for _, r := range recs {
			if a := attr(r, "agent").GetStringValue(); a != agent {
				t.Errorf("group of %s holds a record of %s", agent, a)
			}
		}
		groups = append(groups, group{service, agent, len(recs)})
	}
	want := []group{
		{otlp.ServiceHooks, "claude", 2},
		{otlp.ServiceTranscripts, "claude", 1},
		{otlp.ServiceHooks, "codex", 1},
		{otlp.ServiceTranscripts, "codex", 1},
	}
	if len(groups) != len(want) {
		t.Fatalf("groups = %+v, want %+v", groups, want)
	}
	for i := range want {
		if groups[i] != want[i] {
			t.Errorf("group %d = %+v, want %+v", i, groups[i], want[i])
		}
	}
}

func TestLargeRecordIsNotTruncated(t *testing.T) {
	t.Parallel()

	output := strings.Repeat("0123456789", 1_000_000) // 10 MB
	event := []byte(`{"session_id":"s","hook_event_name":"PostToolUse","cwd":"/w","tool_response":"` + output + `"}`)
	line := []byte(`{"timestamp":"2026-09-30T10:15:30Z","type":"response_item","payload":"` + output + `"}`)
	got := encode(t, []queue.Record{
		hookRecordAt(t, policy.Codex, event, 1),
		transcriptRecordOf(t, transcript.Meta{Source: transcript.Source, Agent: "codex", SessionID: "s", Kind: "main", Path: "p.jsonl", Line: 1}, line),
	}, time.Unix(1, 0))

	bodies := [][]byte{event, line}
	for i, rl := range got.GetResourceLogs() {
		b := rl.GetScopeLogs()[0].GetLogRecords()[0].GetBody().GetStringValue()
		if !bytes.Equal([]byte(b), bodies[i]) {
			t.Errorf("record %d: body of %d bytes, want %d bytes unchanged", i, len(b), len(bodies[i]))
		}
	}
}

func TestInvalidUTF8GoesAsBytes(t *testing.T) {
	t.Parallel()

	line := []byte("{\"text\":\"\xff\xfe\"}")
	got := encode(t, []queue.Record{
		transcriptRecordOf(t, transcript.Meta{Source: transcript.Source, Agent: "claude", SessionID: "s", Kind: "main", Path: "p.jsonl", Line: 3}, line),
	}, time.Unix(1, 0))
	lr := only(t, got)
	if _, ok := lr.GetBody().GetValue().(*commonpb.AnyValue_BytesValue); !ok {
		t.Fatalf("body = %T, want bytesValue", lr.GetBody().GetValue())
	}
	if !bytes.Equal(lr.GetBody().GetBytesValue(), line) {
		t.Error("bytes body changed")
	}
}

func TestHookAttributes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		event string
		want  map[string]string
	}{
		{
			name:  "null and non-string fields are absent",
			event: `{"session_id":"s","hook_event_name":"Stop","cwd":"/w","prompt_id":null,"tool_use_id":7,"turn_id":{"a":1}}`,
			want:  map[string]string{"agent": "claude", "session.id": "s", "hook.event_name": "Stop", "cwd": "/w"},
		},
		{
			name:  "damaged event keeps the fields it has",
			event: `{"session_id":"s","hook_event_name":42,"turn_id":"t"}`,
			want:  map[string]string{"agent": "claude", "session.id": "s", "turn_id": "t"},
		},
		{
			name:  "not a JSON object",
			event: `not json`,
			want:  map[string]string{"agent": "claude"},
		},
		{
			name:  "nested fields are not copied",
			event: `{"session_id":"s","hook_event_name":"PostToolBatch","cwd":"/w","tool_calls":[{"tool_use_id":"x"}]}`,
			want:  map[string]string{"agent": "claude", "session.id": "s", "hook.event_name": "PostToolBatch", "cwd": "/w"},
		},
	}
	for _, c := range cases {
		lr := only(t, encode(t, []queue.Record{hookRecordAt(t, policy.Claude, []byte(c.event), 5)}, time.Unix(0, 9)))
		got := map[string]string{}
		for _, kv := range lr.GetAttributes() {
			got[kv.GetKey()] = kv.GetValue().GetStringValue()
		}
		if len(got) != len(c.want) {
			t.Errorf("%s: attributes %v, want %v", c.name, got, c.want)
			continue
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Errorf("%s: %s = %q, want %q", c.name, k, got[k], v)
			}
		}
		if lr.GetBody().GetStringValue() != c.event {
			t.Errorf("%s: body changed", c.name)
		}
		if lr.GetTimeUnixNano() != 5 || lr.GetObservedTimeUnixNano() != 9 {
			t.Errorf("%s: times %d/%d, want 5/9", c.name, lr.GetTimeUnixNano(), lr.GetObservedTimeUnixNano())
		}
	}
}

func TestTranscriptLineWithoutTime(t *testing.T) {
	t.Parallel()

	for _, line := range []string{`{"type":"summary"}`, `{"timestamp":"yesterday"}`, `{"timestamp":17}`, `not json`} {
		lr := only(t, encode(t, []queue.Record{
			transcriptRecordOf(t, transcript.Meta{
				Source: transcript.Source, Agent: "claude", SessionID: "s", Kind: "subagent",
				Path: "p/s/subagents/agent-a.jsonl", Line: 2, ObservedUnixNano: 77,
			}, []byte(line)),
		}, time.Unix(1, 0)))
		if lr.GetTimeUnixNano() != 0 {
			t.Errorf("%s: timeUnixNano = %d, want 0", line, lr.GetTimeUnixNano())
		}
		if lr.GetObservedTimeUnixNano() != 77 {
			t.Errorf("%s: observedTimeUnixNano = %d, want 77", line, lr.GetObservedTimeUnixNano())
		}
	}
}

func TestUnknownKind(t *testing.T) {
	t.Parallel()

	for i, kind := range []string{`{"kind":"other"}`, `{}`, `garbage`} {
		_, err := otlp.Encode([]queue.Record{{ID: "r" + strconv.Itoa(i), Kind: []byte(kind), Payload: []byte("{}")}}, res, time.Unix(1, 0))
		if err == nil {
			t.Errorf("%s: Encode succeeded", kind)
		}
		if i < 2 && !errors.Is(err, otlp.ErrUnknownKind) {
			t.Errorf("%s: err = %v, want ErrUnknownKind", kind, err)
		}
	}
}

func TestRecordSizeIsTheLogRecordInProtobuf(t *testing.T) {
	t.Parallel()

	observed := time.Unix(1, 0)
	rec := hookRecordAt(t, policy.Claude, []byte(`{"session_id":"s","hook_event_name":"Stop","cwd":"/w"}`), 1)
	got, err := otlp.RecordSize(rec, observed)
	if err != nil {
		t.Fatalf("RecordSize: %v", err)
	}
	if want := proto.Size(only(t, encode(t, []queue.Record{rec}, observed))); got != want {
		t.Errorf("RecordSize = %d, want proto.Size of the encoded LogRecord %d", got, want)
	}

	if _, err := otlp.RecordSize(queue.Record{ID: "r", Kind: []byte(`{}`)}, observed); !errors.Is(err, otlp.ErrUnknownKind) {
		t.Errorf("RecordSize of an unknown kind: err = %v, want ErrUnknownKind", err)
	}
}

func TestStatusMessage(t *testing.T) {
	t.Parallel()

	// google.rpc.Status{code: 3, message: "bad body"} in protobuf.
	pb := []byte{0x08, 0x03, 0x12, 0x08, 'b', 'a', 'd', ' ', 'b', 'o', 'd', 'y'}
	for _, tc := range []struct {
		name, contentType string
		body              []byte
		want              string
	}{
		{"protobuf", "application/x-protobuf", pb, "bad body"},
		{"json", "application/json; charset=utf-8", []byte(`{"code":16,"message":"token revoked"}`), "token revoked"},
		{"empty", "application/x-protobuf", nil, ""},
		{"not protobuf", "text/plain", []byte("<html>"), ""},
		{"not json", "application/json", []byte("<html>"), ""},
	} {
		if got := otlp.StatusMessage(tc.body, tc.contentType); got != tc.want {
			t.Errorf("%s: StatusMessage = %q, want %q", tc.name, got, tc.want)
		}
	}
}
