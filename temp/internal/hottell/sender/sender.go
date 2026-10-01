// Package sender sends the on-disk queue to the service's OTLP intake, as
// docs/specs/hottell-contract/ingest.md («Приём») lays it out: batches of at most 4 MiB
// go to <service>/v1/logs in protobuf, gzipped, with the collector token, and every
// answer of the service has its own reaction.
//
// Run is the daemon's sending loop. It keeps no records in memory between batches: what
// is not acknowledged stays in the queue on disk.
package sender

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/otlp"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/queue"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

// Limits of ingest.md «Лимиты».
const (
	// BatchBytes bounds a batch of the binary.
	BatchBytes int64 = 4 << 20
	// RecordBytes is the largest record the service takes.
	RecordBytes = 8 << 20
)

// Timings of ingest.md «Ответы сервера» and of the card.
const (
	// RequestTimeout bounds one request; a request without an answer by then is retried.
	RequestTimeout = 60 * time.Second
	// PollInterval is how often an idle sender looks at the queue and the credentials.
	PollInterval = 2 * time.Second
	firstBackoff = time.Second
	maxBackoff   = 5 * time.Minute
	// jitter is the random spread of a pause, ±20 %.
	jitter = 0.2
)

// logsPath is the path of the intake under the service address.
const logsPath = "/v1/logs"

// errorBodyBytes bounds how much of an error response is read for its message.
const errorBodyBytes = 64 << 10

// Queue is what the sender takes from the queue; *queue.Queue implements it.
type Queue interface {
	Next(maxBytes int64) ([]queue.Record, error)
	Ack(ids []string) error
	Reject(ids []string, reason string) error
}

// Config is what the sender needs. Queue and Credentials are required.
type Config struct {
	Queue Queue
	// Credentials returns the service address and the collector token; it is read before
	// every request, so a token the daemon saves takes effect without a restart.
	Credentials func() (state.Credentials, error)
	// Resource describes the binary in every request.
	Resource otlp.Resource
	// Wake signals new records in the queue, for example from the transcript readers.
	// Without a signal the sender still looks every PollInterval.
	Wake <-chan struct{}
	// OnUnauthorized is called with the reason when the service answers 401: sending
	// stops until Credentials returns another token. Optional.
	OnUnauthorized func(reason string)
	// StatusFile is where the sender keeps its Status for hottell status; "" keeps none.
	StatusFile string
	// Client sends the requests; nil is a client with RequestTimeout.
	Client *http.Client
	// Log receives the sender's messages; nil discards them.
	Log *slog.Logger
}

// Sender sends the queue; create it with New and run it with Run.
type Sender struct {
	cfg    Config
	client *http.Client
	log    *slog.Logger
	status Status

	// backoff is the next retry pause before jitter; 0 means firstBackoff.
	backoff time.Duration
	// paused is the token the service answered 401 to; sending waits for another one.
	paused *string

	// Seams for the tests.
	now     func() time.Time
	sleep   func(ctx context.Context, d time.Duration) error
	random  func() float64
	poll    time.Duration
	timeout time.Duration
}

// New returns a sender with cfg.
func New(cfg Config) *Sender {
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: RequestTimeout}
	}
	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	// A restart keeps what the previous daemon saw, the last success included, but not
	// a stop on 401: a new daemon sends until the service refuses its token again.
	var st Status
	if cfg.StatusFile != "" {
		var err error
		if st, err = ReadStatus(cfg.StatusFile); err != nil {
			log.Warn("read the sender status", "err", err)
		}
		st.Unauthorized = ""
	}
	return &Sender{
		cfg:     cfg,
		client:  client,
		log:     log,
		status:  st,
		now:     time.Now,
		sleep:   sleepCtx,
		random:  rand.Float64, //nolint:gosec // jitter of a retry pause
		poll:    PollInterval,
		timeout: RequestTimeout,
	}
}

// Run sends the queue until ctx is done, then returns nil. A request in flight when ctx
// ends still waits for its answer, within RequestTimeout, and the answer settles its
// records; no request starts after that, and what is not settled stays in the queue.
func (s *Sender) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		creds, ok := s.credentials()
		if !ok {
			s.idle(ctx)
			continue
		}
		batch, err := s.cfg.Queue.Next(BatchBytes)
		if err != nil {
			s.log.ErrorContext(ctx, "read the queue", "err", err)
			s.idle(ctx)
			continue
		}
		if len(batch) == 0 {
			s.idle(ctx)
			continue
		}
		s.deliver(ctx, &creds, batch)
	}
	return nil
}

// credentials returns the credentials to send with, or false when there are none yet or
// the token is the one the service refused.
func (s *Sender) credentials() (state.Credentials, bool) {
	creds, err := s.cfg.Credentials()
	if err != nil {
		s.log.Error("read the credentials", "err", err)
		return creds, false
	}
	if creds.IngestURL == "" || creds.CollectorToken == "" {
		return creds, false
	}
	if s.paused != nil {
		if creds.CollectorToken == *s.paused {
			return creds, false
		}
		s.paused = nil
		s.status.Unauthorized = ""
		s.saveStatus()
		s.log.Info("new collector token, sending resumes")
	}
	return creds, true
}

