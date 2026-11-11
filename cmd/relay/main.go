// Command relay is the outbox relay worker: it drains the transactional outbox
// of yard-management onto Kafka as CloudEvents (the eleven integration types on
// warehouse.yard-management.events, every type on .analytics, unencodable rows
// on .events.dlq) and stops gracefully on SIGINT/SIGTERM: the pass in flight
// ends, what it already relayed is committed, then the sink and the pool are
// closed.
//
// Environment: DATABASE_URL and KAFKA_BROKERS are required (boot errors name
// them); OUTBOX_MAX_ATTEMPTS (default 20, 0 = unlimited) caps a poison row;
// OUTBOX_POLL_INTERVAL (default 1s) paces the poll; RELAY_HEALTH_ADDR
// (default :8081) serves /healthz for the liveness probe.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	outboundkafka "github.com/claudioed/yard-management/internal/adapters/outbound/kafka"
	"github.com/claudioed/yard-management/internal/adapters/outbound/postgres"
)

// shutdownGrace bounds the health server's own shutdown.
const shutdownGrace = 5 * time.Second

type config struct {
	databaseURL string
	brokers     string
	maxAttempts int
	interval    time.Duration
	healthAddr  string
}

func main() { os.Exit(realMain()) }

// realMain is main without os.Exit, so its deferred stop runs.
func realMain() int {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := loadConfig(os.Getenv, logger)
	if err != nil {
		logger.Error("relay: invalid configuration", "error", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg, logger); err != nil {
		logger.Error("relay: stopped with an error", "error", err)
		return 1
	}
	logger.Info("relay: stopped")
	return 0
}

// loadConfig reads the environment; the errors name the variable at fault.
func loadConfig(getenv func(string) string, logger *slog.Logger) (config, error) {
	cfg := config{
		databaseURL: getenv(postgres.EnvDatabaseURL),
		brokers:     getenv("KAFKA_BROKERS"),
		maxAttempts: outboxMaxAttempts(getenv, logger),
		interval:    time.Second,
		healthAddr:  ":8081",
	}
	if cfg.databaseURL == "" {
		return config{}, errors.New("DATABASE_URL is required")
	}
	if cfg.brokers == "" {
		return config{}, errors.New("KAFKA_BROKERS is required")
	}
	if v := getenv("OUTBOX_POLL_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return config{}, fmt.Errorf("OUTBOX_POLL_INTERVAL %q is not a positive duration", v)
		}
		cfg.interval = d
	}
	if v := getenv("RELAY_HEALTH_ADDR"); v != "" {
		cfg.healthAddr = v
	}
	return cfg, nil
}

// outboxMaxAttempts reads OUTBOX_MAX_ATTEMPTS: unset or invalid means the
// fleet default of 20 (an invalid value is logged), 0 means unlimited.
func outboxMaxAttempts(getenv func(string) string, logger *slog.Logger) int {
	v := getenv("OUTBOX_MAX_ATTEMPTS")
	if v == "" {
		return postgres.DefaultOutboxMaxAttempts
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		logger.Warn("relay: OUTBOX_MAX_ATTEMPTS is not a non-negative integer, using the default",
			"value", v, "default", postgres.DefaultOutboxMaxAttempts)
		return postgres.DefaultOutboxMaxAttempts
	}
	return n
}

// run wires the relay on real Postgres and Kafka and blocks until ctx is
// cancelled; it returns nil after a graceful stop.
func run(ctx context.Context, cfg config, logger *slog.Logger) error {
	pool, err := postgres.NewPool(ctx, cfg.databaseURL)
	if err != nil {
		return fmt.Errorf("connect to DATABASE_URL: %w", err)
	}
	defer pool.Close()

	sink := outboundkafka.NewRelaySink(cfg.brokers)
	defer func() { _ = sink.Close() }()

	relay := postgres.NewOutboxRelay(pool, outboundkafka.NewEncoder(), sink,
		postgres.WithLogger(logger),
		postgres.WithInterval(cfg.interval),
		postgres.WithMaxAttempts(cfg.maxAttempts),
	)

	health := &http.Server{
		Addr:              cfg.healthAddr,
		Handler:           healthMux(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	healthErr := make(chan error, 1)
	go func() {
		if err := health.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			healthErr <- err
		}
	}()

	relayCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- relay.Run(relayCtx) }()

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-healthErr:
		cancel()
	}
	// Graceful shutdown: wait for the relay's pass in flight to end (it
	// commits what it relayed and hands its row back un-failed), then stop
	// the health server; the deferred sink and pool closes come last.
	if err := <-done; err != nil && runErr == nil {
		runErr = err
	}
	shutdownCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer stop()
	if err := health.Shutdown(shutdownCtx); err != nil && runErr == nil {
		runErr = err
	}
	return runErr
}

func healthMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}
