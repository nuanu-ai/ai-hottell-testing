package clickhouse

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// Aggregates of the transcripts (HT-536). The period reads of the Codex facts and the Claude
// usage parsed the JSON of every transcript record of the period on each read, and ClickHouse
// refused the 365-day read on the prod volume with code 241. The materialized views below take
// the same fields once, as the records are inserted, into small tables, and the period reads
// read those. Only tables and views are added: otel_logs is never changed.

// aggregatesReadyTable holds one row once the tables are filled with the records stored before
// their views began: until then the period reads read otel_logs as before.
const aggregatesReadyTable = "otel.ht_aggregates_ready"

// aggregateDDL creates the tables, the views and the ready table, each only when it is not there.
func aggregateDDL() []string {
	codexLines := "SELECT " + userExpr + " AS uid, LogAttributes['session.id'] AS sid, " + fileExpr + " AS file, " +
		lineExpr + " AS line, " + kindExpr + " AS kind, " + codexFactBody() + " AS body, Timestamp AS at " +
		"FROM otel.otel_logs WHERE ServiceName = '" + transcriptsService + "' AND LogAttributes['agent'] = 'codex' AND " +
		wellFormed + " AND " + codexFactRecords
	codexFiles := "SELECT " + userExpr + " AS uid, LogAttributes['session.id'] AS sid, " + fileExpr + " AS file, " +
		"toStartOfMinute(Timestamp) AS minute, argMinState(" + kindExpr + ", (" + lineExpr + ", Timestamp)) AS kind, " +
		"minState(Timestamp) AS at, maxState(" + lineExpr + ") AS maxLine " +
		"FROM otel.otel_logs WHERE ServiceName = '" + transcriptsService + "' AND LogAttributes['agent'] = 'codex' AND " +
		wellFormed + " GROUP BY uid, sid, file, minute"
	claudeUsage := "SELECT " + userExpr + " AS uid, LogAttributes['session.id'] AS sid, " + fileExpr + " AS file, " +
		lineExpr + " AS line, " + kindExpr + " AS kind, " + claudeUsageBody() + " AS body, Timestamp AS at " +
		"FROM otel.otel_logs WHERE ServiceName = '" + transcriptsService + "' AND LogAttributes['agent'] = 'claude' AND " +
		wellFormed + " AND " + claudeUsageRecords
	lineTable := "(uid String, sid String, file String, line UInt64, kind String, body String, at DateTime64(9)) " +
		"ENGINE = MergeTree PARTITION BY toYYYYMM(at) ORDER BY (at, uid, sid, file, line)"
	return []string{
		"CREATE TABLE IF NOT EXISTS otel.ht_codex_fact_lines " + lineTable,
		"CREATE TABLE IF NOT EXISTS otel.ht_codex_files (uid String, sid String, file String, minute DateTime, " +
			"kind AggregateFunction(argMin, String, Tuple(UInt64, DateTime64(9))), " +
			"at AggregateFunction(min, DateTime64(9)), maxLine AggregateFunction(max, UInt64)) " +
			"ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(minute) ORDER BY (minute, uid, sid, file)",
		"CREATE TABLE IF NOT EXISTS otel.ht_claude_usage_lines " + lineTable,
		"CREATE TABLE IF NOT EXISTS " + aggregatesReadyTable + " (at DateTime) ENGINE = MergeTree ORDER BY at",
		"CREATE MATERIALIZED VIEW IF NOT EXISTS otel.ht_codex_fact_lines_mv TO otel.ht_codex_fact_lines AS " + codexLines,
		"CREATE MATERIALIZED VIEW IF NOT EXISTS otel.ht_codex_files_mv TO otel.ht_codex_files AS " + codexFiles,
		"CREATE MATERIALIZED VIEW IF NOT EXISTS otel.ht_claude_usage_lines_mv TO otel.ht_claude_usage_lines AS " + claudeUsage,
	}
	// The backfill reuses the views' SELECTs: backfill below.
}

