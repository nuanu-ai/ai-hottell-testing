package sender

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/hook"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/otlp"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/queue"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

// markerPattern finds the markers the test events carry; the body of a record goes in
// protobuf as its raw text, so the markers of a request are found in its bytes.
var markerPattern = regexp.MustCompile(`mark-[a-z0-9]+`) //nolint:gochecknoglobals // compiled once

// request is what the test server saw of one request.
type request struct {
	markers         []string
	auth            string
	contentType     string
	contentEncoding string
	path            string
}

// answer is the test server's reply to one request.
type answer struct {
	code   int
	header map[string]string
	body   string
	// drop closes the connection without an answer.
	drop bool
	// hang answers nothing until the client gives up.
	hang bool
}

// server is a test intake that answers by reply and records every request.
type server struct {
	*httptest.Server

	mu       sync.Mutex
	requests []request
	reply    func(n int, r request) answer
}

func newServer(t *testing.T, reply func(n int, r request) answer) *server {
	t.Helper()
	s := &server{reply: reply}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

func (s *server) handle(w http.ResponseWriter, r *http.Request) {
	zr, err := gzip.NewReader(r.Body)
	if err != nil {
		http.Error(w, "not gzip", http.StatusBadRequest)
		return
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		http.Error(w, "bad gzip", http.StatusBadRequest)
		return
	}
	req := request{
		markers:         markerPattern.FindAllString(string(raw), -1),
		auth:            r.Header.Get("Authorization"),
		contentType:     r.Header.Get("Content-Type"),
		contentEncoding: r.Header.Get("Content-Encoding"),
		path:            r.URL.Path,
	}
	s.mu.Lock()
	n := len(s.requests)
	s.requests = append(s.requests, req)
	s.mu.Unlock()

	a := s.reply(n, req)
	if a.hang {
		<-r.Context().Done()
		return
	}
	if a.drop {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return
	}
	for k, v := range a.header {
		w.Header().Set(k, v)
	}
	w.WriteHeader(a.code)
	_, _ = io.WriteString(w, a.body)
}

func (s *server) seen() []request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// creds are credentials the test changes while the sender runs.
type creds struct {
	mu sync.Mutex
	c  state.Credentials
}

func (c *creds) get() (state.Credentials, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.c, nil
}

func (c *creds) setToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.c.CollectorToken = token
}

// fixture is a sender on a temporary queue with its test server.
type fixture struct {
	q      *queue.Queue
	root   string
	srv    *server
	creds  *creds
	sender *Sender
	wake   chan struct{}

	mu     sync.Mutex
	sleeps []time.Duration
	unauth []string
}

func newFixture(t *testing.T, reply func(n int, r request) answer) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{
		q:    queue.New(filepath.Join(root, "queue"), filepath.Join(root, "rejected"), 0, nil),
		root: root,
		srv:  newServer(t, reply),
		wake: make(chan struct{}, 1),
	}
	// A trailing slash on the address must not double the path's slash.
	f.creds = &creds{c: state.Credentials{IngestURL: f.srv.URL + "/", CollectorToken: "tok-1"}}
	f.sender = New(Config{
		Queue:       f.q,
		Credentials: f.creds.get,
		Resource:    otlp.Resource{Version: "0.1.0", HostName: "test"},
		Wake:        f.wake,
		OnUnauthorized: func(reason string) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.unauth = append(f.unauth, reason)
		},
		StatusFile: filepath.Join(root, "sender.json"),
	})
	f.sender.poll = 5 * time.Millisecond
	// No jitter, and pauses are recorded instead of slept.
	f.sender.random = func() float64 { return 0.5 }
	f.sender.sleep = func(ctx context.Context, d time.Duration) error {
		f.mu.Lock()
		f.sleeps = append(f.sleeps, d)
		f.mu.Unlock()
		return ctx.Err()
	}
	return f
}

