package clickhouse

import (
	"fmt"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// The hook events and the Claude events as aggregate tables (HT-536): the views compute, once
// on insert, the very columns the period reads computed from otel_logs on every read, under
// the positional names c0, c1, …; the reads then select those columns back in order.

const (
	hookEventsTable   = "otel.ht_hook_events"
	claudeEventsTable = "otel.ht_claude_events"
)

// hookColumns are the column expressions of hookSelect, in order, and its WITH clause.
func hookColumns() (with string, cols []string) {
	q := hookSelect()
	sel := strings.Index(q, "\nSELECT ")
	from := strings.LastIndex(q, "\nFROM otel.otel_logs")
	return q[:sel], splitTopLevel(q[sel+len("\nSELECT ") : from])
}

// claudeEventColumns are the column expressions of the Claude events read, in order.
func claudeEventColumns() []string {
	q := claudeEventsQuery("")
	sel := strings.Index(q, "SELECT ")
	from := strings.Index(q, "\n\t\tFROM otel.otel_logs")
	return splitTopLevel(q[sel+len("SELECT ") : from])
}

// splitTopLevel splits s at the commas outside parentheses, brackets and quotes.
func splitTopLevel(s string) []string {
	var out []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case c == ',' && depth == 0:
			out = append(out, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	return append(out, strings.TrimSpace(s[start:]))
}

// positional is "e0 AS c0, e1 AS c1, …" or, without expressions, "c0, c1, …" for n columns.
func positional(cols []string, n int) string {
	parts := make([]string, n)
	for i := range n {
		if cols != nil {
			parts[i] = cols[i] + " AS c" + fmt.Sprint(i)
		} else {
			parts[i] = "c" + fmt.Sprint(i)
		}
	}
	return strings.Join(parts, ", ")
}

// hookEventsView is the SELECT of the hook events view.
func hookEventsView() string {
	with, cols := hookColumns()
	return with + "\nSELECT " + positional(cols, len(cols)) + " FROM otel.otel_logs WHERE ServiceName = '" + hooksService + "'"
}

// claudeEventsView is the SELECT of the Claude events view.
func claudeEventsView() string {
	cols := claudeEventColumns()
	return "SELECT " + positional(cols, len(cols)) + " FROM otel.otel_logs WHERE ServiceName IN (" + claudeServices +
		") AND LogAttributes['event.name'] IN (" + claudeEventNames + ")"
}

// eventAggregateDDL creates the two tables, typed by their views' SELECT, and the views.
func eventAggregateDDL() []string {
	return []string{
		"CREATE TABLE IF NOT EXISTS " + hookEventsTable + " ENGINE = MergeTree PARTITION BY toYYYYMM(c0) " +
			"ORDER BY (c0, c4) EMPTY AS " + hookEventsView(),
		"CREATE TABLE IF NOT EXISTS " + claudeEventsTable + " ENGINE = MergeTree PARTITION BY toYYYYMM(c0) " +
			"ORDER BY (c0, c2) EMPTY AS " + claudeEventsView(),
		"CREATE MATERIALIZED VIEW IF NOT EXISTS otel.ht_hook_events_mv TO " + hookEventsTable + " AS " + hookEventsView(),
		"CREATE MATERIALIZED VIEW IF NOT EXISTS otel.ht_claude_events_mv TO " + claudeEventsTable + " AS " + claudeEventsView(),
	}
}

// hookAggregateWhere is hookWhere over the hook events table: c0 the time, c1 the person, c2 the
// agent, c4 the session.
func hookAggregateWhere(filter telemetry.Filter, start, end int64) (string, []any) {
	var where strings.Builder
	args := []any{clickhouse.Named("from", start), clickhouse.Named("to", end)}
	where.WriteString("WHERE c0 >= fromUnixTimestamp64Nano(@from) AND c0 < fromUnixTimestamp64Nano(@to)")
	if filter.UserID != uuid.Nil {
		where.WriteString(" AND c1 = @user")
		args = append(args, clickhouse.Named("user", filter.UserID.String()))
	}
	if filter.Agent != "" {
		where.WriteString(" AND c2 = @agent")
		args = append(args, clickhouse.Named("agent", filter.Agent))
	}
	if filter.SessionID != "" {
		where.WriteString(" AND c4 = @session")
		args = append(args, clickhouse.Named("session", filter.SessionID))
	}
	return where.String(), args
}
