//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"

	"github.com/claudioed/yard-management/internal/adapters/outbound/postgres"
	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
	"github.com/claudioed/yard-management/internal/testing/repocontract"
)

// Migrations: up, down, up again; running up twice is a no-op.
func TestMigrationsUpDownUp(t *testing.T) {
	url := pgtest.EmptyURL(t)
	tables := func() int {
		pool := pgtest.Open(t, url)
		var n int
		pgtest.Scan(t, pool, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name <> 'schema_migrations'`, nil, &n)
		return n
	}
	if err := postgres.RunMigrations(url); err != nil {
		t.Fatalf("up: %v", err)
	}
	if err := postgres.RunMigrations(url); err != nil {
		t.Fatalf("a second up must be a no-op: %v", err)
	}
	if got := tables(); got != 16 {
		t.Fatalf("tables after up = %d, want 16", got)
	}
	m, err := postgres.Migrator(url)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("down: %v", err)
	}
	_, _ = m.Close()
	if got := tables(); got != 0 {
		t.Fatalf("tables after down = %d, want 0", got)
	}
	if err := postgres.RunMigrations(url); err != nil {
		t.Fatalf("up after down: %v", err)
	}
	if got := tables(); got != 16 {
		t.Fatalf("tables after the second up = %d, want 16", got)
	}
}

// The unique indexes are the backstop the use cases cannot race past: probed
// directly, below the repositories.
func TestSchemaBackstops(t *testing.T) {
	pool := pgtest.NewPool(t)
	moves := postgres.NewMoves(pool)
	ctx := context.Background()
	if err := moves.Save(ctx, repocontract.NewMove(t, "mv-1", "trl-1", repocontract.Spot("WH1-P1"), 5, time.Time{}, 0)); err != nil {
		t.Fatal(err)
	}
	err := pgtest.Exec(pool, `INSERT INTO yard_moves (move_id, site_id, trailer_id, performer, to_kind, to_code, reason, priority_band, state, requested_at, version)
		VALUES ('mv-x', 'WH1', 'trl-x', 'HOSTLER', 'SPOT', 'WH1-P1', 'RELOCATE', 5, 'REQUESTED', now(), 1)`)
	if err == nil {
		t.Fatal("the unique index on the open-move destination did not fire")
	}
	if err := pgtest.Exec(pool, `INSERT INTO outbox (event_id, aggreg_type, aggreg_id, payload, occurred_at) VALUES ('e1', 'Trailer', 't', '{}', now())`); err != nil {
		t.Fatal(err)
	}
	if err := pgtest.Exec(pool, `INSERT INTO outbox (event_id, aggreg_type, aggreg_id, payload, occurred_at) VALUES ('e1', 'Trailer', 't', '{}', now())`); err == nil {
		t.Fatal("the outbox event_id must be unique")
	}
}

// Two writers racing for the same destination: exactly one wins, the other
// gets the domain error (not a raw SQL failure).
func TestConcurrentDestinationReservation(t *testing.T) {
	pool := pgtest.NewPool(t)
	moves := postgres.NewMoves(pool)
	uow := postgres.NewUnitOfWork(pool)
	var wins, refused atomic.Int32
	var wg sync.WaitGroup
	for i, id := range []string{"mv-1", "mv-2", "mv-3", "mv-4"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := repocontract.NewMove(t, id, "trl-"+id, repocontract.Spot("WH1-P1"), 5, time.Time{}, time.Duration(i))
			err := uow.Do(context.Background(), func(ctx context.Context) error { return moves.Save(ctx, m) })
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, yardmove.ErrDestinationReserved):
				refused.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || refused.Load() != 3 {
		t.Fatalf("wins=%d refused=%d", wins.Load(), refused.Load())
	}
}

// Two hostlers claiming at once never receive the same move (SKIP LOCKED).
func TestConcurrentClaimNextNeverDoubleClaims(t *testing.T) {
	pool := pgtest.NewPool(t)
	moves := postgres.NewMoves(pool)
	uow := postgres.NewUnitOfWork(pool)
	ctx := context.Background()
	for i, id := range []string{"mv-1", "mv-2", "mv-3", "mv-4"} {
		if err := moves.Save(ctx, repocontract.NewMove(t, id, "trl-"+id, repocontract.Spot("WH1-P"+id), 5, time.Time{}, time.Duration(i))); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	got := map[string]int{}
	var wg sync.WaitGroup
	for h := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hostler := string(rune('a' + h))
			err := uow.Do(ctx, func(ctx context.Context) error {
				m, err := moves.ClaimNext(ctx, "WH1")
				if err != nil {
					return err
				}
				if err := m.Claim(hostler, repocontract.T0.Add(time.Hour), time.Minute); err != nil {
					return err
				}
				return moves.Save(ctx, m)
			})
			if err != nil {
				t.Errorf("hostler %s: %v", hostler, err)
				return
			}
			held, err := moves.FindClaimedByHostler(ctx, "WH1", hostler)
			if err != nil {
				t.Errorf("hostler %s holds nothing: %v", hostler, err)
				return
			}
			mu.Lock()
			got[held.ID()]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(got) != 4 {
		t.Fatalf("each hostler must hold a distinct move: %v", got)
	}
}

// The per-location advisory lock serialises two units of work on one code and
// is released at commit.
func TestAdvisoryLocksSerialiseLocationWriters(t *testing.T) {
	pool := pgtest.NewPool(t)
	uow := postgres.NewUnitOfWork(pool)
	locks := postgres.NewLocks()
	ctx := context.Background()

	if err := locks.Lock(ctx, "WH1-P1"); !errors.Is(err, postgres.ErrNoTransaction) {
		t.Fatalf("a lock outside a unit of work must fail, got %v", err)
	}

	held := make(chan struct{})
	release := make(chan struct{})
	var order []string
	var mu sync.Mutex
	note := func(s string) { mu.Lock(); order = append(order, s); mu.Unlock() }
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		err := uow.Do(ctx, func(ctx context.Context) error {
			if err := locks.Lock(ctx, "WH1-P2", "WH1-P1", "WH1-P1"); err != nil {
				return err
			}
			close(held)
			<-release
			note("first-commit")
			return nil
		})
		if err != nil {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		<-held
		err := uow.Do(ctx, func(ctx context.Context) error {
			if err := locks.Lock(ctx, "WH1-P1"); err != nil {
				return err
			}
			note("second-locked")
			return nil
		})
		if err != nil {
			t.Error(err)
		}
	}()
	<-held
	time.Sleep(300 * time.Millisecond) // the second writer is parked on the lock
	mu.Lock()
	early := len(order)
	mu.Unlock()
	if early != 0 {
		t.Fatalf("the second writer got the lock while the first held it: %v", order)
	}
	close(release)
	wg.Wait()
	if len(order) != 2 || order[0] != "first-commit" || order[1] != "second-locked" {
		t.Fatalf("order = %v", order)
	}
}

// The unit of work commits aggregate rows and outbox rows atomically: a
// failure after the outbox insert leaves neither.
func TestUnitOfWorkRollsBackOutboxWithAggregate(t *testing.T) {
	pool := pgtest.NewPool(t)
	uow := postgres.NewUnitOfWork(pool)
	visits, outbox := postgres.NewVisits(pool), postgres.NewOutbox(pool)
	v := repocontract.NewVisit(t, "visit-1", "WH1", "PLATE1", 0, repocontract.Ref("trl-1", "T1"))
	events := v.PullEvents()
	boom := errors.New("boom")
	err := uow.Do(context.Background(), func(ctx context.Context) error {
		if err := visits.Save(ctx, v); err != nil {
			return err
		}
		if err := outbox.Publish(ctx, events...); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if pgtest.Count(t, pool, "yard_visits") != 0 || pgtest.Count(t, pool, "outbox") != 0 {
		t.Fatal("the rollback left rows behind")
	}
	if err := outbox.Publish(context.Background(), events...); err != nil { // outside a unit of work: its own transaction
		t.Fatal(err)
	}
	if pgtest.Count(t, pool, "outbox") != len(events) {
		t.Fatalf("outbox rows = %d, want %d", pgtest.Count(t, pool, "outbox"), len(events))
	}
}

// Concurrent claims of one event id: exactly one caller wins.
func TestProcessedEventsConcurrentClaim(t *testing.T) {
	pool := pgtest.NewPool(t)
	inbox := postgres.NewProcessedEvents(pool)
	var firsts atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			first, err := inbox.Claim(context.Background(), "ev-1")
			if err != nil {
				t.Error(err)
				return
			}
			if first {
				firsts.Add(1)
			}
		}()
	}
	wg.Wait()
	if firsts.Load() != 1 {
		t.Fatalf("winners = %d", firsts.Load())
	}
}

// ports must be satisfied by the sentinel the adapters return for a missing row.
func TestNotFoundIsThePortSentinel(t *testing.T) {
	pool := pgtest.NewPool(t)
	_, err := postgres.NewTrailers(pool).FindByID(context.Background(), "nope")
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}
