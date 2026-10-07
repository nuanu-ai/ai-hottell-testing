package clickhouse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// skillsService is the service.name of the records that hold skill snapshots.
const skillsService = "hottell-skills"

var errSkillSnapshotNotJSON = errors.New("read skill snapshot: the stored body is not a snapshot")

// Sessions returns the sessions with hook events or transcript lines in the period of filter,
// one per person, agent and session id, the latest activity first. One query reads both
// services with UNION ALL; filter's person, agent and session narrow both. Records without a
// session id are left out.
func (r *Reader) Sessions(ctx context.Context, filter telemetry.Filter) ([]telemetry.SessionSummary, error) {
	const op = "read sessions"
	if filter.From.IsZero() || filter.To.IsZero() || !filter.To.After(filter.From) {
		return nil, telemetry.ErrNoSessionPeriod
	}

	var where strings.Builder
	args := []any{
		clickhouse.Named("from", filter.From.UnixNano()),
		clickhouse.Named("to", filter.To.UnixNano()),
	}
	where.WriteString("Timestamp >= fromUnixTimestamp64Nano(@from) AND Timestamp < fromUnixTimestamp64Nano(@to)" +
		" AND LogAttributes['session.id'] != ''")
	if filter.UserID != uuid.Nil {
		where.WriteString(" AND " + userExpr + " = @user")
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
	part := func(service, cwd string, hook int) string {
		return "SELECT " + userExpr + " AS user, LogAttributes['agent'] AS agent, LogAttributes['session.id'] AS session, " +
			cwd + " AS cwd, Timestamp AS at, toUInt8(" + fmt.Sprint(hook) + ") AS hook " +
			"FROM otel.otel_logs WHERE ServiceName = '" + service + "' AND " + where.String()
	}
	query := "SELECT user, agent, session, argMinIf(cwd, at, cwd != ''), min(at), max(at), max(hook), min(hook) FROM (" +
		part(hooksService, "LogAttributes['cwd']", 1) + " UNION ALL " + part(transcriptsService, "''", 0) +
		") GROUP BY user, agent, session ORDER BY max(at) DESC, session, user"

	rows, err := r.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, r.unavailable(op, err)
	}
	defer func() { _ = rows.Close() }()

	var sessions []telemetry.SessionSummary
	for rows.Next() {
		var (
			s                telemetry.SessionSummary
			user             string
			maxHook, minHook uint8
		)
		if err := rows.Scan(&user, &s.Agent, &s.SessionID, &s.Cwd, &s.FirstAt, &s.LastAt, &maxHook, &minHook); err != nil {
			return nil, r.unavailable(op, err)
		}
		// A record the server could not attribute has no id, and one with a malformed id is not
		// the person's either: both read as uuid.Nil.
		s.UserID, _ = uuid.Parse(user)
		s.HasHooks, s.HasTranscript = maxHook == 1, minHook == 0
		sessions = append(sessions, s)
	}
	if err := rows.Err(); err != nil {
		return nil, r.unavailable(op, err)
	}
	return sessions, nil
}

// SkillSnapshot returns the latest skill snapshot by time of the session sessionID of the person
// userID and agent; false when the store holds none. A snapshot has no repeat key, so a repeat
// after a failed send is stored twice and reads the same. No period bounds the read: the bloom
// filter idx_log_attr_value the collector creates over mapValues(LogAttributes) serves
// LogAttributes['session.id'] = s, so the read skips the granules of other sessions, as it does
// for the reads of transcript lines and files.
func (r *Reader) SkillSnapshot(
	ctx context.Context, userID uuid.UUID, agent, sessionID string,
) (telemetry.SkillSnapshot, bool, error) {
	const op = "read skill snapshot"

	rows, err := r.conn.Query(ctx, "SELECT Body FROM otel.otel_logs "+
		"WHERE ServiceName = '"+skillsService+"' AND "+userExpr+" = @user "+
		"AND LogAttributes['agent'] = @agent AND LogAttributes['session.id'] = @session "+
		"ORDER BY Timestamp DESC LIMIT 1",
		clickhouse.Named("user", userID.String()),
		clickhouse.Named("agent", agent),
		clickhouse.Named("session", sessionID))
	if err != nil {
		return telemetry.SkillSnapshot{}, false, r.unavailable(op, err)
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return telemetry.SkillSnapshot{}, false, r.unavailable(op, err)
		}
		return telemetry.SkillSnapshot{}, false, nil
	}
	var body string
	if err := rows.Scan(&body); err != nil {
		return telemetry.SkillSnapshot{}, false, r.unavailable(op, err)
	}
	snapshot, err := decodeSnapshot(body)
	if err != nil {
		return telemetry.SkillSnapshot{}, false, err
	}
	return snapshot, true, nil
}

