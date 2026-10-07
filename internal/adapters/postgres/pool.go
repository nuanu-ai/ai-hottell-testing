// Package postgres holds the PostgreSQL adapters of the service.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// pingTimeout bounds the startup ping so an unreachable database fails fast.
const pingTimeout = 5 * time.Second

var errInvalidURL = errors.New("parse database url: invalid connection string")

// NewPool opens a connection pool for databaseURL and pings the database.
// The caller closes the returned pool on shutdown.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		// The parse error may echo the URL, password included; keep it out of the logs.
		return nil, errInvalidURL
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}
