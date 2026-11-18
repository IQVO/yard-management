//go:build integration

package main

import (
	"net/http"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/yard-management/internal/testing/pgtest"
)

// suspendedCarrierTurnAway drives ADR 0016's promise through the real stack:
// the real HTTP router, the real Idempotency-Key middleware and the real
// Postgres unit of work. A check-in of a SUSPENDED carrier under the REJECT
// policy answers 409 carrier-suspended AND the turn-away is on record: one
// REJECTED visit (reason CARRIER_SUSPENDED) and one VisitRejected outbox row.
// The same request under the same key replays the stored 409 and records
// nothing new, also when eight copies of it arrive at once.
func suspendedCarrierTurnAway(t *testing.T, a *api, _ string, pool *pgxpool.Pool) {
	t.Helper()
	const site = "WH9" // its own site: the other steps' WH1 policy stays untouched
	policy := map[string]any{"requireAppointment": false, "sealMismatchAction": "FLAG", "leaseSeconds": 300, "suspendedCarrierAction": "REJECT"}
	if r := a.call(http.MethodPut, "/sites/"+site+"/yard-policy", policy, a.key()); r.Code != http.StatusOK {
		t.Fatalf("declare the policy: %d %s", r.Code, r.Body)
	}
	if err := pgtest.Exec(pool, `INSERT INTO carriers (scac, name, status, reason, version, updated_at)
		VALUES ('SUSP', 'Suspended Freight', 'SUSPENDED', 'insurance lapsed', 2, now())`); err != nil {
		t.Fatal(err)
	}
	checkIn := func(plate string) map[string]any {
		return map[string]any{
			"siteId": site, "carrier": "Suspended Freight", "carrierScac": "SUSP", "tractorPlate": plate, "purpose": "DROP",
			"trailers": []map[string]any{{"unitNumber": "SUSP-" + plate, "kind": "DRY_VAN", "loadState": "LOADED"}},
		}
	}
	rejected := func(plate string) int {
		return pgtest.Count(t, pool, "yard_visits WHERE site_id = '"+site+"' AND tractor_plate = '"+plate+
			"' AND state = 'REJECTED' AND rejection_reason = 'CARRIER_SUSPENDED'")
	}
	events := func(plate string) int {
		return pgtest.Count(t, pool, "outbox WHERE payload->>'type' = 'VisitRejected' AND aggreg_id IN "+
			"(SELECT visit_id FROM yard_visits WHERE site_id = '"+site+"' AND tractor_plate = '"+plate+"')")
	}

	const key = "itest-suspended-carrier"
	first := a.call(http.MethodPost, "/visits", checkIn("SUSP-1"), key)
	if first.Code != http.StatusConflict || first.Slug() != "carrier-suspended" {
		t.Fatalf("check-in of a suspended carrier = %d %s, want 409 carrier-suspended", first.Code, first.Body)
	}
	if n := rejected("SUSP-1"); n != 1 {
		t.Fatalf("%d REJECTED visits with reason CARRIER_SUSPENDED after the 409, want exactly 1: the answer says it is recorded", n)
	}
	if n := events("SUSP-1"); n != 1 {
		t.Fatalf("%d VisitRejected outbox rows after the 409, want exactly 1", n)
	}

	again := a.call(http.MethodPost, "/visits", checkIn("SUSP-1"), key)
	if again.Code != http.StatusConflict || again.Header.Get("Idempotency-Replayed") != "true" || string(again.Body) != string(first.Body) {
		t.Fatalf("replay = %d replayed=%q %s, want the stored 409 %s", again.Code, again.Header.Get("Idempotency-Replayed"), again.Body, first.Body)
	}
	if rejected("SUSP-1") != 1 || events("SUSP-1") != 1 {
		t.Fatalf("a replay recorded something new: %d visits, %d events", rejected("SUSP-1"), events("SUSP-1"))
	}

	// Eight concurrent copies of one more request: one runs, seven replay.
	const burst = "itest-suspended-carrier-burst"
	var wg sync.WaitGroup
	var mu sync.Mutex
	replays := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := a.call(http.MethodPost, "/visits", checkIn("SUSP-2"), burst)
			mu.Lock()
			defer mu.Unlock()
			if r.Code != http.StatusConflict || r.Slug() != "carrier-suspended" {
				t.Errorf("concurrent copy = %d %s, want 409 carrier-suspended", r.Code, r.Body)
			}
			if r.Header.Get("Idempotency-Replayed") == "true" {
				replays++
			}
		}()
	}
	wg.Wait()
	if replays != 7 || rejected("SUSP-2") != 1 || events("SUSP-2") != 1 {
		t.Fatalf("burst: %d replays, %d visits, %d events, want 7, 1 and 1", replays, rejected("SUSP-2"), events("SUSP-2"))
	}
}
