package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// querier is what the repositories run their SQL on: the pool, or the transaction of the context.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// beginner starts a transaction: the pool starts a new one, a transaction a savepoint.
type beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// txKey is the context key of the transaction opened by TxManager.WithinTx.
type txKey struct{}

// TxManager runs functions within a transaction that the repositories take from the context.
type TxManager struct {
	pool *pgxpool.Pool
}

// NewTxManager returns a TxManager that opens transactions on pool.
func NewTxManager(pool *pgxpool.Pool) *TxManager {
	return &TxManager{pool: pool}
}

// WithinTx runs fn within a transaction: it commits when fn returns nil and rolls back when
// fn returns an error. Repositories called with the context fn receives run in that
// transaction. Called within a transaction already, it opens a savepoint of it.
func (m *TxManager) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	var db beginner = m.pool
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		db = tx
	}
	err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
	if err != nil {
		return fmt.Errorf("within transaction: %w", err)
	}
	return nil
}

// conn returns the transaction of ctx, or pool when ctx carries none.
func conn(ctx context.Context, pool *pgxpool.Pool) querier {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return pool
}
