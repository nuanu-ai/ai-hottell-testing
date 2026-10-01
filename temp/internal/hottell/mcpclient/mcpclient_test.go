package mcpclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/mcpclient"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/mcpconfig"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

const (
	goodKey       = "ht_mcp_good_fixture"
	newKey        = "ht_mcp_new_fixture"
	newKeyToken   = "ht_col_fixture_4567"
	testToken     = "ht_col_fixture_0123"
	testEndpoint  = "http://localhost:18080"
	testVersion   = "0.1.0"
	testEmail     = "dev@example.test"
	settingsURI   = "hottell://settings"
	settingsV12   = `{"version":12,"backfill_history":true}`
	settingsV0    = `{"version":0}`
	settingsV13   = `{"version":13}`
	serverVersion = "test"
)

// testServer is an in-process MCP server with the tools and the resource the binary uses;
// it accepts only the key in key (goodKey at first), drop makes it cut every connection
// and revoke answers 401 to every request.
type testServer struct {
	*httptest.Server

	mcp        *mcp.Server
	settings   atomic.Value // string
	key        atomic.Value // string
	drop       atomic.Bool
	revoke     atomic.Bool
	subscribes atomic.Int32
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()

	ts := &testServer{}
	ts.settings.Store(settingsV12)
	ts.key.Store(goodKey)

	server := mcp.NewServer(&mcp.Implementation{Name: "hottell", Version: serverVersion}, &mcp.ServerOptions{
		SubscribeHandler: func(context.Context, *mcp.SubscribeRequest) error {
			ts.subscribes.Add(1)
			return nil
		},
		UnsubscribeHandler: func(context.Context, *mcp.UnsubscribeRequest) error { return nil },
	})
	ts.mcp = server
	type empty struct{}
	type pingOut struct {
		Version string `json:"version"`
		Email   string `json:"email"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "ping"}, func(context.Context, *mcp.CallToolRequest, empty) (*mcp.CallToolResult, pingOut, error) {
		return nil, pingOut{Version: testVersion, Email: testEmail}, nil
	})
	type tokenOut struct {
		Token        string `json:"token"`
		OTLPEndpoint string `json:"otlp_endpoint"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "get_collector_token"}, func(_ context.Context, req *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, tokenOut, error) {
		token := testToken
		if req.Extra.Header.Get("Authorization") == "Bearer "+newKey {
			token = newKeyToken
		}
		return nil, tokenOut{Token: token, OTLPEndpoint: testEndpoint}, nil
	})
	server.AddResource(&mcp.Resource{URI: settingsURI, Name: "settings", MIMEType: "application/json"},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			text, _ := ts.settings.Load().(string)
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
				{URI: settingsURI, MIMEType: "application/json", Text: text},
			}}, nil
		})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ts.drop.Load() {
			hijackAndClose(t, w)
			return
		}
		key, _ := ts.key.Load().(string)
		if ts.revoke.Load() || r.Header.Get("Authorization") != "Bearer "+key {
			w.Header().Set("WWW-Authenticate", `Bearer realm="hottell", error="invalid_token"`)
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// hijackAndClose cuts the connection without an HTTP answer.
func hijackAndClose(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		t.Errorf("hijack: %v", err)
		return
	}
	_ = conn.Close()
}

// claudeConfig writes a Claude Code config with the hottell server at url and key.
func claudeConfig(t *testing.T, url, key string) []mcpconfig.Source {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".claude.json")
	cfg := map[string]any{"mcpServers": map[string]any{"hottell": map[string]any{
		"type": "http", "url": url, "headers": map[string]string{"Authorization": "Bearer " + key},
	}}}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return []mcpconfig.Source{{Agent: mcpconfig.Claude, Path: path}}
}

func newClient(t *testing.T, sources []mcpconfig.Source) (*mcpclient.Client, state.Paths) {
	t.Helper()
	paths := state.PathsIn(t.TempDir())
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	c := mcpclient.New(mcpclient.Config{Paths: paths, Sources: sources, Version: "dev"})
	t.Cleanup(func() { _ = c.Close() })
	return c, paths
}

func readStatus(t *testing.T, paths state.Paths) mcpclient.Status {
	t.Helper()
	st, err := mcpclient.ReadStatus(paths.MCPFile())
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	return st
}

func TestCallsSucceed(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	c, paths := newClient(t, claudeConfig(t, ts.URL, goodKey))
	ctx := t.Context()

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	info, err := c.Ping(ctx)
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if info.Version != testVersion || info.Email != testEmail {
		t.Errorf("Ping = %+v", info)
	}

	creds, err := c.IngestToken(ctx)
	if err != nil {
		t.Fatalf("IngestToken: %v", err)
	}
	want := state.Credentials{IngestURL: testEndpoint, CollectorToken: testToken}
	if creds != want {
		t.Errorf("IngestToken = %+v, want %+v", creds, want)
	}
	if saved, err := paths.ReadCredentials(); err != nil || saved != want {
		t.Errorf("saved credentials = %+v, %v; want %+v", saved, err, want)
	}

	cache, err := c.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if cache.Version != 12 || string(cache.Document) != settingsV12 {
		t.Errorf("Settings = %d %s", cache.Version, cache.Document)
	}
	if saved, err := paths.ReadSettings(); err != nil || saved.Version != 12 || string(saved.Document) != settingsV12 {
		t.Errorf("saved settings = %d %s, %v", saved.Version, saved.Document, err)
	}

	st := readStatus(t, paths)
	if st.Problem != nil || st.LastSuccess.IsZero() {
		t.Errorf("status = %+v, want a success and no problem", st)
	}
	if st.Source.Agent != mcpconfig.Claude {
		t.Errorf("status source = %+v", st.Source)
	}
}

