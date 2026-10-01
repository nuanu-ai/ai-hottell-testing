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

// testKeepAlive replaces the 30 seconds of New in the tests of the keep-alive.
const testKeepAlive = 50 * time.Millisecond

// bearerTransport adds the Authorization header of key to every request.
type bearerTransport struct {
	key string
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.key)
	return http.DefaultTransport.RoundTrip(r)
}

// failedPings counts the records of failed keep-alive pings a logger receives.
type failedPings struct {
	count atomic.Int64
}

func (*failedPings) Enabled(context.Context, slog.Level) bool { return true }

func (f *failedPings) Handle(_ context.Context, r slog.Record) error {
	if r.Message == keepAliveFailed {
		f.count.Add(1)
	}
	return nil
}

func (f *failedPings) WithAttrs([]slog.Attr) slog.Handler { return f }

func (f *failedPings) WithGroup(string) slog.Handler { return f }

// keepAliveServer starts the handler with testKeepAlive for the user of key; its settings
// never change.
func keepAliveServer(t *testing.T, key string, logger *slog.Logger) *httptest.Server {
	t.Helper()
	userID := uuid.New()
	ctrl := gomock.NewController(t)
	keys := mock.NewMockKeys(ctrl)
	keys.EXPECT().Resolve(gomock.Any(), domain.AccessKeyKindMCP, key).Return(userID, nil).AnyTimes()
	users := mock.NewMockUsers(ctrl)
	users.EXPECT().Me(gomock.Any(), userID).Return(domain.User{ID: userID}, nil).AnyTimes()
	settings := mock.NewMockSettings(ctrl)
	settings.EXPECT().Subscribe(gomock.Any(), userID).Return(make(chan int64)).AnyTimes()
	srv := httptest.NewServer(newHandler(keys, users, settings, "http://localhost:8080", "0.1.0-test", logger,
		testKeepAlive))
	t.Cleanup(srv.Close)
	return srv
}

// connect opens an MCP session to srv with key that sends every ping it receives to pings;
// with noStream the client never opens the standalone GET stream, as Claude Code does not.
func connect(t *testing.T, srv *httptest.Server, key string, noStream bool, pings chan<- struct{}) *sdkmcp.ClientSession {
	t.Helper()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, nil)
	client.AddReceivingMiddleware(func(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
		return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
			if method == "ping" {
				select {
				case pings <- struct{}{}:
				default:
				}
			}
			return next(ctx, method, req)
		}
	})
	session, err := client.Connect(t.Context(), &sdkmcp.StreamableClientTransport{
		Endpoint:             srv.URL,
		HTTPClient:           &http.Client{Transport: bearerTransport{key: key}},
		DisableStandaloneSSE: noStream,
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// TestSessionWithoutStreamSurvivesKeepAlive holds a session without the standalone GET stream
// for several keep-alive intervals: the server does not ping it, so it does not close it, and
// the next tools/call is answered in the same session.
func TestSessionWithoutStreamSurvivesKeepAlive(t *testing.T) {
	t.Parallel()
	const key = "ht_mcp_nostream"
	srv := keepAliveServer(t, key, slog.New(slog.DiscardHandler))
	session := connect(t, srv, key, true, make(chan struct{}, 16))

	time.Sleep(6 * testKeepAlive)

	result, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "ping"})
	if err != nil {
		t.Fatalf("tools/call after the keep-alive intervals: %v", err)
	}
	if result.IsError {
		t.Fatalf("tools/call after the keep-alive intervals: isError, %v", result.Content)
	}
}

// TestServerPingsSubscribedSession checks that the server sends the MCP method ping, over the
// standalone GET stream, to a session subscribed to the settings while the client sends
// nothing; a second subscribe does not add a second ping loop, and unsubscribe stops it.
func TestServerPingsSubscribedSession(t *testing.T) {
	t.Parallel()
	const key = "ht_mcp_keepalive"
	srv := keepAliveServer(t, key, slog.New(slog.DiscardHandler))
	pings := make(chan struct{}, 64)
	session := connect(t, srv, key, false, pings)

	select {
	case <-pings:
		t.Fatal("the server pinged a session not subscribed to the settings")
	case <-time.After(4 * testKeepAlive):
	}

	for range 2 {
		if err := session.Subscribe(t.Context(), &sdkmcp.SubscribeParams{URI: settingsURI}); err != nil {
			t.Fatalf("subscribe: %v", err)
		}
	}
	for range 3 {
		select {
		case <-pings:
		case <-time.After(5 * time.Second):
			t.Fatal("the server sent no ping to the subscribed session")
		}
	}
	drain(pings)
	time.Sleep(10 * testKeepAlive)
	// One loop pings about 10 times in 10 intervals, two loops about 20.
	if got := drain(pings); got > 14 {
		t.Fatalf("pings in 10 intervals after subscribing twice: got %d, want at most 14", got)
	}

	if err := session.Unsubscribe(t.Context(), &sdkmcp.UnsubscribeParams{URI: settingsURI}); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	time.Sleep(2 * testKeepAlive)
	drain(pings)
	select {
	case <-pings:
		t.Fatal("the server pinged the session after unsubscribe")
	case <-time.After(4 * testKeepAlive):
	}
}

// TestPingsStopWhenSessionCloses closes a subscribed session: the ping loop of the session
// ends with it and logs no failed pings afterwards.
func TestPingsStopWhenSessionCloses(t *testing.T) {
	t.Parallel()
	const key = "ht_mcp_closed"
	failed := &failedPings{}
	srv := keepAliveServer(t, key, slog.New(failed))
	pings := make(chan struct{}, 16)
	session := connect(t, srv, key, false, pings)
	if err := session.Subscribe(t.Context(), &sdkmcp.SubscribeParams{URI: settingsURI}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	select {
	case <-pings:
	case <-time.After(5 * time.Second):
		t.Fatal("the server sent no ping to the subscribed session")
	}

	if err := session.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	time.Sleep(2 * testKeepAlive)
	before := failed.count.Load()
	time.Sleep(6 * testKeepAlive)
	if after := failed.count.Load(); after != before {
		t.Fatalf("failed pings after the session closed: %d more", after-before)
	}
}

// drain empties pings and returns how many it held.
func drain(pings <-chan struct{}) int {
	n := 0
	for {
		select {
		case <-pings:
			n++
		default:
			return n
		}
	}
}