// idle waits for a signal of new records, the next poll or the end of ctx.
func (s *Sender) idle(ctx context.Context) {
	t := time.NewTimer(s.poll)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-s.cfg.Wake:
	case <-t.C:
	}
}

// outcome is how the service answered one request.
type outcome int

const (
	accepted outcome = iota
	// refused: 400 or 413, the request as it is will never be taken.
	refused
	unauthorized
	// retry: 429, 5xx, other 4xx or no answer; the same request goes again after a pause.
	retry
	// canceled: ctx ended, or the request could not be built, before an answer settled it.
	canceled
)

// result is the settled answer to one request.
type result struct {
	outcome outcome
	code    int
	reason  string
	// token is the collector token the request went with.
	token string
}

// deliver sends one batch and settles its records by the answer.
func (s *Sender) deliver(ctx context.Context, creds *state.Credentials, batch []queue.Record) {
	observed := s.now()
	batch = s.dropUnsendable(ctx, batch, observed)
	if len(batch) == 0 {
		return
	}
	res := s.send(ctx, creds, batch, observed)
	switch res.outcome {
	case accepted:
		s.ack(ctx, batch)
	case refused:
		if len(batch) == 1 {
			s.reject(ctx, batch, res.reason)
			return
		}
		s.split(ctx, creds, batch, observed)
	case unauthorized:
		s.pause(res.token, res.reason)
	case retry, canceled:
	}
}

// split sends the records of a refused batch one by one (ingest.md «Отказ всей пачки —
// не отказ записей»). A record is rejected only when another record of the batch was
// taken; when every record is refused, the request is at fault, and the batch stays in
// the queue to be retried with a pause.
func (s *Sender) split(ctx context.Context, creds *state.Credentials, batch []queue.Record, observed time.Time) {
	var (
		taken   int
		refusal []result
		refRecs []queue.Record
	)
	settle := func() {
		if taken == 0 {
			return
		}
		for i, rec := range refRecs {
			s.reject(ctx, []queue.Record{rec}, refusal[i].reason)
		}
	}
	for _, rec := range batch {
		if ctx.Err() != nil {
			settle()
			return
		}
		one := []queue.Record{rec}
		res := s.send(ctx, creds, one, observed)
		switch res.outcome {
		case accepted:
			s.ack(ctx, one)
			taken++
		case refused:
			refusal = append(refusal, res)
			refRecs = append(refRecs, rec)
		case unauthorized:
			settle()
			s.pause(res.token, res.reason)
			return
		case retry, canceled:
			settle()
			return
		}
	}
	if taken > 0 {
		settle()
		return
	}
	last := refusal[len(refusal)-1]
	s.failed(last.code, "every record of the batch was refused alone: "+last.reason)
	_ = s.wait(ctx, nil)
}

// dropUnsendable rejects the records the service would never take — larger than
// RecordBytes, or not encodable — and returns the rest.
func (s *Sender) dropUnsendable(ctx context.Context, batch []queue.Record, observed time.Time) []queue.Record {
	kept := batch[:0:0]
	for _, rec := range batch {
		size, err := otlp.RecordSize(rec, observed)
		switch {
		case err != nil:
			s.reject(ctx, []queue.Record{rec}, "not encodable: "+err.Error())
		case size > RecordBytes:
			s.reject(ctx, []queue.Record{rec}, fmt.Sprintf("413: record of %d bytes exceeds %d bytes, not sent", size, RecordBytes))
		default:
			kept = append(kept, rec)
		}
	}
	return kept
}

// send posts batch until an answer settles it: 429, 5xx, other 4xx and network errors
// are retried with a growing pause, and the credentials are read again into creds before
// a retry, so the requests after this one go with them too.
func (s *Sender) send(ctx context.Context, creds *state.Credentials, batch []queue.Record, observed time.Time) result {
	body, err := encode(batch, s.cfg.Resource, observed)
	if err != nil {
		// dropUnsendable encoded every record alone, so this is not the records' fault:
		// they stay in the queue, and the next batch comes after a pause.
		s.log.ErrorContext(ctx, "encode the batch", "err", err)
		s.failed(0, err.Error())
		_ = s.wait(ctx, nil)
		return result{outcome: canceled}
	}
	for {
		if ctx.Err() != nil {
			return result{outcome: canceled}
		}
		res, retryAfter := s.post(ctx, *creds, body)
		if res.outcome != retry {
			return res
		}
		if ctx.Err() != nil {
			return result{outcome: canceled}
		}
		s.failed(res.code, res.reason)
		s.log.WarnContext(ctx, "send failed, retrying", "code", res.code, "reason", res.reason, "records", len(batch))
		if s.wait(ctx, retryAfter) != nil {
			return result{outcome: canceled}
		}
		if next, err := s.cfg.Credentials(); err == nil && next.IngestURL != "" && next.CollectorToken != "" {
			*creds = next
		}
	}
}

