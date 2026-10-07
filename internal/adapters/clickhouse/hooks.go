package clickhouse

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// hooksService is the service.name the hottell binary sends hook events under.
const hooksService = "hottell-hooks"

// bodyField is the SQL of the raw JSON text of the field key of the record's body, taken from the
// map body the WITH of hookSelect parses the body into once per row: the quoted text of a string,
// the JSON text of anything else, empty when the body has no such field or is not a JSON object.
func bodyField(key string) string {
	return "body['" + key + "']"
}

// bodyString is the SQL that reads the field key of the record's body as a string: the text of a
// JSON string, empty for any other value, as JSONExtractString reads it.
func bodyString(key string) string {
	return "JSONExtractString(" + bodyField(key) + ")"
}

// bodyText is the SQL that reads the field key of the record's body as text: the text of a
// string, the JSON text of anything else (Claude sends tool_response as an object, Codex as a
// string). It is empty when the body has no such field or is not JSON. A raw JSON string, and
// only one, starts with a quote.
func bodyText(key string) string {
	return "if(startsWith(" + bodyField(key) + ", '\"'), " + bodyString(key) + ", " + bodyField(key) + ")"
}

// hookSelect is the query of a hook event, without its WHERE: it lists the columns of a hook event
// in the order scanHookEvent reads them. The body is parsed once per row into the map of its
// top-level fields and their raw JSON texts; a JSONExtract call per field parsed the whole body
// twenty times, the most of the time of a read (HT-465). tin and resp are the texts of the tool's
// input and output, read once and cut or hashed below. The cuts are the Hook*Max limits of the
// domain and the tail is the last 400 characters plus one, as the colleague's builder cut them; a
// codex command output is hashed without its preamble lines.
func hookSelect() string {
	return `WITH JSONExtractKeysAndValuesRaw(Body) AS body_fields,
  mapFromArrays(arrayMap(f -> f.1, body_fields), arrayMap(f -> f.2, body_fields)) AS body,
  ` + bodyText("tool_input") + ` AS tin_text, ` + bodyText("tool_response") + ` AS resp_text
SELECT Timestamp,
  ResourceAttributes['hottell.user.id'],
  LogAttributes['agent'],
  LogAttributes['hook.event_name'],
  LogAttributes['session.id'],
  LogAttributes['turn_id'],
  LogAttributes['prompt_id'],
  LogAttributes['tool_use_id'],
  ` + bodyString("tool_name") + `,
  substringUTF8(tin_text, 1, ` + strconv.Itoa(telemetry.HookToolInputMax) + `),
  toString(cityHash64(tin_text)),
  substringUTF8(resp_text, 1, ` + strconv.Itoa(telemetry.HookToolResponseHeadMax) + `),
  substringUTF8(resp_text, toUInt64(greatest(1, toInt64(lengthUTF8(resp_text)) - ` +
		strconv.Itoa(telemetry.HookToolResponseTailMax-1) + `)), ` + strconv.Itoa(telemetry.HookToolResponseTailMax) + `),
  toInt64(length(resp_text)),
  toString(cityHash64(replaceRegexpOne(resp_text,
    '^(?:(?:Chunk ID|Wall time|Process exited with code|Original token count)[^\n]*\n)*(?:Output:\n)?', ''))),
  JSONExtractInt(` + bodyField("duration_ms") + `),
  LogAttributes['cwd'],
  LogAttributes['hottell.repo.root'],
  LogAttributes['hottell.repo.remote'],
  ` + bodyString("model") + `,
  ` + bodyString("permission_mode") + `,
  ` + bodyString("agent_type") + `,
  ` + bodyString("agent_id") + `,
  ` + bodyText("mcp_server") + `,
  ` + bodyString("session_title") + `,
  ` + bodyString("source") + `,
  ` + bodyString("reason") + `,
  ` + bodyString("trigger") + `,
  substringUTF8(` + bodyString("prompt") + `, 1, ` + strconv.Itoa(telemetry.HookPromptMax) + `),
  substringUTF8(` + bodyString("last_assistant_message") + `, 1, ` + strconv.Itoa(telemetry.HookLastAssistantMessageMax) + `),
  substringUTF8(` + bodyText("error") + `, 1, ` + strconv.Itoa(telemetry.HookErrorMax) + `)
FROM otel.otel_logs`
}

