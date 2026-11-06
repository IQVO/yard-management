//go:build integration

package kafka_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	kafkago "github.com/segmentio/kafka-go"

	// The broker and the database come from testcontainers through
	// kafkatest / pgtest; the blank imports keep the fleet rule visible from
	// every file that touches Kafka or pgx.
	_ "github.com/testcontainers/testcontainers-go/modules/kafka"
	_ "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/claudioed/yard-management/internal/adapters/inbound/kafka"
	"github.com/claudioed/yard-management/internal/adapters/outbound/postgres"
	"github.com/claudioed/yard-management/internal/adapters/outbound/system"
	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/testing/kafkatest"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
)

// itSeq mints unique ids (event ids, group ids) per test binary.
var itSeq atomic.Int64

func itID(prefix string) string { return fmt.Sprintf("%s-%d", prefix, itSeq.Add(1)) }

// theGroup mints a unique consumer group id (like the environment variable a
// deployment sets).
func theGroup(name string) string { return itID("itest-" + name) }

// flakyUoW runs the unit of work and then rolls it back with a transient error
// for the first `failures` calls: the claim and the effect were written and
// are undone together, exactly what a lost connection at commit does.
type flakyUoW struct {
	inner    ports.UnitOfWork
	failures atomic.Int32
	calls    atomic.Int32
}

var errCommitLost = errors.New("connection lost before commit")

func (u *flakyUoW) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	u.calls.Add(1)
	return u.inner.Do(ctx, func(ctx context.Context) error {
		if err := fn(ctx); err != nil {
			return err
		}
		if u.failures.Add(-1) >= 0 {
			return errCommitLost
		}
		return nil
	})
}

// world is the production Postgres wiring of the use cases over a fresh,
// migrated database of the shared testcontainers Postgres.
type world struct {
	pool      *pgxpool.Pool
	yard      *usecases.Yard
	uow       *flakyUoW
	locations *postgres.Locations
	appts     *postgres.Appointments
	closures  *postgres.Closures
	receipts  *postgres.Receipts
	asns      *postgres.Asns
}

func newWorld(t *testing.T) *world {
	t.Helper()
	pool := pgtest.NewPool(t)
	uow := &flakyUoW{inner: postgres.NewUnitOfWork(pool)}
	locations, appointments, closures := postgres.NewLocations(pool), postgres.NewAppointments(pool), postgres.NewClosures(pool)
	asns, receipts := postgres.NewAsns(pool), postgres.NewReceipts(pool)
	yard := usecases.New(usecases.Deps{
		UoW: uow, Visits: postgres.NewVisits(pool), Trailers: postgres.NewTrailers(pool), Moves: postgres.NewMoves(pool),
		Checks: postgres.NewChecks(pool), Policies: postgres.NewPolicies(pool),
		Locations: locations, Appointments: appointments, Closures: closures, Asns: asns, Receipts: receipts,
		Locks: postgres.NewLocks(), Processed: postgres.NewProcessedEvents(pool),
		ApptFeed: appointments, LocationFeed: locations, ClosureFeed: closures, AsnFeed: asns, ReceiptFeed: receipts,
		Events: postgres.NewOutbox(pool), IDs: system.NewIDGen(), Clock: system.NewClock(),
	})
	return &world{pool: pool, yard: yard, uow: uow, locations: locations, appts: appointments, closures: closures, receipts: receipts, asns: asns}
}

func (w *world) count(t *testing.T, table string) int {
	t.Helper()
	return pgtest.Count(t, w.pool, table)
}

// seedSite writes the facility facts of site WH1 straight through the use
// cases: spots WH1-SPOT-01..03 and the Both-flow doors WH1-DOCK-IN-01 / -02.
func (w *world) seedSite(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed site: %v", err)
		}
	}
	must(w.yard.ApplyZone(ctx, itID("seed"), "WH1-YARD", "WH1"))
	must(w.yard.ApplyZone(ctx, itID("seed"), "WH1-DOCK", "WH1"))
	for _, code := range []string{"WH1-SPOT-01", "WH1-SPOT-02", "WH1-SPOT-03", "WH1-SPOT-04", "WH1-SPOT-05", "WH1-SPOT-06"} {
		must(w.yard.ApplySlot(ctx, itID("seed"), code, "WH1-YARD", "Yard", ""))
	}
	for _, code := range []string{"WH1-DOCK-IN-01", "WH1-DOCK-IN-02", "WH1-DOCK-IN-03", "WH1-DOCK-IN-04", "WH1-DOCK-IN-05"} {
		must(w.yard.ApplySlot(ctx, itID("seed"), code, "WH1-DOCK", "Dock", "Both"))
	}
}

