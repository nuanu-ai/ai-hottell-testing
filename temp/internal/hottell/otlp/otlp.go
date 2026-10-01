// Package otlp encodes a batch of queue records into the body of one OTLP /v1/logs
// request, as docs/specs/hottell-contract/ingest.md («Записи») lays it out: a hook event
// or a transcript line is one LogRecord with the record whole in its body.
//
// The OTLP protobuf types stay inside this package: Encode returns the request already
// marshaled.
package otlp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/hook"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/queue"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/transcript"
)

// service.name of the two kinds of record.
const (
	ServiceHooks       = "hottell-hooks"
	ServiceTranscripts = "hottell-transcripts"
)

// scopeName is the InstrumentationScope name of every record.
const scopeName = "hottell"

// ErrUnknownKind means a record's kind is neither a hook event nor a transcript line.
var ErrUnknownKind = errors.New("unknown record kind")

// Resource is what the binary says about itself in every request.
type Resource struct {
	// Version is what hottell version prints; it is service.version and the scope version.
	Version string
	// HostName is os.Hostname() of the Mac.
	HostName string
}

// Encode builds the ExportLogsServiceRequest of recs and returns it in protobuf. The
// records are grouped into ResourceLogs by service.name and agent, in the order the
// groups first appear; inside a group they keep the batch order. observed is when the
// daemon assembled the batch: the observed time of the hook events.
func Encode(recs []queue.Record, res Resource, observed time.Time) ([]byte, error) {
	req, err := build(recs, res, observed)
	if err != nil {
		return nil, err
	}
	data, err := proto.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal logs request: %w", err)
	}
	return data, nil
}

// RecordSize is the size of rec's LogRecord in protobuf (proto.Size), the size the
// service's per-record limit counts (ingest.md, «Лимиты»). It fails like Encode on a
// record that cannot be encoded.
func RecordSize(rec queue.Record, observed time.Time) (int, error) {
	_, _, lr, err := logRecord(rec, observed)
	if err != nil {
		return 0, fmt.Errorf("record %s: %w", rec.ID, err)
	}
	return proto.Size(lr), nil
}

// StatusMessage is the message of the google.rpc.Status in an error response of the
// service: JSON when contentType says so, protobuf otherwise. A body that is neither
// gives "".
func StatusMessage(body []byte, contentType string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "application/json") {
		var st struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &st)
		return st.Message
	}
	var msg string
	for len(body) > 0 {
		num, typ, n := protowire.ConsumeTag(body)
		if n < 0 {
			return ""
		}
		body = body[n:]
		// google.rpc.Status: 2 is the message.
		if num == 2 && typ == protowire.BytesType {
			v, m := protowire.ConsumeBytes(body)
			if m < 0 {
				return ""
			}
			msg = string(v)
			body = body[m:]
			continue
		}
		m := protowire.ConsumeFieldValue(num, typ, body)
		if m < 0 {
			return ""
		}
		body = body[m:]
	}
	return msg
}

type groupKey struct {
	service, agent string
}

func build(recs []queue.Record, res Resource, observed time.Time) (*collogspb.ExportLogsServiceRequest, error) {
	req := &collogspb.ExportLogsServiceRequest{}
	groups := map[groupKey]*logspb.ScopeLogs{}
	for _, rec := range recs {
		service, agent, lr, err := logRecord(rec, observed)
		if err != nil {
			return nil, fmt.Errorf("record %s: %w", rec.ID, err)
		}
		key := groupKey{service: service, agent: agent}
		scope, ok := groups[key]
		if !ok {
			scope = &logspb.ScopeLogs{Scope: &commonpb.InstrumentationScope{Name: scopeName, Version: res.Version}}
			groups[key] = scope
			req.ResourceLogs = append(req.ResourceLogs, &logspb.ResourceLogs{
				Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
					str("service.name", service),
					str("service.version", res.Version),
					str("host.name", res.HostName),
				}},
				ScopeLogs: []*logspb.ScopeLogs{scope},
			})
		}
		scope.LogRecords = append(scope.LogRecords, lr)
	}
	return req, nil
}

