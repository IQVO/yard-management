package kafka

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// quiet keeps the expected ERROR lines of the retry tests out of the output.
var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// loopReader programmes the run loop without a broker: it hands out the queued
// messages one at a time and records every commit.
type loopReader struct {
	mu         sync.Mutex
	fetched    int
	msgs       []kafkago.Message
	commits    []kafkago.Message
	fetchErr   error
	commitErr  error
	commitErrN int // how many commits fail before succeeding
}

func (r *loopReader) FetchMessage(context.Context) (kafkago.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fetched >= len(r.msgs) {
		if r.fetchErr != nil {
			return kafkago.Message{}, r.fetchErr
		}
		return kafkago.Message{}, errors.New("no more messages")
	}
	m := r.msgs[r.fetched]
	r.fetched++
	return m, nil
}

func (r *loopReader) CommitMessages(_ context.Context, msgs ...kafkago.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commits = append(r.commits, msgs...)
	if r.commitErrN > 0 {
		r.commitErrN--
		return r.commitErr
	}
	return nil
}

func (*loopReader) Close() error { return nil }

func (r *loopReader) commitsN() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.commits)
}

func loopMsg(offset int, value string) kafkago.Message {
	return kafkago.Message{Partition: 0, Offset: int64(offset), Value: []byte(value)}
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func noSleep(context.Context, time.Duration) error { return nil }

// The offset moves only after handling succeeded: while handling keeps failing
// the SAME message is retried and nothing is committed.
func TestConsumeLoopCommitsOnlyAfterSuccess(t *testing.T) {
	reader := &loopReader{msgs: []kafkago.Message{loopMsg(0, "m0"), loopMsg(1, "m1")}, fetchErr: context.Canceled}
	var mu sync.Mutex
	attempts := 0
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	loop := consumeLoop{
		reader: reader, logger: quiet, name: "test", sleep: noSleep,
		handle: func(context.Context, kafkago.Message) error {
			mu.Lock()
			attempts++
			n := attempts
			mu.Unlock()
			if n < 3 {
				return errors.New("transient")
			}
			if n == 3 {
				<-release // hold the success until the test has looked
			}
			return nil
		},
	}
	done := make(chan error, 1)
	go func() { done <- loop.run(ctx) }()

	waitFor(t, 5*time.Second, "the third handling attempt", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return attempts >= 3
	})
	if n := reader.commitsN(); n != 0 {
		t.Fatalf("%d commits while handling still failed; the offset must move only after success", n)
	}
	close(release)
	waitFor(t, 5*time.Second, "the first commit after success", func() bool { return reader.commitsN() >= 1 })
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("run = %v, want ctx cancellation", err)
	}
}

// The backoff shape: 200ms, 400ms, 800ms ... capped at 5s.
func TestConsumeLoopRetriesWithCappedBackoff(t *testing.T) {
	reader := &loopReader{msgs: []kafkago.Message{loopMsg(0, "m0")}}
	var slept []time.Duration
	loop := consumeLoop{
		reader: reader, logger: quiet, name: "test",
		handle: func(context.Context, kafkago.Message) error { return errors.New("down") },
		sleep: func(_ context.Context, d time.Duration) error {
			slept = append(slept, d)
			if len(slept) >= 7 {
				return context.Canceled // stop the loop under test
			}
			return nil
		},
	}
	if err := loop.run(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("run = %v, want ctx.Canceled after the recorded sleeps", err)
	}
	want := []time.Duration{
		200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond,
		1600 * time.Millisecond, 3200 * time.Millisecond, 5 * time.Second, 5 * time.Second,
	}
	if len(slept) != len(want) {
		t.Fatalf("slept %v, want %v", slept, want)
	}
	for i := range want {
		if slept[i] != want[i] {
			t.Fatalf("slept %v, want %v", slept, want)
		}
	}
}

