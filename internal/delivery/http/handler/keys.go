package handler

import (
	"context"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/application/keys"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/openapi"
)

// mcpServerName is the name the MCP server is added to the agent under.
const mcpServerName = "hottell"

// GetMyKeys answers whether the user of the session has an active MCP key and collector
// token, when each was created and last used.
func (a *API) GetMyKeys(ctx context.Context, _ openapi.GetMyKeysRequestObject) (openapi.GetMyKeysResponseObject, error) {
	me, _, err := signedIn(ctx)
	if err != nil {
		return nil, err
	}
	status, err := a.keys.KeysStatus(ctx, me)
	if err != nil {
		return nil, err
	}
	return openapi.GetMyKeys200JSONResponse{Mcp: keyStatus(status.MCP), Ingest: keyStatus(status.Ingest)}, nil
}

// IssueMyMcpKey issues a new MCP key of the user of the session in place of the one they
// had and answers its open value once, with the address and the name of the MCP server.
func (a *API) IssueMyMcpKey(ctx context.Context, _ openapi.IssueMyMcpKeyRequestObject) (
	openapi.IssueMyMcpKeyResponseObject, error,
) {
	me, _, err := signedIn(ctx)
	if err != nil {
		return nil, err
	}
	key, err := a.keys.IssueMCPKey(ctx, me)
	if err != nil {
		return nil, err
	}
	return openapi.IssueMyMcpKey201JSONResponse{Key: key, McpUrl: a.mcpURL, ServerName: mcpServerName}, nil
}

// RevokeMyMcpKey revokes the MCP key of the user of the session.
func (a *API) RevokeMyMcpKey(ctx context.Context, _ openapi.RevokeMyMcpKeyRequestObject) (
	openapi.RevokeMyMcpKeyResponseObject, error,
) {
	me, _, err := signedIn(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.keys.RevokeMCPKey(ctx, me); err != nil {
		return nil, err
	}
	return openapi.RevokeMyMcpKey204Response{}, nil
}

// ReissueMyIngestToken issues a new collector token of the user of the session in place of
// the one they had; its open value is not answered, only the binary gets it through MCP.
func (a *API) ReissueMyIngestToken(ctx context.Context, _ openapi.ReissueMyIngestTokenRequestObject) (
	openapi.ReissueMyIngestTokenResponseObject, error,
) {
	me, _, err := signedIn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := a.keys.ReissueIngestToken(ctx, me); err != nil {
		return nil, err
	}
	return openapi.ReissueMyIngestToken204Response{}, nil
}

// keyStatus returns s as the keys page shows it: no creation time when there is no key.
func keyStatus(s keys.KeyStatus) openapi.KeyStatus {
	var createdAt *time.Time
	if s.Active {
		createdAt = &s.CreatedAt
	}
	return openapi.KeyStatus{Active: s.Active, CreatedAt: createdAt, LastUsedAt: s.LastUsedAt}
}
