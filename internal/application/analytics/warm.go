package analytics

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// WarmEvery is how often the warm-up passes over the windows of warmDays: once per cacheTTL, so
// that a window it keeps answers at once long before staleMax (HT-538).
const WarmEvery = cacheTTL

// warmDays are the windows the warm-up keeps built: the one of maxDays, which every period that
// ends now is cut from (HT-536).
func warmDays() []int { return []int{maxDays} }

// Warm builds the window of each of warmDays at once and again on every tick, one after the
// other and so one build at a time, waiting for no request: after the start of the app or a long
// time without views the first request of a period finds its window built (HT-538). A window built less than
// cacheTTL ago, by a request or by the warm-up, is left as it is; one a request is building is
// waited for instead of built again. A failed build leaves the dataset there was and is logged,
// and the next tick tries again. It returns when ctx ends, cancelling the build it runs.
func (s *Service) Warm(ctx context.Context, logger *slog.Logger, ticks <-chan time.Time) {
	for {
		// A failed pass, as at the start while ClickHouse is not up yet, is tried again after
		// warmRetry, not at the next tick.
		retry := time.After(warmRetry)
		if s.warmPass(ctx, logger) {
			retry = nil
		}
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		case <-retry:
		}
	}
}

// warmPass warms each window of warmDays in turn and logs what each took.
// warmRetry is how soon a failed warm-up pass is tried again.
const warmRetry = 10 * time.Second

// warmPass warms every period of warmDays; false when a build of one failed.
func (s *Service) warmPass(ctx context.Context, logger *slog.Logger) bool {
	ok := true
	for _, d := range warmDays() {
		if ctx.Err() != nil {
			return ok
		}
		start := time.Now()
		built, err := s.warm(ctx, Filter{Days: &d})
		switch {
		case err != nil && ctx.Err() != nil:
			return ok
		case err != nil:
			ok = false
			logger.ErrorContext(ctx, "analytics warm-up: build the dataset", "days", d, "error", err)
		case built:
			logger.InfoContext(ctx, "analytics warm-up: dataset built", "days", d,
				"duration", time.Since(start).Round(time.Millisecond).String())
		}
	}
	return ok
}

// warm brings the window of f to a build less than cacheTTL old: it waits for the build running,
// leaves a fresh one, replaces a stale one in its place while it still answers, and builds anew
// one past staleMax or missing. built tells whether the warm-up built it itself; the error is the
// build's, or ctx's when it ended first.
func (s *Service) warm(ctx context.Context, f Filter) (built bool, err error) {
	key := f.periodKey()
	s.mu.Lock()
	e, ok := s.entries[key]
	var age time.Duration
	if ok && isDone(e.done) && e.err == nil {
		age = s.clock.Now().Sub(e.built)
	}
	switch {
	case ok && !isDone(e.done):
		s.mu.Unlock()
		return false, waitBuild(ctx, e)
	case ok && e.err == nil && e.refresh != nil:
		r := e.refresh
		s.mu.Unlock()
		return false, waitBuild(ctx, r)
	case ok && e.err == nil && age < cacheTTL:
		s.mu.Unlock()
		return false, nil
	case ok && e.err == nil && age < staleMax:
		old, r := e, &cacheEntry{done: make(chan struct{})}
		e.refresh = r
		s.mu.Unlock()
		s.replace(ctx, f, old, r)
		return true, r.err
	default:
		s.prune(key)
		e = &cacheEntry{done: make(chan struct{})}
		s.entries[key] = e
		s.mu.Unlock()
		s.build(ctx, f, e)
		return true, e.err
	}
}

// waitBuild waits for the build e to end and returns its error, or ctx's when ctx ends first.
func waitBuild(ctx context.Context, e *cacheEntry) error {
	select {
	case <-e.done:
		return e.err
	case <-ctx.Done():
		return fmt.Errorf("wait for the dataset: %w", ctx.Err())
	}
}
