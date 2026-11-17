package memory_test

import (
	"testing"

	"github.com/claudioed/yard-management/internal/adapters/outbound/memory"
	"github.com/claudioed/yard-management/internal/testing/repocontract"
)

// The same behavioural contract the Postgres adapters pass (see
// internal/adapters/outbound/postgres/contract_integration_test.go), run
// against the in-memory doubles in plain unit mode: what the unit and BDD
// tests exercise is what production does.
func TestRepoContract(t *testing.T) {
	repocontract.Run(t, func(t *testing.T) repocontract.Store {
		s := memory.NewStore(repocontract.T0)
		return repocontract.Store{
			UoW: s, Visits: s.Visits(), Trailers: s.Trailers(), Moves: s.Moves(), Checks: s.Checks(), Policies: s.Policies(), Inspections: s.Inspections(), Templates: s.Templates(),
			Locations: s.Locations(), LocationFeed: s.Locations(), Appointments: s.Appointments(), ApptFeed: s.Appointments(),
			Closures: s.Closures(), ClosureFeed: s.Closures(), Asns: s.Asns(), AsnFeed: s.Asns(), Receipts: s.Receipts(), ReceiptFeed: s.Receipts(),
			Processed: s, Idempotency: s, Events: s,
			PendingEvents: func(*testing.T) []string { return s.EventNames() },
		}
	})
}
