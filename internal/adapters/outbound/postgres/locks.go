package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/claudioed/yard-management/internal/adapters/outbound/postgres/pgtx"
)

// ErrNoTransaction is returned by Locks.Lock outside a unit of work: an
// advisory lock held by no transaction would protect nothing.
var ErrNoTransaction = errors.New("postgres: the lock needs a unit of work")

// Locks is the pgx-backed ports.LocationLock: per-location advisory locks,
// transaction-scoped (pg_advisory_xact_lock), so they are released at commit
// or rollback and are safe behind a pooler in transaction mode. The codes are
// taken in sorted order, so two writers that need the same set never deadlock
// on each other.
type Locks struct{}

// NewLocks constructs the advisory-lock port.
func NewLocks() *Locks { return &Locks{} }

// lockKeyPrefix namespaces the lock keys of this service.
const lockKeyPrefix = "yard-management:location:"

// Lock implements ports.LocationLock.
func (Locks) Lock(ctx context.Context, codes ...string) error {
	tx, ok := pgtx.From(ctx)
	if !ok {
		return ErrNoTransaction
	}
	sorted := slices.Clone(codes)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	for _, code := range sorted {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKeyPrefix+code); err != nil {
			return fmt.Errorf("lock location %s: %w", code, err)
		}
	}
	return nil
}
