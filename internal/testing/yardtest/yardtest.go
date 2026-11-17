// Package yardtest wires the use cases over the in-memory store for unit and
// BDD tests, with seeding helpers for the event-fed local copies. It is test
// support only.
package yardtest

import (
	"context"
	"time"

	"github.com/claudioed/yard-management/internal/adapters/outbound/memory"
	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
)

// T0 is the instant every test environment starts at.
var T0 = time.Date(2026, 11, 5, 14, 0, 0, 0, time.UTC)

// Env is a Yard over a fresh in-memory store.
type Env struct {
	Yard  *usecases.Yard
	Store *memory.Store
	Ctx   context.Context
}

// Deps wires every port of the use cases to the store.
func Deps(s *memory.Store) usecases.Deps {
	return usecases.Deps{
		UoW: s, Visits: s.Visits(), Trailers: s.Trailers(), Moves: s.Moves(), Checks: s.Checks(), Policies: s.Policies(), Inspections: s.Inspections(), Templates: s.Templates(),
		Locations: s.Locations(), Appointments: s.Appointments(), Closures: s.Closures(), Asns: s.Asns(), Receipts: s.Receipts(),
		Locks: s, Processed: s, ApptFeed: s.Appointments(), LocationFeed: s.Locations(), ClosureFeed: s.Closures(), AsnFeed: s.Asns(),
		ReceiptFeed: s.Receipts(), Carriers: s.Carriers(), CarrierFeed: s.Carriers(), Events: s, IDs: s, Clock: s,
	}
}

// New builds an environment whose clock reads T0.
func New() *Env {
	s := memory.NewStore(T0)
	return &Env{Yard: usecases.New(Deps(s)), Store: s, Ctx: context.Background()}
}

// Site seeds site siteCode with the yard spots and dock doors given: spots by
// code, doors as code -> dock flow. Zones are "<site>-yard" and "<site>-dock".
func (e *Env) Site(siteCode string, spots []string, doors map[string]string) {
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	feed := e.Store.Locations()
	must(feed.ApplyZone(e.Ctx, siteCode+"-yard", siteCode))
	must(feed.ApplyZone(e.Ctx, siteCode+"-dock", siteCode))
	for _, code := range spots {
		must(feed.ApplySlot(e.Ctx, code, siteCode+"-yard", shared.Spot, "", shared.SpotKindUnknown))
	}
	for code, flow := range doors {
		must(feed.ApplySlot(e.Ctx, code, siteCode+"-dock", shared.Door, flow, shared.SpotKindUnknown))
	}
}

// SpotKind registers the spot code of siteCode again with the facility-layout
// spotKind kind, as a LocationSlotRegistered event carrying it would (ADR 0014).
func (e *Env) SpotKind(siteCode, code string, kind shared.SpotKind) {
	if err := e.Store.Locations().ApplySlot(e.Ctx, code, siteCode+"-yard", shared.Spot, "", kind); err != nil {
		panic(err)
	}
}

// Book seeds a BOOKED appointment copy.
func (e *Env) Book(a views.Appointment) {
	if a.Status == "" {
		a.Status = views.ApptBooked
	}
	if a.Version == 0 {
		a.Version = 1
	}
	if a.ToleranceMinutes == 0 {
		a.ToleranceMinutes = 15
	}
	if err := e.Store.Appointments().Apply(e.Ctx, a); err != nil {
		panic(err)
	}
}
