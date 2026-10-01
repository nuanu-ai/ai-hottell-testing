package mcp_test

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/mcp"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/mcp/mock"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// Synthetic MCP keys.
const (
	keyAlice   = "ht_mcp_alice"
	keyBob     = "ht_mcp_bob"
	keyRevoked = "ht_mcp_revoked"
	keyBroken  = "ht_mcp_store_fails"
	version    = "0.1.0-test"
	origin     = "http://localhost:8080"
)

// Synthetic users.
const (
	aliceID = "00000000-0000-4000-8000-00000000a11c"
	bobID   = "00000000-0000-4000-8000-000000000b0b"
)

func alice() domain.User {
	return domain.User{ID: uuid.MustParse(aliceID), Email: "alice@example.test"}
}

func bob() domain.User {
	return domain.User{ID: uuid.MustParse(bobID), Email: "bob@example.test"}
}

// newServer serves the MCP handler on keys and users of alice and bob and settings kept in
// memory; keyRevoked is revoked, keyBroken fails in the key store, any other key is unknown.
func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv, _ := newServerWithSettings(t)
	return srv
}

// newServerWithSettings is newServer that also returns the settings the server reads, for
// the tests to change them.
func newServerWithSettings(t *testing.T) (*httptest.Server, *fakeSettings) {
	t.Helper()
	ctrl := gomock.NewController(t)
	probe := newFakeSettings()
	srv := httptest.NewServer(mcp.New(knownKeys(t, ctrl), knownUsers(ctrl), probe, origin, version, slog.New(slog.DiscardHandler)))
	t.Cleanup(srv.Close)
	return srv, probe
}

// knownKeys resolves the keys of alice and bob; keyRevoked is revoked, keyBroken fails in
// the store, any other key is unknown.
func knownKeys(t *testing.T, ctrl *gomock.Controller) *mock.MockKeys {
	t.Helper()
	keys := mock.NewMockKeys(ctrl)
	keys.EXPECT().Resolve(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, kind domain.AccessKeyKind, key string) (uuid.UUID, error) {
			if kind != domain.AccessKeyKindMCP {
				t.Errorf("resolved kind %q, want %q", kind, domain.AccessKeyKindMCP)
			}
			switch key {
			case keyAlice:
				return alice().ID, nil
			case keyBob:
				return bob().ID, nil
			case keyRevoked:
				return uuid.Nil, domain.ErrAccessKeyRevoked
			case keyBroken:
				return uuid.Nil, errors.New("store down")
			default:
				return uuid.Nil, domain.ErrAccessKeyNotFound
			}
		}).AnyTimes()
	return keys
}

// knownUsers finds alice and bob.
func knownUsers(ctrl *gomock.Controller) *mock.MockUsers {
	users := mock.NewMockUsers(ctrl)
	users.EXPECT().Me(gomock.Any(), gomock.Any()).DoAndReturn(func(_ any, id uuid.UUID) (domain.User, error) {
		switch id {
		case alice().ID:
			return alice(), nil
		case bob().ID:
			return bob(), nil
		default:
			return domain.User{}, domain.ErrUserNotFound
		}
	}).AnyTimes()
	return users
}

// withAuthorization adds an Authorization header to every request.
type withAuthorization struct {
	value string
}

func (a withAuthorization) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", a.value)
	return http.DefaultTransport.RoundTrip(r)
}

// newAuthorizedClient returns an HTTP client that sends authorization with every request.
func newAuthorizedClient(authorization string) *http.Client {
	return &http.Client{Transport: withAuthorization{value: authorization}}
}

// connect opens an MCP session to srv with the go-sdk client, sending authorization when
// it is not empty.
func connect(t *testing.T, srv *httptest.Server, authorization string) (*sdkmcp.ClientSession, error) {
	t.Helper()
	httpClient := srv.Client()
	if authorization != "" {
		httpClient = newAuthorizedClient(authorization)
	}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(t.Context(),
		&sdkmcp.StreamableClientTransport{Endpoint: srv.URL, HTTPClient: httpClient, DisableStandaloneSSE: true}, nil)
	if err == nil {
		t.Cleanup(func() { _ = session.Close() })
	}
	return session, err
}