// HookEvents returns the hook events matching filter, oldest first. The period
// [filter.From, filter.To) is read in windows of telemetry.HookWindow, one query each, so that no
// query holds the tool payloads of the whole period; every query constrains the service and the
// time of its window. One query first finds the windows that hold a matching record, and only
// those are read: a year is 2232 windows, and reading each of them, empty or not, kept the
// dataset of «Всё» past the patience of its requests (HT-433). A record that lands in an empty
// window between the two queries is left to the next read.
func (r *Reader) HookEvents(ctx context.Context, filter telemetry.Filter) ([]telemetry.HookEvent, error) {
	var events []telemetry.HookEvent
	for e, err := range r.HookEventSeq(ctx, filter) {
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, nil
}

// HookEventSeq yields what HookEvents returns, in its order, as each window is read: only one
// window's events are held at a time, so that a caller laying the events out does not hold the
// whole period beside its copy (HT-533). An error is the last pair it yields; a consumer that
// stops early ends the read.
func (r *Reader) HookEventSeq(ctx context.Context, filter telemetry.Filter) iter.Seq2[telemetry.HookEvent, error] {
	return func(yield func(telemetry.HookEvent, error) bool) {
		if filter.From.IsZero() || filter.To.IsZero() || !filter.To.After(filter.From) {
			yield(telemetry.HookEvent{}, telemetry.ErrNoHookPeriod)
			return
		}
		windows := telemetry.Windows(filter.From, filter.To, telemetry.HookWindow)
		filled, err := r.hookWindowsWithRecords(ctx, filter, len(windows))
		if err != nil {
			yield(telemetry.HookEvent{}, err)
			return
		}
		for _, i := range filled {
			got, err := r.hookWindow(ctx, filter, windows[i])
			if err != nil {
				yield(telemetry.HookEvent{}, err)
				return
			}
			for k := range got {
				if !yield(got[k], nil) {
					return
				}
			}
		}
	}
}

// hookWindowsWithRecords returns, in order, the indexes of the windows of telemetry.Windows over
// the filter's period that hold a matching record; n is the number of those windows. A record's
// index is the whole number of telemetry.HookWindow from filter.From to its time.
func (r *Reader) hookWindowsWithRecords(ctx context.Context, filter telemetry.Filter, n int) ([]int, error) {
	const op = "find the windows of hook events"

	where, args := hookWhere(filter, filter.From, filter.To)
	query := "SELECT intDiv(toUnixTimestamp64Nano(Timestamp) - @from, @step) AS w FROM otel.otel_logs " + where + " GROUP BY w ORDER BY w"
	if r.aggregatesReady(ctx) {
		where, args = hookAggregateWhere(filter, filter.From.UnixNano(), filter.To.UnixNano())
		query = "SELECT intDiv(toUnixTimestamp64Nano(c0) - @from, @step) AS w FROM " + hookEventsTable + " " + where + " GROUP BY w ORDER BY w"
	}
	args = append(args, clickhouse.Named("step", telemetry.HookWindow.Nanoseconds()))
	rows, err := r.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, r.unavailable(op, err)
	}
	defer func() { _ = rows.Close() }()

	var filled []int
	for rows.Next() {
		var w int64
		if err := rows.Scan(&w); err != nil {
			return nil, r.unavailable(op, err)
		}
		if w >= 0 && w < int64(n) {
			filled = append(filled, int(w))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, r.unavailable(op, err)
	}
	return filled, nil
}

func (r *Reader) hookWindow(ctx context.Context, filter telemetry.Filter, w telemetry.Window) ([]telemetry.HookEvent, error) {
	const op = "read hook events"

	where, args := hookWhere(filter, w.Start, w.End)
	query := hookSelect() + " " + where
	if r.aggregatesReady(ctx) {
		_, cols := hookColumns()
		where, args = hookAggregateWhere(filter, w.Start.UnixNano(), w.End.UnixNano())
		query = "SELECT " + positional(nil, len(cols)) + " FROM " + hookEventsTable + " " + where
	}
	rows, err := r.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, r.unavailable(op, err)
	}
	defer func() { _ = rows.Close() }()
	events, err := r.scanHookEvents(op, rows)
	if err != nil {
		return nil, err
	}
	// The window is ordered here: ClickHouse computes the projection of a sorted query after the
	// sort, so ORDER BY Timestamp sorted every record's whole Body and doubled the read (HT-493).
	slices.SortStableFunc(events, func(a, b telemetry.HookEvent) int { return a.Time.Compare(b.Time) })
	return events, nil
}

