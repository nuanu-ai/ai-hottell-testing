package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/mcp"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/mcp/mock"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

const settingsURI = "hottell://settings"

// quiet is how long a test waits to be sure a notification does not come.
const quiet = 300 * time.Millisecond

// fakeSettings keeps the settings of the users in memory and tells the subscribers of a user
// of every update, as the settings use case does with its Notifier; it counts the
// subscriptions the MCP server holds.
type fakeSettings struct {
	mu        sync.Mutex
	documents map[uuid.UUID]domain.TelemetrySettings
	versions  map[uuid.UUID]int64
	subs      map[uuid.UUID]map[chan int64]struct{}

	active atomic.Int64
}

var _ mcp.Settings = (*fakeSettings)(nil)

func newFakeSettings() *fakeSettings {
	return &fakeSettings{
		documents: make(map[uuid.UUID]domain.TelemetrySettings),
		versions:  make(map[uuid.UUID]int64),
		subs:      make(map[uuid.UUID]map[chan int64]struct{}),
	}
}

func (f *fakeSettings) Get(_ context.Context, userID uuid.UUID) (domain.TelemetrySettings, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	document, ok := f.documents[userID]
	if !ok {
		return domain.DefaultTelemetrySettings(), 0, nil
	}
	return document, f.versions[userID], nil
}

func (f *fakeSettings) Subscribe(ctx context.Context, userID uuid.UUID) <-chan int64 {
	ch := make(chan int64, 1)
	f.mu.Lock()
	if f.subs[userID] == nil {
		f.subs[userID] = make(map[chan int64]struct{})
	}
	f.subs[userID][ch] = struct{}{}
	f.mu.Unlock()
	f.active.Add(1)
	context.AfterFunc(ctx, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.subs[userID], ch)
		close(ch)
		f.active.Add(-1)
	})
	return ch
}

// update changes the settings of the user with userID with change, raises their version
// and tells the subscribers of the user; it returns the new version.
func (f *fakeSettings) update(userID uuid.UUID, change func(*domain.TelemetrySettings)) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	document, ok := f.documents[userID]
	if !ok {
		document = domain.DefaultTelemetrySettings()
	}
	change(&document)
	f.documents[userID] = document
	f.versions[userID]++
	version := f.versions[userID]
	for ch := range f.subs[userID] {
		select {
		case ch <- version:
		default:
		}
	}
	return version
}