// put queues a hook event carrying marker, padded to at least size bytes.
func (f *fixture) put(t *testing.T, marker string, size int) {
	t.Helper()
	kind, err := json.Marshal(hook.Meta{Kind: hook.KindHook, Agent: policy.Claude, ReceivedUnixNano: 1})
	if err != nil {
		t.Fatal(err)
	}
	event := `{"session_id":"s","hook_event_name":"Stop","cwd":"/w","m":"mark-` + marker + `","pad":"` +
		strings.Repeat("x", size) + `"}`
	if _, err := f.q.Put(kind, []byte(event)); err != nil {
		t.Fatal(err)
	}
	// Records put in a row keep their order only when their names differ in time.
	time.Sleep(time.Millisecond)
}

// run runs the sender until done holds, then stops it.
func (f *fixture) run(t *testing.T, done func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- f.sender.Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			cancel()
			<-stopped
			t.Fatalf("condition not reached; requests: %+v", f.srv.seen())
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	if err := <-stopped; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func (f *fixture) stats(t *testing.T) queue.Stats {
	t.Helper()
	st, err := f.q.Stats()
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (f *fixture) empty(t *testing.T) func() bool {
	t.Helper()
	return func() bool { return f.stats(t).Queued == 0 }
}

func (f *fixture) pauses() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sleeps)
}

// rejectedReasons are the reasons of the rejected records, by their event's marker.
func (f *fixture) rejectedReasons(t *testing.T) map[string]string {
	t.Helper()
	dir := filepath.Join(f.root, "rejected")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".reason") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		reason, err := os.ReadFile(filepath.Join(dir, e.Name()+".reason"))
		if err != nil {
			t.Fatal(err)
		}
		out[markerPattern.FindString(string(data))] = strings.TrimSpace(string(reason))
	}
	return out
}