// hookWhere is the WHERE of the hook events of filter in [start, end), and its arguments.
func hookWhere(filter telemetry.Filter, start, end time.Time) (string, []any) {
	var where strings.Builder
	args := []any{
		clickhouse.Named("from", start.UnixNano()),
		clickhouse.Named("to", end.UnixNano()),
	}
	where.WriteString("WHERE ServiceName = '" + hooksService + "'" +
		" AND Timestamp >= fromUnixTimestamp64Nano(@from) AND Timestamp < fromUnixTimestamp64Nano(@to)")
	if filter.UserID != uuid.Nil {
		where.WriteString(" AND ResourceAttributes['hottell.user.id'] = @user")
		args = append(args, clickhouse.Named("user", filter.UserID.String()))
	}
	if filter.Agent != "" {
		where.WriteString(" AND LogAttributes['agent'] = @agent")
		args = append(args, clickhouse.Named("agent", filter.Agent))
	}
	if filter.SessionID != "" {
		where.WriteString(" AND LogAttributes['session.id'] = @session")
		args = append(args, clickhouse.Named("session", filter.SessionID))
	}
	return where.String(), args
}

// scanHookEvents reads the hook events of rows, the columns of hookSelect.
func (r *Reader) scanHookEvents(op string, rows driver.Rows) ([]telemetry.HookEvent, error) {
	var events []telemetry.HookEvent
	for rows.Next() {
		var (
			e    telemetry.HookEvent
			user string
		)
		if err := rows.Scan(
			&e.Time, &user, &e.Agent, &e.Event, &e.SessionID, &e.TurnID, &e.PromptID, &e.ToolUseID, &e.Tool,
			&e.ToolInput, &e.ToolInputHash, &e.ToolResponse, &e.ToolResponseTail, &e.ToolResponseLen, &e.ToolResponseHash,
			&e.DurationMs, &e.Cwd, &e.RepoRoot, &e.RepoRemote, &e.Model, &e.PermissionMode, &e.AgentType, &e.AgentID, &e.McpServer,
			&e.SessionTitle, &e.Source, &e.Reason, &e.Trigger, &e.Prompt, &e.LastAssistantMessage, &e.Error,
		); err != nil {
			return nil, r.unavailable(op, err)
		}
		// A record the server could not attribute has no id, and one with a malformed id is not
		// the person's either: both read as uuid.Nil.
		e.UserID, _ = uuid.Parse(user)
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, r.unavailable(op, err)
	}
	return events, nil
}

