package kafka_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/yard-management/internal/adapters/inbound/kafka"
	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
)

// quietLogger keeps the unit tests' output readable.
func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fastRetry makes the transient-failure backoff negligible in unit tests.
var fastRetry = kafka.RetryPolicy{Initial: time.Millisecond, Max: 2 * time.Millisecond}

// errDrained ends a fakeReader's run loop once its queue is empty.
var errDrained = errors.New("fake reader drained")

// fakeReader hands out the queued messages one at a time and records every
// commit; once drained it fails the fetch, which ends Consumer.Run.
type fakeReader struct {
	mu      sync.Mutex
	next    int
	msgs    []kafkago.Message
	commits []int64
	closed  bool
}

func (r *fakeReader) FetchMessage(context.Context) (kafkago.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.next >= len(r.msgs) {
		return kafkago.Message{}, errDrained
	}
	m := r.msgs[r.next]
	r.next++
	return m, nil
}

func (r *fakeReader) CommitMessages(_ context.Context, msgs ...kafkago.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range msgs {
		r.commits = append(r.commits, m.Offset)
	}
	return nil
}

func (r *fakeReader) Close() error {
	r.closed = true
	return nil
}

func (r *fakeReader) committed() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.commits...)
}

type deadLetter struct {
	sourceTopic string
	value       []byte
	cause       string
}

// fakeDLQ records the dead letters.
type fakeDLQ struct {
	mu     sync.Mutex
	letter []deadLetter
	closed bool
}

func (d *fakeDLQ) WriteDeadLetter(_ context.Context, sourceTopic string, msg kafkago.Message, cause error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.letter = append(d.letter, deadLetter{sourceTopic: sourceTopic, value: msg.Value, cause: cause.Error()})
	return nil
}

func (d *fakeDLQ) Close() error {
	d.closed = true
	return nil
}

func (d *fakeDLQ) letters() []deadLetter {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]deadLetter(nil), d.letter...)
}

// fakeKafka is a Factory over fakes: it remembers what the constructors asked
// for and queues the messages the reader will serve.
type fakeKafka struct {
	reader      *fakeReader
	dlq         *fakeDLQ
	readerTopic string
	readerGroup string
	dlqTopic    string
}

func newFakeKafka(values ...[]byte) *fakeKafka {
	f := &fakeKafka{reader: &fakeReader{}, dlq: &fakeDLQ{}}
	for i, v := range values {
		f.reader.msgs = append(f.reader.msgs, kafkago.Message{Partition: 0, Offset: int64(i), Value: v})
	}
	return f
}

func (f *fakeKafka) factory() kafka.Factory {
	return kafka.Factory{
		NewReader: func(topic, groupID string) kafka.Reader {
			f.readerTopic, f.readerGroup = topic, groupID
			return f.reader
		},
		NewDeadLetter: func(topic string) kafka.DeadLetterWriter {
			f.dlqTopic = topic
			return f.dlq
		},
	}
}

// drain runs the consumer until its fake reader is empty.
func drain(t *testing.T, c *kafka.Consumer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Run(ctx); !errors.Is(err, errDrained) {
		t.Fatalf("Run = %v, want the drained reader", err)
	}
}

// recorder implements the three use-case interfaces and records every call as
// a comparable line plus the structured argument.
type recorder struct {
	mu    sync.Mutex
	calls []string
	facts []usecases.AppointmentFact
	clos  []views.Closure
	rcpts []views.Receipt
	// failNext transient errors are returned (and not recorded) before a call succeeds.
	failNext int
}

func (r *recorder) record(line string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failNext > 0 {
		r.failNext--
		return errors.New("database is down")
	}
	r.calls = append(r.calls, line)
	return nil
}

func (r *recorder) log() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func (r *recorder) ApplyAppointment(_ context.Context, id string, f usecases.AppointmentFact) error {
	if err := r.record(fmt.Sprintf("appointment %s %s", id, f.Appointment.ID)); err != nil {
		return err
	}
	r.facts = append(r.facts, f)
	return nil
}

func (r *recorder) ScheduleClosure(_ context.Context, id string, c views.Closure) error {
	if err := r.record(fmt.Sprintf("schedule %s %s", id, c.ID)); err != nil {
		return err
	}
	r.clos = append(r.clos, c)
	return nil
}

func (r *recorder) LiftClosure(_ context.Context, id, closureID string) error {
	return r.record(fmt.Sprintf("lift %s %s", id, closureID))
}

func (r *recorder) ApplyZone(_ context.Context, id, zoneID, site string) error {
	return r.record(fmt.Sprintf("zone %s %s %s", id, zoneID, site))
}

func (r *recorder) ApplySlot(_ context.Context, id, code, zoneID, role, flow string) error {
	return r.record(fmt.Sprintf("slot %s %s %s %s %s", id, code, zoneID, role, flow))
}

func (r *recorder) DecommissionSlot(_ context.Context, id, code string) error {
	return r.record(fmt.Sprintf("decommission %s %s", id, code))
}

func (r *recorder) RegisterAsn(_ context.Context, id, asn string) error {
	return r.record(fmt.Sprintf("asn-registered %s %s", id, asn))
}

func (r *recorder) CancelAsn(_ context.Context, id, asn string) error {
	return r.record(fmt.Sprintf("asn-cancelled %s %s", id, asn))
}

func (r *recorder) OpenReceipt(_ context.Context, id string, rc views.Receipt) error {
	if err := r.record(fmt.Sprintf("receipt-opened %s %s", id, rc.ID)); err != nil {
		return err
	}
	r.rcpts = append(r.rcpts, rc)
	return nil
}

func (r *recorder) CloseReceipt(_ context.Context, id, receiptID, asn string, closedAt time.Time) error {
	return r.record(fmt.Sprintf("receipt-closed %s %s %s %s", id, receiptID, asn, closedAt.Format(time.RFC3339)))
}

// Compile-time proof that the recorder stands in for the real use cases.
var (
	_ kafka.DockSchedulingUseCases   = (*recorder)(nil)
	_ kafka.FacilityUseCases         = (*recorder)(nil)
	_ kafka.InboundReceivingUseCases = (*recorder)(nil)
)
