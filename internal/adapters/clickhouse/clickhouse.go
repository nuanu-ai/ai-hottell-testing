// Package clickhouse holds the ClickHouse adapter of the service: it reads the telemetry the
// collector writes to the database otel.
package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// defaultDialTimeout bounds a connection attempt when the URL sets no dial_timeout, so a read
// while ClickHouse is down answers telemetry.ErrUnavailable in seconds; the driver's own default
// is 30 seconds.
const defaultDialTimeout = 5 * time.Second

// redacted replaces the password wherever a driver error would show it.
const redacted = "[REDACTED]"

var errInvalidURL = errors.New("parse clickhouse url: invalid connection string")

// Reader reads the telemetry from ClickHouse. It connects on the first read, not on Open, so the
// service starts while ClickHouse is down; every read then answers telemetry.ErrUnavailable
// until ClickHouse comes back.
type Reader struct {
	conn driver.Conn
	// secrets are the forms of the password a driver error can show: as is and URL-encoded.
	secrets []string
	logger  *slog.Logger
	// useAggregates makes the period reads read the aggregate tables (aggregates.go).
	useAggregates bool
}

// Option sets up a Reader on Open.
type Option func(*Reader)

// WithLogger sets the logger the Reader writes the driver's text of a failed read to, at the
// debug level, and the operation and code of a refused one at warn; without it the Reader
// writes to slog.Default.
func WithLogger(logger *slog.Logger) Option {
	return func(r *Reader) {
		if logger != nil {
			r.logger = logger
		}
	}
}

// Open returns a Reader for the clickhouse:// URL rawURL without connecting. It fails only when
// the URL does not parse; the error never carries the URL, which holds the password. The caller
// closes the Reader on shutdown.
func Open(rawURL string, options ...Option) (*Reader, error) {
	opts, err := clickhouse.ParseDSN(rawURL)
	if err != nil {
		return nil, errInvalidURL
	}
	if u, err := url.Parse(rawURL); err != nil || !u.Query().Has("dial_timeout") {
		opts.DialTimeout = defaultDialTimeout
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("open clickhouse: %w", errInvalidURL)
	}
	r := &Reader{conn: undeadlined{conn}, secrets: passwordForms(opts.Auth.Password), logger: slog.Default()}
	for _, o := range options {
		o(r)
	}
	return r, nil
}

// passwordForms returns password as is and in its URL encodings, longest first so that a longer
// form is replaced whole before a shorter one inside it; nothing for an empty password.
func passwordForms(password string) []string {
	if password == "" {
		return nil
	}
	forms := []string{password}
	for _, f := range []string{url.QueryEscape(password), url.PathEscape(password)} {
		if !slices.Contains(forms, f) {
			forms = append(forms, f)
		}
	}
	slices.SortFunc(forms, func(a, b string) int { return len(b) - len(a) })
	return forms
}

// Ping checks that ClickHouse accepts the reader; telemetry.ErrUnavailable when it cannot be
// reached or refuses the credentials.
func (r *Reader) Ping(ctx context.Context) error {
	if err := r.conn.Ping(ctx); err != nil {
		return r.unavailable("ping clickhouse", err)
	}
	return nil
}

// Close closes the connections of the Reader.
func (r *Reader) Close() error {
	if err := r.conn.Close(); err != nil {
		return r.unavailable("close clickhouse", err)
	}
	return nil
}

// ErrServerException is answered by a read the connected ClickHouse refused with an exception:
// a syntax error, a denied or readonly statement (164), a memory limit. The store is reachable,
// so it is not telemetry.ErrUnavailable and the API answers it 500 internal, not 503 (HT-481).
var ErrServerException = errors.New("clickhouse refused the read")

// authenticationFailed is the code of the exception ClickHouse answers wrong credentials with:
// the reader cannot get in, which is unavailability as for a server that cannot be reached.
const authenticationFailed = 516

// unavailable returns err of the operation op as telemetry.ErrUnavailable, or as
// ErrServerException when the server refused the read with an exception other than failed
// authentication. The returned error names the operation, the code of a refusing exception and
// the category only: the text of the driver, which the ClickHouse server writes and which can carry parts of
// the query and its values, goes to the debug log of the Reader's logger alone, with the password
// replaced as is and URL-encoded, so neither a response nor an ordinary log line built from the
// error carries it (HT-373). An exception's operation and code go to the warn log, so a refused
// read is seen without debug (HT-481).
func (r *Reader) unavailable(op string, err error) error {
	text := err.Error()
	for _, s := range r.secrets {
		text = strings.ReplaceAll(text, s, redacted)
	}
	r.logger.Debug("clickhouse read failed", "op", op, "error", text)
	var exception *clickhouse.Exception
	if !errors.As(err, &exception) {
		return fmt.Errorf("%s: %w", op, telemetry.ErrUnavailable)
	}
	r.logger.Warn("clickhouse refused the read", "op", op, "code", exception.Code)
	if exception.Code == authenticationFailed {
		return fmt.Errorf("%s: %w", op, telemetry.ErrUnavailable)
	}
	return fmt.Errorf("%s: code %d: %w", op, exception.Code, ErrServerException)
}

// undeadlined is the connection of a Reader: it runs every query without the deadline of its
// context. The driver turns a deadline longer than a second into the setting max_execution_time,
// which the readonly profile of the reader refuses with code 164, so every read under a deadline
// failed (HT-476). The cancellation of the context, by the caller or by its deadline passing,
// still stops the query.
type undeadlined struct {
	driver.Conn
}

// Query runs query under ctx without its deadline; the query's context is cancelled when the
// rows are closed or ctx is done, whichever is first.
func (c undeadlined) Query(ctx context.Context, query string, args ...any) (driver.Rows, error) {
	qctx, release := withoutDeadline(ctx)
	rows, err := c.Conn.Query(qctx, query, args...)
	if err != nil {
		release()
		return nil, err //nolint:wrapcheck // the Reader wraps the driver's errors as ErrUnavailable
	}
	return releasingRows{Rows: rows, release: release}, nil
}

// QueryRow runs query under ctx without its deadline; the query's context is cancelled once the
// row is scanned or ctx is done, whichever is first.
func (c undeadlined) QueryRow(ctx context.Context, query string, args ...any) driver.Row {
	qctx, release := withoutDeadline(ctx)
	return releasingRow{Row: c.Conn.QueryRow(qctx, query, args...), release: release}
}

// withoutDeadline returns a context that carries the values of ctx but not its deadline and is
// cancelled when ctx is done, and the function that cancels it and stops watching ctx.
func withoutDeadline(ctx context.Context) (context.Context, func()) {
	qctx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	stop := context.AfterFunc(ctx, func() { cancel(context.Cause(ctx)) })
	return qctx, func() {
		stop()
		cancel(context.Canceled)
	}
}

// releasingRows releases the query's context when the rows are closed.
type releasingRows struct {
	driver.Rows
	release func()
}

func (r releasingRows) Close() error {
	defer r.release()
	return r.Rows.Close() //nolint:wrapcheck // the Reader wraps the driver's errors as ErrUnavailable
}

// releasingRow releases the query's context once the row is scanned.
type releasingRow struct {
	driver.Row
	release func()
}

func (r releasingRow) Scan(dest ...any) error {
	defer r.release()
	return r.Row.Scan(dest...) //nolint:wrapcheck // the Reader wraps the driver's errors as ErrUnavailable
}

func (r releasingRow) ScanStruct(dest any) error {
	defer r.release()
	return r.Row.ScanStruct(dest) //nolint:wrapcheck // the Reader wraps the driver's errors as ErrUnavailable
}