func TestRetryPolicyDefaultsAndCustomValues(t *testing.T) {
	d := RetryPolicy{}.withDefaults()
	if d.Initial != DefaultRetryInitial || d.Max != DefaultRetryMax {
		t.Fatalf("defaults %+v", d)
	}
	c := RetryPolicy{Initial: time.Second, Max: 3 * time.Second}.withDefaults()
	if c.Initial != time.Second || c.next(time.Second) != 2*time.Second || c.next(2*time.Second) != 3*time.Second {
		t.Fatalf("custom %+v", c)
	}
}

func TestSleepCtxReturnsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepCtx(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepCtx = %v", err)
	}
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("sleepCtx = %v", err)
	}
}

// Only a reader failure (or ctx cancellation) ends the loop.
func TestConsumeLoopFetchFailureStopsRun(t *testing.T) {
	wantErr := errors.New("reader closed")
	loop := consumeLoop{
		reader: &loopReader{fetchErr: wantErr}, logger: quiet, name: "test", sleep: noSleep,
		handle: func(context.Context, kafkago.Message) error { return nil },
	}
	if err := loop.run(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("run = %v, want the fetch error", err)
	}
}

// A poison marker is published to the dead letter topic without a handling
// retry, and the offset then moves past the message.
func TestConsumeLoopDeadLettersPoisonAtOnce(t *testing.T) {
	reader := &loopReader{msgs: []kafkago.Message{loopMsg(0, "poison"), loopMsg(1, "good")}, fetchErr: context.Canceled}
	var dead []string
	handled := map[string]int{}
	var mu sync.Mutex
	ctx, cancel := context.WithCancel(context.Background())
	loop := consumeLoop{
		reader: reader, logger: quiet, name: "test", sleep: noSleep,
		handle: func(_ context.Context, m kafkago.Message) error {
			mu.Lock()
			handled[string(m.Value)]++
			mu.Unlock()
			if string(m.Value) == "poison" {
				return poisonf("no mappable id")
			}
			return nil
		},
		deadLetter: func(_ context.Context, m kafkago.Message, cause error) error {
			mu.Lock()
			dead = append(dead, string(m.Value)+" ("+cause.Error()+")")
			mu.Unlock()
			return nil
		},
	}
	done := make(chan error, 1)
	go func() { done <- loop.run(ctx) }()
	waitFor(t, 5*time.Second, "the poison and the good message committed", func() bool { return reader.commitsN() == 2 })
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("run = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(dead) != 1 || dead[0] != "poison (no mappable id)" {
		t.Fatalf("dead letters = %v, want the poison only", dead)
	}
	if handled["poison"] != 1 {
		t.Fatalf("the poison was handled %d times; it must not be retried", handled["poison"])
	}
}

// A dead-letter publish that fails is retried until it lands: the message is
// never committed past undelivered.
func TestConsumeLoopRetriesAFailedDeadLetterPublish(t *testing.T) {
	reader := &loopReader{msgs: []kafkago.Message{loopMsg(0, "poison")}, fetchErr: context.Canceled}
	attempts := 0
	ctx, cancel := context.WithCancel(context.Background())
	loop := consumeLoop{
		reader: reader, logger: quiet, name: "test", sleep: noSleep,
		handle: func(context.Context, kafkago.Message) error { return poisonf("bad") },
		deadLetter: func(context.Context, kafkago.Message, error) error {
			attempts++
			if attempts < 3 {
				return errors.New("broker down")
			}
			return nil
		},
	}
	done := make(chan error, 1)
	go func() { done <- loop.run(ctx) }()
	waitFor(t, 5*time.Second, "the commit after the third publish", func() bool { return reader.commitsN() == 1 })
	cancel()
	<-done
	if attempts != 3 {
		t.Fatalf("dead-letter attempts = %d, want 3", attempts)
	}
}

