//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	outboundkafka "github.com/claudioed/yard-management/internal/adapters/outbound/kafka"
	"github.com/claudioed/yard-management/internal/adapters/outbound/postgres"
	"github.com/claudioed/yard-management/internal/application/outbox"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
	"github.com/claudioed/yard-management/internal/testing/repocontract"
)

// The relay against a real Postgres with a switchable sink double: the attempt
// cap, the dead-letter path, per-aggregate ordering and the graceful shutdown.

// stubSink records what it was handed and fails or blocks on demand.
type stubSink struct {
	mu       sync.Mutex
	fail     error
	blockOn  int // 1-based number of the Send call that blocks until ctx ends (0: never)
	calls    int
	sent     []outbox.Message
	sendCall chan struct{} // receives one value per Send call that blocks
}

func (s *stubSink) Send(ctx context.Context, msgs ...outbox.Message) error {
	s.mu.Lock()
	s.calls++
	call, fail, block := s.calls, s.fail, s.blockOn
	s.mu.Unlock()
	if block != 0 && call == block {
		if s.sendCall != nil {
			s.sendCall <- struct{}{}
		}
		<-ctx.Done()
		return ctx.Err()
	}
	if fail != nil {
		return fail
	}
	s.mu.Lock()
	s.sent = append(s.sent, msgs...)
	s.mu.Unlock()
	return nil
}

func (s *stubSink) setFail(err error) { s.mu.Lock(); s.fail = err; s.mu.Unlock() }

func (s *stubSink) messages() []outbox.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]outbox.Message(nil), s.sent...)
}

// captureLogger records every log record's message and attributes.
type captureLogger struct {
	mu      sync.Mutex
	records []string
	attrs   []map[string]any
}

func (c *captureLogger) Handle(_ context.Context, r slog.Record) error {
	attrs := map[string]any{}
	r.Attrs(func(a slog.Attr) bool { attrs[a.Key] = a.Value.Any(); return true })
	c.mu.Lock()
	c.records = append(c.records, r.Message)
	c.attrs = append(c.attrs, attrs)
	c.mu.Unlock()
	return nil
}
func (c *captureLogger) Enabled(context.Context, slog.Level) bool { return true }
func (c *captureLogger) WithAttrs([]slog.Attr) slog.Handler       { return c }
func (c *captureLogger) WithGroup(string) slog.Handler            { return c }

func (c *captureLogger) last() (string, map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.records) == 0 {
		return "", nil
	}
	return c.records[len(c.records)-1], c.attrs[len(c.attrs)-1]
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(&strings.Builder{}, nil)) }

func itctx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// claimedMove builds the YardMoveClaimed event of a move id.
func claimedMove(t *testing.T, id string, offset time.Duration) *yardmove.Move {
	t.Helper()
	m := repocontract.NewMove(t, id, "trl-"+id, repocontract.Spot("WH1-P"+id), 5, time.Time{}, offset)
	repocontract.Claim(t, m, "h-1", offset+time.Minute)
	return m
}

