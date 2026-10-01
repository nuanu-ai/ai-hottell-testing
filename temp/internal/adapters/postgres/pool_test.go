package postgres_test

import (
	"net"
	"strings"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
)

func TestNewPool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		url     func(t *testing.T) string
		wantErr string
	}{
		{
			name:    "invalid url",
			url:     func(*testing.T) string { return "postgres://ht:ht@localhost:notaport/ht" },
			wantErr: "parse database url",
		},
		{
			name:    "unreachable database",
			url:     closedPortURL,
			wantErr: "ping database",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pool, err := postgres.NewPool(t.Context(), tt.url(t))
			if err == nil {
				pool.Close()
				t.Fatalf("NewPool() error = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("NewPool() error = %v, want error containing %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("NewPool() error leaks the password: %v", err)
			}
		})
	}
}

// closedPortURL returns a database URL pointing at a local port nobody listens on.
func closedPortURL(t *testing.T) string {
	t.Helper()

	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return "postgres://ht:secret@" + addr + "/ht?sslmode=disable&connect_timeout=1"
}
