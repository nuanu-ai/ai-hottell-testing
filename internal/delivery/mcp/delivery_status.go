package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// deliveryStatusInput is the input of report_delivery_status.
type deliveryStatusInput struct {
	QueueRecords    *int64     `json:"queue_records"`
	QueueBytes      *int64     `json:"queue_bytes"`
	LastSuccessAt   *time.Time `json:"last_success_at"`
	LastError       string     `json:"last_error"`
	LastErrorAt     *time.Time `json:"last_error_at"`
	Unauthorized    bool       `json:"unauthorized"`
	SettingsVersion *int64     `json:"settings_version"`
	BinaryVersion   string     `json:"binary_version"`
}

// deliveryStatusOutput is the result of report_delivery_status.
type deliveryStatusOutput struct {
	ReportedAt time.Time `json:"reported_at"`
}

// deliveryStatusInputSchema is the input schema of report_delivery_status as mcp.md gives it.
// The times are checked as RFC 3339 by decoding them: go-sdk does not check format.
const deliveryStatusInputSchema = `{
  "type": "object",
  "required": ["queue_records", "queue_bytes", "unauthorized", "settings_version", "binary_version"],
  "additionalProperties": false,
  "properties": {
    "queue_records": { "type": ["integer", "null"], "minimum": 0, "description": "Записей в очереди бинаря; null — бинарь не смог прочитать очередь." },
    "queue_bytes": { "type": ["integer", "null"], "minimum": 0, "description": "Байт в очереди бинаря; null — бинарь не смог прочитать очередь." },
    "last_success_at": { "type": ["string", "null"], "format": "date-time", "description": "Когда сервис последний раз принял запрос; нет до первого." },
    "last_error": { "type": "string", "description": "Последний запрос, который сервис не принял; пусто после успеха. Длиннее 500 знаков обрезается." },
    "last_error_at": { "type": ["string", "null"], "format": "date-time", "description": "Когда случилась last_error." },
    "unauthorized": { "type": "boolean", "description": "Сервис отверг токен коллектора, отправка стоит." },
    "settings_version": { "type": ["integer", "null"], "minimum": 0, "description": "Версия настроек, которые применяет бинарь; 0 — настроек нет; null — бинарь не смог прочитать кэш настроек." },
    "binary_version": { "type": "string", "maxLength": 100, "description": "Версия бинаря." }
  }
}`

// deliveryStatusOutputSchema is the output schema of report_delivery_status.
const deliveryStatusOutputSchema = `{
  "type": "object",
  "required": ["reported_at"],
  "additionalProperties": false,
  "properties": {
    "reported_at": { "type": "string", "format": "date-time", "description": "Когда сервис принял отчёт." }
  }
}`

// addDeliveryStatus adds the tool report_delivery_status, by which the hottell binary tells
// how its sending goes; the service keeps the last report of each user.
func addDeliveryStatus(server *sdkmcp.Server, statuses DeliveryStatuses, logger *slog.Logger) {
	tool := &sdkmcp.Tool{
		Name: "report_delivery_status",
		Description: "Принимает отчёт бинаря hottell об отправке телеметрии: очередь, последний успех и ошибку. " +
			"Инструмент нужен бинарю hottell, агенту вызывать его не нужно.",
		Annotations:  &sdkmcp.ToolAnnotations{IdempotentHint: true},
		InputSchema:  json.RawMessage(deliveryStatusInputSchema),
		OutputSchema: json.RawMessage(deliveryStatusOutputSchema),
	}
	sdkmcp.AddTool(server, tool, func(ctx context.Context, req *sdkmcp.CallToolRequest, in deliveryStatusInput) (
		*sdkmcp.CallToolResult, deliveryStatusOutput, error,
	) {
		userID, err := requestUser(req)
		if err != nil {
			logger.ErrorContext(ctx, "report_delivery_status: request user", "error", err)
			return nil, deliveryStatusOutput{}, errInternal
		}
		status, err := report(ctx, statuses, userID, in)
		if err != nil {
			logger.ErrorContext(ctx, "report_delivery_status: keep the status", "error", err)
			return nil, deliveryStatusOutput{}, errInternal
		}
		return nil, deliveryStatusOutput{ReportedAt: status.ReportedAt.UTC()}, nil
	})
}

func report(
	ctx context.Context, statuses DeliveryStatuses, userID uuid.UUID, in deliveryStatusInput,
) (domain.DeliveryStatus, error) {
	return statuses.Report(ctx, userID, domain.NewDeliveryReport(domain.DeliveryReport{
		QueueRecords:    in.QueueRecords,
		QueueBytes:      in.QueueBytes,
		LastSuccessAt:   in.LastSuccessAt,
		LastError:       in.LastError,
		LastErrorAt:     in.LastErrorAt,
		Unauthorized:    in.Unauthorized,
		SettingsVersion: in.SettingsVersion,
		BinaryVersion:   in.BinaryVersion,
	}))
}
