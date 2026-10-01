package ingest_test

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/ingest"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/ingest/mock"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// token is a synthetic collector token.
const token = "ht_ingest_synthetic"

// userID is the synthetic user whose collector token token is.
const userID = "00000000-0000-4000-8000-0000000000aa"

// received is one request the fake collector got.
type received struct {
	path            string
	contentType     string
	contentEncoding string
	body            []byte
}

// fakeCollector serves status and body on every request and sends each request it gets to
// the returned channel.
func fakeCollector(t *testing.T, status int, body string) (*httptest.Server, <-chan received) {
	t.Helper()
	got := make(chan received, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("collector read body: %v", err)
		}
		got <- received{
			path: r.URL.Path, contentType: r.Header.Get("Content-Type"),
			contentEncoding: r.Header.Get("Content-Encoding"), body: b,
		}
		w.Header().Set("Content-Type", r.Header.Get("Content-Type"))
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// acceptingKeys returns Keys that recognise token as the collector token of userID.
func acceptingKeys(t *testing.T) *mock.MockKeys {
	t.Helper()
	keys := mock.NewMockKeys(gomock.NewController(t))
	keys.EXPECT().Resolve(gomock.Any(), domain.AccessKeyKindIngest, token).Return(uuid.MustParse(userID), nil).AnyTimes()
	return keys
}

func post(t *testing.T, path, contentType, contentEncoding, authorization string, body []byte) *http.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, bytes.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	if contentEncoding != "" {
		r.Header.Set("Content-Encoding", contentEncoding)
	}
	if authorization != "" {
		r.Header.Set("Authorization", authorization)
	}
	return r
}

func gzipped(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatalf("gzip: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// TestForwards checks that an accepted request reaches the collector at its path with its
// Content-Type and uncompressed, and that the collector's response comes back; what happens
// to the body is TestSetsUserID's.
func TestForwards(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		path            string
		contentType     string
		contentEncoding string
		body            []byte
		wantBody        []byte
	}{
		{name: "logs protobuf", path: "/v1/logs", contentType: "application/x-protobuf", body: []byte{}, wantBody: []byte{}},
		{name: "metrics gzip", path: "/v1/metrics", contentType: "application/x-protobuf", contentEncoding: "gzip", body: gzipped(t, []byte{}), wantBody: []byte{}},
		{name: "traces json with charset", path: "/v1/traces", contentType: "application/json; charset=utf-8", contentEncoding: "identity", body: []byte(`{"resourceSpans":[]}`), wantBody: []byte(`{}`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collector, got := fakeCollector(t, http.StatusOK, "\x00ok")
			h := ingest.New(acceptingKeys(t), collector.URL, slog.New(slog.DiscardHandler))

			w := serve(h, post(t, tt.path, tt.contentType, tt.contentEncoding, "Bearer "+token, tt.body))

			if w.Code != http.StatusOK || w.Body.String() != "\x00ok" {
				t.Fatalf("response = %d %q, want 200 with the collector's body", w.Code, w.Body.String())
			}
			if ct := w.Header().Get("Content-Type"); ct != tt.contentType {
				t.Errorf("response Content-Type = %q, want %q", ct, tt.contentType)
			}
			r := <-got
			if r.path != tt.path || r.contentType != tt.contentType || r.contentEncoding != "" {
				t.Errorf("collector got %s %q %q, want %s %q without Content-Encoding", r.path, r.contentType,
					r.contentEncoding, tt.path, tt.contentType)
			}
			if !bytes.Equal(r.body, tt.wantBody) {
				t.Errorf("collector got body %q, want %q", r.body, tt.wantBody)
			}
		})
	}
}

func TestUnauthorized(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		authorization string
		resolveErr    error
		wantMessage   string
	}{
		{name: "no header", wantMessage: "Нужен токен"},
		{name: "other scheme", authorization: "Basic " + token, wantMessage: "Нужен токен"},
		{name: "two spaces", authorization: "Bearer  " + token, wantMessage: "Нужен токен"},
		{name: "unknown", authorization: "Bearer " + token, resolveErr: domain.ErrAccessKeyNotFound, wantMessage: "не распознан"},
		{name: "revoked", authorization: "bearer " + token, resolveErr: domain.ErrAccessKeyRevoked, wantMessage: "отозван"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			keys := mock.NewMockKeys(gomock.NewController(t))
			if tt.resolveErr != nil {
				keys.EXPECT().Resolve(gomock.Any(), domain.AccessKeyKindIngest, token).Return(uuid.Nil, tt.resolveErr)
			}
			collector, got := fakeCollector(t, http.StatusOK, "")
			h := ingest.New(keys, collector.URL, slog.New(slog.DiscardHandler))

			w := serve(h, post(t, "/v1/logs", "application/x-protobuf", "", tt.authorization, []byte("x")))

			if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), `"code":16`) ||
				!strings.Contains(w.Body.String(), tt.wantMessage) {
				t.Fatalf("response = %d %s, want 401 with %q", w.Code, w.Body.String(), tt.wantMessage)
			}
			if len(got) != 0 {
				t.Error("request reached the collector")
			}
		})
	}
}

