package postgres_test

import (
	"context"
	"errors"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestTxManager_WithinTx(t *testing.T) {
	t.Parallel()

	errFn := errors.New("fn failed")

	tests := map[string]struct {
		fnErr     error
		wantSaved bool
	}{
		"fn succeeds: commit": {wantSaved: true},
		"fn fails: roll back": {fnErr: errFn},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			users := postgres.NewUsers(testDB.Pool())
			email := uniqueEmail(t)
			// A context without the transaction, to look from outside it.
			outside := t.Context()

			err := postgres.NewTxManager(testDB.Pool()).WithinTx(t.Context(), func(ctx context.Context) error {
				if _, err := users.CreateInvited(ctx, email, "В транзакции"); err != nil {
					return err
				}
				// The repository writes in the transaction: visible in it, not outside yet.
				if _, _, err := users.GetByEmail(ctx, email); err != nil {
					t.Errorf("GetByEmail() within the transaction error = %v, want the user", err)
				}
				if _, _, err := users.GetByEmail(outside, email); !errors.Is(err, domain.ErrUserNotFound) {
					t.Errorf("GetByEmail() outside the transaction error = %v, want %v", err, domain.ErrUserNotFound)
				}
				return tc.fnErr
			})
			if !errors.Is(err, tc.fnErr) {
				t.Fatalf("WithinTx() error = %v, want %v", err, tc.fnErr)
			}

			_, _, err = users.GetByEmail(t.Context(), email)
			if saved := err == nil; saved != tc.wantSaved {
				t.Errorf("after WithinTx() user saved = %t (err = %v), want %t", saved, err, tc.wantSaved)
			}
		})
	}
}

func TestTxManager_WithinTxNested(t *testing.T) {
	t.Parallel()

	users := postgres.NewUsers(testDB.Pool())
	txs := postgres.NewTxManager(testDB.Pool())
	outer, inner := uniqueEmail(t), uniqueEmail(t)
	errInner := errors.New("inner failed")

	err := txs.WithinTx(t.Context(), func(ctx context.Context) error {
		if _, err := users.CreateInvited(ctx, outer, "Внешний"); err != nil {
			return err
		}
		err := txs.WithinTx(ctx, func(ctx context.Context) error {
			if _, err := users.CreateInvited(ctx, inner, "Внутренний"); err != nil {
				return err
			}
			return errInner
		})
		if !errors.Is(err, errInner) {
			t.Errorf("nested WithinTx() error = %v, want %v", err, errInner)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithinTx() error = %v", err)
	}

	// The failed nested call rolls back to its savepoint only.
	if _, _, err := users.GetByEmail(t.Context(), outer); err != nil {
		t.Errorf("GetByEmail(outer) error = %v, want the committed user", err)
	}
	if _, _, err := users.GetByEmail(t.Context(), inner); !errors.Is(err, domain.ErrUserNotFound) {
		t.Errorf("GetByEmail(inner) error = %v, want %v", err, domain.ErrUserNotFound)
	}
}
