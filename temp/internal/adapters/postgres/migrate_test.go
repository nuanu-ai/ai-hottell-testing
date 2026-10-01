package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	migrations "git.alva.dev/alva/harness-telemetry/database/migrations/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres/pgtest"
)

// testDB is the package's one database, set once by TestMain before any test runs;
// sharing it is the point of one container per test package.
var testDB *pgtest.DB //nolint:gochecknoglobals // set once by TestMain, read by the tests

func TestMain(m *testing.M) {
	os.Exit(pgtest.Run(m, func(db *pgtest.DB) { testDB = db }))
}

func TestMigratorEmbeddedMigrations(t *testing.T) {
	t.Parallel()

	sqlDB := stdlib.OpenDBFromPool(testDB.Pool())
	t.Cleanup(func() { _ = sqlDB.Close() })
	migrator, err := postgres.NewMigrator(sqlDB, migrations.FS)
	if err != nil {
		t.Fatalf("NewMigrator() error = %v", err)
	}

	// pgtest has already applied everything, so a repeated Up applies nothing.
	applied, err := migrator.Up(t.Context())
	if err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("Up() applied %d migrations on a migrated database, want 0", len(applied))
	}

	statuses, err := migrator.Status(t.Context())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	for _, s := range statuses {
		if s.State != goose.StateApplied {
			t.Errorf("migration %d state = %q, want %q", s.Source.Version, s.State, goose.StateApplied)
		}
	}
}

func TestMigratorDownWithoutMigrations(t *testing.T) {
	t.Parallel()

	sqlDB := stdlib.OpenDBFromPool(testDB.Pool())
	t.Cleanup(func() { _ = sqlDB.Close() })
	migrator, err := postgres.NewMigrator(sqlDB, os.DirFS(t.TempDir()))
	if err != nil {
		t.Fatalf("NewMigrator() error = %v", err)
	}

	if _, err := migrator.Down(t.Context()); !errors.Is(err, postgres.ErrNothingToRollBack) {
		t.Fatalf("Down() error = %v, want %v", err, postgres.ErrNothingToRollBack)
	}
}

func TestMigrationsRollBackToZeroAndReapply(t *testing.T) {
	t.Parallel()

	// A database of its own: rolling back to zero would break the tests sharing testDB.
	db, err := pgtest.Start(t.Context())
	if err != nil {
		t.Fatalf("pgtest.Start() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.WithoutCancel(t.Context())) })

	sqlDB := stdlib.OpenDBFromPool(db.Pool())
	t.Cleanup(func() { _ = sqlDB.Close() })
	migrator, err := postgres.NewMigrator(sqlDB, migrations.FS)
	if err != nil {
		t.Fatalf("NewMigrator() error = %v", err)
	}
	tables := []string{"users", "sessions", "user_tokens", "passkeys", "webauthn_ceremonies", "access_keys", "telemetry_settings"}

	assertTables(t, db, tables, true)

	for {
		_, err := migrator.Down(t.Context())
		if errors.Is(err, postgres.ErrNothingToRollBack) {
			break
		}
		if err != nil {
			t.Fatalf("Down() error = %v", err)
		}
	}
	assertTables(t, db, tables, false)

	if _, err := migrator.Up(t.Context()); err != nil {
		t.Fatalf("Up() after rolling back to zero error = %v", err)
	}
	assertTables(t, db, tables, true)
}

func assertTables(t *testing.T, db *pgtest.DB, tables []string, want bool) {
	t.Helper()

	for _, table := range tables {
		var exists bool
		err := db.Pool().QueryRow(t.Context(), "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists)
		if err != nil {
			t.Fatalf("query table %s: %v", table, err)
		}
		if exists != want {
			t.Errorf("table %s exists = %t, want %t", table, exists, want)
		}
	}
}

func TestMigrationsCheckConstraints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		insert     string
		constraint string
	}{
		{
			name: "empty passkey name",
			insert: `INSERT INTO passkeys (id, user_id, credential_id, public_key, attestation_type, aaguid,
				sign_count, transports, backup_eligible, backup_state, name)
				VALUES (gen_random_uuid(), gen_random_uuid(), '\x01', '\x01', 'none', '\x00', 0, '{}', false, false, '')`,
			constraint: "passkeys_name_length_check",
		},
		{
			name: "passkey name longer than 64",
			insert: `INSERT INTO passkeys (id, user_id, credential_id, public_key, attestation_type, aaguid,
				sign_count, transports, backup_eligible, backup_state, name)
				VALUES (gen_random_uuid(), gen_random_uuid(), '\x01', '\x01', 'none', '\x00', 0, '{}', false, false,
				repeat('x', 65))`,
			constraint: "passkeys_name_length_check",
		},
		{
			name: "registration without a user",
			insert: `INSERT INTO webauthn_ceremonies (id, kind, session_data, expires_at)
				VALUES (gen_random_uuid(), 'register', '{}', now())`,
			constraint: "webauthn_ceremonies_register_user_check",
		},
		{
			name: "ingest token without plaintext",
			insert: `INSERT INTO access_keys (id, user_id, kind, key_hash)
				VALUES (gen_random_uuid(), gen_random_uuid(), 'ingest', '\x01')`,
			constraint: "access_keys_plaintext_check",
		},
		{
			name: "mcp key with plaintext",
			insert: `INSERT INTO access_keys (id, user_id, kind, key_hash, plaintext)
				VALUES (gen_random_uuid(), gen_random_uuid(), 'mcp', '\x02', 'value')`,
			constraint: "access_keys_plaintext_check",
		},
		{
			name: "unknown access key kind",
			insert: `INSERT INTO access_keys (id, user_id, kind, key_hash, plaintext)
				VALUES (gen_random_uuid(), gen_random_uuid(), 'other', '\x03', 'value')`,
			constraint: "access_keys_kind_check",
		},
		{
			name: "settings document that is not an object",
			insert: `INSERT INTO telemetry_settings (user_id, document, version)
				VALUES (gen_random_uuid(), '[]', 1)`,
			constraint: "telemetry_settings_document_check",
		},
		{
			name: "settings version zero",
			insert: `INSERT INTO telemetry_settings (user_id, document, version)
				VALUES (gen_random_uuid(), '{}', 0)`,
			constraint: "telemetry_settings_version_check",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := testDB.Pool().Exec(t.Context(), tt.insert)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.ConstraintName != tt.constraint {
				t.Fatalf("insert error = %v, want a violation of %s", err, tt.constraint)
			}
		})
	}
}
