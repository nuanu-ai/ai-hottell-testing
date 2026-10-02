// Package mcpclient is the hottell binary's client of the service's MCP server
// (docs/specs/hottell-contract/mcp.md): it connects with the URL and the key of the
// agent's config, fetches the collector token and the settings into the local state, and
// tells hottell status why it could not.
package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/mcpconfig"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

// The tools and the resource of the contract the client uses.
const (
	toolPing           = "ping"
	toolCollectorToken = "get_collector_token"
	settingsURI        = "hottell://settings"
)

// keepAlive is how often the client pings the session; an unanswered ping breaks it
// (mcp.md, «Ресурс настроек»).
const keepAlive = 30 * time.Second

// The errors hottell status tells apart; see Reason.
var (
	// ErrNoServer wraps the *mcpconfig.NotFoundError that names the reason of each config.
	ErrNoServer = errors.New("no MCP server hottell in the agents' configs")
	// ErrUnauthorized: the server answered 401, the key is unknown or revoked.
	ErrUnauthorized = errors.New("the MCP server refused the key (401)")
	// ErrUnreachable: the request did not reach the server or the connection broke.
	ErrUnreachable = errors.New("the MCP server is unreachable")
	// ErrNotConnected: a call before Connect succeeded.
	ErrNotConnected = errors.New("not connected to the MCP server")
)

// Config is what the client needs. Paths is required.
type Config struct {
	// Paths is the local state the results and the status are saved to.
	Paths state.Paths
	// Sources are the agents' configs to read the server from, in order; nil reads
	// mcpconfig.DefaultSources.
	Sources []mcpconfig.Source
	// Version is the binary's version the client reports in initialize.
	Version string
	// HTTPClient sends the requests; nil is http.DefaultClient's transport.
	HTTPClient *http.Client
	// Log receives the SDK's messages; nil discards them.
	Log *slog.Logger
}

// Client talks to the MCP server; create it with New and connect it with Connect. Its
// methods are safe for concurrent use.
type Client struct {
	cfg Config
	log *slog.Logger
	now func() time.Time

	// maxRetries is the SDK's reconnect limit of a stream; 0 is the SDK's default.
	maxRetries int
	// pauses are the waits of Watch.
	pauses pauses

	mu        sync.Mutex
	session   *mcp.ClientSession
	transport *authTransport
	status    Status
	// onUpdated is Watch's hook for notifications/resources/updated of the settings.
	onUpdated func()

	// settingsMu makes the compare and the write of the settings cache one step.
	settingsMu sync.Mutex
}

// New returns a client with cfg; it keeps the status that is already saved.
func New(cfg Config) *Client {
	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	st, err := ReadStatus(cfg.Paths.MCPFile())
	if err != nil {
		log.Warn("read the MCP status", "err", err)
	}
	return &Client{cfg: cfg, log: log, now: time.Now, pauses: defaultPauses(), status: st}
}

// Connect reads the URL and the key from the agents' configs again and opens a new MCP
// session with them, closing the previous one.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.session != nil {
		_ = c.session.Close()
		c.session, c.transport = nil, nil
	}
	session, transport, err := c.connect(ctx)
	if err != nil {
		return c.record(ctx, err)
	}
	c.session, c.transport = session, transport
	return c.record(ctx, nil)
}

func (c *Client) connect(ctx context.Context) (*mcp.ClientSession, *authTransport, error) {
	url, key, src, err := c.find()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrNoServer, err)
	}
	c.status.Source, c.status.URL = src, url

	base := http.DefaultTransport
	if c.cfg.HTTPClient != nil && c.cfg.HTTPClient.Transport != nil {
		base = c.cfg.HTTPClient.Transport
	}
	auth := &authTransport{base: base, authorization: key}
	httpClient := &http.Client{
		Transport: auth,
		// The contract has no redirects, and a redirect must not carry the key elsewhere.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	if c.cfg.HTTPClient != nil {
		httpClient.Timeout = c.cfg.HTTPClient.Timeout
	}
	transport := &mcp.StreamableClientTransport{Endpoint: url, HTTPClient: httpClient, MaxRetries: c.maxRetries}
	onUpdated := c.onUpdated
	client := mcp.NewClient(&mcp.Implementation{Name: "hottell", Version: c.cfg.Version}, &mcp.ClientOptions{
		Logger:    c.log,
		KeepAlive: keepAlive,
		ResourceUpdatedHandler: func(_ context.Context, req *mcp.ResourceUpdatedNotificationRequest) {
			if onUpdated != nil && req.Params != nil && req.Params.URI == settingsURI {
				onUpdated()
			}
		},
	})
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to %s: %w", url, err)
	}
	return session, auth, nil
}