func TestRequestChecks(t *testing.T) {
	t.Parallel()

	withinLimit := logsOfSize(t, ingest.MaxRequestBytes)
	overLimit := logsOfSize(t, ingest.MaxRequestBytes+1)
	corrupt := gzipped(t, []byte("otlp-synthetic"))
	corrupt[len(corrupt)-5] ^= 0xff // breaks the CRC-32 of the member

	tests := []struct {
		name            string
		method          string
		contentType     string
		contentEncoding string
		body            []byte
		wantCode        int
	}{
		{name: "get", method: http.MethodGet, contentType: "application/x-protobuf", wantCode: http.StatusMethodNotAllowed},
		{name: "text content type", method: http.MethodPost, contentType: "text/plain", body: []byte("x"), wantCode: http.StatusUnsupportedMediaType},
		{name: "brotli", method: http.MethodPost, contentType: "application/x-protobuf", contentEncoding: "br", body: []byte("x"), wantCode: http.StatusUnsupportedMediaType},
		{name: "over the limit", method: http.MethodPost, contentType: "application/x-protobuf", body: overLimit, wantCode: http.StatusRequestEntityTooLarge},
		{name: "over the limit once decompressed", method: http.MethodPost, contentType: "application/x-protobuf", contentEncoding: "gzip", body: gzipped(t, overLimit), wantCode: http.StatusRequestEntityTooLarge},
		{name: "corrupt gzip", method: http.MethodPost, contentType: "application/x-protobuf", contentEncoding: "gzip", body: corrupt, wantCode: http.StatusBadRequest},
		{name: "at the limit", method: http.MethodPost, contentType: "application/x-protobuf", body: withinLimit, wantCode: http.StatusOK},
		{name: "at the limit once decompressed", method: http.MethodPost, contentType: "application/x-protobuf", contentEncoding: "gzip", body: gzipped(t, withinLimit), wantCode: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collector, got := fakeCollector(t, http.StatusOK, "")
			h := ingest.New(acceptingKeys(t), collector.URL, slog.New(slog.DiscardHandler))
			r := post(t, "/v1/logs", tt.contentType, tt.contentEncoding, "Bearer "+token, tt.body)
			r.Method = tt.method

			w := serve(h, r)

			if w.Code != tt.wantCode {
				t.Fatalf("status = %d %s, want %d", w.Code, w.Body.String(), tt.wantCode)
			}
			if tt.wantCode != http.StatusOK && len(got) != 0 {
				t.Error("refused request reached the collector")
			}
			if tt.wantCode == http.StatusMethodNotAllowed && w.Header().Get("Allow") != http.MethodPost {
				t.Errorf("Allow = %q, want POST", w.Header().Get("Allow"))
			}
		})
	}
}

func TestUnavailable(t *testing.T) {
	t.Parallel()

	down := httptest.NewServer(http.NotFoundHandler())
	downURL := down.URL
	down.Close()
	refusing, _ := fakeCollector(t, http.StatusBadRequest, "refused")
	failing, _ := fakeCollector(t, http.StatusInternalServerError, "")

	tests := []struct {
		name       string
		collector  string
		resolveErr error
	}{
		{name: "collector down", collector: downURL},
		{name: "collector 4xx", collector: refusing.URL},
		{name: "collector 5xx", collector: failing.URL},
		{name: "token store down", collector: refusing.URL, resolveErr: errors.New("connection refused")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			keys := mock.NewMockKeys(gomock.NewController(t))
			keys.EXPECT().Resolve(gomock.Any(), domain.AccessKeyKindIngest, token).Return(uuid.New(), tt.resolveErr)
			h := ingest.New(keys, tt.collector, slog.New(slog.DiscardHandler))

			w := serve(h, post(t, "/v1/logs", "application/x-protobuf", "", "Bearer "+token, []byte{}))

			if code, _ := statusOf(t, w); w.Code != http.StatusServiceUnavailable || code != 14 {
				t.Fatalf("response = %d %q, want 503 with code 14", w.Code, w.Body.String())
			}
		})
	}
}
