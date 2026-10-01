// Command cli runs operator commands against the service database.
//
// Usage:
//
//	cli migrate up      apply every pending migration
//	cli migrate down    roll back the latest applied migration
//	cli migrate status  list the migrations and whether each is applied
//	cli user create-first  create the first user from HT_BOOTSTRAP_EMAIL, HT_BOOTSTRAP_NAME
//	                       and HT_BOOTSTRAP_PASSWORD, unless a user with that email exists
//	cli user set-password  set HT_USER_PASSWORD for the user HT_USER_EMAIL and close
//	                       every session of the user
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/stdlib"

	migrations "git.alva.dev/alva/harness-telemetry/database/migrations/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/config"
)

const usage = "usage: cli migrate up|down|status | cli user create-first|set-password"

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

	if group == "user" {
		return runUser(ctx, pool, command, out, os.Stderr, os.LookupEnv, logger)
	}

	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()

	migrator, err := postgres.NewMigrator(db, migrations.FS)
	if err != nil {
		logger.Error("load migrations", "error", err)
		return exitFailure
	}

	if err := migrate(ctx, migrator, command, out); err != nil {
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
	}
	return false
}

func migrate(ctx context.Context, migrator *postgres.Migrator, command string, out io.Writer) error {
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
		result, err := migrator.Down(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "rolled back %s (%s)\n", result.Source.Path, result.Duration)
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
