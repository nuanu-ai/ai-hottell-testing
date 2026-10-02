// Package mcptest is an in-process MCP server of the service for the tests of the
// hottell binary's processes: it has the collector token and delivery status tools and the
// settings resource of docs/specs/hottell-contract/mcp.md, and accepts only its key.
package mcptest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

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
	// reports are the arguments of the report_delivery_status calls taken, in order.
	reports     []map[string]any
	failReports bool
	// attempts counts the report_delivery_status calls, failed ones included.
	attempts int
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
	type reportOut struct {
		ReportedAt time.Time `json:"reported_at"`
	}
	mcp.AddTool(s.mcp, &mcp.Tool{Name: "report_delivery_status"},
		func(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, reportOut, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.attempts++
			if s.failReports {
				return nil, reportOut{}, errors.New("internal: the service failed, try again later")
			}
			s.reports = append(s.reports, in)
			return nil, reportOut{ReportedAt: time.Now().UTC()}, nil
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

// Reports returns the arguments of the report_delivery_status calls the server took, in
// order.
func (s *Server) Reports() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.reports...)
}

// FailReports makes report_delivery_status answer the internal error while fail is set;
// a failed call is not taken.
func (s *Server) FailReports(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failReports = fail
}

// ReportAttempts counts the report_delivery_status calls, failed ones included.
func (s *Server) ReportAttempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}
