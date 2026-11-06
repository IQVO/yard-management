//go:build integration

package main

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	outboundkafka "github.com/claudioed/yard-management/internal/adapters/outbound/kafka"
	"github.com/claudioed/yard-management/internal/testing/kafkatest"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
)

// The real entry point on real Postgres and Kafka: REST in, rows and exact
// CloudEvents out, then the broker goes away and /readyz says so. The steps
// share one booted process and run in this order (the last two stop things).
func TestAPIOnRealPostgresAndKafka(t *testing.T) {
	// The service's own topics and the three producers' topics it consumes
	// (the test broker has topic auto-creation off).
	for _, topic := range append(outboundkafka.Topics(), topicDockScheduling, topicFacility, topicInboundReceiving) {
		kafkatest.CreateTopic(t, topic)
	}
	a, dsn := boot(t)
	startRelay(t, dsn)
	pool := pgtest.Open(t, dsn)
	// Zones, spots and doors reach the service ONLY through Kafka.
	publishSite(t, pool)

	for _, step := range []struct {
		name string
		run  func(*testing.T, *api, string, *pgxpool.Pool)
	}{
		{"a drop visit is checked in, parked, docked and worked", dropVisitFlow},
		{"check-in directs a trailer with an appointment to its door; ReceiptClosed completes door work only when every ASN is closed", appointmentToDoorAndReceipts},
		{"a NoShow cancels the open TO_DOOR move of the appointment", noShowCancelsToDoorMove},
		{"two moves to one door: exactly one wins", occupancyRace},
		{"one Idempotency-Key runs once even when sent concurrently", concurrentKey},
		{"readiness turns 503 dependency-unavailable when Kafka stops", readinessWhenKafkaStops},
		{"cancelling the context stops the process gracefully", gracefulStop},
	} {
		t.Run(step.name, func(t *testing.T) { step.run(t, a, dsn, pool) })
	}
}

func dropVisitFlow(t *testing.T, a *api, dsn string, pool *pgxpool.Pool) {
	t.Helper()
	visitID, trailerID := a.parked("ABC1D23", "TR-1")
	req := a.mustPost("/moves", map[string]any{"trailerId": trailerID, "to": map[string]string{"kind": "DOOR", "code": "WH1-D02"}}, http.StatusCreated)
	a.mustPost("/moves/claims", map[string]any{"siteId": "WH1", "hostlerRef": "h1"}, http.StatusCreated)
	a.mustPost("/moves/"+req["moveId"].(string)+"/completions", map[string]any{"hostlerRef": "h1"}, http.StatusOK)
	a.mustPost("/trailers/"+trailerID+"/door-work-starts", nil, http.StatusOK)
	done := a.mustPost("/trailers/"+trailerID+"/door-work-completions", map[string]any{"loadState": "EMPTY"}, http.StatusOK)
	if done["state"] != "DOCKED" || done["doorWork"] != "DONE" || done["loadState"] != "EMPTY" {
		t.Fatalf("trailer %v", done)
	}
	// ClearDoorAfterWork queued the hostler move off the door.
	moves := a.call(http.MethodGet, "/moves?state=REQUESTED&trailerId="+trailerID, nil, "").JSON()["items"].([]any)
	if len(moves) != 1 || moves[0].(map[string]any)["reason"] != "FROM_DOOR" {
		t.Fatalf("open moves %v", moves)
	}
	rejected := a.mustPost("/visit-rejections", map[string]any{"siteId": "WH1", "carrier": "Acme Freight", "tractorPlate": "REJ1", "reason": "SITE_REQUEST"}, http.StatusCreated)

	if n := pgtest.Count(t, pool, "yard_visits"); n != 2 {
		t.Errorf("%d visits, want 2", n)
	}
	if n := pgtest.Count(t, pool, "trailers"); n != 1 {
		t.Errorf("%d trailers, want 1", n)
	}

	events := kafkatest.ReadN(t, outboundkafka.TopicEvents, 3, 90*time.Second)
	wantEvent(t, events[0], "VisitCheckedIn", "yardvisit", visitID, map[string]any{
		"visit_id": visitID, "site_id": "WH1", "carrier": "Acme Freight", "carrier_scac": "ACME", "purpose": "DROP", "appointment_id": nil,
		"trailers":   []any{map[string]any{"trailer_id": trailerID, "unit_number": "TR-1", "carrier_scac": "ACME", "kind": "DRY_VAN", "load_state": "LOADED"}},
		"arrived_at": "<time>", "version": 1,
	})
	wantEvent(t, events[1], "TrailerDocked", "trailer", trailerID, map[string]any{
		"trailer_id": trailerID, "site_id": "WH1", "door_code": "WH1-D02", "appointment_id": nil, "asn_numbers": []any{},
		"visit_id": visitID, "docked_at": "<time>", "version": "<version>",
	})
	wantEvent(t, events[2], "VisitRejected", "yardvisit", rejected["visitId"].(string), map[string]any{
		"visit_id": rejected["visitId"], "site_id": "WH1", "carrier_scac": nil, "appointment_id": nil, "reason": "SITE_REQUEST", "rejected_at": "<time>",
	})
	waitOutboxDrained(t, pool)
}