// Pulse returns the hook events per telemetry.PulseBarWidth and agent over the telemetry.PulseWindow
// before q.Now, and the sessions active in the telemetry.PulseActiveWindow before q.Now, of the
// person and agent of q when set. A session is the person and the session id together.
func (r *Reader) Pulse(ctx context.Context, q telemetry.PulseQuery) (telemetry.Pulse, error) {
	const op = "read hook pulse"

	now := q.Now
	pulse := telemetry.Pulse{At: now, Bars: []telemetry.PulseBar{}, Active: []telemetry.ActiveSession{}}
	var (
		narrow strings.Builder
		args   []any
	)
	if q.UserID != uuid.Nil {
		narrow.WriteString(" AND ResourceAttributes['hottell.user.id'] = @user")
		args = append(args, clickhouse.Named("user", q.UserID.String()))
	}
	if q.Agent != "" {
		narrow.WriteString(" AND LogAttributes['agent'] = @agent")
		args = append(args, clickhouse.Named("agent", q.Agent))
	}

	bars, err := r.conn.Query(ctx, fmt.Sprintf(`SELECT toStartOfInterval(Timestamp, INTERVAL %d SECOND), LogAttributes['agent'],
  toUInt32(count())
FROM otel.otel_logs
WHERE ServiceName = '%s' AND Timestamp >= fromUnixTimestamp64Nano(@from) AND Timestamp < fromUnixTimestamp64Nano(@to)%s
GROUP BY 1, 2 ORDER BY 1, 2`, int(telemetry.PulseBarWidth/time.Second), hooksService, narrow.String()),
		append([]any{
			clickhouse.Named("from", now.Add(-telemetry.PulseWindow).UnixNano()),
			clickhouse.Named("to", now.UnixNano()),
		}, args...)...)
	if err != nil {
		return telemetry.Pulse{}, r.unavailable(op, err)
	}
	defer func() { _ = bars.Close() }()
	for bars.Next() {
		var (
			b     telemetry.PulseBar
			count uint32
		)
		if err := bars.Scan(&b.Start, &b.Agent, &count); err != nil {
			return telemetry.Pulse{}, r.unavailable(op, err)
		}
		b.Count = int(count)
		pulse.Bars = append(pulse.Bars, b)
	}
	if err := bars.Err(); err != nil {
		return telemetry.Pulse{}, r.unavailable(op, err)
	}

	active, err := r.conn.Query(ctx, fmt.Sprintf(`SELECT ResourceAttributes['hottell.user.id'], LogAttributes['session.id'],
  argMax(LogAttributes['agent'], Timestamp),
  argMinIf(LogAttributes['cwd'], Timestamp, LogAttributes['cwd'] != ''), max(Timestamp),
  toUInt32(countIf(Timestamp >= fromUnixTimestamp64Nano(@minute)))
FROM otel.otel_logs
WHERE ServiceName = '%s' AND Timestamp >= fromUnixTimestamp64Nano(@from) AND Timestamp < fromUnixTimestamp64Nano(@to)
  AND LogAttributes['session.id'] != ''%s
GROUP BY 1, 2 ORDER BY 5 DESC, 2, 1 LIMIT %d`, hooksService, narrow.String(), telemetry.PulseActiveLimit),
		append([]any{
			clickhouse.Named("from", now.Add(-telemetry.PulseActiveWindow).UnixNano()),
			clickhouse.Named("minute", now.Add(-time.Minute).UnixNano()),
			clickhouse.Named("to", now.UnixNano()),
		}, args...)...)
	if err != nil {
		return telemetry.Pulse{}, r.unavailable(op, err)
	}
	defer func() { _ = active.Close() }()
	for active.Next() {
		var (
			s      telemetry.ActiveSession
			user   string
			perMin uint32
		)
		if err := active.Scan(&user, &s.SessionID, &s.Agent, &s.Cwd, &s.LastAt, &perMin); err != nil {
			return telemetry.Pulse{}, r.unavailable(op, err)
		}
		s.UserID, _ = uuid.Parse(user)
		s.PerMinute = int(perMin)
		pulse.Active = append(pulse.Active, s)
	}
	if err := active.Err(); err != nil {
		return telemetry.Pulse{}, r.unavailable(op, err)
	}
	return pulse, nil
}