func TestSettingsKeepsNewerCache(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	c, paths := newClient(t, claudeConfig(t, ts.URL, goodKey))
	ctx := t.Context()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if _, err := c.Settings(ctx); err != nil {
		t.Fatalf("Settings: %v", err)
	}

	// An older version than the cached one is not written over it.
	ts.settings.Store(settingsV0)
	cache, err := c.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if cache.Version != 12 {
		t.Errorf("Settings returned version %d, want the cached 12", cache.Version)
	}
	if saved, _ := paths.ReadSettings(); saved.Version != 12 {
		t.Errorf("cache version = %d, want 12", saved.Version)
	}

	// A greater version replaces it.
	ts.settings.Store(settingsV13)
	if cache, err = c.Settings(ctx); err != nil || cache.Version != 13 {
		t.Errorf("Settings = %d, %v; want version 13", cache.Version, err)
	}
	if saved, _ := paths.ReadSettings(); string(saved.Document) != settingsV13 {
		t.Errorf("cache = %s, want the version 13 document", saved.Document)
	}
}

func TestSettingsFirstDocumentAtVersionZero(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	ts.settings.Store(settingsV0)
	c, paths := newClient(t, claudeConfig(t, ts.URL, goodKey))
	if err := c.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if _, err := c.Settings(t.Context()); err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if saved, _ := paths.ReadSettings(); string(saved.Document) != settingsV0 {
		t.Errorf("cache = %s, want the version 0 document saved", saved.Document)
	}
}

func TestUnauthorized(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	c, paths := newClient(t, claudeConfig(t, ts.URL, "ht_mcp_revoked_fixture"))

	err := c.Connect(t.Context())
	if !errors.Is(err, mcpclient.ErrUnauthorized) {
		t.Fatalf("Connect = %v, want ErrUnauthorized", err)
	}
	if got := mcpclient.ReasonOf(err); got != mcpclient.ReasonUnauthorized {
		t.Errorf("ReasonOf = %q", got)
	}
	st := readStatus(t, paths)
	if st.Problem == nil || st.Problem.Reason != mcpclient.ReasonUnauthorized {
		t.Fatalf("status = %+v, want the unauthorized problem", st)
	}
	if !strings.Contains(st.Problem.Message(), "401") {
		t.Errorf("message %q does not name 401", st.Problem.Message())
	}
	if strings.Contains(string(mustRead(t, paths.MCPFile())), "ht_mcp_revoked_fixture") {
		t.Error("the status file holds the MCP key")
	}
}

func TestNoServerInConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, paths := newClient(t, []mcpconfig.Source{
		{Agent: mcpconfig.Claude, Path: path},
		{Agent: mcpconfig.Codex, Path: filepath.Join(dir, "config.toml")},
	})

	err := c.Connect(t.Context())
	if !errors.Is(err, mcpclient.ErrNoServer) {
		t.Fatalf("Connect = %v, want ErrNoServer", err)
	}
	var notFound *mcpconfig.NotFoundError
	if !errors.As(err, &notFound) || len(notFound.Configs) != 2 {
		t.Errorf("Connect = %v, want the reason of both configs", err)
	}
	st := readStatus(t, paths)
	if st.Problem == nil || st.Problem.Reason != mcpclient.ReasonNoServer {
		t.Fatalf("status = %+v, want the no-server problem", st)
	}
}

func TestServerDown(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	url := ts.URL
	ts.Close()
	c, paths := newClient(t, claudeConfig(t, url, goodKey))

	err := c.Connect(t.Context())
	if !errors.Is(err, mcpclient.ErrUnreachable) {
		t.Fatalf("Connect = %v, want ErrUnreachable", err)
	}
	if st := readStatus(t, paths); st.Problem == nil || st.Problem.Reason != mcpclient.ReasonUnreachable {
		t.Fatalf("status = %+v, want the unreachable problem", st)
	}
}

func TestConnectionDropped(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	c, paths := newClient(t, claudeConfig(t, ts.URL, goodKey))
	ctx := t.Context()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ts.drop.Store(true)
	_, err := c.Ping(ctx)
	if !errors.Is(err, mcpclient.ErrUnreachable) {
		t.Fatalf("Ping = %v, want ErrUnreachable", err)
	}
	if st := readStatus(t, paths); st.Problem == nil || st.Problem.Reason != mcpclient.ReasonUnreachable {
		t.Fatalf("status = %+v, want the unreachable problem", st)
	}
}

