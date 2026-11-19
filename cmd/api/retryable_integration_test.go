//go:build integration

package main

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/yard-management/internal/testing/pgtest"
)

// keyRows is how many rows the Idempotency-Key store holds for key.
func keyRows(t *testing.T, pool *pgxpool.Pool, key string) int {
	t.Helper()
	return pgtest.Count(t, pool, "idempotency_keys WHERE key = '"+key+"'")
}

// writerAnswer is what one of the racing writers got.
type writerAnswer struct {
	key  string
	body map[string]any
	r    reply
}

// raceSeals sends n seal records for one trailer at once, each under its own
// key. They all read the same version of the trailer, so some of them lose the
// optimistic lock.
func raceSeals(a *api, trailerID string, round, n int) []writerAnswer {
	answers := make([]writerAnswer, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := map[string]any{"state": "INTACT", "sealNumber": fmt.Sprintf("SEAL-%d-%d", round, i)}
			key := fmt.Sprintf("itest-race-%d-%d", round, i)
			answers[i] = writerAnswer{key: key, body: body, r: a.call(http.MethodPost, "/trailers/"+trailerID+"/seals", body, key)}
		}()
	}
	wg.Wait()
	return answers
}

// concurrentModificationIsNotStored drives the RETRYABLE class through the real
// router, the real Idempotency-Key middleware and the real Postgres unit of
// work: two writers change one trailer, the loser answers 409
// concurrent-modification, and that refusal must not be remembered under its
// key. The same request under the same key then runs again and succeeds; its
// success is stored and replayed.
func concurrentModificationIsNotStored(t *testing.T, a *api, _ string, pool *pgxpool.Pool) {
	t.Helper()
	_, trailerID := a.parked("RTRYCM1", "TR-RTRY-CM1")

	var loser writerAnswer
	for round := 0; loser.key == "" && round < 40; round++ {
		for _, w := range raceSeals(a, trailerID, round, 8) {
			switch {
			case w.r.Code == http.StatusOK:
			case w.r.Code == http.StatusConflict && w.r.Slug() == "concurrent-modification":
				if loser.key == "" {
					loser = w
				}
			default:
				t.Fatalf("a racing seal record answered %d %s, want 200 or 409 concurrent-modification", w.r.Code, w.r.Body)
			}
		}
	}
	if loser.key == "" {
		t.Fatal("no writer lost the optimistic lock in 40 rounds of 8 concurrent seal records")
	}
	if n := keyRows(t, pool, loser.key); n != 0 {
		t.Fatalf("the refused request left %d row(s) under its key: a 409 concurrent-modification must not be stored", n)
	}

	retry := a.call(http.MethodPost, "/trailers/"+trailerID+"/seals", loser.body, loser.key)
	if retry.Code != http.StatusOK || retry.Header.Get("Idempotency-Replayed") != "" {
		t.Fatalf("retry under the same key = %d replayed=%q %s, want 200 from a fresh run", retry.Code, retry.Header.Get("Idempotency-Replayed"), retry.Body)
	}
	if n := keyRows(t, pool, loser.key); n != 1 {
		t.Fatalf("%d row(s) under the key after the successful retry, want 1", n)
	}
	again := a.call(http.MethodPost, "/trailers/"+trailerID+"/seals", loser.body, loser.key)
	if again.Code != http.StatusOK || again.Header.Get("Idempotency-Replayed") != "true" || string(again.Body) != string(retry.Body) {
		t.Fatalf("second retry = %d replayed=%q %s, want the stored success %s", again.Code, again.Header.Get("Idempotency-Replayed"), again.Body, retry.Body)
	}
}

// visitAlreadyOpenIsNotStored is the second RETRYABLE case: a check-in of a
// plate whose earlier visit is still open is refused 409 visit-already-open
// and that refusal is not stored; once the earlier visit is checked out the
// same request under the same key is admitted.
func visitAlreadyOpenIsNotStored(t *testing.T, a *api, _ string, pool *pgxpool.Pool) {
	t.Helper()
	first := a.dropVisit("RTRYVO1", "TR-RTRY-VO1")
	earlier := first["visit"].(map[string]any)["visitId"].(string)
	checkIn := map[string]any{
		"siteId": "WH1", "carrier": "Acme Freight", "carrierScac": "ACME", "tractorPlate": "RTRYVO1", "purpose": "DROP",
		"trailers": []map[string]any{{"unitNumber": "TR-RTRY-VO2", "kind": "DRY_VAN", "loadState": "LOADED"}},
	}

	const key = "itest-visit-already-open"
	refused := a.call(http.MethodPost, "/visits", checkIn, key)
	if refused.Code != http.StatusConflict || refused.Slug() != "visit-already-open" {
		t.Fatalf("second check-in of one plate = %d %s, want 409 visit-already-open", refused.Code, refused.Body)
	}
	if n := keyRows(t, pool, key); n != 0 {
		t.Fatalf("the refused check-in left %d row(s) under its key, want 0", n)
	}

	a.mustPost("/visits/"+earlier+"/check-outs", map[string]any{"trailersOut": []map[string]string{}}, http.StatusOK)

	retry := a.call(http.MethodPost, "/visits", checkIn, key)
	if retry.Code != http.StatusCreated || retry.Header.Get("Idempotency-Replayed") != "" {
		t.Fatalf("retry after the first visit left = %d replayed=%q %s, want 201 from a fresh run", retry.Code, retry.Header.Get("Idempotency-Replayed"), retry.Body)
	}
	again := a.call(http.MethodPost, "/visits", checkIn, key)
	if again.Code != http.StatusCreated || again.Header.Get("Idempotency-Replayed") != "true" || string(again.Body) != string(retry.Body) {
		t.Fatalf("second retry = %d replayed=%q, want the stored 201", again.Code, again.Header.Get("Idempotency-Replayed"))
	}
}
