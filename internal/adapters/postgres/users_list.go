package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// List returns every user: invited ones first, then each group newest first.
func (r *Users) List(ctx context.Context) ([]domain.User, error) {
	rows, err := conn(ctx, r.pool).Query(ctx, `
		SELECT `+userColumns+`
		FROM users
		ORDER BY password_hash IS NULL DESC, created_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("select users: %w", err)
	}
	users, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.User, error) {
		user, _, err := scanUser(row)
		return user, err
	})
	if err != nil {
		return nil, fmt.Errorf("select users: %w", err)
	}
	return users, nil
}
