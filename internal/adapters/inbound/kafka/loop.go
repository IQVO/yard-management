package kafka

import (
	"context"
	"errors"
	"log/slog"

	kafkago "github.com/segmentio/kafka-go"
)

// consumeLoop is the at-least-once run loop shared by the consumers.
type consumeLoop struct {
	reader Reader
	// handle applies one message; non-nil means transient failure => retry
	// (or a poison marker => dead-letter).
	handle func(ctx context.Context, msg kafkago.Message) error
	logger *slog.Logger
	name   string
	retry  RetryPolicy
	sleep  sleepFunc

	// deadLetter publishes a message the consumer can never apply to the
	// topic's dead letter topic (nil: poison is only WARNed and skipped).
	deadLetter func(ctx context.Context, msg kafkago.Message, cause error) error
}

// run fetches one message at a time and does not fetch the next until the
// current one was handled successfully AND its offset committed. It returns
// only when ctx is cancelled (shutdown drain) or FetchMessage itself fails.
func (l *consumeLoop) run(ctx context.Context) error {
	policy := l.retry.withDefaults()
	sleep := l.sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	for {
		msg, err := l.reader.FetchMessage(ctx)
		if err != nil {
			return err
		}
		if err := l.handleWithDeadLetter(ctx, policy, sleep, msg); err != nil {
			return err
		}
		// Commit ONLY now. A commit that fails is retried too: the work is
		// already durable (the processed-events claim), and if the process
		// dies first the redelivery is skipped by that same claim.
		if err := l.retryUntilOK(ctx, policy, sleep, "offset commit", msg, func() error {
			return l.reader.CommitMessages(ctx, msg)
		}); err != nil {
			return err
		}
	}
}

// handleWithDeadLetter applies one message, deciding what a failure means:
//
//   - nil: done, the caller commits;
//   - a POISON marker (a payload of a handled type that can never be applied):
//     dead-lettered at once, without a handling retry, and the offset then
//     moves past it, so a deterministic refusal cannot wedge the partition. The
//     dead-letter PUBLISH itself is retried forever: a message that could not
//     even be dead-lettered must never be silently committed past;
//   - anything else is TRANSIENT: the SAME message is retried with the capped
//     exponential backoff, forever (the effects are idempotent under the
//     claimed event id, so redelivery is always safe).
//
// The ctx.Err() check at the top is the fleet's escape-18b guard and it lives
// HERE, in the function that decides: a handler that failed because the process
// is stopping is neither retried past the cancellation nor dead-lettered; the
// offset stays uncommitted and the message is redelivered on restart.
func (l *consumeLoop) handleWithDeadLetter(ctx context.Context, p RetryPolicy, sleep sleepFunc, msg kafkago.Message) error {
	err := l.handle(ctx, msg)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var poison *deadLetterError
	if l.deadLetter != nil && errors.As(err, &poison) {
		cause := deadLetterCause(err)
		l.logger.ErrorContext(ctx, l.name+" message can never be applied; dead-lettering it without retry",
			"error", cause, "partition", msg.Partition, "offset", msg.Offset)
		return l.retryUntilOK(ctx, p, sleep, "dead-letter publish", msg, func() error {
			return l.deadLetter(ctx, msg, cause)
		})
	}
	return l.retryUntilOK(ctx, p, sleep, "handling", msg, func() error {
		return l.handle(ctx, msg)
	})
}

// retryUntilOK runs op until it returns nil, sleeping with capped exponential
// backoff between attempts. It gives up (returning ctx.Err()) only when ctx is
// cancelled: a message interrupted by shutdown is redelivered on restart, never
// dropped. It NEVER skips: a transiently unhandleable message blocks its
// partition (and is logged at ERROR every attempt) rather than being silently
// lost.
func (l *consumeLoop) retryUntilOK(ctx context.Context, p RetryPolicy, sleep sleepFunc, what string, msg kafkago.Message, op func() error) error {
	delay := p.Initial
	for attempt := 1; ; attempt++ {
		err := op()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		l.logger.ErrorContext(ctx, l.name+" "+what+" failed; retrying the same message",
			"error", err, "partition", msg.Partition, "offset", msg.Offset,
			"attempt", attempt, "retry_in", delay)
		if err := sleep(ctx, delay); err != nil {
			return err
		}
		delay = p.next(delay)
	}
}
