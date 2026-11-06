package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	inboundhttp "github.com/claudioed/yard-management/internal/adapters/inbound/http"
	inboundkafka "github.com/claudioed/yard-management/internal/adapters/inbound/kafka"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeReader blocks in FetchMessage until its context ends, or fails at once
// with failWith.
type fakeReader struct {
	failWith error
	closed   atomic.Bool
}

func (r *fakeReader) FetchMessage(ctx context.Context) (kafkago.Message, error) {
	if r.failWith != nil {
		return kafkago.Message{}, r.failWith
	}
	<-ctx.Done()
	return kafkago.Message{}, ctx.Err()
}
func (r *fakeReader) CommitMessages(context.Context, ...kafkago.Message) error { return nil }
func (r *fakeReader) Close() error                                             { r.closed.Store(true); return nil }

func fakeConsumer(name string, r *fakeReader) *inboundkafka.Consumer {
	return &inboundkafka.Consumer{Name: name, Topic: name + ".events", Reader: r, Logger: quiet()}
}

func TestBuildConsumersCreatesTheThreeConsumersOnTheirTopics(t *testing.T) {
	cfg, err := loadConfig(env(required(nil)))
	if err != nil {
		t.Fatal(err)
	}
	consumers, err := buildConsumers(cfg, nil, quiet())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, c := range consumers {
			_ = c.Close()
		}
	}()
	var topics []string
	for _, c := range consumers {
		topics = append(topics, c.Topic)
	}
	if got := strings.Join(topics, ","); got != "warehouse.dock-scheduling.events,warehouse.facility.events,warehouse.inbound-receiving.events" {
		t.Fatalf("topics %s", got)
	}
}

func TestBuildConsumersRefusesABlankGroupNamingIt(t *testing.T) {
	cfg, err := loadConfig(env(required(nil)))
	if err != nil {
		t.Fatal(err)
	}
	cfg.facilityGroup = ""
	if _, err := buildConsumers(cfg, nil, quiet()); !errors.Is(err, inboundkafka.ErrGroupRequired) || !strings.Contains(err.Error(), "FACILITY_CONSUMER_GROUP") {
		t.Fatalf("buildConsumers = %v", err)
	}
}

func TestStopCancelsWaitsAndClosesEveryConsumer(t *testing.T) {
	r1, r2 := &fakeReader{}, &fakeReader{}
	set := startConsumers(context.Background(), quiet(), []*inboundkafka.Consumer{fakeConsumer("a", r1), fakeConsumer("b", r2)})
	done := make(chan error, 1)
	go func() { done <- set.stop() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stop = %v, want nil: a cancelled consumer is a clean stop", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stop did not return")
	}
	if !r1.closed.Load() || !r2.closed.Load() {
		t.Fatal("stop closes every reader")
	}
	select {
	case err := <-set.failures():
		t.Fatalf("a clean stop is not a failure: %v", err)
	default:
	}
}

func TestADeadConsumerIsReportedByName(t *testing.T) {
	boom := errors.New("broker gone")
	set := startConsumers(context.Background(), quiet(), []*inboundkafka.Consumer{fakeConsumer("facility", &fakeReader{failWith: boom})})
	defer func() { _ = set.stop() }()
	select {
	case err := <-set.failures():
		if !errors.Is(err, boom) || !strings.Contains(err.Error(), "facility died") {
			t.Fatalf("failure %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the failure was never reported")
	}
}

func TestServeShutsDownAndReturnsWhenAConsumerDies(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	readiness := inboundhttp.NewReadiness(0, 0)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })}
	failed := make(chan error, 1)
	failed <- errors.New("dock-scheduling died: boom")
	err = serve(context.Background(), quiet(), srv, ln, readiness, 0, failed)
	if err == nil || !strings.Contains(err.Error(), "dock-scheduling died") || readiness.Ready() {
		t.Fatalf("serve = %v, ready=%v: a dead consumer drains the process and fails it", err, readiness.Ready())
	}
}
