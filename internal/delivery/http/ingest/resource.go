package ingest

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// UserIDAttribute is the resource attribute that carries the id of the user whose collector
// token the request came with; only the server sets it (ingest.md, «Ресурс»).
const UserIDAttribute = "hottell.user.id"

// MaxRecordBytes is the largest record accepted, in the protobuf encoding whatever the
// encoding of the request: a LogRecord, a Metric or a Span.
const MaxRecordBytes = 8 << 20

// errUnknownSignal is returned for a path that is not one of Paths.
var errUnknownSignal = errors.New("unknown OTLP signal path")

// request is a decoded Export…ServiceRequest of one signal.
type request struct {
	msg proto.Message
	// resources are the resource fields of the request's entries, to be set in place.
	resources []**resourcepb.Resource
	// records are the request's LogRecords, Metrics or Spans.
	records []proto.Message
}

// decode reads body as the Export…ServiceRequest of the signal of path, in the protobuf
// encoding or, when isJSON, in the JSON encoding of OTLP.
func decode(path string, isJSON bool, body []byte) (*request, error) {
	var msg proto.Message
	switch path {
	case "/v1/logs":
		msg = &collogspb.ExportLogsServiceRequest{}
	case "/v1/metrics":
		msg = &colmetricspb.ExportMetricsServiceRequest{}
	case "/v1/traces":
		msg = &coltracepb.ExportTraceServiceRequest{}
	default:
		return nil, errUnknownSignal
	}
	if err := unmarshal(isJSON, body, msg); err != nil {
		return nil, err
	}

	req := &request{msg: msg}
	switch m := msg.(type) {
	case *collogspb.ExportLogsServiceRequest:
		for _, rl := range m.GetResourceLogs() {
			req.resources = append(req.resources, &rl.Resource)
			for _, sl := range rl.GetScopeLogs() {
				for _, lr := range sl.GetLogRecords() {
					req.records = append(req.records, lr)
				}
			}
		}
	case *colmetricspb.ExportMetricsServiceRequest:
		for _, rm := range m.GetResourceMetrics() {
			req.resources = append(req.resources, &rm.Resource)
			for _, sm := range rm.GetScopeMetrics() {
				for _, metric := range sm.GetMetrics() {
					req.records = append(req.records, metric)
				}
			}
		}
	case *coltracepb.ExportTraceServiceRequest:
		for _, rs := range m.GetResourceSpans() {
			req.resources = append(req.resources, &rs.Resource)
			for _, ss := range rs.GetScopeSpans() {
				for _, span := range ss.GetSpans() {
					req.records = append(req.records, span)
				}
			}
		}
	}
	return req, nil
}

// hasOversizedRecord reports whether a record of the request is larger than MaxRecordBytes.
func (r *request) hasOversizedRecord() bool {
	return slices.ContainsFunc(r.records, func(record proto.Message) bool {
		return proto.Size(record) > MaxRecordBytes
	})
}

// setUserID drops every UserIDAttribute of every resource of the request and appends one
// with userID, creating the resource where an entry has none.
func (r *request) setUserID(userID string) {
	for _, res := range r.resources {
		if *res == nil {
			*res = &resourcepb.Resource{}
		}
		attrs := slices.DeleteFunc((*res).Attributes, func(kv *commonpb.KeyValue) bool {
			return kv.GetKey() == UserIDAttribute
		})
		attrs = append(attrs, &commonpb.KeyValue{
			Key:   UserIDAttribute,
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: userID}},
		})
		(*res).Attributes = attrs
	}
}

// encode writes the request in the protobuf encoding or, when isJSON, in the JSON encoding
// of OTLP.
func (r *request) encode(isJSON bool) ([]byte, error) {
	if !isJSON {
		return proto.Marshal(r.msg)
	}
	body, err := protojson.Marshal(r.msg)
	if err != nil {
		return nil, err
	}
	return convertIDs(body, base64ToHex)
}

func unmarshal(isJSON bool, body []byte, msg proto.Message) error {
	if !isJSON {
		return proto.Unmarshal(body, msg)
	}
	converted, err := convertIDs(body, hexToBase64)
	if err != nil {
		return err
	}
	// Unknown fields are refused, not dropped: protojson cannot keep them the way proto
	// keeps them in the binary encoding, and a request of another signal is refused too.
	return protojson.Unmarshal(converted, msg)
}

// isIDKey reports whether a JSON key is that of a trace or span id, which the JSON encoding
// of OTLP writes as a hex string where protojson expects base64; both the lowerCamelCase
// and the original field names are accepted on input.
func isIDKey(key string) bool {
	switch key {
	case "traceId", "spanId", "parentSpanId", "trace_id", "span_id", "parent_span_id":
		return true
	default:
		return false
	}
}

// convertIDs rewrites with conv every string value of an id key anywhere in the JSON
// document body. Keys of OTLP JSON are field names only: attribute keys are values of "key".
func convertIDs(body []byte, conv func(string) (string, error)) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data after the JSON document")
	}
	if err := walkIDs(doc, conv); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}

func walkIDs(v any, conv func(string) (string, error)) error {
	switch v := v.(type) {
	case map[string]any:
		for key, value := range v {
			if s, ok := value.(string); ok && isIDKey(key) {
				converted, err := conv(s)
				if err != nil {
					return fmt.Errorf("%s: %w", key, err)
				}
				v[key] = converted
				continue
			}
			if err := walkIDs(value, conv); err != nil {
				return err
			}
		}
	case []any:
		for _, value := range v {
			if err := walkIDs(value, conv); err != nil {
				return err
			}
		}
	}
	return nil
}

func hexToBase64(s string) (string, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func base64ToHex(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
