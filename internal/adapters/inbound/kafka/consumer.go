package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	ce "github.com/cloudevents/sdk-go/v2/event"
	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/yard-management/internal/adapters/kafka/cloudevents"
)

// HandlerFunc applies one decoded CloudEvent. nil means done; an error wrapping
// deadLetterError means a poison message of a handled type (dead-lettered and
// committed past); any other non-nil error is transient and retried.
type HandlerFunc func(ctx context.Context, e ce.Event) error

// Consumer is one consumer: it reads Topic under a stable consumer group and
// dispatches each message on the FULL CloudEvents `type` to its handler. Types
// it has no handler for are ignored.
type Consumer struct {
	Name     string
	Topic    string
	Reader   Reader
	Handlers map[string]HandlerFunc
	Logger   *slog.Logger
	Retry    RetryPolicy

	// DLQ is the dead-letter writer (topic Topic+DLQSuffix): a message of a
	// handled type that can never be applied (a payload that never decodes, a
	// blank natural id) is moved there at once and committed past instead of
	// blocking the partition. Not-a-CloudEvent and unknown types never reach it.
	DLQ DeadLetterWriter

	sleep sleepFunc // test hook; nil => real, ctx-cancellable sleep
}

// ErrGroupRequired is returned by a constructor given a blank consumer group:
// the group is mandatory configuration (a locally run process must never join a
// live cluster group by accident).
var ErrGroupRequired = errors.New("kafka: the consumer group is required")

// newConsumer builds a Consumer reading topic under groupID (from the
// environment variable envName at the composition root, never a literal).
// Constructing the reader and the dead-letter writer does not dial.
func newConsumer(name, topic, groupID, envName string, f Factory, handlers map[string]HandlerFunc, logger *slog.Logger) (*Consumer, error) {
	if strings.TrimSpace(groupID) == "" {
		return nil, fmt.Errorf("%w: set %s for the %s", ErrGroupRequired, envName, name)
	}
	return &Consumer{
		Name:     name,
		Topic:    topic,
		Reader:   f.NewReader(topic, groupID),
		Handlers: handlers,
		Logger:   defaultLogger(logger),
		DLQ:      f.NewDeadLetter(topic),
	}, nil
}

// Run consumes until ctx is cancelled (shutdown drain) or the reader fails. A
// message's offset is committed only after handling returned nil; a transient
// failure retries the SAME message with capped exponential backoff, forever
// (the effects are idempotent under the claimed event id); a poison message of
// a handled type is dead-lettered and committed past.
func (c *Consumer) Run(ctx context.Context) error {
	loop := consumeLoop{
		reader: c.Reader,
		handle: func(ctx context.Context, msg kafkago.Message) error { return c.HandleMessage(ctx, msg.Value) },
		logger: defaultLogger(c.Logger),
		name:   c.Name,
		retry:  c.Retry,
		sleep:  c.sleep,
	}
	if c.DLQ != nil {
		loop.deadLetter = func(ctx context.Context, msg kafkago.Message, cause error) error {
			return c.DLQ.WriteDeadLetter(ctx, c.Topic, msg, cause)
		}
	}
	return loop.run(ctx)
}

// Close releases the reader and the dead-letter writer.
func (c *Consumer) Close() error {
	err := c.Reader.Close()
	if c.DLQ != nil {
		err = errors.Join(err, c.DLQ.Close())
	}
	return err
}

// HandleMessage decodes one CloudEvents 1.0 message and dispatches it:
//   - not a valid CloudEvent: WARN, commit past (never dead-lettered: the type
//     is unknowable, so there is nothing to replay from the DLQ);
//   - a type this consumer does not act on: ignored;
//   - deadLetterError (poison of a handled type): WARN, the run loop
//     dead-letters it and commits past (nil here when no DLQ is wired);
//   - anything else: transient, returned for the capped retry.
func (c *Consumer) HandleMessage(ctx context.Context, value []byte) error {
	logger := defaultLogger(c.Logger)
	e, err := cloudevents.Decode(value)
	if err != nil {
		logger.WarnContext(ctx, "skipping a message that is not a valid CloudEvent", "consumer", c.Name, "topic", c.Topic, "error", err)
		return nil
	}
	handle, ok := c.Handlers[e.Type()]
	if !ok {
		return nil
	}
	err = handle(ctx, e)
	var poison *deadLetterError
	if errors.As(err, &poison) {
		logger.WarnContext(ctx, "an event that can never be applied; dead-lettering it",
			"consumer", c.Name, "type", e.Type(), "event_id", e.ID(), "error", deadLetterCause(err))
		if c.DLQ == nil {
			return nil
		}
		return err
	}
	if err == nil {
		logger.InfoContext(ctx, "event consumed", "consumer", c.Name, "type", e.Type(), "event_id", e.ID(), "subject", e.Subject())
	}
	return err
}

// on adapts a typed handler: the event's JSON `data` is decoded into a D (a
// payload that does not decode is poison; unknown fields are ignored and absent
// ones default, the tolerant-reader rule) and fn runs on it.
func on[D any](fn func(ctx context.Context, e ce.Event, d D) error) HandlerFunc {
	return func(ctx context.Context, e ce.Event) error {
		var d D
		if err := e.DataAs(&d); err != nil {
			return poisonf("payload of %s does not decode: %v", e.Type(), err)
		}
		return fn(ctx, e, d)
	}
}

// requireNonBlank is poison when any of the named values is blank: such an
// event can never key a local copy.
func requireNonBlank(e ce.Event, pairs ...string) error {
	for i := 0; i+1 < len(pairs); i += 2 {
		if strings.TrimSpace(pairs[i+1]) == "" {
			return poisonf("payload of %s carries no %s", e.Type(), pairs[i])
		}
	}
	return nil
}
