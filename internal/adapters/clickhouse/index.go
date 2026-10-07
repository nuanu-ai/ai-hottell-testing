package clickhouse

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// SessionIndex is the bloom filter over mapValues(LogAttributes) of otel.otel_logs that keeps the
// reads of one session, which take no period, from scanning the service's history (HT-372). The
// collector creates it with the table (create_schema: true), not this repository.
const SessionIndex = "idx_log_attr_value"

// Error codes ClickHouse answers SHOW CREATE TABLE of a table that does not exist with.
const (
	codeUnknownTable              = 60
	codeCannotGetCreateTableQuery = 390
)

// HasSessionIndex tells whether otel.otel_logs carries SessionIndex; false when the table has no
// such index or does not exist. telemetry.ErrUnavailable when ClickHouse cannot answer.
func (r *Reader) HasSessionIndex(ctx context.Context) (bool, error) {
	return r.hasIndex(ctx, "otel_logs", SessionIndex)
}

// hasIndex tells whether the table of the database otel carries the data skipping index name. It
// reads the table's CREATE statement, which the reader's SELECT on otel may show, where
// system.data_skipping_indices needs a grant of its own. table and name are identifiers of the
// code, never input.
func (r *Reader) hasIndex(ctx context.Context, table, name string) (bool, error) {
	const op = "read table schema"

	var stmt string
	if err := r.conn.QueryRow(ctx, "SHOW CREATE TABLE otel.`"+strings.ReplaceAll(table, "`", "")+"`").Scan(&stmt); err != nil {
		exc, ok := errors.AsType[*clickhouse.Exception](err)
		if ok && (exc.Code == codeUnknownTable || exc.Code == codeCannotGetCreateTableQuery) {
			return false, nil
		}
		return false, r.unavailable(op, err)
	}
	return regexp.MustCompile(`\bINDEX\s+` + regexp.QuoteMeta(name) + `\s`).MatchString(stmt), nil
}