// publishMoves publishes the events of the given moves (YardMoveRequested then
// YardMoveClaimed each) through the production Outbox, one unit of work.
func publishMoves(t *testing.T, pool *pgxpool.Pool, ids ...string) int {
	t.Helper()
	pub := postgres.NewOutbox(pool)
	uow := postgres.NewUnitOfWork(pool)
	n := 0
	err := uow.Do(itctx(t), func(ctx context.Context) error {
		for i, id := range ids {
			m := claimedMove(t, id, time.Duration(i)*time.Hour)
			events := m.PullEvents()
			n += len(events)
			if err := pub.Publish(ctx, events...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func forceDue(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if err := pgtest.Exec(pool, `UPDATE outbox SET next_attempt_at = now()`); err != nil {
		t.Fatal(err)
	}
}

func newRelay(pool *pgxpool.Pool, sink postgres.RelaySink, logger *slog.Logger, opts ...postgres.OutboxRelayOption) *postgres.OutboxRelay {
	base := []postgres.OutboxRelayOption{postgres.WithLogger(logger), postgres.WithInterval(time.Millisecond)}
	return postgres.NewOutboxRelay(pool, outboundkafka.NewEncoder(), sink, append(base, opts...)...)
}

func TestRelayPublishesInOrderAndDeletesTheRows(t *testing.T) {
	pool := pgtest.NewPool(t)
	rows := publishMoves(t, pool, "mv-1", "mv-2")
	sink := &stubSink{}
	n, err := newRelay(pool, sink, quietLogger(), postgres.WithMaxAttempts(postgres.DefaultOutboxMaxAttempts)).RelayOnce(itctx(t))
	if err != nil || n != rows {
		t.Fatalf("RelayOnce = (%d, %v), want (%d, nil)", n, err, rows)
	}
	if pgtest.Count(t, pool, "outbox") != 0 {
		t.Fatal("sent rows are deleted")
	}
	var got []string
	for _, m := range sink.messages() {
		got = append(got, m.EventType[strings.LastIndex(m.EventType, ".")+1:]+"/"+string(m.Key)+"/"+m.Topic[strings.LastIndex(m.Topic, ".")+1:])
	}
	// analytics-only types: ONE message each, to the analytics topic, in publish order
	want := []string{
		"YardMoveRequested/mv-1/analytics", "YardMoveClaimed/mv-1/analytics",
		"YardMoveRequested/mv-2/analytics", "YardMoveClaimed/mv-2/analytics",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("sent %v, want %v", got, want)
	}
}

func TestRelayAttemptCapExcludesTheRowAndAReArmPublishesIt(t *testing.T) {
	pool := pgtest.NewPool(t)
	publishMoves(t, pool, "mv-1")
	boom := errors.New("broker down")
	sink := &stubSink{fail: boom}
	logs := &captureLogger{}
	relay := newRelay(pool, sink, slog.New(logs), postgres.WithMaxAttempts(3))
	ctx := itctx(t)

	for pass := 1; pass <= 3; pass++ {
		n, err := relay.RelayOnce(ctx)
		var sf *outbox.SendFailureError
		if n != 0 || !errors.As(err, &sf) || !errors.Is(err, boom) || sf.Attempts != pass {
			t.Fatalf("pass %d = (%d, %v), want a send failure with attempts=%d", pass, n, err, pass)
		}
		forceDue(t, pool)
	}
	// the capped (first) row stays, excluded from claims: the second row now
	// overtakes it (the accepted trade of the cap) and, the broker still down,
	// fails in its place.
	n, err := relay.RelayOnce(ctx)
	var second *outbox.SendFailureError
	if n != 0 || !errors.As(err, &second) || second.Attempts != 1 {
		t.Fatalf("pass after the cap = (%d, %v), want the second row failing once", n, err)
	}
	assertCapped(t, pool, logs)

	// the operator re-arms the rows; the next pass publishes them
	if err := pgtest.Exec(pool, `UPDATE outbox SET attempts = 0, next_attempt_at = now()`); err != nil {
		t.Fatal(err)
	}
	sink.setFail(nil)
	n, err = relay.RelayOnce(ctx)
	if err != nil || n != 2 || pgtest.Count(t, pool, "outbox") != 0 {
		t.Fatalf("after the re-arm = (%d, %v), outbox rows %d", n, err, pgtest.Count(t, pool, "outbox"))
	}
}

// assertCapped checks the capped row stays with its attempts and error, and
// that the pass reaching the cap logged id, topic, type, key and attempts.
func assertCapped(t *testing.T, pool *pgxpool.Pool, logs *captureLogger) {
	t.Helper()
	var attempts int
	var lastError string
	pgtest.Scan(t, pool, `SELECT attempts, last_error FROM outbox ORDER BY id LIMIT 1`, nil, &attempts, &lastError)
	if attempts != 3 || !strings.Contains(lastError, "broker down") {
		t.Fatalf("first row attempts=%d last_error=%q, want 3 and the broker error", attempts, lastError)
	}
	var capLogged map[string]any
	logs.mu.Lock()
	for i, r := range logs.records {
		if r == postgres.AttemptCapReachedMsg {
			capLogged = logs.attrs[i]
		}
	}
	logs.mu.Unlock()
	if capLogged == nil || capLogged["topic"] != outboundkafka.TopicAnalytics || capLogged["key"] != "mv-1" ||
		capLogged["event_type"] != "com.warehouse.wms.yard-management.yardmove.YardMoveRequested" || capLogged["attempts"] != int64(3) {
		t.Fatalf("cap log = %v, want id, topic, event type, key and attempts", capLogged)
	}
}

func TestRelayFailureStopsThePassSoLaterRowsDoNotOvertake(t *testing.T) {
	pool := pgtest.NewPool(t)
	publishMoves(t, pool, "mv-1")
	sink := &stubSink{fail: errors.New("broker down")}
	relay := newRelay(pool, sink, quietLogger(), postgres.WithMaxAttempts(10), postgres.WithInterval(time.Hour))
	if _, err := relay.RelayOnce(itctx(t)); err == nil {
		t.Fatal("want the send failure")
	}
	sink.mu.Lock()
	calls := sink.calls
	sink.mu.Unlock()
	if calls != 1 {
		t.Fatalf("the pass sent %d times, want it to stop at the first failed row", calls)
	}
	var second int
	pgtest.Scan(t, pool, `SELECT attempts FROM outbox ORDER BY id OFFSET 1 LIMIT 1`, nil, &second)
	if second != 0 {
		t.Fatalf("the second row was touched: attempts=%d", second)
	}
	// the failed row backs off: it is not due again right away
	sink.setFail(nil)
	n, err := relay.RelayOnce(itctx(t))
	if err != nil || n != 1 || pgtest.Count(t, pool, "outbox") != 1 {
		t.Fatalf("second pass = (%d, %v), outbox %d: the second row publishes, the backed-off first waits", n, err, pgtest.Count(t, pool, "outbox"))
	}
}

func TestRelayDeadLettersAPoisonRowAndLaterRowsStillPublish(t *testing.T) {
	pool := pgtest.NewPool(t)
	if err := pgtest.Exec(pool, `INSERT INTO outbox (event_id, aggreg_type, aggreg_id, payload, occurred_at)
		VALUES ('poison-1', 'yardmove', 'mv-bad', '{"type":"Bogus","data":{"x":1}}', now())`); err != nil {
		t.Fatal(err)
	}
	publishMoves(t, pool, "mv-1")
	sink := &stubSink{}
	logs := &captureLogger{}
	n, err := newRelay(pool, sink, slog.New(logs), postgres.WithMaxAttempts(5)).RelayOnce(itctx(t))
	if err != nil || n != 3 || pgtest.Count(t, pool, "outbox") != 0 {
		t.Fatalf("RelayOnce = (%d, %v), outbox %d: the poison row leaves via the DLQ", n, err, pgtest.Count(t, pool, "outbox"))
	}
	sent := sink.messages()
	if len(sent) != 3 || sent[0].Topic != outboundkafka.TopicDLQ || string(sent[0].Key) != "mv-bad" ||
		string(sent[0].Value) != `{"type":"Bogus","data":{"x":1}}` {
		t.Fatalf("sent %+v", sent)
	}
	if sent[1].Topic != outboundkafka.TopicAnalytics {
		t.Fatalf("the good rows publish behind it: %+v", sent[1])
	}
	if msg, attrs := logs.last(); msg == "" || attrs == nil {
		t.Fatal("the dead-lettering is logged")
	}
}

func TestRelayShutdownHandsTheRowBackUnfailed(t *testing.T) {
	pool := pgtest.NewPool(t)
	publishMoves(t, pool, "mv-1") // 2 rows
	sink := &stubSink{blockOn: 2, sendCall: make(chan struct{}, 1)}
	relay := newRelay(pool, sink, quietLogger(), postgres.WithMaxAttempts(5), postgres.WithInterval(time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- relay.Run(ctx) }()

	select {
	case <-sink.sendCall: // the second row is in flight
	case <-time.After(30 * time.Second):
		t.Fatal("the relay never reached the second send")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v, want nil after a graceful stop", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after the shutdown")
	}
	// the first row was relayed and committed; the in-flight one stays due with its attempts untouched
	var attempts int
	var id int64
	pgtest.Scan(t, pool, `SELECT id, attempts FROM outbox`, nil, &id, &attempts)
	if pgtest.Count(t, pool, "outbox") != 1 || attempts != 0 {
		t.Fatalf("outbox rows %d attempts %d, want exactly the in-flight row, not failed", pgtest.Count(t, pool, "outbox"), attempts)
	}
	if len(sink.messages()) != 1 {
		t.Fatalf("sent %d messages before the shutdown, want 1", len(sink.messages()))
	}
}

func TestRelayRunRetriesUntilTheBrokerComesBackAndStopsOnCancel(t *testing.T) {
	pool := pgtest.NewPool(t)
	rows := publishMoves(t, pool, "mv-1")
	sink := &stubSink{fail: errors.New("broker down")}
	relay := newRelay(pool, sink, quietLogger(), postgres.WithMaxAttempts(0), postgres.WithMaxBackoff(5*time.Millisecond), postgres.WithBatchSize(1))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- relay.Run(ctx) }()

	time.Sleep(200 * time.Millisecond)
	sink.setFail(nil)
	deadline := time.Now().Add(30 * time.Second)
	for pgtest.Count(t, pool, "outbox") != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the outbox never drained (%d of %d rows left)", pgtest.Count(t, pool, "outbox"), rows)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRelayWithAnEmptyOutboxDoesNothing(t *testing.T) {
	pool := pgtest.NewPool(t)
	n, err := newRelay(pool, &stubSink{}, quietLogger()).RelayOnce(itctx(t))
	if n != 0 || err != nil {
		t.Fatalf("RelayOnce = (%d, %v)", n, err)
	}
}
