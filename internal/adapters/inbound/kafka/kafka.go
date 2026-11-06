// Package kafka holds this service's INBOUND Kafka consumers (ADR 0003 and
// ADR 0009): dock-scheduling (appointments and door closures), facility-layout
// (zones and location slots) and inbound-receiving (ASNs and receipts). Every
// consumer decodes CloudEvents 1.0 only, via internal/adapters/kafka/cloudevents,
// and dispatches on the FULL `type` string. The consumers only decode and map:
// every effect is a use case of internal/application/usecases.
//
// Delivery guarantee: AT-LEAST-ONCE with an ATOMIC effect. Each message is
// handled inside ONE ports.UnitOfWork (the processed_events claim of the
// CloudEvents id and the effect commit or roll back together, see the Apply*
// use cases) and its offset is committed only AFTER handling returned nil
// (FetchMessage + CommitMessages).
//
// Deterministic problems are logged at WARN and committed past: a message that
// is not a valid CloudEvent (the type is unknowable) and a type this consumer
// has no handler for. A message of a HANDLED type that can never be applied (a
// payload that does not decode, a blank natural id, an unparseable time) is
// additionally DEAD-LETTERED to "<topic>.dlq" at once, headers carrying the
// source topic and the cause, and the offset moves past it. A TRANSIENT failure
// (Postgres down, a failed commit) is retried on the SAME message with capped
// exponential backoff, forever: every effect is idempotent under the claimed
// event id, so a redelivery after a crash is always safe, and dead-lettering a
// message only because the database was down would park good facts.
//
// None of these consumers is wired in cmd/**: the composition root calls the
// New*Consumer constructors with a Factory and the use cases.
package kafka

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// Reader is the subset of *kafkago.Reader a consumer needs, so unit tests
// never need a live broker. FetchMessage/CommitMessages (not ReadMessage)
// because ReadMessage with a GroupID auto-commits the offset the moment it
// returns, i.e. before the message has been handled: a failed handling would
// then never be redelivered. Offsets are committed explicitly, after success
// only.
type Reader interface {
	FetchMessage(ctx context.Context) (kafkago.Message, error)
	CommitMessages(ctx context.Context, msgs ...kafkago.Message) error
	Close() error
}

// Factory builds the Kafka-facing parts of a consumer, so the constructors take
// them as a dependency and tests never need a broker. Both functions must not
// dial: the connection opens in the consumer's own goroutine.
type Factory struct {
	// NewReader builds the reader of topic under the consumer group groupID.
	NewReader func(topic, groupID string) Reader
	// NewDeadLetter builds the dead-letter writer of topic ("<topic>.dlq").
	NewDeadLetter func(topic string) DeadLetterWriter
}

// NewFactory is the production Factory over kafka-go: readers under the given
// group (offsets committed synchronously, after handling) and dead-letter
// writers with acks=all. Reader errors that kafka-go would retry silently (a
// missing topic, an unreachable broker) are logged at ERROR.
func NewFactory(brokers []string, logger *slog.Logger) Factory {
	logger = defaultLogger(logger)
	return Factory{
		NewReader: func(topic, groupID string) Reader {
			cfg := readerConfig(brokers, topic, groupID)
			cfg.ErrorLogger = readerErrorLogger(logger, topic)
			return kafkago.NewReader(cfg)
		},
		NewDeadLetter: func(topic string) DeadLetterWriter { return newDLQWriter(brokers, topic) },
	}
}

// readerConfig builds the kafka-go reader configuration shared by the
// consumers. GroupID always comes from the caller (an environment variable at
// the composition root, never a literal here), satisfying
// internal/architecture's TestKafkaConsumerGroupNeverHardcodedInline.
//
// CommitInterval is deliberately left UNSET: with a GroupID kafka-go then
// commits synchronously inside CommitMessages, which the run loop calls only
// after a message was handled successfully. A group without a committed offset
// starts at the first offset, so a fresh deployment replays the topic and the
// local copies converge.
//
// kafkago.NewReader does not dial the broker: the connection is opened by the
// first FetchMessage inside the consumer's own goroutine, so a missing topic or
// an unreachable broker never blocks process startup.
func readerConfig(brokers []string, topic, groupID string) kafkago.ReaderConfig {
	return kafkago.ReaderConfig{Brokers: brokers, Topic: topic, GroupID: groupID, MaxWait: 500 * time.Millisecond}
}

func defaultLogger(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.Default()
	}
	return logger
}

// readerErrorLogger adapts logger to kafka-go's ErrorLogger: without it
// kafka-go silently retries a failing group join, a missing topic or an
// unreachable broker; surface every such retry as an ERROR log line.
func readerErrorLogger(logger *slog.Logger, topic string) kafkago.LoggerFunc {
	return func(format string, args ...any) {
		logger.Error("kafka reader error; retrying", "topic", topic, "detail", fmt.Sprintf(format, args...))
	}
}

// RetryPolicy is the capped exponential backoff the run loop applies while the
// SAME message keeps failing with a transient error. Zero fields fall back to
// DefaultRetryInitial / DefaultRetryMax.
type RetryPolicy struct {
	Initial time.Duration
	Max     time.Duration
}

// Default backoff: 200ms, 400ms, 800ms ... capped at 5s.
const (
	DefaultRetryInitial = 200 * time.Millisecond
	DefaultRetryMax     = 5 * time.Second
)

func (p RetryPolicy) withDefaults() RetryPolicy {
	if p.Initial <= 0 {
		p.Initial = DefaultRetryInitial
	}
	if p.Max <= 0 {
		p.Max = DefaultRetryMax
	}
	return p
}

// next doubles d, capped at p.Max.
func (p RetryPolicy) next(d time.Duration) time.Duration {
	d *= 2
	if d > p.Max {
		return p.Max
	}
	return d
}

// sleepFunc waits d or until ctx is done (returning ctx.Err()). A field so
// tests can record the backoff sequence without really sleeping.
type sleepFunc func(ctx context.Context, d time.Duration) error

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