// backfill fills the tables with the records stored before the views began, one month at a
// time so that no single insert holds the whole history. A record inserted while it runs can
// land twice: the reads drop the repeats of a line (firstOfEachLine), and the file aggregates
// are min, max and argMin, which a repeat does not change.
func backfill(ctx context.Context, conn clickhouse.Conn, from, to time.Time) error {
	ddl := aggregateDDL()
	selects := map[string]string{
		"otel.ht_codex_fact_lines":   viewSelect(ddl[4]),
		"otel.ht_codex_files":        viewSelect(ddl[5]),
		"otel.ht_claude_usage_lines": viewSelect(ddl[6]),
	}
	for m := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC); m.Before(to); m = m.AddDate(0, 1, 0) {
		next := m.AddDate(0, 1, 0)
		for table, sel := range selects {
			q := "INSERT INTO " + table + " " + withPeriod(sel, m, next)
			// Small blocks on one thread: a block holds the records' whole Body. A refusal for
			// memory is tried again after a pause.
			settings := clickhouse.Settings{
				"max_threads": 1, "max_insert_threads": 1, "max_block_size": 256,
				"min_insert_block_size_rows": 4096, "min_insert_block_size_bytes": 16 << 20,
			}
			var err error
			for attempt := 0; attempt < 10; attempt++ {
				if err = conn.Exec(clickhouse.Context(ctx, clickhouse.WithSettings(settings)), q); err == nil {
					break
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(10 * time.Second):
				}
			}
			if err != nil {
				return fmt.Errorf("backfill %s %s: %w", table, m.Format("2006-01"), err)
			}
		}
	}
	return nil
}

// viewSelect is the SELECT of a CREATE MATERIALIZED VIEW … AS statement.
func viewSelect(ddl string) string {
	const as = " AS SELECT "
	for i := 0; i+len(as) <= len(ddl); i++ {
		if ddl[i:i+len(as)] == as {
			return ddl[i+4:]
		}
	}
	return ""
}

// withPeriod adds a Timestamp range to the WHERE of sel, before its GROUP BY when it has one.
func withPeriod(sel string, from, to time.Time) string {
	cond := fmt.Sprintf(" AND Timestamp >= toDateTime64('%s', 9) AND Timestamp < toDateTime64('%s', 9)",
		from.UTC().Format("2006-01-02 15:04:05"), to.UTC().Format("2006-01-02 15:04:05"))
	const group = " GROUP BY "
	for i := 0; i+len(group) <= len(sel); i++ {
		if sel[i:i+len(group)] == group {
			return sel[:i] + cond + sel[i:]
		}
	}
	return sel + cond
}

// EnsureAggregates creates the aggregate tables and views over the admin URL adminURL and, once,
// fills them with the history and marks them ready. It runs in the background of the service's
// start and logs its end; a failure leaves the period reads on otel_logs.
func EnsureAggregates(ctx context.Context, adminURL string, logger *slog.Logger) {
	opts, err := clickhouse.ParseDSN(adminURL)
	if err != nil {
		logger.ErrorContext(ctx, "clickhouse aggregates: parse the admin url")
		return
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		logger.ErrorContext(ctx, "clickhouse aggregates: open")
		return
	}
	defer func() { _ = conn.Close() }()
	for attempt := 0; ; attempt++ {
		if err = conn.Ping(ctx); err == nil {
			break
		}
		if attempt > 60 {
			logger.ErrorContext(ctx, "clickhouse aggregates: clickhouse does not answer")
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
	for _, q := range append(aggregateDDL(), eventAggregateDDL()...) {
		if err := conn.Exec(ctx, q); err != nil {
			logger.ErrorContext(ctx, "clickhouse aggregates: create", "error", err.Error())
			return
		}
	}
	var ready uint64
	if err := conn.QueryRow(ctx, "SELECT count() FROM "+aggregatesReadyTable).Scan(&ready); err != nil {
		logger.ErrorContext(ctx, "clickhouse aggregates: read ready", "error", err.Error())
		return
	}
	if ready > 0 {
		logger.InfoContext(ctx, "clickhouse aggregates: ready")
		return
	}
	var first time.Time
	if err := conn.QueryRow(ctx, "SELECT min(Timestamp) FROM otel.otel_logs WHERE ServiceName = ?",
		transcriptsService).Scan(&first); err != nil {
		logger.ErrorContext(ctx, "clickhouse aggregates: read the first record", "error", err.Error())
		return
	}
	start := time.Now()
	// Up to a minute past now, so that the records inserted while the views began are in.
	if err := backfill(ctx, conn, first, time.Now().Add(time.Minute)); err != nil {
		logger.ErrorContext(ctx, "clickhouse aggregates: backfill", "error", err.Error())
		return
	}
	if err := conn.Exec(ctx, "INSERT INTO "+aggregatesReadyTable+" VALUES (now())"); err != nil {
		logger.ErrorContext(ctx, "clickhouse aggregates: mark ready", "error", err.Error())
		return
	}
	logger.InfoContext(ctx, "clickhouse aggregates: filled", "duration", time.Since(start).String())
}

// WithAggregates makes the period reads of the Codex facts and the Claude usage read the
// aggregate tables (HT-536); without it they read otel_logs.
func WithAggregates(on bool) Option {
	return func(r *Reader) { r.useAggregates = on }
}

// aggregatesReady reports whether the period reads read the aggregate tables.
func (r *Reader) aggregatesReady(context.Context) bool {
	return r.useAggregates
}
