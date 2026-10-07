package clickhouse

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"
)

// ActivityPulse returns the hook events of each person by hour over [from, to), in one
// aggregating query (HT-541): a person's slice has one count per hour from from, the empty hours
// 0. The hours are counted from from, which the caller sets at the start of an hour; the records
// of no person are left out. It reads the hook events table when the aggregates are on.
func (r *Reader) ActivityPulse(ctx context.Context, from, to time.Time) (map[uuid.UUID][]int, error) {
	const op = "read activity pulse"

	query := "SELECT ResourceAttributes['hottell.user.id'] AS u, intDiv(toUnixTimestamp64Nano(Timestamp) - @from, @step) AS h, " +
		"toUInt32(count()) FROM otel.otel_logs WHERE ServiceName = '" + hooksService + "'" +
		" AND Timestamp >= fromUnixTimestamp64Nano(@from) AND Timestamp < fromUnixTimestamp64Nano(@to) GROUP BY u, h"
	if r.aggregatesReady(ctx) {
		query = "SELECT c1 AS u, intDiv(toUnixTimestamp64Nano(c0) - @from, @step) AS h, toUInt32(count()) FROM " +
			hookEventsTable + " WHERE c0 >= fromUnixTimestamp64Nano(@from) AND c0 < fromUnixTimestamp64Nano(@to) GROUP BY u, h"
	}
	rows, err := r.conn.Query(ctx, query,
		clickhouse.Named("from", from.UnixNano()), clickhouse.Named("to", to.UnixNano()),
		clickhouse.Named("step", time.Hour.Nanoseconds()))
	if err != nil {
		return nil, r.unavailable(op, err)
	}
	defer func() { _ = rows.Close() }()

	hours := int(to.Sub(from) / time.Hour)
	out := map[uuid.UUID][]int{}
	for rows.Next() {
		var (
			user  string
			hour  int64
			count uint32
		)
		if err := rows.Scan(&user, &hour, &count); err != nil {
			return nil, r.unavailable(op, err)
		}
		id, err := uuid.Parse(user)
		if err != nil || id == uuid.Nil || hour < 0 || hour >= int64(hours) {
			continue
		}
		if out[id] == nil {
			out[id] = make([]int, hours)
		}
		out[id][hour] += int(count)
	}
	if err := rows.Err(); err != nil {
		return nil, r.unavailable(op, err)
	}
	return out, nil
}
