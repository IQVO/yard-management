//go:build integration

package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	// Real Postgres and Kafka come from the shared testcontainers helpers.
	_ "github.com/testcontainers/testcontainers-go/modules/kafka"
	_ "github.com/testcontainers/testcontainers-go/modules/postgres"

	outboundkafka "github.com/claudioed/yard-management/internal/adapters/outbound/kafka"
	"github.com/claudioed/yard-management/internal/adapters/outbound/postgres"
	"github.com/claudioed/yard-management/internal/testing/kafkatest"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
	"github.com/claudioed/yard-management/internal/testing/repocontract"
)

func TestMain(m *testing.M) {
	m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
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

// run() on real Postgres and Kafka: the outbox rows of a unit of work reach
// the fleet topics, and cancelling the context stops it gracefully (nil error).
func TestRunRelaysTheOutboxAndStopsGracefully(t *testing.T) {
	for _, topic := range outboundkafka.Topics() {
		kafkatest.CreateTopic(t, topic)
	}
	dsn := pgtest.URL(t)
	pool := pgtest.Open(t, dsn)
	m := repocontract.NewMove(t, "mv-1", "trl-1", repocontract.Spot("WH1-P1"), 5, time.Time{}, 0)
	if err := postgres.NewOutbox(pool).Publish(context.Background(), m.PullEvents()...); err != nil {
		t.Fatal(err)
	}

	cfg := config{
		databaseURL: dsn, brokers: strings.Join(kafkatest.Brokers(t), ","), maxAttempts: 5,
		interval: 10 * time.Millisecond, healthAddr: freeAddr(t),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))) }()

	got := kafkatest.ReadN(t, outboundkafka.TopicAnalytics, 1, 60*time.Second)
	if got[0].Key != "mv-1" || !strings.Contains(string(got[0].Value), `"type":"com.warehouse.wms.yard-management.yardmove.YardMoveRequested"`) {
		t.Fatalf("analytics message = %+v", got[0])
	}
	deadline := time.Now().Add(30 * time.Second)
	for pgtest.Count(t, pool, "outbox") != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the relayed row was never deleted")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run = %v, want nil after a graceful stop", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("run did not stop after the context was cancelled")
	}
}
