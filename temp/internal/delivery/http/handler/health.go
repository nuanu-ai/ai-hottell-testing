// Package handler holds the HTTP handlers of the service.
package handler

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// readyPingTimeout bounds the database ping of a readiness probe,
// so an unresponsive database answers 503 instead of hanging the probe.
const readyPingTimeout = 2 * time.Second

// Pinger checks that a dependency the service needs is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Health answers liveness and readiness probes.
// Readiness is an atomic flag flipped once startup completes,
// combined with a ping of the database on every probe.
type Health struct {
	ready atomic.Bool
	db    Pinger
}

// NewHealth returns a Health that is alive but not yet ready.
func NewHealth(db Pinger) *Health {
	return &Health{db: db}
}

// MarkReady reports that startup has completed.
func (h *Health) MarkReady() {
	h.ready.Store(true)
}

// Live answers whether the process is alive.
func (*Health) Live(w http.ResponseWriter, _ *http.Request) {
	writeText(w, http.StatusOK, "ok")
}

// Ready answers whether the service has completed startup and reaches the database.
func (h *Health) Ready(w http.ResponseWriter, r *http.Request) {
	if !h.ready.Load() || h.ping(r.Context()) != nil {
		writeText(w, http.StatusServiceUnavailable, "not ready")
		return
	}
	writeText(w, http.StatusOK, "ok")
}

func (h *Health) ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, readyPingTimeout)
	defer cancel()
	return h.db.Ping(ctx)
}

func writeText(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
