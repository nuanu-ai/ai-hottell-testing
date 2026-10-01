package mcpclient

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

// The pauses of Watch (mcp.md, «Разрыв и переподключение»).
const (
	// firstPause doubles up to maxPause after every failed connection.
	firstPause = time.Second
	maxPause   = 60 * time.Second
	// unauthorizedPause is how often a revoked key is tried again: only the user issues
	// a new one.
	unauthorizedPause = 5 * time.Minute
	// stableAfter is how long a connection holds before the pauses start over.
	stableAfter = 60 * time.Second
	// jitter is the random share each pause changes by.
	jitter = 0.2
)

// pauses are the waits of Watch; tests shorten them.
type pauses struct {
	first, max, unauthorized, stable time.Duration
}

func defaultPauses() pauses {
	return pauses{first: firstPause, max: maxPause, unauthorized: unauthorizedPause, stable: stableAfter}
}

// backoff counts the pauses 1, 2, 4 … max seconds, each changed by ±20 %.
type backoff struct {
	p    pauses
	next time.Duration
}

func (b *backoff) reset() { b.next = 0 }

func (b *backoff) pause() time.Duration {
	if b.next == 0 {
		b.next = b.p.first
	}
	d := b.next
	b.next = min(2*b.next, b.p.max)
	return time.Duration(float64(d) * (1 + jitter*(2*rand.Float64()-1)))
}

// Watch keeps a subscription to the settings resource until ctx ends, and returns
// ctx's error. On every connection it re-reads the key from the agents' configs,
// fetches the collector token, subscribes and reads the settings; on every
// notifications/resources/updated it reads them again. Each read goes through Settings,
// and onChange gets the cache whenever its version grew. A broken connection is
// reconnected after 1 s doubling to 60 s, and a revoked key (401) is tried again every
// 5 minutes; the reason stays in the status for hottell status. A failed call on a live
// connection is retried with the same pauses without reconnecting.
func (c *Client) Watch(ctx context.Context, onChange func(state.SettingsCache)) error {
	updated := make(chan struct{}, 1)
	c.mu.Lock()
	c.onUpdated = func() {
		select {
		case updated <- struct{}{}:
		default: // a read is already due; it gets the latest version
		}
	}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.onUpdated = nil
		c.mu.Unlock()
		_ = c.Close()
	}()

	reconnect := backoff{p: c.pauses}
	for {
		started := c.now()
		err := c.watchSession(ctx, updated, onChange)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if c.now().Sub(started) >= c.pauses.stable {
			reconnect.reset()
		}
		var wait time.Duration
		switch {
		case ReasonOf(err) == ReasonUnauthorized:
			wait = c.pauses.unauthorized
		case errors.Is(err, errSessionGone):
			// The service restarted: the first initialize goes right away, and the pauses
			// start only if it fails too.
		default:
			wait = reconnect.pause()
		}
		c.log.WarnContext(ctx, "the MCP connection is down", "reason", ReasonOf(err), "err", err, "retry_in", wait)
		if err := sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// errSessionGone: the server no longer knows the session (404), it was restarted.
var errSessionGone = errors.New("the MCP server lost the session (404)")

// watchSession connects once and serves the connection until it breaks; it returns why.
func (c *Client) watchSession(ctx context.Context, updated <-chan struct{}, onChange func(state.SettingsCache)) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}
	session, transport, err := c.current()
	if err != nil {
		return err
	}
	closed := make(chan error, 1)
	go func() { closed <- session.Wait() }()

	// Subscribe before the read, so that no change between them is lost; a subscription
	// that succeeds only on a retry is followed by a read again. A step that failed on a
	// live connection does not hold up the others and is retried.
	needToken, needSubscribe, needRead := true, true, true
	retry := backoff{p: c.pauses}
	for {
		var failed error
		for _, step := range []struct {
			need *bool
			run  func() error
		}{
			{&needToken, func() error { _, err := c.IngestToken(ctx); return err }},
			{&needSubscribe, func() error {
				err := c.subscribe(ctx, session, transport)
				needRead = needRead || err == nil
				return err
			}},
			{&needRead, func() error { return c.readAndNotify(ctx, onChange) }},
		} {
			if !*step.need {
				continue
			}
			err := step.run()
			if err != nil && ReasonOf(err) != ReasonFailed {
				return err // the connection is broken
			}
			*step.need = err != nil
			failed = cmp.Or(failed, err)
		}

		var (
			timer *time.Timer
			again <-chan time.Time
		)
		if failed != nil {
			c.log.WarnContext(ctx, "an MCP call failed, retrying", "err", failed)
			timer = time.NewTimer(retry.pause())
			again = timer.C
		} else {
			retry.reset()
		}
		var down error
		select {
		case <-ctx.Done():
			down = ctx.Err()
		case err := <-closed:
			down = c.closedBecause(ctx, transport, err)
		case <-updated:
			needRead = true
		case <-again:
		}
		if timer != nil {
			timer.Stop()
		}
		if down != nil {
			return down
		}
	}
}

func (c *Client) subscribe(ctx context.Context, session *mcp.ClientSession, transport *authTransport) error {
	if err := session.Subscribe(ctx, &mcp.SubscribeParams{URI: settingsURI}); err != nil {
		return c.locked(ctx, transport.explain(fmt.Errorf("subscribe to %s: %w", settingsURI, err)))
	}
	c.succeeded(ctx)
	return nil
}

func (c *Client) readAndNotify(ctx context.Context, onChange func(state.SettingsCache)) error {
	cache, changed, err := c.updateSettings(ctx)
	if err != nil {
		return err
	}
	if changed && onChange != nil {
		onChange(cache)
	}
	return nil
}

// closedBecause records why the SDK closed the session.
func (c *Client) closedBecause(ctx context.Context, transport *authTransport, err error) error {
	if err == nil {
		err = mcp.ErrConnectionClosed
	}
	err = transport.explain(fmt.Errorf("the MCP session closed: %w", err))
	if transport.sessionGone.Load() {
		err = fmt.Errorf("%w: %w", errSessionGone, err)
	}
	if ReasonOf(err) == ReasonFailed {
		err = fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	return c.locked(ctx, err)
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