func (c *Client) find() (url, key string, src mcpconfig.Source, err error) {
	if c.cfg.Sources != nil {
		return mcpconfig.FindIn(c.cfg.Sources...)
	}
	return mcpconfig.Find("")
}

// Close closes the session; the client can Connect again.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == nil {
		return nil
	}
	err := c.session.Close()
	c.session, c.transport = nil, nil
	return err
}

// PingResult is the answer of the ping tool.
type PingResult struct {
	// Version is the service's version.
	Version string `json:"version"`
	// Email is the email of the key's user.
	Email string `json:"email"`
}

// Ping checks the connection and the key with the ping tool.
func (c *Client) Ping(ctx context.Context) (PingResult, error) {
	var out PingResult
	if err := c.callTool(ctx, toolPing, nil, &out); err != nil {
		return PingResult{}, err
	}
	return out, nil
}

// IngestToken fetches the collector token and the OTLP address and saves them to
// state.Credentials.
func (c *Client) IngestToken(ctx context.Context) (state.Credentials, error) {
	var out struct {
		Token        string `json:"token"`
		OTLPEndpoint string `json:"otlp_endpoint"`
	}
	if err := c.callTool(ctx, toolCollectorToken, nil, &out); err != nil {
		return state.Credentials{}, err
	}
	if out.Token == "" || out.OTLPEndpoint == "" {
		return state.Credentials{}, c.locked(ctx, fmt.Errorf("%s returned no token or no otlp_endpoint", toolCollectorToken))
	}
	creds := state.Credentials{IngestURL: out.OTLPEndpoint, CollectorToken: out.Token}
	if err := c.cfg.Paths.WriteCredentials(creds); err != nil {
		return state.Credentials{}, fmt.Errorf("save the collector token: %w", err)
	}
	return creds, nil
}

// Settings reads the settings resource and saves the document to state.SettingsCache
// when its version is greater than the cached one, or nothing is cached yet. It returns
// the cache as it stands afterwards.
func (c *Client) Settings(ctx context.Context) (state.SettingsCache, error) {
	cache, _, err := c.updateSettings(ctx)
	return cache, err
}

// updateSettings is Settings that also says whether the cache was written.
func (c *Client) updateSettings(ctx context.Context) (state.SettingsCache, bool, error) {
	doc, err := c.readSettings(ctx)
	if err != nil {
		return state.SettingsCache{}, false, err
	}
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	cached, err := c.cfg.Paths.ReadSettings()
	if err != nil {
		return state.SettingsCache{}, false, fmt.Errorf("read the settings cache: %w", err)
	}
	fresh, err := state.NewSettingsCache(doc)
	if err != nil {
		return state.SettingsCache{}, false, c.locked(ctx, fmt.Errorf("the settings resource: %w", err))
	}
	if cached.Document != nil && fresh.Version <= cached.Version {
		return cached, false, nil
	}
	if err := c.cfg.Paths.WriteSettings(fresh); err != nil {
		return state.SettingsCache{}, false, fmt.Errorf("save the settings: %w", err)
	}
	return fresh, true, nil
}

func (c *Client) readSettings(ctx context.Context) ([]byte, error) {
	session, transport, err := c.current()
	if err != nil {
		return nil, err
	}
	res, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: settingsURI})
	if err != nil {
		return nil, c.locked(ctx, transport.explain(fmt.Errorf("read %s: %w", settingsURI, err)))
	}
	if len(res.Contents) != 1 || res.Contents[0].Text == "" {
		return nil, c.locked(ctx, fmt.Errorf("read %s: want one text content, got %d", settingsURI, len(res.Contents)))
	}
	c.succeeded(ctx)
	return []byte(res.Contents[0].Text), nil
}

// callTool calls a tool with arguments, {} when nil, decodes its structured result into
// out and records the outcome in the status.
func (c *Client) callTool(ctx context.Context, name string, arguments, out any) error {
	err := c.callToolUnrecorded(ctx, name, arguments, out)
	switch {
	case err == nil:
		c.succeeded(ctx)
		return nil
	case errors.Is(err, ErrNotConnected):
		// No call was made: nothing to tell of the server.
		return err
	}
	return c.locked(ctx, err)
}

