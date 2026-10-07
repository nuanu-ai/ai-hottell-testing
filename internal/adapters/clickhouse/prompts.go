package clickhouse

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// SessionPrompts returns, in one read, the UserPromptSubmit events of the sessions stored in
// [from, to), oldest first. Only the prompt is read of each, cut to telemetry.HookPromptMax as
// hookSelect cuts it, with its time, person, agent and session: the pulse asks for the first
// prompts of its active sessions and needs nothing else of their hooks (HT-477). A session is
// its person, agent and session id together; nothing is read without sessions.
func (r *Reader) SessionPrompts(
	ctx context.Context, sessions []telemetry.ActiveSession, from, to time.Time,
) ([]telemetry.HookEvent, error) {
	const op = "read session prompts"

	if len(sessions) == 0 {
		return nil, nil
	}
	type key struct {
		user           uuid.UUID
		agent, session string
	}
	want := make(map[key]bool, len(sessions))
	ids := make([]string, 0, len(sessions))
	for _, s := range sessions {
		want[key{s.UserID, s.Agent, s.SessionID}] = true
		ids = append(ids, s.SessionID)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)

	rows, err := r.conn.Query(ctx, `SELECT Timestamp, ResourceAttributes['hottell.user.id'], LogAttributes['agent'],
  LogAttributes['session.id'],
  substringUTF8(JSONExtractString(Body, 'prompt'), 1, `+strconv.Itoa(telemetry.HookPromptMax)+`)
FROM otel.otel_logs
WHERE ServiceName = '`+hooksService+`' AND LogAttributes['hook.event_name'] = 'UserPromptSubmit'
  AND Timestamp >= fromUnixTimestamp64Nano(@from) AND Timestamp < fromUnixTimestamp64Nano(@to)
  AND LogAttributes['session.id'] IN @sessions
ORDER BY Timestamp`,
		clickhouse.Named("from", from.UnixNano()), clickhouse.Named("to", to.UnixNano()),
		clickhouse.Named("sessions", ids))
	if err != nil {
		return nil, r.unavailable(op, err)
	}
	defer func() { _ = rows.Close() }()
	var events []telemetry.HookEvent
	for rows.Next() {
		var (
			e    telemetry.HookEvent
			user string
		)
		if err := rows.Scan(&e.Time, &user, &e.Agent, &e.SessionID, &e.Prompt); err != nil {
			return nil, r.unavailable(op, err)
		}
		e.UserID = parseUser(user)
		if !want[key{e.UserID, e.Agent, e.SessionID}] {
			continue
		}
		e.Event = "UserPromptSubmit"
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, r.unavailable(op, err)
	}
	return events, nil
}