// kind holds the fields that tell a hook event's metadata from a transcript line's.
type kind struct {
	Kind   string `json:"kind"`
	Source string `json:"source"`
}

func logRecord(rec queue.Record, observed time.Time) (service, agent string, lr *logspb.LogRecord, err error) {
	var k kind
	if err := json.Unmarshal(rec.Kind, &k); err != nil {
		return "", "", nil, fmt.Errorf("decode record kind: %w", err)
	}
	switch {
	case k.Kind == hook.KindHook:
		var m hook.Meta
		if err := json.Unmarshal(rec.Kind, &m); err != nil {
			return "", "", nil, fmt.Errorf("decode hook metadata: %w", err)
		}
		return ServiceHooks, string(m.Agent), hookRecord(m, rec.Payload, observed), nil
	case k.Source == transcript.Source:
		var m transcript.Meta
		if err := json.Unmarshal(rec.Kind, &m); err != nil {
			return "", "", nil, fmt.Errorf("decode transcript line metadata: %w", err)
		}
		return ServiceTranscripts, m.Agent, transcriptRecord(m, rec.Payload), nil
	default:
		return "", "", nil, fmt.Errorf("%w: %q", ErrUnknownKind, rec.Kind)
	}
}

// hookAttributes are the attributes a hook event copies from its top-level fields, in
// the order they are sent.
var hookAttributes = []struct{ attr, field string }{ //nolint:gochecknoglobals // read-only table
	{"hook.event_name", "hook_event_name"},
	{"session.id", "session_id"},
	{"cwd", "cwd"},
	{"prompt_id", "prompt_id"},
	{"tool_use_id", "tool_use_id"},
	{"turn_id", "turn_id"},
}

func hookRecord(m hook.Meta, event []byte, observed time.Time) *logspb.LogRecord {
	attrs := []*commonpb.KeyValue{str("agent", string(m.Agent))}
	// A damaged event that is not a JSON object leaves only the agent attribute.
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(event, &fields)
	for _, a := range hookAttributes {
		// A null or a value that is not a string counts as absent.
		var v *string
		if err := json.Unmarshal(fields[a.field], &v); err == nil && v != nil {
			attrs = append(attrs, str(a.attr, *v))
		}
	}
	return &logspb.LogRecord{
		TimeUnixNano:         unixNano(m.ReceivedUnixNano),
		ObservedTimeUnixNano: unixNano(observed.UnixNano()),
		Body:                 body(event),
		Attributes:           attrs,
	}
}

func transcriptRecord(m transcript.Meta, line []byte) *logspb.LogRecord {
	return &logspb.LogRecord{
		TimeUnixNano:         lineTime(line),
		ObservedTimeUnixNano: unixNano(m.ObservedUnixNano),
		Body:                 body(line),
		Attributes: []*commonpb.KeyValue{
			str("agent", m.Agent),
			str("session.id", m.SessionID),
			str("transcript.kind", m.Kind),
			str("transcript.path", m.Path),
			{Key: "transcript.line", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: m.Line}}},
			{Key: "backfill", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: m.Backfill}}},
		},
	}
}

// lineTime is the top-level timestamp of a transcript line, or 0 when the line has none
// that parses: the time is unknown.
func lineTime(line []byte) uint64 {
	var v struct {
		Timestamp *string `json:"timestamp"`
	}
	if err := json.Unmarshal(line, &v); err != nil || v.Timestamp == nil {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, *v.Timestamp)
	if err != nil {
		return 0
	}
	return unixNano(t.UnixNano())
}

// body is the record's text as a stringValue, or its bytes unchanged as a bytesValue
// when they are not valid UTF-8.
func body(data []byte) *commonpb.AnyValue {
	if utf8.Valid(data) {
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: string(data)}}
	}
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: data}}
}

func str(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}}
}

// unixNano turns a time before the epoch, which no record has, into 0: unknown.
func unixNano(n int64) uint64 {
	if n < 0 {
		return 0
	}
	return uint64(n)
}