func TestInitializeAndPingWithActiveKey(t *testing.T) {
	t.Parallel()
	srv := newServer(t)

	for _, user := range []struct {
		key   string
		email string
	}{{"Bearer " + keyAlice, "alice@example.test"}, {"bearer " + keyBob, "bob@example.test"}} {
		session, err := connect(t, srv, user.key)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		info := session.InitializeResult().ServerInfo
		if info.Name != "hottell" || info.Version != version {
			t.Fatalf("server info: got %s %s, want hottell %s", info.Name, info.Version, version)
		}

		res, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "ping", Arguments: map[string]any{}})
		if err != nil {
			t.Fatalf("call ping: %v", err)
		}
		if res.IsError {
			t.Fatalf("ping failed: %+v", res.Content)
		}
		want := `{"email":"` + user.email + `","version":"` + version + `"}`
		if got := mustJSON(t, res.StructuredContent); got != want {
			t.Fatalf("structured content: got %s, want %s", got, want)
		}
		text, ok := res.Content[0].(*sdkmcp.TextContent)
		if !ok || len(res.Content) != 1 {
			t.Fatalf("content: got %+v, want one text", res.Content)
		}
		if got := mustJSON(t, json.RawMessage(text.Text)); got != want {
			t.Fatalf("text content: got %s, want %s", got, want)
		}
	}
}

func TestPingSchemas(t *testing.T) {
	t.Parallel()
	session, err := connect(t, newServer(t), "Bearer "+keyAlice)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 4 || tools.Tools[0].Name != "get_collector_token" ||
		tools.Tools[1].Name != "get_install_instructions" || tools.Tools[2].Name != "get_settings" ||
		tools.Tools[3].Name != "ping" {
		t.Fatalf("tools: got %+v, want get_collector_token, get_install_instructions, get_settings and ping", tools.Tools)
	}
	ping := tools.Tools[3]
	if ping.Annotations == nil || !ping.Annotations.ReadOnlyHint {
		t.Fatalf("annotations: got %+v, want readOnlyHint", ping.Annotations)
	}
	// The same schema as { "type": "object", "properties": {}, "additionalProperties": false }
	// of mcp.md: go-sdk leaves out the empty properties.
	if got, want := mustJSON(t, ping.InputSchema), `{"additionalProperties":false,"type":"object"}`; got != want {
		t.Fatalf("input schema: got %s, want %s", got, want)
	}
	var output struct {
		Type                 string         `json:"type"`
		Required             []string       `json:"required"`
		AdditionalProperties any            `json:"additionalProperties"`
		Properties           map[string]any `json:"properties"`
	}
	if err := json.Unmarshal([]byte(mustJSON(t, ping.OutputSchema)), &output); err != nil {
		t.Fatalf("decode output schema: %v", err)
	}
	if output.Type != "object" || strings.Join(output.Required, ",") != "version,email" ||
		output.AdditionalProperties != false || len(output.Properties) != 2 {
		t.Fatalf("output schema: got %s", mustJSON(t, ping.OutputSchema))
	}

	res, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{
		Name: "ping", Arguments: map[string]any{"extra": 1},
	})
	if err != nil {
		t.Fatalf("call ping with extra argument: %v", err)
	}
	if !res.IsError {
		t.Fatalf("ping with extra argument: got %+v, want isError", res)
	}
}

