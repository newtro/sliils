package db

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
)

// TxScope is what handlers work with inside WithTx. Queries are bound to the
// active transaction; the raw *pgx.Tx is exposed for escape hatches (raw SQL
// for RLS probes, migrations in tests, etc.).
type TxScope struct {
	Tx      pgx.Tx
	Queries *sqlcgen.Queries
}

// TxOptions controls the Postgres session GUCs set at the start of the
// transaction. Zero values mean "don't set" — critical for unauthenticated
// routes and install-level events where `app.user_id` should remain NULL
// so RLS policies that require it return 0 rows rather than panicking.
type TxOptions struct {
	UserID      int64 // sets app.user_id if > 0
	WorkspaceID int64 // sets app.workspace_id if > 0
	ReadOnly    bool
}

// WithTx opens a transaction, sets GUCs, and yields a TxScope to the caller.
// Commits on nil error, rolls back on any error. Safe to use inside handlers
// that want explicit rollback-on-error without manual BEGIN/COMMIT ceremony.
//
// The GUC writes use SET LOCAL so they clear automatically at COMMIT/ROLLBACK.
// This is the mechanism that enforces RLS — without app.user_id /
// app.workspace_id set, every RLS policy's current_setting() returns NULL and
// the USING clause evaluates to NULL (treated as false) for tenant tables.
func WithTx(ctx context.Context, pool *pgxpool.Pool, opts TxOptions, fn func(TxScope) error) error {
	txOpts := pgx.TxOptions{}
	if opts.ReadOnly {
		txOpts.AccessMode = pgx.ReadOnly
	}

	tx, err := pool.BeginTx(ctx, txOpts)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	if opts.UserID > 0 {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.user_id', $1, true)", strconv.FormatInt(opts.UserID, 10)); err != nil {
			return fmt.Errorf("set app.user_id: %w", err)
		}
	}
	if opts.WorkspaceID > 0 {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.workspace_id', $1, true)", strconv.FormatInt(opts.WorkspaceID, 10)); err != nil {
			return fmt.Errorf("set app.workspace_id: %w", err)
		}
	}

	scope := TxScope{Tx: tx, Queries: sqlcgen.New(tx)}
	if err := fn(scope); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	committed = true
	return nil
}