func (f *fixture) status(t *testing.T) Status {
	t.Helper()
	st, err := ReadStatus(filepath.Join(f.root, "sender.json"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func ok(int, request) answer { return answer{code: http.StatusOK} }

// statusJSON is an error body of the service in JSON.
func statusJSON(msg string) answer {
	return answer{header: map[string]string{"Content-Type": "application/json"}, body: `{"code":3,"message":"` + msg + `"}`}
}

func TestAcceptedBatchIsSentAndAcked(t *testing.T) {
	t.Parallel()

	f := newFixture(t, ok)
	f.put(t, "a", 0)
	f.put(t, "b", 0)
	f.put(t, "c", 0)
	f.run(t, f.empty(t))

	reqs := f.srv.seen()
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want one batch", len(reqs))
	}
	r := reqs[0]
	if want := []string{"mark-a", "mark-b", "mark-c"}; !slices.Equal(r.markers, want) {
		t.Errorf("records %v, want %v in queue order", r.markers, want)
	}
	if r.path != "/v1/logs" || r.auth != "Bearer tok-1" || r.contentType != "application/x-protobuf" || r.contentEncoding != "gzip" {
		t.Errorf("request %+v, want POST /v1/logs, Bearer tok-1, protobuf, gzip", r)
	}
	if st := f.stats(t); st.Rejected != 0 {
		t.Errorf("%d records rejected, want none", st.Rejected)
	}
	if st := f.status(t); st.LastSuccess.IsZero() || st.LastError != nil {
		t.Errorf("status %+v, want a last success and no error", st)
	}
}

func TestBatchesGoInQueueOrder(t *testing.T) {
	t.Parallel()

	f := newFixture(t, ok)
	// 1.5 MiB each: two fit in a 4 MiB batch, the third starts the next one.
	for _, m := range []string{"a", "b", "c", "d", "e"} {
		f.put(t, m, 1536<<10)
	}
	f.run(t, f.empty(t))

	var got []string
	for _, r := range f.srv.seen() {
		got = append(got, strings.Join(r.markers, ","))
	}
	if want := []string{"mark-a,mark-b", "mark-c,mark-d", "mark-e"}; !slices.Equal(got, want) {
		t.Errorf("requests %v, want %v", got, want)
	}
}

func TestRefusedSingleRecordIsRejected(t *testing.T) {
	t.Parallel()

	for _, code := range []int{http.StatusBadRequest, http.StatusRequestEntityTooLarge} {
		f := newFixture(t, func(int, request) answer {
			a := statusJSON("no good")
			a.code = code
			return a
		})
		f.put(t, "a", 0)
		f.run(t, f.empty(t))

		if n := len(f.srv.seen()); n != 1 {
			t.Errorf("%d: %d requests, want 1 and no retry", code, n)
		}
		want := map[string]string{"mark-a": strconv.Itoa(code) + ": no good"}
		if got := f.rejectedReasons(t); !maps.Equal(got, want) {
			t.Errorf("%d: rejected %v, want %v", code, got, want)
		}
		if p := f.pauses(); len(p) != 0 {
			t.Errorf("%d: pauses %v, want none", code, p)
		}
	}
}

func TestRefusedBatchIsSplit(t *testing.T) {
	t.Parallel()

	// A batch is too large; alone, b is not protobuf and the others go through.
	f := newFixture(t, func(_ int, r request) answer {
		switch {
		case len(r.markers) > 1:
			return answer{code: http.StatusRequestEntityTooLarge}
		case r.markers[0] == "mark-b":
			return answer{code: http.StatusBadRequest}
		default:
			return answer{code: http.StatusOK}
		}
	})
	f.put(t, "a", 0)
	f.put(t, "b", 0)
	f.put(t, "c", 0)
	f.run(t, f.empty(t))

	var got []string
	for _, r := range f.srv.seen() {
		got = append(got, strings.Join(r.markers, ","))
	}
	if want := []string{"mark-a,mark-b,mark-c", "mark-a", "mark-b", "mark-c"}; !slices.Equal(got, want) {
		t.Errorf("requests %v, want %v", got, want)
	}
	if got, want := f.rejectedReasons(t), map[string]string{"mark-b": "400"}; !maps.Equal(got, want) {
		t.Errorf("rejected %v, want %v", got, want)
	}
}

func TestBatchRefusedRecordByRecordStaysQueued(t *testing.T) {
	t.Parallel()

	f := newFixture(t, func(int, request) answer { return answer{code: http.StatusBadRequest} })
	f.put(t, "a", 0)
	f.put(t, "b", 0)
	// The batch, each record alone, then the batch again after a pause.
	f.run(t, func() bool { return len(f.srv.seen()) >= 4 })

	if st := f.stats(t); st.Queued != 2 || st.Rejected != 0 {
		t.Errorf("queued %d, rejected %d; want both records kept in the queue", st.Queued, st.Rejected)
	}
	if p := f.pauses(); len(p) == 0 || p[0] != time.Second {
		t.Errorf("pauses %v, want a retry pause of 1s before the batch goes again", p)
	}
	if st := f.status(t); st.LastError == nil || st.LastError.Code != http.StatusBadRequest {
		t.Errorf("status %+v, want the refusal as the last error", st)
	}
}

func TestRetriesPauseWithGrowingBackoff(t *testing.T) {
	t.Parallel()

	replies := []answer{
		{code: http.StatusServiceUnavailable},
		{code: http.StatusInternalServerError},
		{code: http.StatusTooManyRequests, header: map[string]string{"Retry-After": "10"}},
		{code: http.StatusNotFound},
		{drop: true},
		{code: http.StatusForbidden},
		{code: http.StatusBadGateway},
		{code: http.StatusServiceUnavailable, header: map[string]string{"Retry-After": "3600"}},
		{code: http.StatusMethodNotAllowed},
		{code: http.StatusUnsupportedMediaType},
		{code: http.StatusOK},
		// The pause starts again after a success.
		{code: http.StatusServiceUnavailable},
		{code: http.StatusOK},
	}
	f := newFixture(t, func(n int, _ request) answer { return replies[n] })
	f.put(t, "a", 0)
	f.run(t, f.empty(t))
	f.put(t, "b", 0)
	f.run(t, f.empty(t))

	sec := time.Second
	want := []time.Duration{
		1 * sec, 2 * sec,
		10 * sec, // Retry-After over the 4 s backoff
		8 * sec, 16 * sec, 32 * sec, 64 * sec,
		5 * time.Minute, // Retry-After of an hour, capped over the 128 s backoff
		256 * sec,
		5 * time.Minute, // 512 s, capped
		1 * sec,
	}
	if got := f.pauses(); !slices.Equal(got, want) {
		t.Errorf("pauses %v, want %v", got, want)
	}
	reqs := f.srv.seen()
	for i, r := range reqs[:11] {
		if !slices.Equal(r.markers, []string{"mark-a"}) {
			t.Errorf("request %d carries %v, want the same request of mark-a", i, r.markers)
		}
	}
	if st := f.stats(t); st.Rejected != 0 {
		t.Errorf("%d records rejected, want none", st.Rejected)
	}
}

func TestBackoffJitter(t *testing.T) {
	t.Parallel()

	s := New(Config{})
	for _, tc := range []struct {
		random float64
		want   time.Duration
	}{{0, 800 * time.Millisecond}, {1, 1200 * time.Millisecond}} {
		s.backoff = 0
		s.random = func() float64 { return tc.random }
		if got := s.nextBackoff(); got != tc.want {
			t.Errorf("random %v: pause %v, want %v", tc.random, got, tc.want)
		}
	}
	s.backoff = maxBackoff
	s.random = func() float64 { return 1 }
	if got := s.nextBackoff(); got != maxBackoff {
		t.Errorf("pause %v at the cap with +20 %%, want %v", got, maxBackoff)
	}
}

func TestUnauthorizedPausesUntilANewToken(t *testing.T) {
	t.Parallel()

	f := newFixture(t, func(_ int, r request) answer {
		if r.auth == "Bearer tok-1" {
			a := statusJSON("token revoked")
			a.code = http.StatusUnauthorized
			return a
		}
		return answer{code: http.StatusOK}
	})
	f.put(t, "a", 0)
	f.put(t, "b", 0)

	unauthorized := func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.unauth) > 0
	}
	f.run(t, unauthorized)
	// While the token is the refused one, nothing more is sent.
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_ = f.sender.Run(ctx)

	if n := len(f.srv.seen()); n != 1 {
		t.Errorf("%d requests with the refused token, want 1", n)
	}
	if got := f.unauth; !slices.Equal(got, []string{"401: token revoked"}) {
		t.Errorf("OnUnauthorized got %v, want the reason once", got)
	}
	if st := f.status(t); st.Unauthorized != "401: token revoked" {
		t.Errorf("status %+v, want the reason of the stop", st)
	}
	if st := f.stats(t); st.Queued != 2 || st.Rejected != 0 {
		t.Errorf("queued %d, rejected %d; want the records kept", st.Queued, st.Rejected)
	}
	if p := f.pauses(); len(p) != 0 {
		t.Errorf("pauses %v, want none: 401 is not retried", p)
	}

	f.creds.setToken("tok-2")
	f.run(t, f.empty(t))

	reqs := f.srv.seen()
	if last := reqs[len(reqs)-1]; last.auth != "Bearer tok-2" || !slices.Equal(last.markers, []string{"mark-a", "mark-b"}) {
		t.Errorf("request after the new token %+v, want both records with tok-2", last)
	}
	if st := f.status(t); st.Unauthorized != "" || st.LastError != nil {
		t.Errorf("status %+v, want the stop cleared", st)
	}
}