// topics creates a unique events topic and its dead letter topic (the test
// broker has topic auto-creation off).
func topics(t *testing.T, prefix string) (topic, dlq string) {
	t.Helper()
	topic = kafkatest.Topic(t, prefix)
	dlq = kafka.DeadLetterTopic(topic)
	kafkatest.CreateTopic(t, dlq)
	return topic, dlq
}

// produce publishes values to topic under key, in order.
func produce(t *testing.T, topic, key string, values ...[]byte) {
	t.Helper()
	msgs := make([]kafkatest.Message, len(values))
	for i, v := range values {
		msgs[i] = kafkatest.Message{Key: key, Value: v}
	}
	kafkatest.Produce(t, topic, msgs...)
}

// startConsumer runs c until stop() is called or the test ends, and returns
// stop (idempotent): it cancels the consumer, waits for Run to return and
// closes it.
func startConsumer(t *testing.T, c *kafka.Consumer) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var runErr error
	go func() { defer close(done); runErr = c.Run(ctx) }()
	var stopped atomic.Bool
	stop = func() {
		if !stopped.CompareAndSwap(false, true) {
			return
		}
		cancel()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Errorf("the %s did not stop after the context cancellation", c.Name)
			return
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Errorf("the %s died during the test: %v", c.Name, runErr)
		}
		if err := c.Close(); err != nil {
			t.Errorf("close the %s: %v", c.Name, err)
		}
	}
	t.Cleanup(stop)
	return stop
}

// eventually polls cond until it holds or the bound expires.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// committedPast waits until the group committed `want` offsets of topic: every
// message, the skipped ones (non-CloudEvent, unknown type) and the
// dead-lettered poison included.
func committedPast(t *testing.T, group, topic string, want int64) {
	t.Helper()
	eventually(t, fmt.Sprintf("%d committed offsets of %s", want, topic), func() bool {
		return committedOffset(t, group, topic) == want
	})
}

// committedOffset is the offset the group has committed on partition 0 of
// topic, or -1 while the group has not joined or committed yet (the shared
// kafkatest helper fails the test in that window, which a poll must survive).
func committedOffset(t *testing.T, group, topic string) int64 {
	t.Helper()
	client := &kafkago.Client{Addr: kafkago.TCP(kafkatest.Brokers(t)...), Timeout: 10 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := client.OffsetFetch(ctx, &kafkago.OffsetFetchRequest{GroupID: group, Topics: map[string][]int{topic: {0}}})
	if err != nil {
		return -1
	}
	for _, p := range resp.Topics[topic] {
		if p.Partition == 0 && p.Error == nil {
			return p.CommittedOffset
		}
	}
	return -1
}

func factory(t *testing.T) kafka.Factory {
	t.Helper()
	return kafka.NewFactory(kafkatest.Brokers(t), quietLogger())
}

func mustConsumer(t *testing.T, c *kafka.Consumer, err error) *kafka.Consumer {
	t.Helper()
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	c.Retry = kafka.RetryPolicy{Initial: 50 * time.Millisecond, Max: 200 * time.Millisecond}
	return c
}

func (w *world) dockConsumer(t *testing.T, topic, group string) *kafka.Consumer {
	t.Helper()
	c, err := kafka.NewDockSchedulingConsumerForTopic(factory(t), topic, group, w.yard, quietLogger())
	return mustConsumer(t, c, err)
}

func (w *world) facilityConsumer(t *testing.T, topic, group string) *kafka.Consumer {
	t.Helper()
	c, err := kafka.NewFacilityConsumerForTopic(factory(t), topic, group, w.yard, quietLogger())
	return mustConsumer(t, c, err)
}

func (w *world) inboundConsumer(t *testing.T, topic, group string) *kafka.Consumer {
	t.Helper()
	c, err := kafka.NewInboundReceivingConsumerForTopic(factory(t), topic, group, w.yard, quietLogger())
	return mustConsumer(t, c, err)
}

// endOffset is the number of messages ever written to the single partition of
// topic (the offset the next message will get).
func endOffset(t *testing.T, topic string) int64 {
	t.Helper()
	conn, err := kafkago.DialLeader(context.Background(), "tcp", kafkatest.Brokers(t)[0], topic, 0)
	if err != nil {
		t.Fatalf("dial the leader of %s: %v", topic, err)
	}
	defer func() { _ = conn.Close() }()
	last, err := conn.ReadLastOffset()
	if err != nil {
		t.Fatalf("last offset of %s: %v", topic, err)
	}
	return last
}

// in is the instant d from now, truncated to the second (the wire has no finer
// resolution worth asserting).
func in(d time.Duration) time.Time { return time.Now().UTC().Add(d).Truncate(time.Second) }

func rfc(ts time.Time) string { return ts.UTC().Format(time.RFC3339) }
