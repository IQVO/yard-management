package http_test

import (
	"net/http"
	"testing"

	"github.com/claudioed/yard-management/internal/testing/yardtest"
)

// The unit of work under these tests is the in-memory Store, and it has the
// SAME join-and-rollback semantics as postgres.UnitOfWork, which is what
// decides the outcome of the middleware's handling of a refusal:
//
//	postgres/db.go  Do: `if _, ok := pgtx.From(ctx); ok { return fn(ctx) }` (JOIN the open
//	                transaction), `if err := fn(...); err != nil { return err }` and the
//	                deferred tx.Rollback (a non-nil return from the outermost Do ROLLS BACK).
//	memory/memory.go Do: `if ctx.Value(ctxKey{}) != nil { return fn(ctx) }` (JOIN), and
//	                `if err := fn(...); err != nil { s.st = before; return err }` (the snapshot
//	                taken by the outermost Do is restored: ROLLBACK).
//
// The router, the Idempotency-Key middleware and the use cases are the real
// ones, so the nested Yard.atomically of CheckInVisit joins the middleware's
// unit of work exactly as it does on Postgres.

// suspendedCarrierRig is a site under the REJECT policy and a carrier the
// copy says is suspended.
func suspendedCarrierRig(t *testing.T) *rig {
	t.Helper()
	g := newRig(t)
	g.seedSite()
	policy := map[string]any{"requireAppointment": false, "sealMismatchAction": "FLAG", "leaseSeconds": 300, "suspendedCarrierAction": "REJECT"}
	g.mustCode(g.do(http.MethodPut, "/sites/WH1/yard-policy", policy, "Idempotency-Key", g.freshKey()), http.StatusOK)
	if err := g.env.Store.Carriers().Suspend(g.env.Ctx, "ACME", "insurance lapsed", 2, yardtest.T0); err != nil {
		t.Fatal(err)
	}
	return g
}

// dropVisitKeyed checks a DROP visit of ACME in under an explicit key.
func (g *rig) dropVisitKeyed(key, plate, unit string) reply {
	g.t.Helper()
	return g.do(http.MethodPost, "/visits", map[string]any{
		"siteId": "WH1", "carrier": "Acme Freight", "carrierScac": "ACME", "tractorPlate": plate, "purpose": "DROP",
		"trailers": []map[string]any{{"unitNumber": unit, "kind": "DRY_VAN", "loadState": "LOADED"}},
	}, "Idempotency-Key", key)
}

// rejectedVisits and visitRejectedEvents are what the turn-away leaves on record.
func (g *rig) rejectedVisits() []any {
	g.t.Helper()
	return g.mustCode(g.get("/visits?siteId=WH1&state=REJECTED"), http.StatusOK).JSON()["items"].([]any)
}

func (g *rig) visitRejectedEvents() int {
	n := 0
	for _, e := range g.env.Store.Events() {
		if e.EventName() == "VisitRejected" {
			n++
		}
	}
	return n
}

// TestSuspendedCarrierTurnAwayIsCommittedAndStoredUnderTheKey is ADR 0016's
// promise over REST: the 409 carrier-suspended answers a turn-away that is on
// record, and a replay of the key returns the stored 409 and records nothing.
func TestSuspendedCarrierTurnAwayIsCommittedAndStoredUnderTheKey(t *testing.T) {
	g := suspendedCarrierRig(t)
	first := g.dropVisitKeyed("k-susp", "TRK1", "TR-1")
	if first.Code != http.StatusConflict || first.Slug() != "carrier-suspended" {
		t.Fatalf("check-in of a suspended carrier: %d %s, want 409 carrier-suspended", first.Code, first.Body)
	}
	visits := g.rejectedVisits()
	if len(visits) != 1 || visits[0].(map[string]any)["rejectionReason"] != "CARRIER_SUSPENDED" {
		t.Fatalf("REJECTED visits after the 409: %v, want exactly one with reason CARRIER_SUSPENDED", visits)
	}
	if n := g.visitRejectedEvents(); n != 1 {
		t.Fatalf("%d VisitRejected events after the 409, want 1", n)
	}

	again := g.dropVisitKeyed("k-susp", "TRK1", "TR-1")
	if again.Code != http.StatusConflict || again.Header.Get("Idempotency-Replayed") != "true" || string(again.Body) != string(first.Body) {
		t.Fatalf("replay: %d replayed=%q %s, want the stored 409 %s", again.Code, again.Header.Get("Idempotency-Replayed"), again.Body, first.Body)
	}
	if n := len(g.rejectedVisits()); n != 1 || g.visitRejectedEvents() != 1 {
		t.Fatalf("a replay recorded something: %d visits, %d events, want 1 and 1", n, g.visitRejectedEvents())
	}
}

// TestOtherRefusalsOfTheGateStillRollBackTheirWrites pins that only the
// carrier-suspended refusal commits: a plate that is already open is refused
// with 409 visit-already-open, nothing of that request is recorded, and (a
// RETRYABLE refusal, ADR 0019) it is not stored under its key.
func TestOtherRefusalsOfTheGateStillRollBackTheirWrites(t *testing.T) {
	g := newRig(t)
	g.seedSite()
	g.mustCode(g.dropVisitKeyed("k-open", "TRK1", "TR-1"), http.StatusCreated)
	events := len(g.env.Store.Events())
	first := g.dropVisitKeyed("k-dup", "TRK1", "TR-2")
	if first.Code != http.StatusConflict || first.Slug() != "visit-already-open" {
		t.Fatalf("second check-in of one plate: %d %s", first.Code, first.Body)
	}
	if n := len(g.env.Store.Events()); n != events {
		t.Fatalf("a refused check-in left %d new events", n-events)
	}
	again := g.dropVisitKeyed("k-dup", "TRK1", "TR-2")
	if again.Code != http.StatusConflict || again.Header.Get("Idempotency-Replayed") != "" || string(again.Body) != string(first.Body) {
		t.Fatalf("the retry of a RETRYABLE refusal: %d replayed=%q %s, want a fresh run that refuses again", again.Code, again.Header.Get("Idempotency-Replayed"), again.Body)
	}
}
