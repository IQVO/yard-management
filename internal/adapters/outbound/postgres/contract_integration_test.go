//go:build integration

package postgres_test

import (
	"testing"

	"github.com/claudioed/yard-management/internal/adapters/outbound/postgres"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
	"github.com/claudioed/yard-management/internal/testing/repocontract"
)

// The same behavioural contract the in-memory doubles pass (see
// internal/adapters/outbound/memory/repocontract_test.go), run against a real
// Postgres: every contract case gets its own database.
func TestRepoContract(t *testing.T) {
	repocontract.Run(t, func(t *testing.T) repocontract.Store {
		pool := pgtest.NewPool(t)
		locations, appointments, closures := postgres.NewLocations(pool), postgres.NewAppointments(pool), postgres.NewClosures(pool)
		asns, receipts := postgres.NewAsns(pool), postgres.NewReceipts(pool)
		return repocontract.Store{
			UoW: postgres.NewUnitOfWork(pool), Visits: postgres.NewVisits(pool), Trailers: postgres.NewTrailers(pool), Moves: postgres.NewMoves(pool),
			Checks: postgres.NewChecks(pool), Policies: postgres.NewPolicies(pool),
			Locations: locations, LocationFeed: locations, Appointments: appointments, ApptFeed: appointments,
			Closures: closures, ClosureFeed: closures, Asns: asns, AsnFeed: asns, Receipts: receipts, ReceiptFeed: receipts,
			Processed: postgres.NewProcessedEvents(pool), Idempotency: postgres.NewIdempotencyStore(pool), Events: postgres.NewOutbox(pool),
			PendingEvents: func(t *testing.T) []string { return pendingEvents(t, pool) },
		}
	})
}
