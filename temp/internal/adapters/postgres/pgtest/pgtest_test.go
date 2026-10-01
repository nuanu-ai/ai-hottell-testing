package pgtest_test

import (
	"os"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres/pgtest"
)

// testDB is the package's one database, set once by TestMain before any test runs;
// sharing it is the point of one container per test package.
var testDB *pgtest.DB //nolint:gochecknoglobals // set once by TestMain, read by the tests

func TestMain(m *testing.M) {
	os.Exit(pgtest.Run(m, func(db *pgtest.DB) { testDB = db }))
}

func TestPoolPings(t *testing.T) {
	t.Parallel()

	if err := testDB.Pool().Ping(t.Context()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}

func TestMigrationsApplied(t *testing.T) {
	t.Parallel()

	var exists bool
	err := testDB.Pool().QueryRow(t.Context(),
		"SELECT to_regclass('goose_db_version') IS NOT NULL").Scan(&exists)
	if err != nil {
		t.Fatalf("query version table: %v", err)
	}
	if !exists {
		t.Fatal("goose_db_version does not exist, want it created by migrating the database")
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()

	pool := testDB.Pool()
	if _, err := pool.Exec(t.Context(),
		"CREATE TABLE truncate_probe (id int PRIMARY KEY); INSERT INTO truncate_probe VALUES (1)"); err != nil {
		t.Fatalf("create probe table: %v", err)
	}

	testDB.Truncate(t, "truncate_probe")

	var n int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM truncate_probe").Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if n != 0 {
		t.Fatalf("rows after Truncate = %d, want 0", n)
	}
}
