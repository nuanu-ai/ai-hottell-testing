package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

// ErrNothingToRollBack is returned by Migrator.Down when no migration is applied.
var ErrNothingToRollBack = errors.New("no migration to roll back")

// Migrator applies the goose migrations of a filesystem to a database.
type Migrator struct {
	db *sql.DB
	// provider is nil when the filesystem holds no migrations.
	provider *goose.Provider
}

// NewMigrator returns a Migrator for the migrations in fsys.
func NewMigrator(db *sql.DB, fsys fs.FS) (*Migrator, error) {
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys)
	if errors.Is(err, goose.ErrNoMigrations) {
		return &Migrator{db: db}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("create migration provider: %w", err)
	}
	return &Migrator{db: db, provider: provider}, nil
}

// Up applies every pending migration and returns the ones it applied.
func (m *Migrator) Up(ctx context.Context) ([]*goose.MigrationResult, error) {
	if m.provider == nil {
		return nil, m.ensureVersionTable(ctx)
	}
	results, err := m.provider.Up(ctx)
	if err != nil {
		return nil, fmt.Errorf("apply migrations: %w", err)
	}
	return results, nil
}

// Down rolls back the latest applied migration.
func (m *Migrator) Down(ctx context.Context) (*goose.MigrationResult, error) {
	if m.provider == nil {
		return nil, ErrNothingToRollBack
	}
	result, err := m.provider.Down(ctx)
	if errors.Is(err, goose.ErrNoNextVersion) {
		return nil, ErrNothingToRollBack
	}
	if err != nil {
		return nil, fmt.Errorf("roll back migration: %w", err)
	}
	return result, nil
}

// Status reports every known migration and whether it is applied.
func (m *Migrator) Status(ctx context.Context) ([]*goose.MigrationStatus, error) {
	if m.provider == nil {
		return nil, m.ensureVersionTable(ctx)
	}
	statuses, err := m.provider.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("read migration status: %w", err)
	}
	return statuses, nil
}

// ensureVersionTable creates the goose version table when no migration exists to create it.
func (m *Migrator) ensureVersionTable(ctx context.Context) error {
	if _, err := goose.EnsureDBVersionContext(ctx, m.db); err != nil {
		return fmt.Errorf("create version table: %w", err)
	}
	return nil
}
