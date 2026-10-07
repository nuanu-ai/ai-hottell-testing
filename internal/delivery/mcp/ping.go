package mcp

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// errInternal is the error of a tool that failed on the service side; the client retries
// later (mcp.md, section «Инструменты»).
var errInternal = errors.New("internal: the service failed, try again later")

// pingInput is the empty input of ping.
type pingInput struct{}

// pingOutput is the result of ping.
type pingOutput struct {
	Version string `json:"version" jsonschema:"Версия сервиса."`
	Email   string `json:"email" jsonschema:"Email пользователя, чей ключ MCP пришёл в запросе."`
}

// addPing adds the tool ping, which checks the connection and the MCP key: it answers the
// service version and the email of the key's user.
func addPing(server *sdkmcp.Server, users Users, version string, logger *slog.Logger) {
	tool := &sdkmcp.Tool{
		Name:        "ping",
		Description: "Проверяет подключение к hottell и ключ MCP: возвращает версию сервиса и email пользователя ключа.",
		Annotations: &sdkmcp.ToolAnnotations{ReadOnlyHint: true},
	}
	sdkmcp.AddTool(server, tool, func(ctx context.Context, req *sdkmcp.CallToolRequest, _ pingInput) (
		*sdkmcp.CallToolResult, pingOutput, error,
	) {
		userID, err := requestUser(req)
		if err != nil {
			logger.ErrorContext(ctx, "ping: request user", "error", err)
			return nil, pingOutput{}, errInternal
		}
		user, err := users.Me(ctx, userID)
		if err != nil {
			logger.ErrorContext(ctx, "ping: get user", "error", err)
			return nil, pingOutput{}, errInternal
		}
		return nil, pingOutput{Version: version, Email: user.Email.String()}, nil
	})
}

// requestUser returns the id of the user whose MCP key the request carries, as authenticate
// put it into the request's TokenInfo.
func requestUser(req *sdkmcp.CallToolRequest) (uuid.UUID, error) {
	if req.Extra == nil || req.Extra.TokenInfo == nil {
		return uuid.Nil, errors.New("request carries no user")
	}
	return uuid.Parse(req.Extra.TokenInfo.UserID)
}