// waitActive fails the test unless the server holds want subscriptions within a second.
func (f *fakeSettings) waitActive(t *testing.T, want int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for f.active.Load() != want {
		if time.Now().After(deadline) {
			t.Fatalf("active subscriptions: got %d, want %d", f.active.Load(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// connectSubscriber opens an MCP session to srv with the go-sdk client and its standalone
// GET stream, as the hottell binary does; the URIs of notifications/resources/updated the
// session gets arrive on the returned channel.
func connectSubscriber(t *testing.T, srv *httptest.Server, key string) (*sdkmcp.ClientSession, <-chan string) {
	t.Helper()
	updates := make(chan string, 16)
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, &sdkmcp.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, req *sdkmcp.ResourceUpdatedNotificationRequest) {
			updates <- req.Params.URI
		},
	})
	session, err := client.Connect(t.Context(), &sdkmcp.StreamableClientTransport{
		Endpoint: srv.URL, HTTPClient: newAuthorizedClient("Bearer " + key),
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, updates
}

// readSettings reads the settings resource and decodes its document.
func readSettings(t *testing.T, session *sdkmcp.ClientSession) (settingsDoc, string) {
	t.Helper()
	res, err := session.ReadResource(t.Context(), &sdkmcp.ReadResourceParams{URI: settingsURI})
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if len(res.Contents) != 1 || res.Contents[0].URI != settingsURI || res.Contents[0].MIMEType != "application/json" {
		t.Fatalf("contents: got %+v, want one application/json of %s", res.Contents, settingsURI)
	}
	var doc settingsDoc
	if err := json.Unmarshal([]byte(res.Contents[0].Text), &doc); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	return doc, res.Contents[0].Text
}

// settingsDoc is the part of a settings document the tests look at.
type settingsDoc struct {
	Version         int64 `json:"version"`
	BackfillHistory bool  `json:"backfill_history"`
}

// waitUpdate fails the test unless a notification of the settings arrives within 5 seconds.
func waitUpdate(t *testing.T, updates <-chan string) {
	t.Helper()
	select {
	case uri := <-updates:
		if uri != settingsURI {
			t.Fatalf("updated URI: got %q, want %q", uri, settingsURI)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no notifications/resources/updated")
	}
}

// expectNoUpdate fails the test when a notification arrives within quiet.
func expectNoUpdate(t *testing.T, updates <-chan string) {
	t.Helper()
	select {
	case uri := <-updates:
		t.Fatalf("unexpected notifications/resources/updated of %q", uri)
	case <-time.After(quiet):
	}
}

func TestSettingsSubscriptionNotifiesOfUpdate(t *testing.T) {
	t.Parallel()
	srv, probe := newServerWithSettings(t)
	session, updates := connectSubscriber(t, srv, keyAlice)
	bobSession, bobUpdates := connectSubscriber(t, srv, keyBob)

	if err := session.Subscribe(t.Context(), &sdkmcp.SubscribeParams{URI: settingsURI}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := bobSession.Subscribe(t.Context(), &sdkmcp.SubscribeParams{URI: settingsURI}); err != nil {
		t.Fatalf("subscribe bob: %v", err)
	}
	probe.waitActive(t, 2)
	if doc, _ := readSettings(t, session); doc.Version != 0 || doc.BackfillHistory {
		t.Fatalf("settings before update: got %+v, want version 0 without backfill", doc)
	}

	if version := probe.update(alice().ID, func(s *domain.TelemetrySettings) { s.BackfillHistory = true }); version != 1 {
		t.Fatalf("update: got version %d, want 1", version)
	}
	waitUpdate(t, updates)
	if doc, _ := readSettings(t, session); doc.Version != 1 || !doc.BackfillHistory {
		t.Fatalf("settings after update: got %+v, want version 1 with backfill", doc)
	}
	// Bob is subscribed to his own settings, which did not change.
	expectNoUpdate(t, bobUpdates)

	if err := session.Unsubscribe(t.Context(), &sdkmcp.UnsubscribeParams{URI: settingsURI}); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	probe.waitActive(t, 1)
	probe.update(alice().ID, func(s *domain.TelemetrySettings) { s.BackfillHistory = false })
	expectNoUpdate(t, updates)
	if doc, _ := readSettings(t, session); doc.Version != 2 {
		t.Fatalf("settings after second update: got %+v, want version 2", doc)
	}
}

func TestSettingsSubscriptionReachesEverySessionOfUser(t *testing.T) {
	t.Parallel()
	srv, probe := newServerWithSettings(t)
	first, firstUpdates := connectSubscriber(t, srv, keyAlice)
	second, secondUpdates := connectSubscriber(t, srv, keyAlice)
	for _, session := range []*sdkmcp.ClientSession{first, second} {
		// A second subscribe of the same session keeps one subscription.
		for range 2 {
			if err := session.Subscribe(t.Context(), &sdkmcp.SubscribeParams{URI: settingsURI}); err != nil {
				t.Fatalf("subscribe: %v", err)
			}
		}
	}
	probe.waitActive(t, 2)

	probe.update(alice().ID, func(s *domain.TelemetrySettings) { s.BackfillHistory = true })
	waitUpdate(t, firstUpdates)
	waitUpdate(t, secondUpdates)
	expectNoUpdate(t, firstUpdates)
}

func TestSettingsSubscriptionEndsWithSession(t *testing.T) {
	t.Parallel()
	srv, probe := newServerWithSettings(t)
	session, _ := connectSubscriber(t, srv, keyAlice)
	if err := session.Subscribe(t.Context(), &sdkmcp.SubscribeParams{URI: settingsURI}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	probe.waitActive(t, 1)

	if err := session.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}
	probe.waitActive(t, 0)
}

func TestSettingsResourceAndToolGiveSameDocument(t *testing.T) {
	t.Parallel()
	srv, probe := newServerWithSettings(t)
	session, _ := connectSubscriber(t, srv, keyAlice)
	probe.update(alice().ID, func(s *domain.TelemetrySettings) { s.Folders.Denied = []string{"~/work/**"} })

	resources, err := session.ListResources(t.Context(), nil)
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	want := `[{"mimeType":"application/json","name":"settings","title":"Настройки-запреты hottell","uri":"hottell://settings"}]`
	if got := mustJSON(t, resources.Resources); got != want {
		t.Fatalf("resources: got %s, want %s", got, want)
	}

	_, text := readSettings(t, session)
	res, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "get_settings", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("call get_settings: %v", err)
	}
	if res.IsError {
		t.Fatalf("get_settings failed: %+v", res.Content)
	}
	if got, want := mustJSON(t, res.StructuredContent), mustJSON(t, json.RawMessage(text)); got != want {
		t.Fatalf("structured content: got %s, want the resource %s", got, want)
	}
	content, ok := res.Content[0].(*sdkmcp.TextContent)
	if !ok || len(res.Content) != 1 || mustJSON(t, json.RawMessage(content.Text)) != mustJSON(t, json.RawMessage(text)) {
		t.Fatalf("content: got %+v, want the resource as text", res.Content)
	}
	if !strings.Contains(text, `"denied":["~/work/**"]`) || !strings.Contains(text, `"version":1`) {
		t.Fatalf("resource: got %s, want the saved folders at version 1", text)
	}
}

func TestGetSettingsSchemas(t *testing.T) {
	t.Parallel()
	session, err := connect(t, newServer(t), "Bearer "+keyAlice)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	tool := tools.Tools[2]
	if tool.Name != "get_settings" || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Fatalf("tool: got %+v, want get_settings with readOnlyHint", tool)
	}
	if got, want := mustJSON(t, tool.InputSchema), `{"additionalProperties":false,"type":"object"}`; got != want {
		t.Fatalf("input schema: got %s, want %s", got, want)
	}
	contract, err := os.ReadFile("../../../docs/specs/hottell-contract/settings.schema.json")
	if err != nil {
		t.Fatalf("read contract schema: %v", err)
	}
	if got, want := mustJSON(t, tool.OutputSchema), mustJSON(t, json.RawMessage(contract)); got != want {
		t.Fatalf("output schema: got %s, want settings.schema.json", got)
	}
}

func TestSettingsSchemaCopyMatchesContract(t *testing.T) {
	t.Parallel()
	contract, err := os.ReadFile("../../../docs/specs/hottell-contract/settings.schema.json")
	if err != nil {
		t.Fatalf("read contract schema: %v", err)
	}
	embedded, err := os.ReadFile("settings.schema.json")
	if err != nil {
		t.Fatalf("read embedded schema: %v", err)
	}
	if !bytes.Equal(contract, embedded) {
		t.Fatal("internal/delivery/mcp/settings.schema.json differs from docs/specs/hottell-contract/settings.schema.json")
	}
}

func TestSettingsUnknownURIIsNotFound(t *testing.T) {
	t.Parallel()
	session, _ := connectSubscriber(t, newServer(t), keyAlice)
	const other = "hottell://other"

	calls := map[string]func() error{
		"read": func() error {
			_, err := session.ReadResource(t.Context(), &sdkmcp.ReadResourceParams{URI: other})
			return err
		},
		"subscribe": func() error {
			return session.Subscribe(t.Context(), &sdkmcp.SubscribeParams{URI: other})
		},
		"unsubscribe": func() error {
			return session.Unsubscribe(t.Context(), &sdkmcp.UnsubscribeParams{URI: other})
		},
	}
	for name, call := range calls {
		var wireErr *jsonrpc.Error
		if err := call(); !errors.As(err, &wireErr) || wireErr.Code != jsonrpc.CodeInvalidParams ||
			wireErr.Message != "Resource not found" || !strings.Contains(string(wireErr.Data), other) {
			t.Fatalf("%s: got %v, want -32602 Resource not found of %s", name, err, other)
		}
	}
}

func TestSettingsStoreFailure(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := mock.NewMockSettings(ctrl)
	store.EXPECT().Get(gomock.Any(), alice().ID).Return(domain.TelemetrySettings{}, int64(0), errors.New("store down")).Times(2)
	srv := httptest.NewServer(mcp.New(knownKeys(t, ctrl), knownUsers(ctrl), store, origin, version, slog.New(slog.DiscardHandler)))
	t.Cleanup(srv.Close)
	session, _ := connectSubscriber(t, srv, keyAlice)

	_, err := session.ReadResource(t.Context(), &sdkmcp.ReadResourceParams{URI: settingsURI})
	var wireErr *jsonrpc.Error
	if !errors.As(err, &wireErr) || wireErr.Code != jsonrpc.CodeInternalError {
		t.Fatalf("read: got %v, want -32603", err)
	}

	res, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "get_settings", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("call get_settings: %v", err)
	}
	text, ok := res.Content[0].(*sdkmcp.TextContent)
	if !res.IsError || res.StructuredContent != nil || !ok || !strings.HasPrefix(text.Text, "internal: ") {
		t.Fatalf("result: got %+v, want isError with internal", res)
	}
}

func TestInitializeAnnouncesResourceSubscriptions(t *testing.T) {
	t.Parallel()
	session, err := connect(t, newServer(t), "Bearer "+keyAlice)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	want := `{"resources":{"subscribe":true},"tools":{}}`
	if got := mustJSON(t, session.InitializeResult().Capabilities); got != want {
		t.Fatalf("capabilities: got %s, want %s", got, want)
	}
}
