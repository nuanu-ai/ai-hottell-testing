package ioc_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	keysmock "git.alva.dev/alva/harness-telemetry/internal/application/keys/mock"
	"git.alva.dev/alva/harness-telemetry/internal/application/session/mock"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// memorySettings keeps the settings documents of the users in memory, as the PostgreSQL
// repository keeps them in the database.
type memorySettings struct {
	mu        sync.Mutex
	documents map[uuid.UUID]json.RawMessage
	versions  map[uuid.UUID]int64
}

func (m *memorySettings) Get(_ context.Context, userID uuid.UUID) (json.RawMessage, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	document, ok := m.documents[userID]
	if !ok {
		return json.RawMessage(`{}`), 0, nil
	}
	return document, m.versions[userID], nil
}

func (m *memorySettings) Save(_ context.Context, userID uuid.UUID, document json.RawMessage, expectedVersion int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.versions[userID] != expectedVersion {
		return domain.ErrSettingsVersionConflict
	}
	m.documents[userID] = document
	m.versions[userID] = expectedVersion + 1
	return nil
}

// mcpKey is a well-formed synthetic MCP key.
const mcpKey = "QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQ"

// mcpSession speaks JSON-RPC to /mcp over HTTP in one MCP session, as a client of the
// streamable HTTP transport does.
type mcpSession struct {
	srv *httptest.Server
	id  string
}

// call posts message to /mcp in the session and returns the JSON-RPC response to it, read
// from a JSON body or from the SSE stream the server answers with.
func (s *mcpSession) call(t *testing.T, message string) json.RawMessage {
	t.Helper()
	resp := s.post(t, message)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: got status %d, want 200", message, resp.StatusCode)
	}
	if s.id == "" {
		s.id = resp.Header.Get("Mcp-Session-Id")
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		var body json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("%s: decode response: %v", message, err)
		}
		return body
	}
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok && strings.Contains(data, `"id"`) {
			return json.RawMessage(data)
		}
	}
	t.Fatalf("%s: no response in the SSE stream", message)
	return nil
}

// notify posts the notification message to /mcp in the session.
func (s *mcpSession) notify(t *testing.T, message string) {
	t.Helper()
	resp := s.post(t, message)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("%s: got status %d, want 202", message, resp.StatusCode)
	}
}

func (s *mcpSession) post(t *testing.T, message string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.srv.URL+"/mcp", strings.NewReader(message))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	s.setHeaders(req)
	resp, err := s.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s: %v", message, err)
	}
	return resp
}

func (s *mcpSession) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+mcpKey)
	req.Header.Set("Mcp-Protocol-Version", "2025-06-18")
	if s.id != "" {
		req.Header.Set("Mcp-Session-Id", s.id)
	}
}

// listen opens the session's GET stream, the one server notifications travel on, and
// returns the channel its JSON-RPC messages arrive on.
func (s *mcpSession) listen(t *testing.T) <-chan string {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.srv.URL+"/mcp", http.NoBody)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	s.setHeaders(req)
	resp, err := s.srv.Client().Do(req) //nolint:bodyclose // closed by the reading goroutine
	if err != nil {
		t.Fatalf("open GET stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("open GET stream: got status %d, want 200", resp.StatusCode)
	}
	messages := make(chan string, 16)
	go func() {
		defer func() { _ = resp.Body.Close() }()
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
				messages <- data
			}
		}
	}()
	return messages
}

