// Package analyticsstore holds the Postgres adapters of the analytics read
// side (ADR 0006) over the "analytics" schema: Projection (the writer used by
// cmd/yard-projector, under its own role) and Reader (the read-only report
// queries used by cmd/yard-reports and, when configured, cmd/api). No OLTP
// package may import this one (arch test); it shares nothing with the OLTP
// tables.
package analyticsstore

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres" // the postgres:// driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/yard-management/analytics"
)

const (
	// MaxConns is the projector's connection ceiling: one consumer loop issuing
	// single-event upserts needs very few.
	MaxConns = 5
	// ReportsMaxConns is the reports process's ceiling (stateless reads,
	// horizontally scalable by the chart).
	ReportsMaxConns = 5
	// StatementTimeout bounds one projector statement.
	StatementTimeout = "10s"
	// ReportsStatementTimeout bounds one report query: aggregates over a
	// caller-chosen window (at most 92 days) get more headroom than a single-row
	// upsert, but never run unbounded.
	ReportsStatementTimeout = "15s"
)

// RunMigrations creates the schema when missing and applies every pending
// embedded analytical migration (analytics/migrations) against databaseURL, a
// DIRECT DSN of the projector's role (golang-migrate's advisory lock does not
// survive PgBouncer transaction pooling). The migrations table lives in the
// analytics schema too, so the OLTP migrations (same database or not) never see
// it.
func RunMigrations(ctx context.Context, databaseURL string) error {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect to create the %s schema: %w", analytics.Schema, err)
	}
	_, err = conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+analytics.Schema)
	_ = conn.Close(ctx)
	if err != nil {
		return fmt.Errorf("create the %s schema: %w", analytics.Schema, err)
	}
	migrateURL, err := withSearchPath(databaseURL)
	if err != nil {
		return err
	}
	src, err := iofs.New(analytics.Migrations, "migrations")
	if err != nil {
		return fmt.Errorf("open embedded analytics migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, migrateURL)
	if err != nil {
		return err
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

// withSearchPath returns databaseURL with search_path=analytics, so the
// migrations table is created in the analytics schema.
func withSearchPath(databaseURL string) (string, error) {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return "", fmt.Errorf("parse the analytics database URL: %w", err)
	}
	q := u.Query()
	q.Set("search_path", analytics.Schema)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// NewPool opens the WRITER pool.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	return newPool(ctx, databaseURL, MaxConns, StatementTimeout, false)
}

// NewReadOnlyPool opens the READER pool: every connection defaults to
// read-only transactions, so a bug in a report cannot mutate the model even if
// its database role could (defence in depth on top of the role).
func NewReadOnlyPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	return newPool(ctx, databaseURL, ReportsMaxConns, ReportsStatementTimeout, true)
}

func newPool(ctx context.Context, databaseURL string, maxConns int32, statementTimeout string, readOnly bool) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = maxConns
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = analytics.Schema
	if readOnly {
		cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, fmt.Sprintf("SET statement_timeout = '%s'", statementTimeout))
		return err
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}
