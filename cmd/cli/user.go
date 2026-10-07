package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/argon2id"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/clock"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/token"
	"git.alva.dev/alva/harness-telemetry/internal/application/session"
	"git.alva.dev/alva/harness-telemetry/internal/application/users"
	"git.alva.dev/alva/harness-telemetry/internal/config"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// The environment the user commands read; passwords never leave it for the output or the log.
const (
	envBootstrapEmail    = "HT_BOOTSTRAP_EMAIL"
	envBootstrapName     = "HT_BOOTSTRAP_NAME"
	envBootstrapPassword = "HT_BOOTSTRAP_PASSWORD"
	envUserEmail         = "HT_USER_EMAIL"
	envUserPassword      = "HT_USER_PASSWORD"
)

// runUser runs the user command on the database of pool: it reads its input from lookup,
// reports the outcome to out and a refusal to errOut; logger records a failure.
func runUser(
	ctx context.Context, pool *pgxpool.Pool, command string,
	out, errOut io.Writer, lookup config.LookupFunc, logger *slog.Logger,
) int {
	svc := users.NewService(
		postgres.NewUsers(pool),
		postgres.NewLinks(pool),
		postgres.NewSessions(pool),
		session.NewService(postgres.NewSessions(pool), token.Issuer{}, clock.System{}),
		argon2id.New(argon2id.DefaultMemoryKiB),
		token.Issuer{},
		clock.System{},
		postgres.NewTxManager(pool),
		"", // the user commands issue no links
	)

	var err error
	switch command {
	case "create-first":
		err = createFirst(ctx, svc, lookup, out)
	case "set-password":
		err = setPassword(ctx, svc, lookup, out)
	default:
		err = errors.New("unknown command " + command)
	}
	if err == nil {
		return 0
	}

	var missing missingEnvError
	var public *domain.Error
	switch {
	case errors.As(err, &missing):
		fmt.Fprintf(errOut, "Не задана переменная окружения %s\n", string(missing))
	case errors.As(err, &public):
		fmt.Fprintln(errOut, public.Message)
	default:
		logger.ErrorContext(ctx, "user "+command, "error", err)
	}
	return exitFailure
}

func createFirst(ctx context.Context, svc *users.Service, lookup config.LookupFunc, out io.Writer) error {
	env, err := requireEnv(lookup, envBootstrapEmail, envBootstrapName, envBootstrapPassword)
	if err != nil {
		return err
	}
	user, created, err := svc.CreateFirst(ctx, env[envBootstrapEmail], env[envBootstrapName], env[envBootstrapPassword])
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(out, "Создан пользователь %s\n", user.Email)
	} else {
		fmt.Fprintf(out, "Пользователь %s уже существует\n", user.Email)
	}
	return nil
}

func setPassword(ctx context.Context, svc *users.Service, lookup config.LookupFunc, out io.Writer) error {
	env, err := requireEnv(lookup, envUserEmail, envUserPassword)
	if err != nil {
		return err
	}
	user, err := svc.ForceSetPassword(ctx, env[envUserEmail], env[envUserPassword])
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Пароль %s изменён, сессии закрыты\n", user.Email)
	return nil
}

// missingEnvError names a required environment variable that is unset or empty.
type missingEnvError string

func (e missingEnvError) Error() string {
	return string(e) + ": required but not set"
}

// requireEnv returns the values of keys; missingEnvError for the first one unset or empty.
func requireEnv(lookup config.LookupFunc, keys ...string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		v, ok := lookup(key)
		if !ok || v == "" {
			return nil, missingEnvError(key)
		}
		values[key] = v
	}
	return values, nil
}
