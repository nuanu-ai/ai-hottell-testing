package mcp_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/mcp"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/mcp/mock"
)

// ingestTokens keeps one collector token per user the way the keys service does: IngestToken
// creates it on the first call and returns it after, reissue replaces it.
type ingestTokens struct {
	mu      sync.Mutex
	issued  int
	byUser  map[uuid.UUID]string
	created int
}

func newIngestTokens() *ingestTokens {
	return &ingestTokens{byUser: map[uuid.UUID]string{}}
}

func (s *ingestTokens) IngestToken(_ any, userID uuid.UUID) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token, ok := s.byUser[userID]; ok {
		return token, nil
	}
	s.created++
	return s.issueLocked(userID), nil
}

// reissue replaces the token of the user with userID, as ReissueIngestToken does.
func (s *ingestTokens) reissue(userID uuid.UUID) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issueLocked(userID)
}

func (s *ingestTokens) issueLocked(userID uuid.UUID) string {
	s.issued++
	token := fmt.Sprintf("ht_col_%04d", s.issued)
	s.byUser[userID] = token
	return token
}

// newCollectorServer serves the MCP handler with the keys of alice and bob and their
// collector tokens in tokens.
func newCollectorServer(t *testing.T, tokens *ingestTokens) *httptest.Server {
	t.Helper()
	ctrl := gomock.NewController(t)
	keys := knownKeys(t, ctrl)
	keys.EXPECT().IngestToken(gomock.Any(), gomock.Any()).DoAndReturn(tokens.IngestToken).AnyTimes()
	srv := httptest.NewServer(mcp.New(keys, knownUsers(ctrl), mock.NewMockSettings(ctrl), origin, version,
		slog.New(slog.DiscardHandler)))
	t.Cleanup(srv.Close)
	return srv
}

// collectorToken calls get_collector_token and returns its structured content, failing the
// test unless the text content carries the same object.
func collectorToken(t *testing.T, session *sdkmcp.ClientSession) (token, endpoint string) {
	t.Helper()
	res, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{
		Name: "get_collector_token", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("call get_collector_token: %v", err)
	}
	if res.IsError {
		t.Fatalf("get_collector_token failed: %+v", res.Content)
	}
	var out struct {
		Token        string `json:"token"`
		OTLPEndpoint string `json:"otlp_endpoint"`
	}
	structured := mustJSON(t, res.StructuredContent)
	if err := json.Unmarshal([]byte(structured), &out); err != nil {
		t.Fatalf("decode structured content %s: %v", structured, err)
	}
	text, ok := res.Content[0].(*sdkmcp.TextContent)
	if !ok || len(res.Content) != 1 {
		t.Fatalf("content: got %+v, want one text", res.Content)
	}
	if got := mustJSON(t, json.RawMessage(text.Text)); got != structured {
		t.Fatalf("text content: got %s, want %s", got, structured)
	}
	return out.Token, out.OTLPEndpoint
}

func TestCollectorTokenCreatedThenSameThenReissued(t *testing.T) {
	t.Parallel()
	tokens := newIngestTokens()
	srv := newCollectorServer(t, tokens)

	session, err := connect(t, srv, "Bearer "+keyAlice)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	first, endpoint := collectorToken(t, session)
	if first == "" || tokens.created != 1 {
		t.Fatalf("first call: got token %q after %d creations, want a created token", first, tokens.created)
	}
	if endpoint != origin {
		t.Fatalf("otlp_endpoint: got %q, want %q", endpoint, origin)
	}

	// Another machine of the same user, in a session of its own, gets the same token.
	again, err := connect(t, srv, "Bearer "+keyAlice)
	if err != nil {
		t.Fatalf("connect again: %v", err)
	}
	for _, s := range []*sdkmcp.ClientSession{session, again} {
		if got, _ := collectorToken(t, s); got != first {
			t.Fatalf("repeated call: got %q, want the same %q", got, first)
		}
	}
	if tokens.created != 1 {
		t.Fatalf("repeated calls created %d tokens, want 1", tokens.created)
	}

	reissued := tokens.reissue(alice().ID)
	if got, _ := collectorToken(t, session); got != reissued || got == first {
		t.Fatalf("after reissue: got %q, want the new %q", got, reissued)
	}
}

