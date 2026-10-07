package mcp

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// settingsURI is the URI of the settings resource, one for every user: its contents are the
// settings of the user whose MCP key the request carries (mcp.md, section «Ресурс настроек»).
const settingsURI = "hottell://settings"

// settingsSchema is docs/specs/hottell-contract/settings.schema.json, the output schema of
// get_settings; a test keeps the copy equal to the contract.
//
//go:embed settings.schema.json
var settingsSchema json.RawMessage

// readInternalError is the answer to resources/read when the settings cannot be read:
// JSON-RPC -32603 (mcp.md, section «Ресурс настроек»); the SDK passes a plain error on
// with no such code.
func readInternalError() error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "internal: the service failed, try again later"}
}

// settingsDocument is the settings document of a user in full form with its version, the
// contents of the settings resource and the result of get_settings.
type settingsDocument struct {
	Version int64 `json:"version"`
	domain.TelemetrySettings
}

// getSettingsInput is the empty input of get_settings.
type getSettingsInput struct{}

// readSettings returns the settings document of the user with userID.
func readSettings(ctx context.Context, settings Settings, userID uuid.UUID) (settingsDocument, error) {
	parsed, version, err := settings.Get(ctx, userID)
	if err != nil {
		return settingsDocument{}, fmt.Errorf("get settings: %w", err)
	}
	return settingsDocument{Version: version, TelemetrySettings: parsed}, nil
}

// addSettings adds to the server of a session of the user with userID the settings resource
// and the tool get_settings, which gives the same document to clients without resources.
func addSettings(server *sdkmcp.Server, settings Settings, userID uuid.UUID, logger *slog.Logger) {
	server.AddResource(&sdkmcp.Resource{
		URI:      settingsURI,
		Name:     "settings",
		Title:    "Настройки-запреты hottell",
		MIMEType: "application/json",
	}, func(ctx context.Context, _ *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
		document, err := readSettings(ctx, settings, userID)
		if err != nil {
			logger.ErrorContext(ctx, "read settings resource", "error", err)
			return nil, readInternalError()
		}
		text, err := json.Marshal(document)
		if err != nil {
			logger.ErrorContext(ctx, "marshal settings resource", "error", err)
			return nil, readInternalError()
		}
		return &sdkmcp.ReadResourceResult{Contents: []*sdkmcp.ResourceContents{
			{URI: settingsURI, MIMEType: "application/json", Text: string(text)},
		}}, nil
	})

	tool := &sdkmcp.Tool{
		Name: "get_settings",
		Description: "Возвращает текущие настройки-запреты пользователя hottell с версией: те же данные, " +
			"что ресурс hottell://settings, для клиентов без поддержки ресурсов.",
		Annotations:  &sdkmcp.ToolAnnotations{ReadOnlyHint: true},
		OutputSchema: settingsSchema,
	}
	sdkmcp.AddTool(server, tool, func(ctx context.Context, _ *sdkmcp.CallToolRequest, _ getSettingsInput) (
		*sdkmcp.CallToolResult, settingsDocument, error,
	) {
		document, err := readSettings(ctx, settings, userID)
		if err != nil {
			logger.ErrorContext(ctx, "get_settings", "error", err)
			return nil, settingsDocument{}, errInternal
		}
		return nil, document, nil
	})
}

// keepAliveFailed is the log message of a keep-alive ping the session did not answer.
const keepAliveFailed = "keep-alive ping failed"

// subscription is the subscription of one MCP session to the settings of its user: while it
// is on, every new version of the settings sends notifications/resources/updated to the
// session, and the session is pinged every keepAlive. The server of a session serves that
// session alone, so its ResourceUpdated reaches no other session.
type subscription struct {
	server    *sdkmcp.Server
	settings  Settings
	userID    uuid.UUID
	keepAlive time.Duration
	logger    *slog.Logger

	mu     sync.Mutex
	cancel context.CancelFunc // nil while unsubscribed
	watch  sync.Once
}

// subscribe answers resources/subscribe: it subscribes the session to the settings of its
// user, once however often it is called, and drops the subscription when the session closes.
func (s *subscription) subscribe(ctx context.Context, req *sdkmcp.SubscribeRequest) error {
	if req.Params.URI != settingsURI {
		return sdkmcp.ResourceNotFoundError(req.Params.URI)
	}
	s.watch.Do(func() {
		go func() {
			_ = req.Session.Wait()
			s.stop()
		}()
	})

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return nil
	}
	// The subscription outlives the subscribe request: it ends with stop alone.
	subCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.cancel = cancel
	versions := s.settings.Subscribe(subCtx, s.userID)
	go func() {
		for range versions {
			_ = s.server.ResourceUpdated(subCtx, &sdkmcp.ResourceUpdatedNotificationParams{URI: settingsURI})
		}
	}()
	go s.ping(subCtx, req.Session)
	return nil
}

// ping sends the MCP method ping to the session every keepAlive until ctx is done, so that
// the standalone GET stream the notifications go to is not closed by a proxy while idle. A
// ping the session does not answer is logged and leaves the session open: the client keeps
// its own keep-alive and reconnects.
func (s *subscription) ping(ctx context.Context, session *sdkmcp.ServerSession) {
	ticker := time.NewTicker(s.keepAlive)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// The ping takes a context of its own: one derived from the subscribe request
			// would send it to the closed stream of that request, not the GET stream.
			pingCtx, cancel := context.WithTimeout(context.Background(), s.keepAlive/2)
			err := session.Ping(pingCtx, nil) //nolint:contextcheck // not the context of the subscribe request
			cancel()
			if err != nil && ctx.Err() == nil {
				s.logger.WarnContext(ctx, keepAliveFailed, "session", session.ID(), "error", err)
			}
		}
	}
}

// unsubscribe answers resources/unsubscribe: no more notifications reach the session.
func (s *subscription) unsubscribe(_ context.Context, req *sdkmcp.UnsubscribeRequest) error {
	if req.Params.URI != settingsURI {
		return sdkmcp.ResourceNotFoundError(req.Params.URI)
	}
	s.stop()
	return nil
}

// stop drops the subscription to the settings, if there is one.
func (s *subscription) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}
