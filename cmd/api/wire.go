package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/claudioed/yard-management/internal/adapters/inbound/http"
	inboundkafka "github.com/claudioed/yard-management/internal/adapters/inbound/kafka"
	"github.com/claudioed/yard-management/internal/adapters/outbound/analyticsstore"
	outboundkafka "github.com/claudioed/yard-management/internal/adapters/outbound/kafka"
	"github.com/claudioed/yard-management/internal/adapters/outbound/postgres"
	"github.com/claudioed/yard-management/internal/adapters/outbound/system"
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
	analytics *pgxpool.Pool // read-only pool of the reports role; nil when not configured
	server    *inboundhttp.Server
	readiness *inboundhttp.Readiness
	consumers []*inboundkafka.Consumer
}

// close releases the pools; the caller stops the HTTP server and the consumers first.
func (rt *runtime) close() {
	if rt.analytics != nil {
		rt.analytics.Close()
	}
	rt.pool.Close()
}

// wire opens Postgres (running the embedded migrations first, on
// MIGRATIONS_DATABASE_URL when set, else DATABASE_URL) and builds the use
// cases and the REST server over the real adapters. The caller closes the pool
// after the HTTP server stopped.
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
	uow := postgres.NewUnitOfWork(pool)
	locks := postgres.NewLocks()
	clock := system.NewClock()
	idem := inboundhttp.IdempotencyDeps{UoW: uow, Store: postgres.NewIdempotencyStore(pool), Locks: locks, Clock: clock}
	readiness := inboundhttp.NewReadiness(inboundhttp.DefaultReadinessTTL, inboundhttp.DefaultReadinessTimeout,
		inboundhttp.Dependency{Name: "postgres", Check: pool.Ping},
		inboundhttp.Dependency{Name: "kafka", Check: outboundkafka.NewBrokerCheck(cfg.brokers).Check},
	)
	yard := usecases.New(deps(pool, uow, locks, clock))
	server := &inboundhttp.Server{
		Yard:             yard,
		Idempotency:      inboundhttp.NewIdempotency(idem),
		SweepIdempotency: inboundhttp.NewOptionalIdempotency(idem),
		Readiness:        readiness,
	}
	rt := &runtime{pool: pool, server: server, readiness: readiness}
	// The analytics read side (ADR 0006) is optional here: with a reader DSN the
	// report routes answer from the analytical schema (read-only pool); without
	// one they answer the contract shape with no rows. cmd/yard-reports serves
	// the same routes as a process of its own.
	if cfg.analyticsReaderURL != "" {
		if rt.analytics, err = analyticsstore.NewReadOnlyPool(ctx, cfg.analyticsReaderURL); err != nil {
			pool.Close()
			return nil, fmt.Errorf("open the analytics reader pool on %s: %w", envAnalyticsRead, err)
		}
		server.Reports = analyticsstore.NewReader(rt.analytics)
	}
	if rt.consumers, err = buildConsumers(cfg, yard, logger); err != nil {
		rt.close()
		return nil, fmt.Errorf("build the kafka consumers: %w", err)
	}
	return rt, nil
}

// deps wires every port of the use cases to its Postgres adapter.
func deps(pool *pgxpool.Pool, uow *postgres.UnitOfWork, locks *postgres.Locks, clock *system.Clock) usecases.Deps {
	locations, appointments, closures := postgres.NewLocations(pool), postgres.NewAppointments(pool), postgres.NewClosures(pool)
	asns, receipts := postgres.NewAsns(pool), postgres.NewReceipts(pool)
	return usecases.Deps{
		UoW: uow, Visits: postgres.NewVisits(pool), Trailers: postgres.NewTrailers(pool), Moves: postgres.NewMoves(pool),
		Checks: postgres.NewChecks(pool), Policies: postgres.NewPolicies(pool),
		Inspections: postgres.NewInspections(pool), Templates: postgres.NewTemplates(pool),
		Locations: locations, Appointments: appointments, Closures: closures, Asns: asns, Receipts: receipts,
		Locks: locks, Processed: postgres.NewProcessedEvents(pool),
		ApptFeed: appointments, LocationFeed: locations, ClosureFeed: closures, AsnFeed: asns, ReceiptFeed: receipts,
		Events: postgres.NewOutbox(pool), IDs: system.NewIDGen(), Clock: clock,
	}
}
