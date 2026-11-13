//go:build integration

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	// Real Postgres and Kafka come from the shared testcontainers helpers;
	// the blank imports keep the fleet rule visible from this package.
	_ "github.com/testcontainers/testcontainers-go/modules/kafka"
	_ "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/claudioed/yard-management/internal/adapters/outbound/analyticsstore"
	outboundkafka "github.com/claudioed/yard-management/internal/adapters/outbound/kafka"
	"github.com/claudioed/yard-management/internal/application/outbox"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
	"github.com/claudioed/yard-management/internal/testing/kafkatest"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
	"github.com/claudioed/yard-management/internal/testing/repocontract"
)

func TestMain(m *testing.M) {
	code := m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
	os.Exit(code)
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func probe200(addr string) func() bool {
	return func() bool {
		resp, err := http.Get("http://" + addr)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}
}

// The real entry point on an EMPTY analytical database and a testcontainers
// broker: run() applies the embedded analytics migrations itself (ADR 0006
// §3), serves /healthz and /readyz on ADMIN_ADDR, and its consumer folds the
// events the repo's own encoder wrote to the analytics topic. The journey is
// one visit + trailer (check-in, dock, door work, undock, depart, check-out,
// ADR 0006 §1) and one hostler move; every other published type on the topic
// is acknowledged and ignored. A cancelled context drains to a clean exit.
func TestProjectorBootsMigratesProjectsAndStopsCleanly(t *testing.T) {
	kafkatest.CreateTopic(t, outboundkafka.TopicAnalytics)
	url := pgtest.EmptyURL(t)

	t0 := repocontract.T0
	dockAt := t0.Add(10 * time.Minute)
	workStart := t0.Add(15 * time.Minute)
	workDone := t0.Add(35 * time.Minute)
	undockAt := t0.Add(40 * time.Minute)
	trailerGone := t0.Add(90 * time.Minute)
	visitGone := t0.Add(95 * time.Minute)
	moveDone := t0.Add(8 * time.Minute)

	visit := repocontract.NewVisit(t, "visit-p1", "WH1", "PLATE-1", 0, repocontract.Ref("trl-p1", "UNIT-1"))
	if err := visit.CheckOut([]yardvisit.Departure{{TrailerID: "trl-p1", OnSite: true, Released: true}}, visitGone); err != nil {
		t.Fatal(err)
	}
	tr := repocontract.Arrived(t, "trl-p1", "WH1", "UNIT-1", 0)
	repocontract.ArriveAt(t, tr, repocontract.Door("WH1-D01"), 10*time.Minute)
	if err := tr.StartDoorWork(workStart); err != nil {
		t.Fatal(err)
	}
	if err := tr.CompleteDoorWork(shared.LoadEmpty, workDone); err != nil {
		t.Fatal(err)
	}
	if err := tr.StartMoving(undockAt); err != nil {
		t.Fatal(err)
	}
	repocontract.ArriveAt(t, tr, repocontract.Spot("WH1-Y01"), 45*time.Minute)
	repocontract.Depart(t, tr, 90*time.Minute)
	mv := repocontract.NewMove(t, "mv-p1", "trl-p2", repocontract.Spot("WH1-Y02"), 2, t0.Add(30*time.Minute), 0)
	repocontract.Claim(t, mv, "hostler-p1", 5*time.Minute)
	if err := mv.Complete(yardmove.CompleteInput{Hostler: "hostler-p1", At: moveDone}); err != nil {
		t.Fatal(err)
	}

	// The repo's own path to the wire: domain event -> outbox row -> the
	// encoder of ADR 0004/0005 -> the analytics twin topic. Only the
	// analytics-destination message of each row is produced (the projector
	// never reads the integration topic, ADR 0006 "Alternatives rejected").
	var events []shared.Event
	for _, e := range [][]shared.Event{visit.PullEvents(), tr.PullEvents(), mv.PullEvents()} {
		events = append(events, e...)
	}
	for i, e := range events {
		payload, err := outbox.Payload(e)
		if err != nil {
			t.Fatal(err)
		}
		row := outbox.Row{EventID: fmt.Sprintf("projector-itest-%d", i+1), AggregType: e.Entity(),
			AggregID: e.AggregateID(), Payload: payload, OccurredAt: e.OccurredAt()}
		msgs, cause, ok := outboundkafka.NewEncoder().Encode(row)
		if !ok {
			t.Fatalf("encode %s.%s: %s", e.Entity(), e.EventName(), cause)
		}
		for _, msg := range msgs {
			if msg.Topic != outboundkafka.TopicAnalytics {
				continue
			}
			headers := map[string]string{}
			for _, h := range msg.Headers {
				headers[h.Key] = h.Value
			}
			kafkatest.Produce(t, msg.Topic, kafkatest.Message{Key: string(msg.Key), Value: msg.Value, Headers: headers})
		}
	}

	addr := freeAddr(t)
	env := map[string]string{
		"ANALYTICS_DATABASE_URL":   url,
		"KAFKA_BROKERS":            strings.Join(kafkatest.Brokers(t), ","),
		"ANALYTICS_CONSUMER_GROUP": fmt.Sprintf("yard-projector-itest-%d", time.Now().UnixNano()),
		"ADMIN_ADDR":               addr,
		"LOG_LEVEL":                "error",
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, func(k string) string { return env[k] }) }()

	waitUntil(t, "/healthz 200", probe200(addr+"/healthz"))
	waitUntil(t, "/readyz 200", probe200(addr+"/readyz"))

	pool, err := analyticsstore.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	q := func(sql string, args ...any) int {
		var n int
		if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
			return -1
		}
		return n
	}
	// The documented folds (ADR 0006 §2; the visit facts of ADR 0011 §3):
	// trailer_facts from the check-in and the departure, the door docking
	// opened by TrailerDocked and closed by TrailerUndocked, the door work
	// session, the hostler move and the gate-to-gate visit fact.
	waitUntil(t, "the documented projection of the journey", func() bool {
		return q(`SELECT
			(SELECT count(*) FROM analytics.trailer_facts WHERE trailer_id='trl-p1' AND kind='DRY_VAN' AND unit_number='UNIT-1'
				AND visit_id='visit-p1' AND arrived_at=$1 AND first_door_work_started_at=$2 AND departed_at=$3 AND dwell_minutes=90)
			+ (SELECT count(*) FROM analytics.door_dockings WHERE trailer_id='trl-p1' AND site_id='WH1' AND door_code='WH1-D01'
				AND docked_at=$4 AND undocked_at=$5)
			+ (SELECT count(*) FROM analytics.door_work WHERE trailer_id='trl-p1' AND site_id='WH1' AND door_code='WH1-D01'
				AND started_at=$2 AND completed_at=$6)
			+ (SELECT count(*) FROM analytics.hostler_moves WHERE move_id='mv-p1' AND site_id='WH1' AND trailer_id='trl-p2'
				AND hostler_ref='hostler-p1' AND completed_at=$7 AND duration_seconds=180)
			+ (SELECT count(*) FROM analytics.visit_facts WHERE visit_id='visit-p1' AND site_id='WH1' AND purpose='LIVE_UNLOAD'
				AND arrived_at=$1 AND departed_at=$8 AND on_site_minutes=95)`,
			t0, workStart, trailerGone, dockAt, undockAt, workDone, moveDone, visitGone) == 5
	})
	// Exactly the eight events of the folded kinds were claimed in
	// analytics.processed_events (the dedupe set, ADR 0006 §2); the other
	// published types of the journey were ignored, as documented.
	waitUntil(t, "processed_events to hold exactly the eight folded kinds", func() bool {
		return q(`SELECT count(*) FROM analytics.processed_events`) == 8
	})

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a cancelled projector must drain cleanly, got %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the projector did not stop after the context was cancelled")
	}
}