func occupancyRace(t *testing.T, a *api, dsn string, pool *pgxpool.Pool) {
	t.Helper()
	_, first := a.parked("RACE1", "TR-R1")
	_, second := a.parked("RACE2", "TR-R2")
	codes := make([]int, 2)
	slugs := make([]string, 2)
	var wg sync.WaitGroup
	for i, id := range []string{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := a.post("/moves", map[string]any{"trailerId": id, "to": map[string]string{"kind": "DOOR", "code": "WH1-D01"}})
			codes[i] = r.Code
			if r.Code != http.StatusCreated {
				slugs[i] = r.Slug()
			}
		}()
	}
	wg.Wait()
	sort.Ints(codes)
	if codes[0] != http.StatusCreated || codes[1] != http.StatusConflict {
		t.Fatalf("statuses %v, want one 201 and one 409", codes)
	}
	if loser := strings.Join(slugs, ""); loser != "destination-reserved" && loser != "location-occupied" {
		t.Fatalf("the loser's problem is %q", loser)
	}
	if n := pgtest.Count(t, pool, "yard_moves WHERE state IN ('REQUESTED','CLAIMED') AND to_code = 'WH1-D01'"); n != 1 {
		t.Fatalf("%d open moves to the door, want 1", n)
	}
}

func concurrentKey(t *testing.T, a *api, dsn string, pool *pgxpool.Pool) {
	t.Helper()
	const key = "itest-concurrent-key"
	var wg sync.WaitGroup
	var mu sync.Mutex
	codes := map[int]int{}
	replays := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := a.call(http.MethodPost, "/yard-checks", map[string]any{"siteId": "WH1"}, key)
			mu.Lock()
			defer mu.Unlock()
			codes[r.Code]++
			if r.Header.Get("Idempotency-Replayed") == "true" {
				replays++
			}
		}()
	}
	wg.Wait()
	if codes[http.StatusCreated] != 8 || replays != 7 {
		t.Fatalf("statuses %v with %d replays, want eight 201 and 7 replays", codes, replays)
	}
	if n := pgtest.Count(t, pool, "yard_checks"); n != 1 {
		t.Fatalf("%d yard checks, want 1", n)
	}
	reused := a.call(http.MethodPost, "/yard-checks", map[string]any{"siteId": "WH2"}, key)
	if reused.Code != http.StatusConflict || reused.Slug() != "idempotency-key-reused" {
		t.Fatalf("the key with another body: %d %s", reused.Code, reused.Body)
	}
}

func readinessWhenKafkaStops(t *testing.T, a *api, dsn string, pool *pgxpool.Pool) {
	t.Helper()
	if r := a.call(http.MethodGet, "/readyz", nil, ""); r.Code != http.StatusOK {
		t.Fatalf("ready before: %d %s", r.Code, r.Body)
	}
	kafkatest.Stop(t)
	deadline := time.Now().Add(60 * time.Second)
	for {
		r := a.call(http.MethodGet, "/readyz", nil, "")
		if r.Code == http.StatusServiceUnavailable {
			if r.Slug() != "dependency-unavailable" || !strings.Contains(string(r.Body), "kafka") || strings.Contains(string(r.Body), "postgres") {
				t.Fatalf("503 body %s", r.Body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("/readyz stayed %d after Kafka stopped", r.Code)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if r := a.call(http.MethodGet, "/healthz", nil, ""); r.Code != http.StatusOK {
		t.Fatalf("liveness does not depend on Kafka: %d", r.Code)
	}
}

func gracefulStop(t *testing.T, a *api, dsn string, pool *pgxpool.Pool) {
	t.Helper()
	a.stop()
	select {
	case err := <-a.done:
		if err != nil {
			t.Fatalf("run = %v, want nil after a graceful stop", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("run did not stop")
	}
	a.done <- nil
}

// waitOutboxDrained waits until the relay deleted every outbox row it relayed.
func waitOutboxDrained(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for pgtest.Count(t, pool, "outbox") != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the outbox was never drained")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
