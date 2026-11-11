//go:build integration

package kafka_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/adapters/kafka/cloudevents"
	outboundkafka "github.com/claudioed/yard-management/internal/adapters/outbound/kafka"
	"github.com/claudioed/yard-management/internal/adapters/outbound/postgres"
	"github.com/claudioed/yard-management/internal/testing/kafkatest"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
)

// The relay is proven end to end against real infrastructure, both started
// with testcontainers (never skip-gated, never a hardcoded broker address):
// the production Outbox publisher writes rows inside a real unit of work, the
// production OutboxRelay drains them through the production encoder and
// RelaySink to a real broker on explicitly created topics, and the CloudEvents
// are read back and compared with the hand-written envelopes of the golden
// unit test - the SAME cases, so the wire contract has one source of truth.

func itctx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func assertConsumed(t *testing.T, msg kafkatest.Message, c goldenCase, id, stream string) {
	t.Helper()
	if msg.Key != c.subject {
		t.Errorf("%s: key = %q, want %q", c.name, msg.Key, c.subject)
	}
	if len(msg.Headers) != 1 || msg.Headers["content-type"] != cloudevents.MediaType {
		t.Errorf("%s: headers = %v, want exactly the CloudEvents content-type", c.name, msg.Headers)
	}
	if _, err := cloudevents.Decode(msg.Value); err != nil {
		t.Fatalf("%s: not a valid CloudEvent: %v", c.name, err)
	}
	want := wantEnvelope(c, id, stream)
	if got := decodeJSON(t, c.name, msg.Value); !reflect.DeepEqual(got, decodeJSON(t, "want", []byte(want))) {
		t.Errorf("%s: envelope differs\n got: %s\nwant: %s", c.name, msg.Value, want)
	}
	if got := dataOf(t, msg.Value); got != c.data {
		t.Errorf("%s: data differs\n got: %s\nwant: %s", c.name, got, c.data)
	}
}

func topicsAndEncoder(t *testing.T) (events, analytics, dlq string, enc *outboundkafka.Encoder) {
	t.Helper()
	events = kafkatest.Topic(t, outboundkafka.TopicEvents)
	analytics = kafkatest.Topic(t, outboundkafka.TopicAnalytics)
	dlq = kafkatest.Topic(t, outboundkafka.TopicDLQ)
	enc = outboundkafka.NewEncoder()
	enc.EventsTopic, enc.AnalyticsTopic, enc.DLQTopic = events, analytics, dlq
	return events, analytics, dlq, enc
}