func TestReconnectRereadsConfig(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	sources := claudeConfig(t, ts.URL, "ht_mcp_revoked_fixture")
	c, paths := newClient(t, sources)
	if err := c.Connect(t.Context()); !errors.Is(err, mcpclient.ErrUnauthorized) {
		t.Fatalf("Connect = %v, want ErrUnauthorized", err)
	}

	// The user issues a new key and the agent's config gets it: the next Connect uses it.
	fresh := claudeConfig(t, ts.URL, goodKey)
	data := mustRead(t, fresh[0].Path)
	if err := os.WriteFile(sources[0].Path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.Connect(t.Context()); err != nil {
		t.Fatalf("Connect after the new key: %v", err)
	}
	if _, err := c.Ping(t.Context()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if st := readStatus(t, paths); st.Problem != nil {
		t.Errorf("status = %+v, want the problem cleared", st)
	}
}

// The SDK reports a failure of its background stream only as a closed connection; the
// calls after it still tell a revoked key and a lost network apart.
func TestBrokenMidSession(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		breakServer func(ts *testServer)
		want        mcpclient.Reason
	}{
		"key revoked":  {func(ts *testServer) { ts.revoke.Store(true) }, mcpclient.ReasonUnauthorized},
		"network lost": {func(ts *testServer) { ts.drop.Store(true) }, mcpclient.ReasonUnreachable},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ts := newTestServer(t)
			c, paths := newClient(t, claudeConfig(t, ts.URL, goodKey))
			c.SetMaxRetries(1)
			ctx := t.Context()
			if err := c.Connect(ctx); err != nil {
				t.Fatalf("Connect: %v", err)
			}

			tt.breakServer(ts)
			ts.CloseClientConnections() // ends the background stream; its reconnect fails

			// Until the SDK closes the connection a Ping may also meet a pooled connection
			// the server closed; only the reason of the closed connection is the point.
			deadline := time.Now().Add(10 * time.Second)
			for {
				_, err := c.Ping(ctx)
				if err == nil {
					t.Fatal("Ping succeeded against the broken server")
				}
				if errors.Is(err, mcp.ErrConnectionClosed) {
					if got := mcpclient.ReasonOf(err); got != tt.want {
						t.Fatalf("Ping = %v: reason %q, want %q", err, got, tt.want)
					}
					break
				}
				if got := mcpclient.ReasonOf(err); got == mcpclient.ReasonFailed {
					t.Fatalf("Ping = %v: reason %q before the connection closed", err, got)
				}
				if time.Now().After(deadline) {
					t.Fatalf("the SDK never closed the connection; last error %v", err)
				}
				time.Sleep(50 * time.Millisecond)
			}
			if st := readStatus(t, paths); st.Problem == nil || st.Problem.Reason != tt.want {
				t.Fatalf("status = %+v, want reason %q", st, tt.want)
			}
		})
	}
}

func TestCancelledCallKeepsStatus(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	c, paths := newClient(t, claudeConfig(t, ts.URL, goodKey))
	if err := c.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	before := readStatus(t, paths)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.Ping(ctx); err == nil {
		t.Fatal("Ping with a cancelled context succeeded")
	}
	if err := c.Connect(ctx); err == nil {
		t.Fatal("Connect with a cancelled context succeeded")
	}
	if after := readStatus(t, paths); after.Problem != nil || !after.LastSuccess.Equal(before.LastSuccess) {
		t.Errorf("status = %+v, want it unchanged from %+v", after, before)
	}
}

func TestDeadlineIsUnreachable(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	c, paths := newClient(t, claudeConfig(t, ts.URL, goodKey))
	if err := c.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	if _, err := c.Ping(ctx); err == nil {
		t.Fatal("Ping past its deadline succeeded")
	}
	if st := readStatus(t, paths); st.Problem == nil || st.Problem.Reason != mcpclient.ReasonUnreachable {
		t.Fatalf("status = %+v, want the unreachable problem", st)
	}
}

func TestRedirectDoesNotCarryKey(t *testing.T) {
	t.Parallel()

	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked.Store(true)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(other.Close)
	redirecting := httptest.NewServer(http.RedirectHandler(other.URL+"/mcp", http.StatusTemporaryRedirect))
	t.Cleanup(redirecting.Close)

	c, _ := newClient(t, claudeConfig(t, redirecting.URL, goodKey))
	if err := c.Connect(t.Context()); err == nil {
		t.Fatal("Connect through a redirect succeeded")
	}
	if leaked.Load() {
		t.Error("the redirect target received the Authorization header")
	}
}

func TestCallBeforeConnect(t *testing.T) {
	t.Parallel()

	c, _ := newClient(t, nil)
	if _, err := c.Ping(t.Context()); !errors.Is(err, mcpclient.ErrNotConnected) {
		t.Errorf("Ping = %v, want ErrNotConnected", err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