// Without a dead-letter writer a poison marker is a transient error to the loop
// (Consumer.HandleMessage hides it in that case), so it is simply retried.
func TestConsumeLoopWithoutADeadLetterSinkRetriesPoison(t *testing.T) {
	reader := &loopReader{msgs: []kafkago.Message{loopMsg(0, "poison")}}
	var slept int
	loop := consumeLoop{
		reader: reader, logger: quiet, name: "test",
		handle: func(context.Context, kafkago.Message) error { return poisonf("bad") },
		sleep: func(context.Context, time.Duration) error {
			slept++
			if slept == 2 {
				return context.Canceled
			}
			return nil
		},
	}
	if err := loop.run(context.Background()); !errors.Is(err, context.Canceled) || reader.commitsN() != 0 {
		t.Fatalf("run = %v, commits %d", err, reader.commitsN())
	}
}

// The fleet escape-18b guard: a handler that fails BECAUSE the process is
// stopping is never dead-lettered; the offset stays uncommitted and the message
// is redelivered on restart.
func TestConsumeLoopNeverDeadLettersACancelledMessage(t *testing.T) {
	reader := &loopReader{msgs: []kafkago.Message{loopMsg(0, "good")}, fetchErr: context.Canceled}
	dead := 0
	ctx, cancel := context.WithCancel(context.Background())
	loop := consumeLoop{
		reader: reader, logger: quiet, name: "test", sleep: noSleep,
		handle: func(context.Context, kafkago.Message) error {
			cancel() // the shutdown lands mid-handle
			return poisonf("cancelled while decoding")
		},
		deadLetter: func(context.Context, kafkago.Message, error) error {
			dead++
			return nil
		},
	}
	if err := loop.run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("run = %v, want the cancellation", err)
	}
	if reader.commitsN() != 0 {
		t.Fatalf("%d commits of a message interrupted by shutdown", reader.commitsN())
	}
	if dead != 0 {
		t.Fatal("a message interrupted by shutdown was dead-lettered")
	}
}

// The effect is durable (the processed-events claim), so a failed commit is
// retried until it succeeds.
func TestConsumeLoopCommitFailureIsRetried(t *testing.T) {
	reader := &loopReader{
		msgs: []kafkago.Message{loopMsg(0, "m0")}, commitErr: errors.New("commit down"), commitErrN: 2,
		fetchErr: context.Canceled, // ends the loop once the stream is drained
	}
	ctx, cancel := context.WithCancel(context.Background())
	loop := consumeLoop{
		reader: reader, logger: quiet, name: "test", sleep: noSleep,
		handle: func(context.Context, kafkago.Message) error { return nil },
	}
	done := make(chan error, 1)
	go func() { done <- loop.run(ctx) }()
	waitFor(t, 5*time.Second, "the retried commit", func() bool { return reader.commitsN() == 3 })
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("run = %v", err)
	}
}

// The dead letter carries the source topic and the cause as headers and keeps
// the key and value untouched.
func TestDeadLetterTopicAndCauseHelpers(t *testing.T) {
	if got := DeadLetterTopic("warehouse.facility.events"); got != "warehouse.facility.events.dlq" {
		t.Fatalf("topic %q", got)
	}
	err := poisonf("no id: %d", 7)
	if got := deadLetterCause(err).Error(); got != "no id: 7" {
		t.Fatalf("cause %q", got)
	}
	plain := errors.New("plain")
	if deadLetterCause(plain) != plain {
		t.Fatal("a non-poison error is its own cause")
	}
}

func TestReaderConfigNeverCommitsOnATimerAndTheFactoryDoesNotDial(t *testing.T) {
	cfg := readerConfig([]string{"broker:9092"}, "t", "g")
	if cfg.GroupID != "g" || cfg.Topic != "t" || cfg.CommitInterval != 0 {
		t.Fatalf("config %+v: CommitInterval must stay unset so commits are synchronous and explicit", cfg)
	}
	f := NewFactory([]string{"127.0.0.1:1"}, nil) // nothing listens there: constructing must not dial
	r := f.NewReader("warehouse.facility.events", "g")
	dlq := f.NewDeadLetter("warehouse.facility.events")
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}
	if err := dlq.Close(); err != nil {
		t.Fatalf("close dlq: %v", err)
	}
}