// TestRelay_RealPostgresAndKafka_PublishesTheExactEnvelopes publishes EVERY
// golden event through the production publisher inside one real unit of work,
// runs one relay pass and asserts the exact CloudEvents JSON: the eleven
// integration types on the events topic, every type on analytics.
func TestRelay_RealPostgresAndKafka_PublishesTheExactEnvelopes(t *testing.T) {
	ctx := itctx(t)
	eventsTopic, analyticsTopic, _, enc := topicsAndEncoder(t)
	pool := pgtest.NewPool(t)

	cases := goldenCases()
	pub := postgres.NewOutbox(pool)
	next := 0
	pub.NewID = func() string { id := eventID(next); next++; return id }
	uow := postgres.NewUnitOfWork(pool)
	if err := uow.Do(ctx, func(ctx context.Context) error {
		for _, c := range cases {
			if err := pub.Publish(ctx, c.event); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("publish unit of work: %v", err)
	}
	if n := pgtest.Count(t, pool, "outbox"); n != len(cases) {
		t.Fatalf("outbox has %d rows before the relay, want %d", n, len(cases))
	}

	sink := outboundkafka.NewRelaySink(strings.Join(kafkatest.Brokers(t), ","))
	t.Cleanup(func() { _ = sink.Close() })
	relay := postgres.NewOutboxRelay(pool, enc, sink,
		postgres.WithLogger(slog.New(slog.NewTextHandler(&strings.Builder{}, nil))),
		postgres.WithInterval(time.Millisecond),
		postgres.WithBatchSize(len(cases)+1),
		postgres.WithMaxAttempts(postgres.DefaultOutboxMaxAttempts),
	)
	n, err := relay.RelayOnce(ctx)
	if err != nil || n != len(cases) {
		t.Fatalf("RelayOnce = (%d, %v), want (%d, nil)", n, err, len(cases))
	}
	if n := pgtest.Count(t, pool, "outbox"); n != 0 {
		t.Fatalf("outbox has %d rows after the relay, want 0 (sent rows are deleted)", n)
	}

	var integration []int
	for i, c := range cases {
		if c.integration {
			integration = append(integration, i)
		}
	}
	got := kafkatest.ReadN(t, eventsTopic, len(integration), 60*time.Second)
	for j, i := range integration {
		assertConsumed(t, got[j], cases[i], eventID(i), "events")
	}
	twin := kafkatest.ReadN(t, analyticsTopic, len(cases), 60*time.Second)
	for i, c := range cases {
		assertConsumed(t, twin[i], c, eventID(i), "analytics")
	}
}

// poisonPayload is an outbox row the encoder refuses: an event name the
// service does not publish.
const poisonPayload = `{"type":"Bogus","data":{"x":1}}`

// TestRelay_DeadLettersPoisonRowAndLaterRowsStillPublish inserts a poison row
// BEFORE a good one, runs one pass and asserts: the poison row lands on the
// DLQ topic with its payload verbatim and the cause in dlq-reason, the good
// row behind it still publishes, and the outbox ends empty.
func TestRelay_DeadLettersPoisonRowAndLaterRowsStillPublish(t *testing.T) {
	ctx := itctx(t)
	eventsTopic, analyticsTopic, dlqTopic, enc := topicsAndEncoder(t)
	pool := pgtest.NewPool(t)

	if err := pgtest.Exec(pool, `INSERT INTO outbox (event_id, aggreg_type, aggreg_id, payload, occurred_at)
		VALUES ('poison-event-id', 'yardmove', 'mv-bad', $1, now())`, poisonPayload); err != nil {
		t.Fatalf("insert poison row: %v", err)
	}
	cases := goldenCases()
	good := cases[1] // VisitRejected: an integration type
	pub := postgres.NewOutbox(pool)
	pub.NewID = func() string { return eventID(1) }
	if err := pub.Publish(ctx, good.event); err != nil {
		t.Fatalf("publish good row: %v", err)
	}

	sink := outboundkafka.NewRelaySink(strings.Join(kafkatest.Brokers(t), ","))
	t.Cleanup(func() { _ = sink.Close() })
	relay := postgres.NewOutboxRelay(pool, enc, sink,
		postgres.WithLogger(slog.New(slog.NewTextHandler(&strings.Builder{}, nil))),
		postgres.WithInterval(time.Millisecond),
		postgres.WithMaxAttempts(postgres.DefaultOutboxMaxAttempts),
	)
	n, err := relay.RelayOnce(ctx)
	if err != nil || n != 2 {
		t.Fatalf("RelayOnce = (%d, %v), want (2, nil): the poison row is dead-lettered, the good row published", n, err)
	}
	if n := pgtest.Count(t, pool, "outbox"); n != 0 {
		t.Fatalf("outbox has %d rows after the pass, want 0 (poison left via the DLQ)", n)
	}

	dlq := kafkatest.ReadN(t, dlqTopic, 1, 60*time.Second)[0]
	if dlq.Key != "mv-bad" {
		t.Errorf("dlq key = %q, want mv-bad", dlq.Key)
	}
	var poison, gotPayload any
	if err := json.Unmarshal([]byte(poisonPayload), &poison); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(dlq.Value, &gotPayload); err != nil {
		t.Fatalf("dlq value is not JSON: %v\n%s", err, dlq.Value)
	}
	if !reflect.DeepEqual(gotPayload, poison) {
		t.Errorf("dlq value = %s, want the poison payload verbatim (%s)", dlq.Value, poisonPayload)
	}
	if dlq.Headers["content-type"] != "application/json" {
		t.Errorf("dlq content-type = %q, want application/json", dlq.Headers["content-type"])
	}
	if reason := dlq.Headers["dlq-reason"]; !strings.Contains(reason, "not a published event") {
		t.Errorf("dlq-reason = %q, want it to name the unpublished event", reason)
	}

	assertConsumed(t, kafkatest.ReadN(t, eventsTopic, 1, 60*time.Second)[0], good, eventID(1), "events")
	assertConsumed(t, kafkatest.ReadN(t, analyticsTopic, 1, 60*time.Second)[0], good, eventID(1), "analytics")
}
