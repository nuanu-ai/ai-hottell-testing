// Command cli runs operator commands against the service database.
//
// Usage:
//
//	cli migrate up      apply every pending migration
//	cli migrate down    roll back the latest applied migration, or, with DELIVERY_MIGRATIONS_DOWN
//	                    set, the migrations it names, newest first, comma separated
//	cli migrate status  list the migrations and whether each is applied
//	cli user create-first  create the first user from HT_BOOTSTRAP_EMAIL, HT_BOOTSTRAP_NAME
//	                       and HT_BOOTSTRAP_PASSWORD, unless a user with that email exists
//	cli user set-password  set HT_USER_PASSWORD for the user HT_USER_EMAIL and close
//	                       every session of the user
//	cli journal verify     check the whole hash chain of the decision journal and, with
//	                       HT_JOURNAL_HEAD set to a head it printed before, that it ends there
package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path"
	"slices"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	migrations "git.alva.dev/alva/harness-telemetry/database/migrations/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/config"
)

const usage = "usage: cli migrate up|down|status | cli user create-first|set-password | cli journal verify"

// Exit codes: a failed command and a malformed invocation.
const (
	exitFailure = 1
	exitUsage   = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}

func run(args []string, out io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	if !validCommand(args) {
		fmt.Fprintln(os.Stderr, usage)
		return exitUsage
	}
	group, command := args[0], args[1]

	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		logger.Error("load config", "error", err)
		return exitFailure
	}

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("connect database", "error", err)
		return exitFailure
	}
	defer pool.Close()

	switch group {
	case "user":
		return runUser(ctx, pool, command, out, os.Stderr, os.LookupEnv, logger)
	case "journal":
		return runJournal(ctx, pool, command, out, os.Stderr, os.LookupEnv)
	}

	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()

	migrator, err := postgres.NewMigrator(db, migrations.FS)
	if err != nil {
		logger.Error("load migrations", "error", err)
		return exitFailure
	}

	if err := migrate(ctx, migrator, command, out, os.LookupEnv); err != nil {
		logger.Error("migrate "+command, "error", err)
		return exitFailure
	}
	return 0
}

// validCommand reports whether args name a known command group and command.
func validCommand(args []string) bool {
	if len(args) != 2 {
		return false
	}
	switch args[0] {
	case "migrate":
		return args[1] == "up" || args[1] == "down" || args[1] == "status"
	case "user":
		return args[1] == "create-first" || args[1] == "set-password"
	case "journal":
		return args[1] == "verify"
	}
	return false
}

// envMigrationsDown names the migrations "migrate down" rolls back. The deploy dispatcher sets
// it when it rolls a release back: migration file names relative to the migrations directory,
// newest first, comma separated. Unset, "migrate down" rolls back the latest migration only.
const envMigrationsDown = "DELIVERY_MIGRATIONS_DOWN"

func migrate(
	ctx context.Context, migrator *postgres.Migrator, command string, out io.Writer, lookup config.LookupFunc,
) error {
	switch command {
	case "up":
		results, err := migrator.Up(ctx)
		if err != nil {
			return err
		}
		if len(results) == 0 {
			fmt.Fprintln(out, "no pending migrations")
		}
		for _, r := range results {
			fmt.Fprintf(out, "applied %s (%s)\n", r.Source.Path, r.Duration)
		}
	case "down":
		list, set := lookup(envMigrationsDown)
		if !set {
			return rollBack(ctx, migrator, out)
		}
		return rollBackList(ctx, migrator, splitMigrationList(list), out)
	case "status":
		statuses, err := migrator.Status(ctx)
		if err != nil {
			return err
		}
		if len(statuses) == 0 {
			fmt.Fprintln(out, "no migrations")
		}
		for _, s := range statuses {
			fmt.Fprintf(out, "%-8s %s\n", s.State, s.Source.Path)
		}
	default:
		return errors.New("unknown command " + command)
	}
	return nil
}

// rollBack rolls back the latest applied migration.
func rollBack(ctx context.Context, migrator *postgres.Migrator, out io.Writer) error {
	result, err := migrator.Down(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "rolled back %s (%s)\n", result.Source.Path, result.Duration)
	return nil
}

// splitMigrationList returns the non-empty elements of a comma-separated list.
func splitMigrationList(list string) []string {
	var names []string
	for name := range strings.SplitSeq(list, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// rollBackList rolls back the migrations names lists, newest first, one at a time. The whole
// list must match the applied migrations from the latest down before anything is rolled back,
// and each step checks again that the latest applied migration is the one it names, so a list
// that disagrees with the database never rolls back a migration it does not name.
func rollBackList(ctx context.Context, migrator *postgres.Migrator, names []string, out io.Writer) error {
	if len(names) == 0 {
		fmt.Fprintln(out, "no migrations requested in "+envMigrationsDown)
		return nil
	}
	known, applied, err := migrationNames(ctx, migrator)
	if err != nil {
		return err
	}
	for _, name := range names {
		if !known[name] {
			return fmt.Errorf("%s names %s, which is not a migration", envMigrationsDown, name)
		}
	}
	for i, name := range names {
		if err := checkNext(name, applied[min(i, len(applied)):]); err != nil {
			return err
		}
	}
	for _, name := range names {
		_, applied, err := migrationNames(ctx, migrator)
		if err != nil {
			return err
		}
		if err := checkNext(name, applied); err != nil {
			return err
		}
		if err := rollBack(ctx, migrator, out); err != nil {
			return err
		}
	}
	return nil
}

// checkNext reports an error unless name is the first of applied, the migration to roll back next.
func checkNext(name string, applied []string) error {
	if len(applied) == 0 {
		return fmt.Errorf("%s names %s next, but no applied migration is left", envMigrationsDown, name)
	}
	if applied[0] != name {
		return fmt.Errorf("%s names %s next, but the latest applied migration is %s",
			envMigrationsDown, name, applied[0])
	}
	return nil
}

// migrationNames returns the file names of every known migration and of the applied ones,
// latest first.
func migrationNames(ctx context.Context, migrator *postgres.Migrator) (map[string]bool, []string, error) {
	statuses, err := migrator.Status(ctx)
	if err != nil {
		return nil, nil, err
	}
	slices.SortFunc(statuses, func(a, b *goose.MigrationStatus) int {
		return cmp.Compare(b.Source.Version, a.Source.Version)
	})
	known := make(map[string]bool, len(statuses))
	var applied []string
	for _, s := range statuses {
		name := path.Base(s.Source.Path)
		known[name] = true
		if s.State == goose.StateApplied {
			applied = append(applied, name)
		}
	}
	return known, applied, nil
}
