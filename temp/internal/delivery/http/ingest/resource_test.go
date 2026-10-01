package ingest_test

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/ingest"
)

// Synthetic trace and span ids, in the hex of OTLP JSON.
const (
	traceIDHex = "5b8efff798038103d269b633813fc60c"
	spanIDHex  = "eee19b7ec3c1b174"
)

// resourceAttrs is the resource of the first entry of every request below: a forged
// hottell.user.id among ordinary attributes. The second entry of every request has no
// resource at all.
const resourceAttrs = `"resource":{"attributes":[` +
	`{"key":"service.name","value":{"stringValue":"claude-code"}},` +
	`{"key":"hottell.user.id","value":{"stringValue":"00000000-0000-4000-8000-0000000000ff"}},` +
	`{"key":"hottell.user.id","value":{"intValue":"7"}}]}`

// signalRequests are an Export…ServiceRequest of each signal in the JSON encoding of OTLP.
type signalRequest struct {
	path      string
	json      string
	newMsg    func() proto.Message
	resources func(proto.Message) []*resourcepb.Resource
}

func signalRequests() []signalRequest {
	return []signalRequest{
		{
			path: "/v1/logs",
			json: `{"resourceLogs":[{` + resourceAttrs + `,"scopeLogs":[{"logRecords":[{"body":{"stringValue":"synthetic"},` +
				`"traceId":"` + traceIDHex + `","spanId":"` + spanIDHex + `"}]}]},{"scopeLogs":[]}]}`,
			newMsg: func() proto.Message { return &collogspb.ExportLogsServiceRequest{} },
			resources: func(m proto.Message) []*resourcepb.Resource {
				var out []*resourcepb.Resource
				for _, rl := range m.(*collogspb.ExportLogsServiceRequest).GetResourceLogs() {
					out = append(out, rl.GetResource())
				}
				return out
			},
		},
		{
			path: "/v1/metrics",
			json: `{"resourceMetrics":[{` + resourceAttrs + `,"scopeMetrics":[{"metrics":[{"name":"synthetic.count",` +
				`"sum":{"dataPoints":[{"asInt":"1","exemplars":[{"asInt":"1","traceId":"` + traceIDHex + `","spanId":"` + spanIDHex + `"}]}]}}]}]},` +
				`{"scopeMetrics":[]}]}`,
			newMsg: func() proto.Message { return &colmetricspb.ExportMetricsServiceRequest{} },
			resources: func(m proto.Message) []*resourcepb.Resource {
				var out []*resourcepb.Resource
				for _, rm := range m.(*colmetricspb.ExportMetricsServiceRequest).GetResourceMetrics() {
					out = append(out, rm.GetResource())
				}
				return out
			},
		},
		{
			path: "/v1/traces",
			json: `{"resourceSpans":[{` + resourceAttrs + `,"scopeSpans":[{"spans":[{"name":"synthetic",` +
				`"traceId":"` + traceIDHex + `","spanId":"` + spanIDHex + `","parentSpanId":"` + spanIDHex + `"}]}]},` +
				`{"scopeSpans":[]}]}`,
			newMsg: func() proto.Message { return &coltracepb.ExportTraceServiceRequest{} },
			resources: func(m proto.Message) []*resourcepb.Resource {
				var out []*resourcepb.Resource
				for _, rs := range m.(*coltracepb.ExportTraceServiceRequest).GetResourceSpans() {
					out = append(out, rs.GetResource())
				}
				return out
			},
		},
	}
}

// base64IDs rewrites the hex ids of an OTLP JSON request into the base64 protojson reads.
func base64IDs(t *testing.T, s string) string {
	t.Helper()
	for _, id := range []string{traceIDHex, spanIDHex} {
		b, err := hex.DecodeString(id)
		if err != nil {
			t.Fatalf("decode %s: %v", id, err)
		}
		s = strings.ReplaceAll(s, `"`+id+`"`, `"`+base64.StdEncoding.EncodeToString(b)+`"`)
	}
	return s
}

// checkUserID fails unless every resource has exactly one hottell.user.id, the string of
// userID, and the first keeps its service.name.
func checkUserID(t *testing.T, resources []*resourcepb.Resource) {
	t.Helper()
	if len(resources) != 2 {
		t.Fatalf("collector got %d resource entries, want 2", len(resources))
	}
	for i, res := range resources {
		var ids []string
		serviceName := ""
		for _, kv := range res.GetAttributes() {
			switch kv.GetKey() {
			case ingest.UserIDAttribute:
				ids = append(ids, protojson.Format(kv.GetValue()))
			case "service.name":
				serviceName = kv.GetValue().GetStringValue()
			}
		}
		want := `{"stringValue":"` + userID + `"}`
		if len(ids) != 1 || compact(t, ids[0]) != want {
			t.Errorf("resource %d: hottell.user.id = %v, want only %s", i, ids, want)
		}
		if i == 0 && serviceName != "claude-code" {
			t.Errorf("resource 0: service.name = %q, want claude-code kept", serviceName)
		}
	}
}