// TestSettingsUpdateNotifiesMcpSubscriber saves the settings through the API of the
// assembled container and checks that the MCP session subscribed to hottell://settings of
// the same user is notified on its GET stream and reads the new version, and that it hears
// nothing once unsubscribed: the API and the MCP server share one settings use case and
// its Notifier.
func TestSettingsUpdateNotifiesMcpSubscriber(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	userID := uuid.New()
	usedAt := time.Now()
	sessions := mock.NewMockSessions(ctrl)
	sessions.EXPECT().GetByTokenHash(gomock.Any(), gomock.Any()).
		Return(domain.Session{ID: uuid.New(), UserID: userID, ExpiresAt: time.Now().Add(domain.SessionTTL)}, nil).
		AnyTimes()
	sessions.EXPECT().Extend(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	accessKeys := keysmock.NewMockAccessKeys(ctrl)
	accessKeys.EXPECT().FindByHash(gomock.Any(), gomock.Any()).
		Return(domain.AccessKey{ID: uuid.New(), UserID: userID, Kind: domain.AccessKeyKindMCP, LastUsedAt: &usedAt}, nil).
		AnyTimes()
	stores := stores(t, sessions)
	stores.AccessKeys = accessKeys
	stores.Settings = &memorySettings{documents: make(map[uuid.UUID]json.RawMessage), versions: make(map[uuid.UUID]int64)}
	container := newContainer(t, stores, slog.New(slog.DiscardHandler), fstest.MapFS{"index.html": {Data: []byte(indexHTML)}})
	srv := httptest.NewServer(container.Handler)
	t.Cleanup(srv.Close)

	session := &mcpSession{srv: srv}
	session.call(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",`+
		`"capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`)
	if session.id == "" {
		t.Fatal("initialize: no Mcp-Session-Id")
	}
	session.notify(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	stream := session.listen(t)

	session.call(t, `{"jsonrpc":"2.0","id":2,"method":"resources/subscribe","params":{"uri":"hottell://settings"}}`)
	if got := readVersion(t, session, 3); got != 0 {
		t.Fatalf("version before save: got %d, want 0", got)
	}

	saveSettings(t, srv, `{"settings":{"backfill_history":true},"expectedVersion":0}`)
	select {
	case got := <-stream:
		if !strings.Contains(got, `"method":"notifications/resources/updated"`) ||
			!strings.Contains(got, `"uri":"hottell://settings"`) {
			t.Fatalf("GET stream: got %s, want notifications/resources/updated of hottell://settings", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no notifications/resources/updated after the save")
	}
	if got := readVersion(t, session, 4); got != 1 {
		t.Fatalf("version after save: got %d, want 1", got)
	}

	session.call(t, `{"jsonrpc":"2.0","id":5,"method":"resources/unsubscribe","params":{"uri":"hottell://settings"}}`)
	saveSettings(t, srv, `{"settings":{"backfill_history":false},"expectedVersion":1}`)
	select {
	case got := <-stream:
		t.Fatalf("GET stream after unsubscribe: got %s, want nothing", got)
	case <-time.After(300 * time.Millisecond):
	}
	if got := readVersion(t, session, 6); got != 2 {
		t.Fatalf("version after second save: got %d, want 2", got)
	}
}

// saveSettings saves the settings of the signed-in user through the API.
func saveSettings(t *testing.T, srv *httptest.Server, body string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, srv.URL+"/api/me/telemetry-settings",
		strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8080")
	req.AddCookie(&http.Cookie{Name: "ht_session", Value: strings.Repeat("A", 43)})
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("save settings: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save settings: got status %d, want 200", resp.StatusCode)
	}
}

// readVersion reads hottell://settings in the session with JSON-RPC id and returns the
// version of the document.
func readVersion(t *testing.T, session *mcpSession, id int) int64 {
	t.Helper()
	raw := session.call(t, fmt.Sprintf(
		`{"jsonrpc":"2.0","id":%d,"method":"resources/read","params":{"uri":"hottell://settings"}}`, id))
	var resp struct {
		Result struct {
			Contents []struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			} `json:"contents"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil || len(resp.Result.Contents) != 1 {
		t.Fatalf("read settings: got %s, want one content", raw)
	}
	var doc struct {
		Version int64 `json:"version"`
	}
	if err := json.Unmarshal([]byte(resp.Result.Contents[0].Text), &doc); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	return doc.Version
}
