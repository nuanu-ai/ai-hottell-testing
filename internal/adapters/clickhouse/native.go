package clickhouse

import (
	"context"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

const (
	nativeAgentClaude = "claude"
	nativeAgentCodex  = "codex"

	// userAttribute is the resource attribute the ingest sets on every record, the native ones
	// included, to the person the collector token belongs to.
	userAttribute = "hottell.user.id"

	// claudeServices are the service.name values of Claude Code's native OpenTelemetry.
	claudeServices = "'claude-code', 'claude-code-desktop'"
	// claudeEventNames are the event.name values read as ClaudeEvent.
	claudeEventNames = "'api_request', 'api_error', 'tool_result', 'tool_decision', 'user_prompt', 'subagent_completed'"
	// codexService selects the Codex records: the service.name is the client's originator
	// (codex_exec, codex-app-server, Codex Desktop and so on).
	codexService = "(ServiceName = 'Codex Desktop' OR startsWith(ServiceName, 'codex'))"

	// costRecorded tells that cost_usd is a price: a finite, non-negative number. nan, inf or a
	// negative cost, which a client can send, is no price and must not reach a total.
	costRecorded = "ifNull(isFinite(toFloat64OrNull(LogAttributes['cost_usd'])) " +
		"AND toFloat64OrNull(LogAttributes['cost_usd']) >= 0, false)"
)

// ClaudeEvents returns the native OpenTelemetry events of Claude Code that match f, oldest
// first. It answers telemetry.ErrNoNativePeriod when f has neither a period nor a session, and
// nothing when f.Agent is the other agent.
func (r *Reader) ClaudeEvents(ctx context.Context, f telemetry.Filter) ([]telemetry.ClaudeEvent, error) {
	if err := checkNativeFilter(f); err != nil {
		return nil, err
	}
	if !readsNativeAgent(f, nativeAgentClaude) {
		return nil, nil
	}
	where, args := nativeWhere(f, "LogAttributes['session.id']", "Timestamp")
	query := claudeEventsQuery(where)
	if r.aggregatesReady(ctx) {
		// The aggregate table (aggregates_events.go): c0 the time, c2 the session, c21 the person.
		where, args = nativeAggregateWhere(f)
		query = "SELECT " + positional(nil, len(claudeEventColumns())) + " FROM " + claudeEventsTable +
			" WHERE 1 = 1" + where + " ORDER BY c0"
	}
	return queryNative(ctx, r, "read claude events", query, args, func(rows driver.Rows, e *telemetry.ClaudeEvent) error {
		var user string
		if err := rows.Scan(&e.Time, &e.Event, &e.SessionID, &e.PromptID, &e.Model,
			&e.InputTokens, &e.OutputTokens, &e.CacheReadTokens, &e.CacheCreationTokens,
			&e.CostUSD, &e.CostRecorded, &e.DurationMs,
			&e.QuerySource, &e.ToolName, &e.ToolUseID, &e.Success, &e.Error,
			&e.Decision, &e.Source, &e.StatusCode, &e.AgentType, &user); err != nil {
			return err
		}
		e.UserID = parseUser(user)
		return nil
	})
}

// ClaudeMetrics returns the claude_code.* data points of Claude Code's native OpenTelemetry that
// match f, oldest first. It answers telemetry.ErrNoNativePeriod when f has neither a period nor
// a session, and nothing when f.Agent is the other agent.
func (r *Reader) ClaudeMetrics(ctx context.Context, f telemetry.Filter) ([]telemetry.ClaudeMetric, error) {
	if err := checkNativeFilter(f); err != nil {
		return nil, err
	}
	if !readsNativeAgent(f, nativeAgentClaude) {
		return nil, nil
	}
	where, args := nativeWhere(f, "Attributes['session.id']", "TimeUnix")
	query := `SELECT TimeUnix, MetricName, Attributes['session.id'], Attributes['type'], Attributes['model'], Value,
		ResourceAttributes['` + userAttribute + `']
		FROM otel.otel_metrics_sum
		WHERE ServiceName IN (` + claudeServices + `)
		AND startsWith(MetricName, 'claude_code.')` + where + `
		ORDER BY TimeUnix`
	return queryNative(ctx, r, "read claude metrics", query, args, func(rows driver.Rows, m *telemetry.ClaudeMetric) error {
		var user string
		if err := rows.Scan(&m.Time, &m.Metric, &m.SessionID, &m.Type, &m.Model, &m.Value, &user); err != nil {
			return err
		}
		m.UserID = parseUser(user)
		return nil
	})
}

// CodexSSE returns the codex.sse_event events of Codex that carry a conversation.id and match f,
// oldest first. f.SessionID is matched against the conversation.id. It answers
// telemetry.ErrNoNativePeriod when f has neither a period nor a session, and nothing when f.Agent
// is the other agent.
func (r *Reader) CodexSSE(ctx context.Context, f telemetry.Filter) ([]telemetry.CodexSSE, error) {
	if err := checkNativeFilter(f); err != nil {
		return nil, err
	}
	if !readsNativeAgent(f, nativeAgentCodex) {
		return nil, nil
	}
	where, args := nativeWhere(f, "LogAttributes['conversation.id']", "Timestamp")
	query := `SELECT Timestamp, LogAttributes['conversation.id'], LogAttributes['event.kind'], LogAttributes['model'],
		toInt64OrZero(LogAttributes['input_token_count']), toInt64OrZero(LogAttributes['cached_token_count']),
		toInt64OrZero(LogAttributes['output_token_count']), toInt64OrZero(LogAttributes['reasoning_token_count']),
		ResourceAttributes['` + userAttribute + `'], ServiceName
		FROM otel.otel_logs
		WHERE ` + codexService + `
		AND LogAttributes['event.name'] = 'codex.sse_event'
		AND LogAttributes['conversation.id'] != ''` + where + `
		ORDER BY Timestamp`
	return queryNative(ctx, r, "read codex sse events", query, args, func(rows driver.Rows, e *telemetry.CodexSSE) error {
		var user string
		if err := rows.Scan(&e.Time, &e.SessionID, &e.Kind, &e.Model,
			&e.InputTokens, &e.CachedTokens, &e.OutputTokens, &e.ReasoningTokens, &user, &e.Service); err != nil {
			return err
		}
		e.UserID = parseUser(user)
		return nil
	})
}

// CodexCoverage counts the Codex events that match f by service, event name and endpoint, split
// by whether they carry a conversation.id. f.SessionID is matched against the conversation.id,
// so a session narrows the count to its linked events. It answers telemetry.ErrNoNativePeriod
// when f has neither a period nor a session, and nothing when f.Agent is the other agent.
func (r *Reader) CodexCoverage(ctx context.Context, f telemetry.Filter) ([]telemetry.CodexCoverage, error) {
	if err := checkNativeFilter(f); err != nil {
		return nil, err
	}
	if !readsNativeAgent(f, nativeAgentCodex) {
		return nil, nil
	}
	where, args := nativeWhere(f, "LogAttributes['conversation.id']", "Timestamp")
	query := `SELECT ServiceName, LogAttributes['event.name'], substringUTF8(LogAttributes['endpoint'], 1, 80) AS endpoint,
		LogAttributes['conversation.id'] != '' AS linked,
		count(), uniqExactIf(LogAttributes['conversation.id'], linked)
		FROM otel.otel_logs
		WHERE ` + codexService + where + `
		GROUP BY ServiceName, LogAttributes['event.name'], endpoint, linked
		ORDER BY ServiceName, LogAttributes['event.name'], endpoint, linked`
	return queryNative(ctx, r, "read codex coverage", query, args, func(rows driver.Rows, c *telemetry.CodexCoverage) error {
		return rows.Scan(&c.Service, &c.Event, &c.Endpoint, &c.Linked, &c.Events, &c.Sessions)
	})
}

// parseUser is the person of a native record's hottell.user.id; uuid.Nil when it holds none.
func parseUser(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return id
}

// checkNativeFilter refuses a filter that bounds a read neither by a period (From and To, To after
// From) nor by a session: such a read would scan the whole table. Every native query constrains
// ServiceName, and the period or the session is what bounds the rest.
func checkNativeFilter(f telemetry.Filter) error {
	if f.SessionID != "" {
		return nil
	}
	if f.From.IsZero() || f.To.IsZero() || !f.To.After(f.From) {
		return telemetry.ErrNoNativePeriod
	}
	return nil
}

// readsNativeAgent tells whether f reads the records of agent: it names no agent or that one.
func readsNativeAgent(f telemetry.Filter, agent string) bool {
	return f.Agent == "" || f.Agent == agent
}

// nativeWhere returns the conditions f adds to a native read, each starting with " AND ", and
// their arguments. The person is the resource's hottell.user.id and the session is read from
// sessionExpr; checkNativeFilter has already ruled that the read has a period or a session, and a
// zero From or To leaves that end open. Times are bound as epoch nanoseconds, which keeps the instant exact for the DateTime64 and DateTime columns alike.
func nativeWhere(f telemetry.Filter, sessionExpr, timeColumn string) (where string, args []any) {
	var b strings.Builder
	if f.UserID != uuid.Nil {
		b.WriteString(" AND ResourceAttributes['" + userAttribute + "'] = ?")
		args = append(args, f.UserID.String())
	}
	if f.SessionID != "" {
		b.WriteString(" AND " + sessionExpr + " = ?")
		args = append(args, f.SessionID)
	}
	if !f.From.IsZero() {
		b.WriteString(" AND " + timeColumn + " >= fromUnixTimestamp64Nano(?, 'UTC')")
		args = append(args, f.From.UnixNano())
	}
	if !f.To.IsZero() {
		b.WriteString(" AND " + timeColumn + " < fromUnixTimestamp64Nano(?, 'UTC')")
		args = append(args, f.To.UnixNano())
	}
	return b.String(), args
}

// queryNative runs query and scans every row with scan; a driver error is answered as
// telemetry.ErrUnavailable of the operation op. It returns nil when there are no rows.
func queryNative[T any](
	ctx context.Context, r *Reader, op, query string, args []any,
	scan func(rows driver.Rows, row *T) error,
) ([]T, error) {
	rows, err := r.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, r.unavailable(op, err)
	}
	defer func() { _ = rows.Close() }()

	var out []T
	for rows.Next() {
		var row T
		if err := scan(rows, &row); err != nil {
			return nil, r.unavailable(op, err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, r.unavailable(op, err)
	}
	return out, nil
}

// claudeEventsQuery is the read of the Claude events from otel_logs with the conditions where.
func claudeEventsQuery(where string) string {
	return `SELECT Timestamp,
		LogAttributes['event.name'], LogAttributes['session.id'], LogAttributes['prompt.id'], LogAttributes['model'],
		toInt64OrZero(LogAttributes['input_tokens']), toInt64OrZero(LogAttributes['output_tokens']),
		toInt64OrZero(LogAttributes['cache_read_tokens']), toInt64OrZero(LogAttributes['cache_creation_tokens']),
		if(` + costRecorded + `, toFloat64OrZero(LogAttributes['cost_usd']), 0), ` + costRecorded + `,
		toInt64OrZero(LogAttributes['duration_ms']),
		LogAttributes['query_source'], LogAttributes['tool_name'], LogAttributes['tool_use_id'],
		LogAttributes['success'] = 'true', substringUTF8(LogAttributes['error'], 1, 300),
		LogAttributes['decision'], LogAttributes['source'], LogAttributes['status_code'], LogAttributes['agent_type'],
		ResourceAttributes['` + userAttribute + `']
		FROM otel.otel_logs
		WHERE ServiceName IN (` + claudeServices + `)
		AND LogAttributes['event.name'] IN (` + claudeEventNames + `)` + where + `
		ORDER BY Timestamp`
}

// nativeAggregateWhere is nativeWhere over the Claude events table.
func nativeAggregateWhere(f telemetry.Filter) (string, []any) {
	where, args := nativeWhere(f, "c2", "c0")
	return strings.ReplaceAll(where, "ResourceAttributes['"+userAttribute+"']", "c21"), args
}
