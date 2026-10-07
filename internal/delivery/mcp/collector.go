package mcp

import (
	"context"
	"encoding/json"
	"log/slog"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// collectorTokenInput is the empty input of get_collector_token.
type collectorTokenInput struct{}

// collectorTokenOutput is the result of get_collector_token.
type collectorTokenOutput struct {
	Token        string `json:"token"`
	OTLPEndpoint string `json:"otlp_endpoint"`
}

// collectorTokenOutputSchema is the output schema of get_collector_token as mcp.md gives it:
// the pattern and format a schema inferred from collectorTokenOutput would lack.
const collectorTokenOutputSchema = `{
  "type": "object",
  "required": ["token", "otlp_endpoint"],
  "additionalProperties": false,
  "properties": {
    "token": {
      "type": "string",
      "pattern": "^[A-Za-z0-9_-]+$",
      "description": "Токен коллектора, открытым значением. Без символов «,» и «=»: у Claude заголовки OTel задаются строкой ключ=значение через запятую (native-otel.md)."
    },
    "otlp_endpoint": {
      "type": "string",
      "format": "uri",
      "description": "Базовый адрес приёма OTLP/HTTP: HT_PUBLIC_ORIGIN без пути /v1/...; клиент дописывает /v1/logs, /v1/metrics, /v1/traces."
    }
  }
}`

// addCollectorToken adds the tool get_collector_token, which answers the collector token of
// the key's user, creating it when they have none, and the base address of the OTLP ingest,
// origin.
func addCollectorToken(server *sdkmcp.Server, keys Keys, origin string, logger *slog.Logger) {
	tool := &sdkmcp.Tool{
		Name: "get_collector_token",
		Description: "Возвращает токен коллектора пользователя и адрес приёма OTLP. Инструмент нужен бинарю hottell, " +
			"агенту вызывать его не нужно.",
		Annotations:  &sdkmcp.ToolAnnotations{IdempotentHint: true},
		OutputSchema: json.RawMessage(collectorTokenOutputSchema),
	}
	sdkmcp.AddTool(server, tool, func(ctx context.Context, req *sdkmcp.CallToolRequest, _ collectorTokenInput) (
		*sdkmcp.CallToolResult, collectorTokenOutput, error,
	) {
		userID, err := requestUser(req)
		if err != nil {
			logger.ErrorContext(ctx, "get_collector_token: request user", "error", err)
			return nil, collectorTokenOutput{}, errInternal
		}
		token, err := keys.IngestToken(ctx, userID)
		if err != nil {
			logger.ErrorContext(ctx, "get_collector_token: ingest token", "error", err)
			return nil, collectorTokenOutput{}, errInternal
		}
		return nil, collectorTokenOutput{Token: token, OTLPEndpoint: origin}, nil
	})
}
