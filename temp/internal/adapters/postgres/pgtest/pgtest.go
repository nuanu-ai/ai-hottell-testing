// Package pgtest runs a PostgreSQL 17 container for the tests of database adapters.
// It needs a running Docker daemon.
package pgtest

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	migrations "git.alva.dev/alva/harness-telemetry/database/migrations/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
)

const image = "postgres:17"

// DB is a migrated PostgreSQL database shared by the tests of one package.
type DB struct {
	pool      *pgxpool.Pool
	container *tcpostgres.PostgresContainer
}

// Run starts one database for the test package, hands it to use, runs the tests and removes
// the container. Call it from TestMain: os.Exit(pgtest.Run(m, func(db *pgtest.DB) { ... })).
func Run(m *testing.M, use func(*DB)) int {
	ctx := context.Background()
	db, err := Start(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgtest: %v\n", err)
		return 1
	}
	defer func() {
		if err := db.Close(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "pgtest: %v\n", err)
		}
	}()

	use(db)
	return m.Run()
}

// Start runs a PostgreSQL container and applies every embedded migration to it.
// The caller closes the returned DB.
func Start(ctx context.Context) (*DB, error) {
	container, err := tcpostgres.Run(ctx, image,
		tcpostgres.WithDatabase("ht"),
		tcpostgres.WithUsername("ht"),
		tcpostgres.WithPassword("ht"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		if container != nil {
			_ = testcontainers.TerminateContainer(container)
		}
		return nil, fmt.Errorf("start postgres container: %w", err)
	}
	db := &DB{container: container}

	if err := db.open(ctx); err != nil {
		_ = db.Close(ctx)
		return nil, err
	}
	return db, nil
}

func (db *DB) open(ctx context.Context) error {
	url, err := db.container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return fmt.Errorf("read connection string: %w", err)
	}
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		return err
	}
	db.pool = pool

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	migrator, err := postgres.NewMigrator(sqlDB, migrations.FS)
	if err != nil {
		return err
	}
	if _, err := migrator.Up(ctx); err != nil {
		return err
	}
	return nil
}

// Pool returns the connection pool of the database.
func (db *DB) Pool() *pgxpool.Pool {
	return db.pool
}

// Truncate empties tables and resets their sequences, so a test starts from a clean state.
func (db *DB) Truncate(tb testing.TB, tables ...string) {
	tb.Helper()

	if len(tables) == 0 {
		return
	}
	names := make([]string, len(tables))
	for i, table := range tables {
		names[i] = pgx.Identifier{table}.Sanitize()
	}
	query := "TRUNCATE TABLE " + strings.Join(names, ", ") + " RESTART IDENTITY CASCADE"
	if _, err := db.pool.Exec(tb.Context(), query); err != nil {
		tb.Fatalf("truncate %v: %v", tables, err)
	}
}

// Close closes the pool and removes the container.
func (db *DB) Close(ctx context.Context) error {
	if db.pool != nil {
		db.pool.Close()
	}
	if err := db.container.Terminate(ctx); err != nil {
		return fmt.Errorf("terminate postgres container: %w", err)
	}
	return nil
}