func TestEmptyQueueWaitsForTheSignal(t *testing.T) {
	t.Parallel()

	f := newFixture(t, ok)
	f.sender.poll = time.Hour
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- f.sender.Run(ctx) }()
	defer func() {
		cancel()
		<-stopped
	}()

	// The sender is idle on an empty queue; a record alone does not wake it within the hour.
	time.Sleep(20 * time.Millisecond)
	f.put(t, "a", 0)
	time.Sleep(20 * time.Millisecond)
	if n := len(f.srv.seen()); n != 0 {
		t.Fatalf("%d requests before the signal, want none", n)
	}
	f.wake <- struct{}{}
	deadline := time.Now().Add(5 * time.Second)
	for f.stats(t).Queued != 0 {
		if time.Now().After(deadline) {
			t.Fatal("record not sent after the signal")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestEmptyQueueIsPolled(t *testing.T) {
	t.Parallel()

	f := newFixture(t, ok)
	f.sender.poll = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- f.sender.Run(ctx) }()
	defer func() {
		cancel()
		<-stopped
	}()

	time.Sleep(20 * time.Millisecond)
	f.put(t, "a", 0)
	deadline := time.Now().Add(5 * time.Second)
	for f.stats(t).Queued != 0 {
		if time.Now().After(deadline) {
			t.Fatal("record not sent by the poll")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if New(Config{}).poll != 2*time.Second {
		t.Error("the default poll is not 2 s")
	}
}

func TestUnsendableRecordsAreRejectedWithoutARequest(t *testing.T) {
	t.Parallel()

	f := newFixture(t, ok)
	f.put(t, "big", RecordBytes)
	if _, err := f.q.Put([]byte(`{"kind":"other"}`), []byte(`{"m":"mark-unknown"}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	f.put(t, "a", 0)
	f.run(t, f.empty(t))

	reqs := f.srv.seen()
	if len(reqs) != 1 || !slices.Equal(reqs[0].markers, []string{"mark-a"}) {
		t.Errorf("requests %+v, want one with mark-a only", reqs)
	}
	got := f.rejectedReasons(t)
	if r := got["mark-big"]; !strings.HasPrefix(r, "413: record of ") {
		t.Errorf("reason of the large record %q, want a 413 reason", r)
	}
	if r := got["mark-unknown"]; !strings.HasPrefix(r, "not encodable: ") {
		t.Errorf("reason of the unknown record %q, want not encodable", r)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	t.Parallel()

	if d := retryAfter("7"); d == nil || *d != 7*time.Second {
		t.Errorf("retryAfter(7) = %v", d)
	}
	for _, v := range []string{"", "soon", "-1", "Wed, 21 Oct 2026 07:28:00 GMT"} {
		if d := retryAfter(v); d != nil {
			t.Errorf("retryAfter(%q) = %v, want none", v, *d)
		}
	}
}

func TestNetworkErrorIsRetried(t *testing.T) {
	t.Parallel()

	// A port nobody listens on: the first attempts fail to connect.
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := "http://" + l.Addr().String()
	_ = l.Close()

	f := newFixture(t, ok)
	live := f.creds.c.IngestURL
	f.creds.c.IngestURL = dead
	f.put(t, "a", 0)
	f.sender.sleep = func(ctx context.Context, d time.Duration) error {
		f.mu.Lock()
		f.sleeps = append(f.sleeps, d)
		n := len(f.sleeps)
		f.mu.Unlock()
		if n == 2 {
			f.creds.mu.Lock()
			f.creds.c.IngestURL = live
			f.creds.mu.Unlock()
		}
		return ctx.Err()
	}
	f.run(t, f.empty(t))

	if got, want := f.pauses(), []time.Duration{time.Second, 2 * time.Second}; !slices.Equal(got, want) {
		t.Errorf("pauses %v, want %v", got, want)
	}
	if n := len(f.srv.seen()); n != 1 {
		t.Errorf("%d requests reached the service, want 1", n)
	}
}

func TestUnauthorizedRemembersTheTokenOfTheRetry(t *testing.T) {
	t.Parallel()

	// 503 with tok-1; the daemon saves tok-2 during the pause, and the service refuses it.
	f := newFixture(t, func(_ int, r request) answer {
		if r.auth == "Bearer tok-1" {
			return answer{code: http.StatusServiceUnavailable}
		}
		return answer{code: http.StatusUnauthorized}
	})
	f.sender.sleep = func(ctx context.Context, _ time.Duration) error {
		f.creds.setToken("tok-2")
		return ctx.Err()
	}
	f.put(t, "a", 0)
	f.run(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.unauth) > 0
	})
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_ = f.sender.Run(ctx)

	var auths []string
	for _, r := range f.srv.seen() {
		auths = append(auths, r.auth)
	}
	if want := []string{"Bearer tok-1", "Bearer tok-2"}; !slices.Equal(auths, want) {
		t.Errorf("requests with %v, want %v and nothing more with the refused tok-2", auths, want)
	}
}

// brokenQueue fails Ack or Reject the first times it is asked.
type brokenQueue struct {
	*queue.Queue

	mu                    sync.Mutex
	ackFails, rejectFails int
}

func (b *brokenQueue) Ack(ids []string) error {
	b.mu.Lock()
	fail := b.ackFails > 0
	b.ackFails--
	b.mu.Unlock()
	if fail {
		return errors.New("read-only volume")
	}
	return b.Queue.Ack(ids)
}

func (b *brokenQueue) Reject(ids []string, reason string) error {
	b.mu.Lock()
	fail := b.rejectFails > 0
	b.rejectFails--
	b.mu.Unlock()
	if fail {
		return errors.New("disk full")
	}
	return b.Queue.Reject(ids, reason)
}

func TestFailedAckPausesBeforeSendingAgain(t *testing.T) {
	t.Parallel()

	f := newFixture(t, ok)
	f.sender.cfg.Queue = &brokenQueue{Queue: f.q, ackFails: 3}
	f.put(t, "a", 0)
	f.run(t, f.empty(t))

	// Taken four times, removed the fourth; the pause grows though every answer is 2xx.
	if n := len(f.srv.seen()); n != 4 {
		t.Errorf("%d requests, want 4", n)
	}
	if got, want := f.pauses(), []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}; !slices.Equal(got, want) {
		t.Errorf("pauses %v, want %v", got, want)
	}
	// After the records are gone the pause starts again at 1 s.
	if f.sender.backoff != 0 {
		t.Errorf("backoff %v after the records were removed, want reset", f.sender.backoff)
	}
}

func TestFailedRejectPausesBeforeTryingAgain(t *testing.T) {
	t.Parallel()

	f := newFixture(t, func(int, request) answer { return answer{code: http.StatusBadRequest} })
	f.sender.cfg.Queue = &brokenQueue{Queue: f.q, rejectFails: 2}
	f.put(t, "a", 0)
	f.run(t, f.empty(t))

	if n := len(f.srv.seen()); n != 3 {
		t.Errorf("%d requests, want 3", n)
	}
	if got, want := f.pauses(), []time.Duration{time.Second, 2 * time.Second}; !slices.Equal(got, want) {
		t.Errorf("pauses %v, want %v", got, want)
	}
	if got := f.rejectedReasons(t); !maps.Equal(got, map[string]string{"mark-a": "400"}) {
		t.Errorf("rejected %v, want mark-a with 400", got)
	}
	if st := f.status(t); st.LastError == nil || !strings.Contains(st.LastError.Reason, "disk full") {
		t.Errorf("status %+v, want the queue's failure", st)
	}
}

func TestSplitStoppedByUnauthorized(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// firstAlone is the answer to a alone; b alone gets 400, c alone 401.
		firstAlone int
		rejected   map[string]string
		queued     int
	}{
		// a was taken, so b is the record's fault; c waits for a new token.
		{"after a taken record", http.StatusOK, map[string]string{"mark-b": "400"}, 1},
		// Nothing was taken: b's refusal proves nothing, and b stays with c.
		{"with nothing taken", http.StatusBadRequest, map[string]string{}, 3},
	} {
		f := newFixture(t, func(_ int, r request) answer {
			switch {
			case len(r.markers) > 1:
				return answer{code: http.StatusRequestEntityTooLarge}
			case r.markers[0] == "mark-a":
				return answer{code: tc.firstAlone}
			case r.markers[0] == "mark-b":
				return answer{code: http.StatusBadRequest}
			default:
				return answer{code: http.StatusUnauthorized}
			}
		})
		f.put(t, "a", 0)
		f.put(t, "b", 0)
		f.put(t, "c", 0)
		f.run(t, func() bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			return len(f.unauth) > 0
		})

		got := map[string]string{}
		if _, err := os.Stat(filepath.Join(f.root, "rejected")); err == nil {
			got = f.rejectedReasons(t)
		}
		if !maps.Equal(got, tc.rejected) {
			t.Errorf("%s: rejected %v, want %v", tc.name, got, tc.rejected)
		}
		if st := f.stats(t); st.Queued != tc.queued {
			t.Errorf("%s: %d queued, want %d", tc.name, st.Queued, tc.queued)
		}
	}
}

func TestSplitGoesWithTheCredentialsOfTheRetry(t *testing.T) {
	t.Parallel()

	// The batch gets 503 with tok-1; tok-2 arrives during the pause, and the batch is
	// then too large. tok-1 is revoked by then.
	f := newFixture(t, func(_ int, r request) answer {
		switch {
		case r.auth == "Bearer tok-1" && len(r.markers) > 1:
			return answer{code: http.StatusServiceUnavailable}
		case r.auth == "Bearer tok-1":
			return answer{code: http.StatusUnauthorized}
		case len(r.markers) > 1:
			return answer{code: http.StatusRequestEntityTooLarge}
		default:
			return answer{code: http.StatusOK}
		}
	})
	f.sender.sleep = func(ctx context.Context, _ time.Duration) error {
		f.creds.setToken("tok-2")
		return ctx.Err()
	}
	f.put(t, "a", 0)
	f.put(t, "b", 0)
	f.run(t, f.empty(t))

	var auths []string
	for _, r := range f.srv.seen() {
		auths = append(auths, r.auth)
	}
	if want := []string{"Bearer tok-1", "Bearer tok-2", "Bearer tok-2", "Bearer tok-2"}; !slices.Equal(auths, want) {
		t.Errorf("requests with %v, want %v", auths, want)
	}
	if len(f.unauth) != 0 {
		t.Errorf("OnUnauthorized got %v, want no call", f.unauth)
	}
}

func TestStatusSurvivesARestart(t *testing.T) {
	t.Parallel()

	f := newFixture(t, func(n int, _ request) answer {
		if n == 0 {
			return answer{code: http.StatusOK}
		}
		return answer{code: http.StatusServiceUnavailable}
	})
	f.put(t, "a", 0)
	f.run(t, f.empty(t))
	first := f.status(t).LastSuccess

	// The previous daemon had also stopped on 401; a new one does not know that yet.
	saved := f.status(t)
	saved.Unauthorized = "401: token revoked"
	if err := writeStatus(filepath.Join(f.root, "sender.json"), saved); err != nil {
		t.Fatal(err)
	}

	// A new sender on the same state sees the 503 and keeps the last success.
	restarted := newFixture(t, nil)
	restarted.srv = f.srv
	restarted.sender = New(Config{Queue: f.q, Credentials: f.creds.get, StatusFile: filepath.Join(f.root, "sender.json")})
	restarted.sender.sleep = func(context.Context, time.Duration) error { return context.Canceled }
	f.put(t, "b", 0)
	restarted.run(t, func() bool { return f.status(t).LastError != nil })

	st := f.status(t)
	if !st.LastSuccess.Equal(first) || st.LastError == nil || st.LastError.Code != http.StatusServiceUnavailable {
		t.Errorf("status %+v, want the last success %v kept with the 503", st, first)
	}
	if st.Unauthorized != "" {
		t.Errorf("status %+v, want the saved 401 stop cleared by the restart", st)
	}
}

func TestRequestWithoutAnAnswerIsRetried(t *testing.T) {
	t.Parallel()

	f := newFixture(t, func(n int, _ request) answer {
		if n == 0 {
			return answer{hang: true}
		}
		return answer{code: http.StatusOK}
	})
	// A client of the daemon's own without a timeout is bounded all the same.
	f.sender.client = &http.Client{}
	f.sender.timeout = time.Second
	f.put(t, "a", 0)
	f.run(t, f.empty(t))

	if n := len(f.srv.seen()); n != 2 {
		t.Errorf("%d requests, want the one without an answer and its retry", n)
	}
	if got := f.pauses(); !slices.Equal(got, []time.Duration{time.Second}) {
		t.Errorf("pauses %v, want one of 1s", got)
	}
	if New(Config{}).timeout != 60*time.Second {
		t.Error("the default request timeout is not 60 s")
	}
}

func TestStopWaitsForTheRequestInFlight(t *testing.T) {
	t.Parallel()

	arrived, release := make(chan struct{}), make(chan struct{})
	f := newFixture(t, func(int, request) answer {
		close(arrived)
		<-release
		return answer{code: http.StatusOK}
	})
	f.put(t, "a", 0)

	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- f.sender.Run(ctx) }()
	<-arrived
	cancel()
	select {
	case <-stopped:
		close(release)
		t.Fatal("Run returned before the request in flight was answered")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if err := <-stopped; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st := f.stats(t); st.Queued != 0 || st.Rejected != 0 {
		t.Errorf("queue %+v after the answer, want the record acked", st)
	}
	if n := len(f.srv.seen()); n != 1 {
		t.Errorf("%d requests, want only the one in flight", n)
	}
}