// callToolUnrecorded is callTool that leaves the status alone, for a call whose failure
// says nothing hottell status should show.
func (c *Client) callToolUnrecorded(ctx context.Context, name string, arguments, out any) error {
	session, transport, err := c.current()
	if err != nil {
		return err
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return transport.explain(fmt.Errorf("call %s: %w", name, err))
	}
	if res.IsError {
		return fmt.Errorf("call %s: %s", name, toolText(res))
	}
	if res.StructuredContent == nil {
		return fmt.Errorf("call %s: no structured result", name)
	}
	data, err := json.Marshal(res.StructuredContent)
	if err == nil {
		err = json.Unmarshal(data, out)
	}
	if err != nil {
		return fmt.Errorf("call %s: decode the structured result: %w", name, err)
	}
	return nil
}

func toolText(res *mcp.CallToolResult) string {
	for _, content := range res.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			return text.Text
		}
	}
	return "tool error without text"
}

// Connected reports whether a session is open; a call may still find it broken.
func (c *Client) Connected() bool {
	_, _, err := c.current()
	return err == nil
}

func (c *Client) current() (*mcp.ClientSession, *authTransport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == nil {
		return nil, nil, ErrNotConnected
	}
	return c.session, c.transport, nil
}

// locked records the outcome of a call under the lock.
func (c *Client) locked(ctx context.Context, err error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.record(ctx, err)
}

// succeeded records a successful call.
func (c *Client) succeeded(ctx context.Context) {
	_ = c.locked(ctx, nil)
}

// record keeps the outcome in the status file and returns err. A call the caller
// cancelled says nothing about the server and leaves the status alone; a call past its
// deadline got no answer in time, so the server is unreachable. The caller holds mu.
func (c *Client) record(ctx context.Context, err error) error {
	if err != nil {
		switch ctxErr := ctx.Err(); {
		case errors.Is(ctxErr, context.Canceled):
			return err
		case ctxErr != nil && ReasonOf(err) == ReasonFailed:
			err = fmt.Errorf("%w: no answer in time: %w", ErrUnreachable, err)
		}
	}
	if err == nil {
		c.status.LastSuccess = c.now()
		c.status.Problem = nil
	} else {
		c.status.Problem = &Problem{At: c.now(), Reason: ReasonOf(err), Detail: err.Error()}
	}
	if werr := writeStatus(c.cfg.Paths.MCPFile(), c.status); werr != nil {
		c.log.WarnContext(ctx, "write the MCP status", "err", werr)
	}
	return err
}

// authTransport adds the Authorization header of the agent's config to every request
// and turns a 401 and a failed round trip into ErrUnauthorized and ErrUnreachable.
// It remembers a failure of a GET and a 401 of any request: the SDK reports a failure of
// its background stream, and a 401 of its keepalive ping, only as text or as a closed
// connection, while the failure of a POST reaches the caller wrapped.
type authTransport struct {
	base          http.RoundTripper
	authorization string

	// cause is the last 401, or failed round trip of a GET; nil after a GET answered
	// otherwise.
	cause atomic.Pointer[error]
	// sessionGone: the server answered 404 to a request of the session.
	sessionGone atomic.Bool
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", t.authorization)
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		if errors.Is(req.Context().Err(), context.Canceled) {
			return nil, err
		}
		return nil, t.fail(req, fmt.Errorf("%w: %w", ErrUnreachable, err))
	}
	if resp.StatusCode == http.StatusUnauthorized {
		_ = resp.Body.Close()
		t.cause.Store(&ErrUnauthorized)
		return nil, ErrUnauthorized
	}
	if resp.StatusCode == http.StatusNotFound && req.Header.Get("Mcp-Session-Id") != "" {
		t.sessionGone.Store(true)
	}
	if req.Method == http.MethodGet {
		t.cause.Store(nil)
	}
	return resp, nil
}

func (t *authTransport) fail(req *http.Request, err error) error {
	if req.Method == http.MethodGet {
		t.cause.Store(&err)
	}
	return err
}

// explain adds the background stream's failure to an error of a session call, so that
// ReasonOf tells a revoked key and a lost network apart after the SDK broke the
// connection over it.
func (t *authTransport) explain(err error) error {
	if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrUnreachable) {
		return err
	}
	if cause := t.cause.Load(); cause != nil {
		return fmt.Errorf("%w: %w", *cause, err)
	}
	return err
}