func compact(t *testing.T, s string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(s)); err != nil {
		t.Fatalf("compact %q: %v", s, err)
	}
	return buf.String()
}

func TestSetsUserID(t *testing.T) {
	t.Parallel()

	for _, sig := range signalRequests() {
		for _, encoding := range []string{"protobuf", "json"} {
			for _, compressed := range []bool{false, true} {
				name := sig.path + " " + encoding
				if compressed {
					name += " gzip"
				}
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					contentType, body := "application/json", []byte(sig.json)
					if encoding == "protobuf" {
						msg := sig.newMsg()
						if err := protojson.Unmarshal([]byte(base64IDs(t, sig.json)), msg); err != nil {
							t.Fatalf("build request: %v", err)
						}
						var err error
						if body, err = proto.Marshal(msg); err != nil {
							t.Fatalf("marshal request: %v", err)
						}
						contentType = "application/x-protobuf"
					}
					contentEncoding := ""
					if compressed {
						body, contentEncoding = gzipped(t, body), "gzip"
					}
					collector, got := fakeCollector(t, http.StatusOK, "")
					h := ingest.New(acceptingKeys(t), collector.URL, slog.New(slog.DiscardHandler))

					w := serve(h, post(t, sig.path, contentType, contentEncoding, "Bearer "+token, body))

					if w.Code != http.StatusOK {
						t.Fatalf("status = %d %s, want 200", w.Code, w.Body.String())
					}
					r := <-got
					if r.contentType != contentType || r.contentEncoding != "" {
						t.Errorf("collector got %q %q, want %q uncompressed", r.contentType, r.contentEncoding, contentType)
					}
					forwarded := sig.newMsg()
					if encoding == "protobuf" {
						if err := proto.Unmarshal(r.body, forwarded); err != nil {
							t.Fatalf("collector got a body that is not protobuf: %v", err)
						}
					} else {
						// The ids must stay in hex: the collector reads OTLP JSON, not protojson.
						for _, id := range []string{`"traceId":"` + traceIDHex + `"`, `"spanId":"` + spanIDHex + `"`} {
							if !bytes.Contains(r.body, []byte(id)) {
								t.Errorf("collector got %s without %s", r.body, id)
							}
						}
						if err := protojson.Unmarshal([]byte(base64IDs(t, string(r.body))), forwarded); err != nil {
							t.Fatalf("collector got a body that is not OTLP JSON: %v\n%s", err, r.body)
						}
					}
					checkUserID(t, sig.resources(forwarded))
				})
			}
		}
	}
}

func TestMalformedBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		path            string
		contentType     string
		contentEncoding string
		body            []byte
	}{
		{name: "not protobuf", path: "/v1/logs", contentType: "application/x-protobuf", body: []byte("\x0a\xff\xff")},
		{name: "not protobuf gzip", path: "/v1/metrics", contentType: "application/x-protobuf", contentEncoding: "gzip", body: gzipped(t, []byte("\x0a\xff\xff"))},
		{name: "not json", path: "/v1/traces", contentType: "application/json", body: []byte(`{"resourceSpans":[`)},
		{name: "json of another signal", path: "/v1/logs", contentType: "application/json", body: []byte(`{"resourceSpans":[]}`)},
		{name: "json id not hex", path: "/v1/traces", contentType: "application/json", body: []byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"zz"}]}]}]}`)},
		{name: "two json documents", path: "/v1/logs", contentType: "application/json", body: []byte(`{} {}`)},
		{name: "json trailing bracket", path: "/v1/logs", contentType: "application/json", body: []byte(`{"resourceLogs":[]}}`)},
		{name: "json null entry", path: "/v1/logs", contentType: "application/json", body: []byte(`{"resourceLogs":[null]}`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collector, got := fakeCollector(t, http.StatusOK, "")
			h := ingest.New(acceptingKeys(t), collector.URL, slog.New(slog.DiscardHandler))

			w := serve(h, post(t, tt.path, tt.contentType, tt.contentEncoding, "Bearer "+token, tt.body))

			code, message := statusOf(t, w)
			if w.Code != http.StatusBadRequest || code != 3 || !strings.Contains(message, "не разобрано") {
				t.Fatalf("response = %d %q, want 400 with code 3 and the reason", w.Code, w.Body.String())
			}
			if ct := w.Header().Get("Content-Type"); ct != tt.contentType {
				t.Errorf("response Content-Type = %q, want the request's %q", ct, tt.contentType)
			}
			if len(got) != 0 {
				t.Error("request reached the collector")
			}
		})
	}
}

// logsOfSize returns an ExportLogsServiceRequest of exactly size bytes in protobuf: four
// records with string bodies, each below MaxRecordBytes, the last of the length that makes
// the request so.
func logsOfSize(t *testing.T, size int) []byte {
	t.Helper()
	record := func(n int) *logspb.LogRecord {
		return &logspb.LogRecord{
			Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: strings.Repeat("a", n)}},
		}
	}
	quarter := size / 4
	build := func(n int) []byte {
		b, err := proto.Marshal(&collogspb.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{
			ScopeLogs: []*logspb.ScopeLogs{{LogRecords: []*logspb.LogRecord{
				record(quarter), record(quarter), record(quarter), record(n),
			}}},
		}}})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return b
	}
	n := quarter
	for range 8 {
		b := build(n)
		if len(b) == size {
			return b
		}
		n -= len(b) - size
	}
	t.Fatalf("no request of %d bytes", size)
	return nil
}

// statusOf decodes the google.rpc.Status of an error response in the encoding its
// Content-Type names.
func statusOf(t *testing.T, w *httptest.ResponseRecorder) (int, string) {
	t.Helper()
	body := w.Body.Bytes()
	if w.Header().Get("Content-Type") == "application/json" {
		var st struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(body, &st); err != nil {
			t.Fatalf("status %q is not JSON: %v", body, err)
		}
		return st.Code, st.Message
	}
	code, message := 0, ""
	for len(body) > 0 {
		num, typ, n := protowire.ConsumeTag(body)
		if n < 0 {
			t.Fatalf("status %q is not protobuf", w.Body.Bytes())
		}
		body = body[n:]
		switch {
		case num == 1 && typ == protowire.VarintType:
			v, m := protowire.ConsumeVarint(body)
			code, n = int(v), m
		case num == 2 && typ == protowire.BytesType:
			v, m := protowire.ConsumeString(body)
			message, n = v, m
		default:
			n = protowire.ConsumeFieldValue(num, typ, body)
		}
		if n < 0 {
			t.Fatalf("status %q is not protobuf", w.Body.Bytes())
		}
		body = body[n:]
	}
	return code, message
}

func TestRecordLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		contentType string
		recordBytes int
		wantCode    int
	}{
		{name: "at the limit", contentType: "application/x-protobuf", recordBytes: ingest.MaxRecordBytes, wantCode: http.StatusOK},
		{name: "over the limit", contentType: "application/x-protobuf", recordBytes: ingest.MaxRecordBytes + 1, wantCode: http.StatusRequestEntityTooLarge},
		{name: "over the limit in json", contentType: "application/json", recordBytes: ingest.MaxRecordBytes + 1, wantCode: http.StatusRequestEntityTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Two records: the one of recordBytes and a small one, so the request is not
			// refused for being a single record.
			record := recordOfSize(t, tt.recordBytes)
			msg := &collogspb.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{
				ScopeLogs: []*logspb.ScopeLogs{{LogRecords: []*logspb.LogRecord{record, {}}}},
			}}}
			var (
				body []byte
				err  error
			)
			if tt.contentType == "application/json" {
				body, err = protojson.Marshal(msg)
			} else {
				body, err = proto.Marshal(msg)
			}
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			collector, got := fakeCollector(t, http.StatusOK, "")
			h := ingest.New(acceptingKeys(t), collector.URL, slog.New(slog.DiscardHandler))

			w := serve(h, post(t, "/v1/logs", tt.contentType, "", "Bearer "+token, body))

			if w.Code != tt.wantCode {
				t.Fatalf("status = %d %q, want %d", w.Code, w.Body.String(), tt.wantCode)
			}
			if tt.wantCode == http.StatusOK {
				return
			}
			if code, message := statusOf(t, w); code != 8 || !strings.Contains(message, "8 МиБ") {
				t.Errorf("status = %d %q, want code 8 about 8 МиБ", code, message)
			}
			if ct := w.Header().Get("Content-Type"); ct != tt.contentType {
				t.Errorf("response Content-Type = %q, want the request's %q", ct, tt.contentType)
			}
			if len(got) != 0 {
				t.Error("refused request reached the collector")
			}
		})
	}
}

// recordOfSize returns a LogRecord of exactly size bytes in protobuf.
func recordOfSize(t *testing.T, size int) *logspb.LogRecord {
	t.Helper()
	n := size
	for range 8 {
		record := &logspb.LogRecord{
			Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: strings.Repeat("a", n)}},
		}
		if got := proto.Size(record); got != size {
			n -= got - size
			continue
		}
		return record
	}
	t.Fatalf("no record of %d bytes", size)
	return nil
}