// SkillSnapshotsInPeriod returns, in one read, the latest skill snapshot of every session of a
// person and agent with a snapshot stored in [f.From, f.To), ordered by person, agent and
// session: the read SkillSnapshot makes per session, for the sessions of a whole dataset at once
// (HT-461). f.UserID and f.Agent narrow it when set; f.SessionID is not read. A record without a
// session id or a person's id is left out, as SkillSnapshot never matches it. A damaged snapshot
// does not fail the read: its entry carries the error SkillSnapshot answers, for the caller to
// fail only the session it builds. telemetry.ErrNoSessionPeriod without a period.
func (r *Reader) SkillSnapshotsInPeriod(
	ctx context.Context, f telemetry.Filter,
) ([]telemetry.SessionSkillSnapshot, error) {
	if f.From.IsZero() || f.To.IsZero() || !f.To.After(f.From) {
		return nil, telemetry.ErrNoSessionPeriod
	}
	where := " AND Timestamp >= fromUnixTimestamp64Nano(@from) AND Timestamp < fromUnixTimestamp64Nano(@to)"
	args := []any{clickhouse.Named("from", f.From.UnixNano()), clickhouse.Named("to", f.To.UnixNano())}
	if f.UserID != uuid.Nil {
		where += " AND uid = @user"
		args = append(args, clickhouse.Named("user", f.UserID.String()))
	}
	if f.Agent != "" {
		where += " AND agent = @agent"
		args = append(args, clickhouse.Named("agent", f.Agent))
	}
	return r.skillSnapshots(ctx, "read skill snapshots of the period", where, args)
}

// SkillSnapshotsOfSessions returns, in one read and with no period, the latest skill snapshot of
// every person and agent of the sessions sessionIDs, as SkillSnapshotsInPeriod returns them: the
// read SkillSnapshot makes per session, for the sessions a period read cannot settle (HT-461). As
// in SkillSnapshot, the bloom filter over mapValues(LogAttributes) serves the session ids, so the
// read skips the granules of other sessions. No ids read nothing.
func (r *Reader) SkillSnapshotsOfSessions(
	ctx context.Context, sessionIDs []string,
) ([]telemetry.SessionSkillSnapshot, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	ids := make([]any, len(sessionIDs))
	for i, id := range sessionIDs {
		ids[i] = id
	}
	return r.skillSnapshots(ctx, "read skill snapshots of sessions", " AND LogAttributes['session.id'] IN @sessions",
		[]any{clickhouse.Named("sessions", clickhouse.GroupSet{Value: ids})})
}

// skillSnapshots reads the latest skill snapshot per person, agent and session of the
// hottell-skills records that also match where, a condition over uid, agent, sid and the
// record's columns starting with AND.
func (r *Reader) skillSnapshots(
	ctx context.Context, op, where string, args []any,
) ([]telemetry.SessionSkillSnapshot, error) {
	rows, err := r.conn.Query(ctx, "SELECT "+userExpr+" AS uid, LogAttributes['agent'] AS agent, "+
		"LogAttributes['session.id'] AS sid, argMax(Body, Timestamp) FROM otel.otel_logs "+
		"WHERE ServiceName = '"+skillsService+"' AND sid != '' AND uid != ''"+where+
		" GROUP BY uid, agent, sid ORDER BY uid, agent, sid", args...)
	if err != nil {
		return nil, r.unavailable(op, err)
	}
	defer func() { _ = rows.Close() }()

	var out []telemetry.SessionSkillSnapshot
	for rows.Next() {
		var (
			s          telemetry.SessionSkillSnapshot
			user, body string
		)
		if err := rows.Scan(&user, &s.Agent, &s.SessionID, &body); err != nil {
			return nil, r.unavailable(op, err)
		}
		// A malformed id is no person's, as the per-session read never matches it.
		if s.UserID, err = uuid.Parse(user); err != nil {
			continue
		}
		s.Snapshot, s.Err = decodeSnapshot(body)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, r.unavailable(op, err)
	}
	return out, nil
}

// decodeSnapshot decodes a stored skill snapshot; the error names where it fails without the
// stored text.
func decodeSnapshot(body string) (telemetry.SkillSnapshot, error) {
	var snapshot telemetry.SkillSnapshot
	if err := json.Unmarshal([]byte(body), &snapshot); err != nil {
		return telemetry.SkillSnapshot{}, fmt.Errorf("%w: %s", errSkillSnapshotNotJSON, decodeCause(err))
	}
	return snapshot, nil
}

// decodeCause says where a stored snapshot fails to decode without the stored text: the offset
// of a syntax error, the field and the type of a value of the wrong type, the field of a time
// that does not parse. The decoder's own text of a time error carries the value, so it is not
// used.
func decodeCause(err error) string {
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return fmt.Sprintf("invalid JSON at offset %d", syntax.Offset)
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return fmt.Sprintf("field %s is not a %s", typeErr.Field, typeErr.Type)
	}
	var timeErr *time.ParseError
	if errors.As(err, &timeErr) {
		return "a time (captured_at or modified_at) does not parse"
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return "invalid JSON at offset end"
	}
	return "the body does not decode"
}
