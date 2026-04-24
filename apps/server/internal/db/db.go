// Package db owns the Postgres connection pool and bundled migration runner.
//
// Two kinds of connections coexist here:
//
//   - Runtime pool: every connection does `SET ROLE sliils_app` via
//     AfterConnect so row-level-security policies are enforced. Handlers
//     reach for this one.
//
//   - Migration DB: a separate, short-lived sql.DB opened under the DSN's
//     original role (typically postgres). No role switch, so DDL works.
//     Used by RunMigrations at startup, then closed.
//
// The separation exists because Postgres superusers bypass RLS regardless of
// FORCE ROW LEVEL SECURITY — if the app connects as postgres and skips
// SET ROLE, RLS is a silent no-op.
package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// RuntimeRole is the Postgres role every runtime connection switches into
// after connecting. Migration 2 creates this role.
const RuntimeRole = "sliils_app"

// Pool wraps pgxpool.Pool with the runtime connection pool that has
// SET ROLE sliils_app applied on every new connection.
type Pool struct {
	*pgxpool.Pool
	logger *slog.Logger
	dsn    string
}

// Open creates the runtime pool. The caller is responsible for ensuring
// migrations have already run (so that the sliils_app role exists); use
// RunMigrations before Open on the same DSN.
func Open(ctx context.Context, dsn string, logger *slog.Logger) (*Pool, error) {
	if dsn == "" {
		return nil, errors.New("database URL is required (set SLIILS_DATABASE_URL)")
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}

	cfg.MaxConns = 25
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 1 * time.Minute
	cfg.ConnConfig.RuntimeParams["application_name"] = "sliils-app"

	// Every new physical connection switches into the non-owner runtime role.
	// SET ROLE persists for the session, and pgxpool reuses the connection,
	// so every subsequent query runs under sliils_app.
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+RuntimeRole)
		return err
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("new pgx pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	logger.Info("database pool ready",
		slog.Int("max_conns", int(cfg.MaxConns)),
		slog.String("runtime_role", RuntimeRole),
	)
	return &Pool{Pool: pool, logger: logger, dsn: dsn}, nil
}

// Ping is used by the /readyz handler.
func (p *Pool) Ping(ctx context.Context) error {
	return p.Pool.Ping(ctx)
}

// OpenOwner opens a second pool that connects as the DSN's original role
// (typically the database owner) without the sliils_app SET ROLE hook. The
// returned pool therefore bypasses row-level security — use it only for
// privileged background work that operates across every tenant, like the
// search indexer or River's migrator. Handlers must never reach for this.
//
// The pool is sized smaller than the runtime pool because no latency-sensitive
// request path depends on it.
func OpenOwner(ctx context.Context, dsn string, logger *slog.Logger) (*pgxpool.Pool, error) {
	if dsn == "" {
		return nil, errors.New("database URL is required (set SLIILS_DATABASE_URL)")
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}

	cfg.MaxConns = 6
	cfg.MinConns = 1
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 1 * time.Minute
	cfg.ConnConfig.RuntimeParams["application_name"] = "sliils-owner"

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("new owner pgx pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database (owner): %w", err)
	}

	logger.Info("owner database pool ready", slog.Int("max_conns", int(cfg.MaxConns)))
	return pool, nil
}

// RunMigrations applies any pending up-migrations. It opens its own short-
// lived connection to the DSN without the SET ROLE hook, so DDL runs under
// the connecting user (typically the DB owner / superuser).
//
// Call this BEFORE Open at startup: the first migration creates the
// sliils_app role that Open requires.
func RunMigrations(ctx context.Context, dsn string, migrations fs.FS, subdir string, logger *slog.Logger) error {
	if dsn == "" {
		return errors.New("database URL is required for migrations")
	}

	connCfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse migration DSN: %w", err)
	}
	connCfg.RuntimeParams["application_name"] = "sliils-migrations"

	sqlDB := stdlib.OpenDB(*connCfg)
	defer sqlDB.Close()

	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("goose set dialect: %w", err)
	}
	goose.SetLogger(gooseSlogger{logger: logger})

	logger.Info("running database migrations")
	if err := goose.UpContext(ctx, sqlDB, subdir); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	logger.Info("database migrations complete")
	return nil
}

// gooseSlogger adapts slog to goose's logger interface.
type gooseSlogger struct {
	logger *slog.Logger
}

func (g gooseSlogger) Fatalf(format string, v ...any) {
	g.logger.Error(fmt.Sprintf(format, v...))
}

func (g gooseSlogger) Printf(format string, v ...any) {
	g.logger.Info(fmt.Sprintf(format, v...))
}
