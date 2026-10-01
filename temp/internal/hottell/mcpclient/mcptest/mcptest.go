// Package mcptest is an in-process MCP server of the service for the tests of the
// hottell binary's processes: it has the collector token tool and the settings resource
// of docs/specs/hottell-contract/mcp.md, and accepts only its key.
package mcptest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const settingsURI = "hottell://settings"

// Server is the test MCP server; create it with New and stop it with Close.
type Server struct {
	*httptest.Server

	mcp *mcp.Server
	key string

	mu       sync.Mutex
	token    string
	endpoint string
	settings string
}

// New starts a server that accepts the key and answers with the collector token, the
// OTLP endpoint and the settings document.
func New(key, token, endpoint, settings string) *Server {
	s := &Server{key: key, token: token, endpoint: endpoint, settings: settings}
	s.mcp = mcp.NewServer(&mcp.Implementation{Name: "hottell", Version: "test"}, &mcp.ServerOptions{
		SubscribeHandler:   func(context.Context, *mcp.SubscribeRequest) error { return nil },
		UnsubscribeHandler: func(context.Context, *mcp.UnsubscribeRequest) error { return nil },
	})
	type empty struct{}
	type tokenOut struct {
		Token        string `json:"token"`
		OTLPEndpoint string `json:"otlp_endpoint"`
	}
	mcp.AddTool(s.mcp, &mcp.Tool{Name: "get_collector_token"},
		func(context.Context, *mcp.CallToolRequest, empty) (*mcp.CallToolResult, tokenOut, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			return nil, tokenOut{Token: s.token, OTLPEndpoint: s.endpoint}, nil
		})
	s.mcp.AddResource(&mcp.Resource{URI: settingsURI, Name: "settings", MIMEType: "application/json"},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
				{URI: settingsURI, MIMEType: "application/json", Text: s.settings},
			}}, nil
		})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.mcp }, nil)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+s.key {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	return s
}

// SetToken changes the collector token the tool returns, as a reissue in the interface
// does.
func (s *Server) SetToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = token
}

// SetSettings changes the settings document and notifies the subscribers.
func (s *Server) SetSettings(ctx context.Context, settings string) error {
	s.mu.Lock()
	s.settings = settings
	s.mu.Unlock()
	return s.mcp.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{URI: settingsURI})
}
