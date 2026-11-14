package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/claudioed/yard-management/internal/adapters/inbound/http"
	inboundmcp "github.com/claudioed/yard-management/internal/adapters/inbound/mcp"
	outboundkafka "github.com/claudioed/yard-management/internal/adapters/outbound/kafka"
	"github.com/claudioed/yard-management/internal/adapters/outbound/postgres"
	"github.com/claudioed/yard-management/internal/adapters/outbound/system"
	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/usecases"
)

// bootAttempts and bootBackoff bound the retries of the first outbound dial:
// an injected pod can see its first connection reset while the sidecar starts.
var (
	bootAttempts = 6
	bootBackoff  = 500 * time.Millisecond
)

// retry runs fn up to bootAttempts times with a doubling pause and returns the
// LAST error; a cancelled ctx stops it at once.
func retry(ctx context.Context, logger *slog.Logger, what string, fn func() error) error {
	var err error
	pause := bootBackoff
	for attempt := 1; attempt <= bootAttempts; attempt++ {
		if err = fn(); err == nil {
			return nil
		}
		if attempt == bootAttempts {
			break
		}
		logger.Warn("boot step failed; retrying", "step", what, "attempt", attempt, "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pause):
		}
		pause *= 2
	}
	return fmt.Errorf("%s: %w", what, err)
}

// runtime is everything the process wires from the environment.
type runtime struct {
	pool      *pgxpool.Pool
	deps      inboundmcp.Deps
	readiness *inboundhttp.Readiness
}

// wire opens Postgres (running the embedded migrations first, on
// MIGRATIONS_DATABASE_URL when set, else DATABASE_URL) and builds the use
// cases and the MCP deps over the real adapters. Like every fleet cmd/mcp it
// migrates on start, so it can boot against a fresh database before cmd/api
// has: golang-migrate's advisory lock makes concurrent starts safe. The caller
// closes the pool after the HTTP server stopped.
func wire(ctx context.Context, cfg config, getenv func(string) string, logger *slog.Logger) (*runtime, error) {
	databaseURL := getenv(envDatabaseURL)
	if err := retry(ctx, logger, "run migrations", func() error { return postgres.RunMigrations(postgres.MigrationsURL(getenv)) }); err != nil {
		return nil, err
	}
	pool, err := postgres.NewPool(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", envDatabaseURL, err)
	}
	if err := retry(ctx, logger, "ping postgres", func() error { return pool.Ping(ctx) }); err != nil {
		pool.Close()
		return nil, err
	}
	readiness := inboundhttp.NewReadiness(inboundhttp.DefaultReadinessTTL, inboundhttp.DefaultReadinessTimeout,
		inboundhttp.Dependency{Name: "postgres", Check: pool.Ping},
		inboundhttp.Dependency{Name: "kafka", Check: outboundkafka.NewBrokerCheck(cfg.brokers).Check},
	)
	return &runtime{pool: pool, deps: mcpDeps(pool, checkInMetrics(logger)), readiness: readiness}, nil
}

// mcpDeps wires the tools over the Postgres adapters. Events leave through the
// transactional outbox behind the attributing decorator, which stamps
// `initiated_by` into the data of every event an MCP write raises. The
// analytics read side (ADR 0006) is a later binary's job: Reports is left
// unset, and get_yard_dwell_report answers no rows until it is wired, exactly
// like GET /reports/yard-dwell.
func mcpDeps(pool *pgxpool.Pool, metrics ports.CheckInMetrics) inboundmcp.Deps {
	uow := postgres.NewUnitOfWork(pool)
	locks := postgres.NewLocks()
	clock := system.NewClock()
	d := yardDeps(pool, uow, locks, clock, inboundmcp.NewAttributingPublisher(postgres.NewOutbox(pool)))
	d.Metrics = metrics
	return inboundmcp.Deps{
		Yard:        usecases.New(d),
		Idempotency: inboundmcp.IdempotencyDeps{UoW: uow, Store: postgres.NewIdempotencyStore(pool), Locks: locks, Clock: clock},
	}
}

// yardDeps wires every port of the use cases to its Postgres adapter, the same
// wiring as cmd/api; only the event publisher differs.
func yardDeps(pool *pgxpool.Pool, uow *postgres.UnitOfWork, locks *postgres.Locks, clock *system.Clock, events ports.EventPublisher) usecases.Deps {
	locations, appointments, closures := postgres.NewLocations(pool), postgres.NewAppointments(pool), postgres.NewClosures(pool)
	carriers := postgres.NewCarriers(pool)
	asns, receipts := postgres.NewAsns(pool), postgres.NewReceipts(pool)
	return usecases.Deps{
		UoW: uow, Visits: postgres.NewVisits(pool), Trailers: postgres.NewTrailers(pool), Moves: postgres.NewMoves(pool),
		Checks: postgres.NewChecks(pool), Policies: postgres.NewPolicies(pool),
		Inspections: postgres.NewInspections(pool), Templates: postgres.NewTemplates(pool),
		Locations: locations, Appointments: appointments, Closures: closures, Carriers: carriers, Asns: asns, Receipts: receipts,
		Locks: locks, Processed: postgres.NewProcessedEvents(pool),
		ApptFeed: appointments, LocationFeed: locations, ClosureFeed: closures, CarrierFeed: carriers, AsnFeed: asns, ReceiptFeed: receipts,
		Events: events, IDs: system.NewIDGen(), Clock: clock,
	}
}