func TestUnauthorized(t *testing.T) {
	t.Parallel()
	srv := newServer(t)

	tests := []struct {
		name          string
		authorization string
		wantChallenge string
	}{
		{name: "no key", wantChallenge: `Bearer realm="hottell"`},
		{
			name: "revoked key", authorization: "Bearer " + keyRevoked,
			wantChallenge: `Bearer realm="hottell", error="invalid_token"`,
		},
		{
			name: "unknown key", authorization: "Bearer ht_mcp_unknown",
			wantChallenge: `Bearer realm="hottell", error="invalid_token"`,
		},
		{
			name: "not bearer", authorization: "Basic " + keyAlice,
			wantChallenge: `Bearer realm="hottell", error="invalid_token"`,
		},
		{
			name: "two spaces", authorization: "Bearer  " + keyAlice,
			wantChallenge: `Bearer realm="hottell", error="invalid_token"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := connect(t, srv, tt.authorization); err == nil {
				t.Fatal("connect: got a session, want a refusal")
			}

			resp := post(t, srv, tt.authorization, "", initializeBody)
			body := resp.body
			if resp.status != http.StatusUnauthorized {
				t.Fatalf("status: got %d %s, want 401", resp.status, body)
			}
			if got := resp.header.Get("WWW-Authenticate"); got != tt.wantChallenge {
				t.Fatalf("WWW-Authenticate: got %q, want %q", got, tt.wantChallenge)
			}
			want := `{"error":"unauthorized","message":"MCP key is missing, unknown or revoked; ` +
				`issue a new one on the Connect page"}`
			if got := strings.TrimSpace(string(body)); got != want || resp.header.Get("Content-Type") != "application/json" {
				t.Fatalf("body: got %s %q, want %s", resp.header.Get("Content-Type"), got, want)
			}
		})
	}
}

func TestKeyStoreFailureIsUnavailable(t *testing.T) {
	t.Parallel()
	resp := post(t, newServer(t), "Bearer "+keyBroken, "", initializeBody)
	if resp.status != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503", resp.status)
	}
}

func TestForeignOrUnknownSessionIsNotFound(t *testing.T) {
	t.Parallel()
	srv := newServer(t)
	session, err := connect(t, srv, "Bearer "+keyAlice)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	tests := []struct {
		name          string
		authorization string
		sessionID     string
		wantStatus    int
	}{
		{name: "own session", authorization: "Bearer " + keyAlice, sessionID: session.ID(), wantStatus: http.StatusOK},
		{name: "foreign session", authorization: "Bearer " + keyBob, sessionID: session.ID(), wantStatus: http.StatusNotFound},
		{name: "unknown session", authorization: "Bearer " + keyBob, sessionID: "unknown", wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp := post(t, srv, tt.authorization, tt.sessionID,
				`{"jsonrpc":"2.0","id":7,"method":"tools/list","params":{}}`)
			if resp.status != tt.wantStatus {
				t.Fatalf("status: got %d %s, want %d", resp.status, resp.body, tt.wantStatus)
			}
		})
	}
}

func TestPingFailsInternalWhenUserStoreFails(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	keys := mock.NewMockKeys(ctrl)
	keys.EXPECT().Resolve(gomock.Any(), domain.AccessKeyKindMCP, keyAlice).Return(alice().ID, nil).AnyTimes()
	users := mock.NewMockUsers(ctrl)
	users.EXPECT().Me(gomock.Any(), alice().ID).Return(domain.User{}, errors.New("store down"))
	srv := httptest.NewServer(mcp.New(keys, users, mock.NewMockSettings(ctrl), origin, version, slog.New(slog.DiscardHandler)))
	t.Cleanup(srv.Close)

	session, err := connect(t, srv, "Bearer "+keyAlice)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	res, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "ping", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("call ping: %v", err)
	}
	text, ok := res.Content[0].(*sdkmcp.TextContent)
	if !res.IsError || res.StructuredContent != nil || !ok || !strings.HasPrefix(text.Text, "internal: ") {
		t.Fatalf("result: got %+v, want isError with internal", res)
	}
}

const initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{` +
	`"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`

// response is an HTTP response read to the end.
type response struct {
	status int
	header http.Header
	body   []byte
}

// post sends body to srv as an MCP client would, with authorization and sessionID when
// they are not empty.
func post(t *testing.T, srv *httptest.Server, authorization, sessionID, body string) response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", "2025-06-18")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// An SSE answer stays open until the call is answered; the status is what the tests need.
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	return response{status: resp.StatusCode, header: resp.Header, body: respBody}
}

// mustJSON returns v encoded as JSON with the keys of its objects sorted.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	sorted, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return string(sorted)
}
