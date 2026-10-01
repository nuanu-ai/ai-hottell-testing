package mcp

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/mcp/mock"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// TestSubscriptionOutlivesWriteTimeout holds a subscribed session for three times the write
// timeout of the HTTP server, scaled down with the keep-alive from the 30 seconds of both in
// cmd/api/main.go and New: the standalone GET stream is never cut and reopened, and a
// notification still arrives at the end.
func TestSubscriptionOutlivesWriteTimeout(t *testing.T) {
	t.Parallel()
	const (
		key          = "ht_mcp_stream"
		writeTimeout = time.Second
		keepAlive    = 300 * time.Millisecond
	)
	userID := uuid.MustParse("00000000-0000-4000-8000-0000000000bb")
	ctrl := gomock.NewController(t)
	keys := mock.NewMockKeys(ctrl)
	keys.EXPECT().Resolve(gomock.Any(), domain.AccessKeyKindMCP, key).Return(userID, nil).AnyTimes()
	versions := make(chan int64, 1)
	subscribed := make(chan struct{})
	settings := mock.NewMockSettings(ctrl)
	settings.EXPECT().Subscribe(gomock.Any(), userID).DoAndReturn(func(context.Context, uuid.UUID) <-chan int64 {
		close(subscribed)
		return versions
	})
	handler := newHandler(keys, mock.NewMockUsers(ctrl), settings, "http://localhost:8080", "0.1.0-test",
		slog.New(slog.DiscardHandler), keepAlive)
	var streams atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			streams.Add(1)
		}
		handler.ServeHTTP(w, r)
	}))
	srv.Config.WriteTimeout = writeTimeout
	srv.Start()
	t.Cleanup(srv.Close)

	updates := make(chan string, 16)
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, &sdkmcp.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, req *sdkmcp.ResourceUpdatedNotificationRequest) {
			updates <- req.Params.URI
		},
	})
	session, err := client.Connect(t.Context(), &sdkmcp.StreamableClientTransport{
		Endpoint: srv.URL, HTTPClient: &http.Client{Transport: bearerTransport{key: key}},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := session.Subscribe(t.Context(), &sdkmcp.SubscribeParams{URI: settingsURI}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	<-subscribed

	time.Sleep(3 * writeTimeout)

	versions <- 1
	select {
	case uri := <-updates:
		if uri != settingsURI {
			t.Fatalf("updated URI: got %q, want %q", uri, settingsURI)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no notifications/resources/updated after the write timeout")
	}
	if got := streams.Load(); got != 1 {
		t.Fatalf("standalone GET streams opened: got %d, want 1", got)
	}
}
