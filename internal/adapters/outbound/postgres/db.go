// Package postgres provides the pgxpool-backed implementations of the
// outbound ports (the repositories, the event-fed local copies, the inbox,
// the Idempotency-Key store, the transactional outbox, the per-location
// advisory locks and the UnitOfWork) plus a golang-migrate runner over the SQL
// migrations EMBEDDED in the top-level migrations package, so the binary needs
// no migrations directory on disk. Every repository joins the UnitOfWork
// transaction carried in ctx when there is one (package pgtx) and falls back
// to the pool otherwise.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/yard-management/internal/adapters/outbound/postgres/pgtx"
	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/domain/shared"
)

// MaxConns is this adapter's per-process connection ceiling (the service
// reaches Postgres through a pooler in transaction mode; ten direct session
// connections for DDL-adjacent work and the pool's own reads is the fleet's
// working default).
const MaxConns = 10

// StatementTimeout bounds how long a single query may hold a connection.
// Every query here is a single-row read/write or one bounded list page.
const StatementTimeout = "5s"

// NewPool opens a connection pool against databaseURL with MaxConns and
// StatementTimeout applied to every connection.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	config.MaxConns = MaxConns
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET statement_timeout = '"+StatementTimeout+"'")
		return err
	}
	return pgxpool.NewWithConfig(ctx, config)
}

// querier is the subset of pgx shared by *pgxpool.Pool and pgx.Tx that the
// repositories need, so the same SQL runs against either.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// UnitOfWork is the pgx-backed ports.UnitOfWork: Do begins one transaction,
// carries it in the ctx handed to fn (see package pgtx), commits on a nil
// return and rolls back otherwise. If ctx already carries a transaction Do
// joins it instead of beginning a nested one — the port's re-entrancy
// contract. The aggregates' Saves, the outbox rows and the advisory locks of
// one use case therefore commit or roll back together.
type UnitOfWork struct {
	pool *pgxpool.Pool
}

// NewUnitOfWork constructs a UnitOfWork over pool.
func NewUnitOfWork(pool *pgxpool.Pool) *UnitOfWork {
	return &UnitOfWork{pool: pool}
}

// Do implements ports.UnitOfWork.
func (u *UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := pgtx.From(ctx); ok {
		return fn(ctx)
	}
	tx, err := u.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin unit of work: %w", err)
	}
	// Rollback after a successful Commit is a harmless no-op (pgx.ErrTxClosed);
	// it also covers a panic in fn. Use a context that survives cancellation of
	// ctx so a cancelled message still rolls back.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if err := fn(pgtx.With(ctx, tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit unit of work: %w", err)
	}
	return nil
}

// queryFor returns the transaction carried by ctx, or pool when there is none
// (repositories used outside a UnitOfWork run each statement on its own).
func queryFor(ctx context.Context, pool *pgxpool.Pool) querier {
	if tx, ok := pgtx.From(ctx); ok {
		return tx
	}
	return pool
}

// inTx runs fn atomically. When ctx already carries a unit-of-work transaction
// fn simply runs on it — the repo MUST NOT begin or commit its own (the outer
// unit of work owns that). When there is none, inTx begins, commits and rolls
// back its own transaction.
func inTx(ctx context.Context, pool *pgxpool.Pool, fn func(q querier) error) error {
	if tx, ok := pgtx.From(ctx); ok {
		return fn(tx)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// uniqueViolation returns the name of the unique constraint or index a unique
// violation (SQLSTATE 23505) broke, or "".
func uniqueViolation(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return pgErr.ConstraintName
	}
	return ""
}

// saveError maps the unique violations of an aggregate write: byConstraint
// names the domain error each index stands for; the primary key means another
// writer created the id first (ports.ErrConcurrentModification); anything else
// is a plain failure.
func saveError(what string, err error, byConstraint map[string]error) error {
	if err == nil {
		return nil
	}
	name := uniqueViolation(err)
	if name == "" {
		return fmt.Errorf("save %s: %w", what, err)
	}
	if mapped, ok := byConstraint[name]; ok {
		return fmt.Errorf("%w: %s", mapped, name)
	}
	if strings.HasSuffix(name, "_pkey") {
		return ports.ErrConcurrentModification
	}
	return fmt.Errorf("save %s: %w", what, err)
}

// nullTime maps the zero time to SQL NULL.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

// timeOrZero maps SQL NULL back to the zero time.
func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

// strs keeps a nil slice from becoming SQL NULL (the columns are NOT NULL).
func strs(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// location splits a Location into its two columns.
func location(l shared.Location) (kind, code string) { return string(l.Kind), l.Code }

// whereBuilder assembles a WHERE clause with numbered parameters.
type whereBuilder struct {
	conds []string
	args  []any
}

// add appends cond, with every "?" replaced by the next parameter number of
// arg (one arg per condition).
func (w *whereBuilder) add(cond string, arg any) {
	w.args = append(w.args, arg)
	w.conds = append(w.conds, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(w.args))))
}

// raw appends a condition with no parameter.
func (w *whereBuilder) raw(cond string) { w.conds = append(w.conds, cond) }

// clause returns " WHERE a AND b" (or "").
func (w *whereBuilder) clause() string {
	if len(w.conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(w.conds, " AND ")
}

// limit appends the LIMIT parameter and returns its placeholder.
func (w *whereBuilder) limit(n int) string {
	w.args = append(w.args, n)
	return "$" + strconv.Itoa(len(w.args))
}
