// Package repocontract holds the behavioural contract of the outbound
// repositories, local copies and driven ports. The in-memory doubles
// (internal/adapters/outbound/memory) and the Postgres adapters
// (internal/adapters/outbound/postgres) run the SAME cases, so what the unit
// and BDD tests exercise is what production does. It is test support only.
package repocontract

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/ports"
)

// T0 is the fixed instant the cases build on. It has no sub-microsecond part,
// so a Postgres round trip returns it unchanged.
var T0 = time.Date(2026, 11, 5, 14, 0, 0, 0, time.UTC)

var bg = context.Background()

// Store is the set of ports one contract case runs against. NewStore must
// return EMPTY ports on every call.
type Store struct {
	UoW          ports.UnitOfWork
	Visits       ports.VisitRepo
	Trailers     ports.TrailerRepo
	Moves        ports.MoveRepo
	Checks       ports.CheckRepo
	Policies     ports.PolicyRepo
	Locations    ports.Locations
	LocationFeed ports.LocationFeed
	Appointments ports.Appointments
	ApptFeed     ports.AppointmentFeed
	Closures     ports.Closures
	ClosureFeed  ports.ClosureFeed
	Asns         ports.Asns
	AsnFeed      ports.AsnFeed
	Receipts     ports.Receipts
	ReceiptFeed  ports.ReceiptFeed
	Processed    ports.ProcessedEvents
	Idempotency  ports.IdempotencyStore
	Events       ports.EventPublisher
	// PendingEvents lists, in order, the names of the events published and
	// not yet relayed (the in-memory log, or the outbox rows).
	PendingEvents func(t *testing.T) []string
}

// NewStore builds an empty Store for one case.
type NewStore func(t *testing.T) Store

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

// run executes the named cases, each on a fresh Store.
func run(t *testing.T, newStore NewStore, cases map[string]func(t *testing.T, s Store)) {
	t.Helper()
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) { fn(t, newStore(t)) })
	}
}

// Run runs every contract suite.
func Run(t *testing.T, newStore NewStore) {
	t.Helper()
	suites := map[string]func(*testing.T, NewStore){
		"VisitRepo": VisitRepo, "TrailerRepo": TrailerRepo, "MoveRepo": MoveRepo, "CheckRepo": CheckRepo, "PolicyRepo": PolicyRepo,
		"Locations": Locations, "Appointments": Appointments, "Closures": Closures, "Asns": Asns, "Receipts": Receipts,
		"ProcessedEvents": ProcessedEvents, "IdempotencyStore": IdempotencyStore, "UnitOfWork": UnitOfWork,
	}
	for name, suite := range suites {
		t.Run(name, func(t *testing.T) { suite(t, newStore) })
	}
}
