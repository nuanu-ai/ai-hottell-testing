package clickhouse

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

var errNoReceivedUser = errors.New("last received: the query names no user")

// LastReceived returns, for each agent and source of the records of the person userID taken
// at or after since, when the last of them arrived, ordered by agent and source. A hook or
// transcript record names its agent; a native record is Claude's by its service and Codex's
// otherwise.
func (r *Reader) LastReceived(ctx context.Context, userID uuid.UUID, since time.Time) ([]telemetry.LastReceived, error) {
	const op = "read last received"
	if userID == uuid.Nil {
		return nil, errNoReceivedUser
	}
	rows, err := r.conn.Query(ctx, `SELECT agent, source, max(Timestamp)
FROM (
  SELECT Timestamp,
    multiIf(ServiceName = '`+hooksService+`', '`+telemetry.ReceivedHooks+`',
            ServiceName = '`+transcriptsService+`', '`+telemetry.ReceivedTranscripts+`', '`+telemetry.ReceivedOTel+`') AS source,
    multiIf(ServiceName IN ('`+hooksService+`', '`+transcriptsService+`'), LogAttributes['agent'],
            ServiceName IN (`+claudeServices+`), '`+nativeAgentClaude+`', '`+nativeAgentCodex+`') AS agent
  FROM otel.otel_logs
  WHERE ResourceAttributes['`+userAttribute+`'] = ? AND Timestamp >= fromUnixTimestamp64Nano(?)
    AND (ServiceName IN ('`+hooksService+`', '`+transcriptsService+`', `+claudeServices+`) OR `+codexService+`)
)
WHERE agent IN ('`+nativeAgentClaude+`', '`+nativeAgentCodex+`')
GROUP BY agent, source
ORDER BY agent, source`, userID.String(), since.UnixNano())
	if err != nil {
		return nil, r.unavailable(op, err)
	}
	defer func() { _ = rows.Close() }()
	var out []telemetry.LastReceived
	for rows.Next() {
		var lr telemetry.LastReceived
		if err := rows.Scan(&lr.Agent, &lr.Source, &lr.At); err != nil {
			return nil, r.unavailable(op, err)
		}
		lr.At = lr.At.UTC()
		out = append(out, lr)
	}
	if err := rows.Err(); err != nil {
		return nil, r.unavailable(op, err)
	}
	return out, nil
}