func TestCollectorTokenIsPerUser(t *testing.T) {
	t.Parallel()
	srv := newCollectorServer(t, newIngestTokens())

	aliceSession, err := connect(t, srv, "Bearer "+keyAlice)
	if err != nil {
		t.Fatalf("connect alice: %v", err)
	}
	bobSession, err := connect(t, srv, "Bearer "+keyBob)
	if err != nil {
		t.Fatalf("connect bob: %v", err)
	}
	aliceToken, _ := collectorToken(t, aliceSession)
	bobToken, _ := collectorToken(t, bobSession)
	if aliceToken == bobToken {
		t.Fatalf("alice and bob share the token %q", aliceToken)
	}
}

func TestCollectorTokenSchemas(t *testing.T) {
	t.Parallel()
	session, err := connect(t, newCollectorServer(t, newIngestTokens()), "Bearer "+keyAlice)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var tool *sdkmcp.Tool
	for _, candidate := range tools.Tools {
		if candidate.Name == "get_collector_token" {
			tool = candidate
		}
	}
	if tool == nil {
		t.Fatalf("tools: got %+v, want get_collector_token", tools.Tools)
	}
	if a := tool.Annotations; a == nil || a.ReadOnlyHint || !a.IdempotentHint {
		t.Fatalf("annotations: got %+v, want idempotentHint and not readOnlyHint", a)
	}
	if !strings.Contains(tool.Description, "hottell") {
		t.Fatalf("description: got %q, want it to name the hottell binary", tool.Description)
	}
	if got, want := mustJSON(t, tool.InputSchema), `{"additionalProperties":false,"type":"object"}`; got != want {
		t.Fatalf("input schema: got %s, want %s", got, want)
	}
	var output struct {
		Type                 string   `json:"type"`
		Required             []string `json:"required"`
		AdditionalProperties any      `json:"additionalProperties"`
		Properties           map[string]struct {
			Type    string `json:"type"`
			Pattern string `json:"pattern"`
			Format  string `json:"format"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(mustJSON(t, tool.OutputSchema)), &output); err != nil {
		t.Fatalf("decode output schema: %v", err)
	}
	if output.Type != "object" || strings.Join(output.Required, ",") != "token,otlp_endpoint" ||
		output.AdditionalProperties != false || len(output.Properties) != 2 ||
		output.Properties["token"].Pattern != "^[A-Za-z0-9_-]+$" || output.Properties["otlp_endpoint"].Format != "uri" {
		t.Fatalf("output schema: got %s", mustJSON(t, tool.OutputSchema))
	}

	res, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{
		Name: "get_collector_token", Arguments: map[string]any{"extra": 1},
	})
	if err != nil {
		t.Fatalf("call with extra argument: %v", err)
	}
	if !res.IsError {
		t.Fatalf("call with extra argument: got %+v, want isError", res)
	}
}

func TestCollectorTokenFailsInternalWhenKeyStoreFails(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	keys := knownKeys(t, ctrl)
	keys.EXPECT().IngestToken(gomock.Any(), alice().ID).Return("", errors.New("store down"))
	srv := httptest.NewServer(mcp.New(keys, knownUsers(ctrl), mock.NewMockSettings(ctrl), origin, version,
		slog.New(slog.DiscardHandler)))
	t.Cleanup(srv.Close)

	session, err := connect(t, srv, "Bearer "+keyAlice)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	res, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{
		Name: "get_collector_token", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("call get_collector_token: %v", err)
	}
	text, ok := res.Content[0].(*sdkmcp.TextContent)
	if !res.IsError || res.StructuredContent != nil || !ok || !strings.HasPrefix(text.Text, "internal: ") {
		t.Fatalf("result: got %+v, want isError with internal", res)
	}
}