// post sends one request. A retryable failure comes with the Retry-After of a 429 or
// 503 when the service gave one. The end of ctx does not cut the request short: once
// sent, its answer is waited for within the timeout, so that the records it took are
// not sent again.
func (s *Sender) post(ctx context.Context, creds state.Credentials, body []byte) (result, *time.Duration) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.timeout)
	defer cancel()
	url := strings.TrimSuffix(creds.IngestURL, "/") + logsPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return result{outcome: retry, reason: err.Error()}, nil
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Authorization", "Bearer "+creds.CollectorToken)
	resp, err := s.client.Do(req)
	if err != nil {
		return result{outcome: retry, reason: err.Error()}, nil
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyBytes))
	code := resp.StatusCode
	if code >= 200 && code < 300 {
		s.status.LastSuccess = s.now()
		s.status.LastError = nil
		s.saveStatus()
		return result{outcome: accepted, code: code}, nil
	}
	reason := strconv.Itoa(code)
	if msg := otlp.StatusMessage(data, resp.Header.Get("Content-Type")); msg != "" {
		reason += ": " + msg
	}
	switch code {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		return result{outcome: refused, code: code, reason: reason}, nil
	case http.StatusUnauthorized:
		return result{outcome: unauthorized, code: code, reason: reason, token: creds.CollectorToken}, nil
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return result{outcome: retry, code: code, reason: reason}, retryAfter(resp.Header.Get("Retry-After"))
	default:
		return result{outcome: retry, code: code, reason: reason}, nil
	}
}

// wait sleeps the next retry pause: 1 s doubling to 5 min with ±20 % spread, at least
// retryAfter and never over 5 min. It fails when ctx ends first.
func (s *Sender) wait(ctx context.Context, retryAfter *time.Duration) error {
	d := s.nextBackoff()
	if retryAfter != nil && *retryAfter > d {
		d = min(*retryAfter, maxBackoff)
	}
	return s.sleep(ctx, d)
}

func (s *Sender) nextBackoff() time.Duration {
	base := s.backoff
	if base == 0 {
		base = firstBackoff
	}
	s.backoff = min(base*2, maxBackoff)
	d := time.Duration(float64(base) * (1 + jitter*(2*s.random()-1)))
	return min(d, maxBackoff)
}

// pause stops sending with token until the credentials hold another one.
func (s *Sender) pause(token, reason string) {
	s.paused = &token
	s.status.Unauthorized = reason
	s.failed(http.StatusUnauthorized, reason)
	s.log.Warn("the service refused the collector token, sending stops", "reason", reason)
	if s.cfg.OnUnauthorized != nil {
		s.cfg.OnUnauthorized(reason)
	}
}

// ack removes the records the service took. The pause starts again only once they are
// gone: a queue that cannot remove them would otherwise send them again at once.
func (s *Sender) ack(ctx context.Context, recs []queue.Record) {
	if err := s.cfg.Queue.Ack(ids(recs)); err != nil {
		s.stuck(ctx, "remove sent records", err)
		return
	}
	s.backoff = 0
}

func (s *Sender) reject(ctx context.Context, recs []queue.Record, reason string) {
	s.log.WarnContext(ctx, "records rejected", "records", len(recs), "reason", reason)
	if err := s.cfg.Queue.Reject(ids(recs), reason); err != nil {
		s.stuck(ctx, "move rejected records", err)
	}
}

// stuck records a queue that cannot settle records and pauses: the records stay at its
// head and would come back at once.
func (s *Sender) stuck(ctx context.Context, what string, err error) {
	s.log.ErrorContext(ctx, what, "err", err)
	s.failed(0, what+": "+err.Error())
	_ = s.wait(ctx, nil)
}

func (s *Sender) failed(code int, reason string) {
	s.status.LastError = &Failure{At: s.now(), Code: code, Reason: reason}
	s.saveStatus()
}

func (s *Sender) saveStatus() {
	if s.cfg.StatusFile == "" {
		return
	}
	if err := writeStatus(s.cfg.StatusFile, s.status); err != nil {
		s.log.Error("save the sender status", "err", err)
	}
}

// encode is the gzipped OTLP request of batch.
func encode(batch []queue.Record, res otlp.Resource, observed time.Time) ([]byte, error) {
	raw, err := otlp.Encode(batch, res, observed)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return nil, fmt.Errorf("gzip the request: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("gzip the request: %w", err)
	}
	return buf.Bytes(), nil
}

// retryAfter reads a Retry-After in seconds; a date or garbage is ignored.
func retryAfter(v string) *time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return nil
	}
	d := time.Duration(n) * time.Second
	return &d
}

func ids(recs []queue.Record) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.ID
	}
	return out
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
