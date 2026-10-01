package handler_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/handler"
)

func TestHealth(t *testing.T) {
	t.Parallel()

	errDown := errors.New("database down")

	tests := []struct {
		name     string
		ready    bool
		pingErr  error
		hang     bool
		probe    func(*handler.Health) http.HandlerFunc
		wantCode int
		wantBody string
	}{
		{name: "live before startup", probe: liveProbe, wantCode: http.StatusOK, wantBody: "ok"},
		{name: "live with database down", ready: true, pingErr: errDown, probe: liveProbe, wantCode: http.StatusOK, wantBody: "ok"},
		{name: "ready before startup", probe: readyProbe, wantCode: http.StatusServiceUnavailable, wantBody: "not ready"},
		{name: "ready after startup", ready: true, probe: readyProbe, wantCode: http.StatusOK, wantBody: "ok"},
		{name: "ready with database down", ready: true, pingErr: errDown, probe: readyProbe, wantCode: http.StatusServiceUnavailable, wantBody: "not ready"},
		{name: "ready with database hanging", ready: true, hang: true, probe: readyProbe, wantCode: http.StatusServiceUnavailable, wantBody: "not ready"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := handler.NewHealth(pingerFunc(func(ctx context.Context) error {
				if tt.hang {
					<-ctx.Done()
					return ctx.Err()
				}
				return tt.pingErr
			}))
			if tt.ready {
				h.MarkReady()
			}
			rec := httptest.NewRecorder()
			tt.probe(h)(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))

			if rec.Code != tt.wantCode || rec.Body.String() != tt.wantBody {
				t.Fatalf("got %d %q, want %d %q", rec.Code, rec.Body.String(), tt.wantCode, tt.wantBody)
			}
		})
	}
}

type pingerFunc func(context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error { return f(ctx) }

func liveProbe(h *handler.Health) http.HandlerFunc  { return h.Live }
func readyProbe(h *handler.Health) http.HandlerFunc { return h.Ready }
