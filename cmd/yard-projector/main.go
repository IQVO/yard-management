// Command yard-projector is the WRITER of the yard-management analytics read
// side (ADR 0006). It consumes this service's own analytics topic
// (warehouse.yard-management.analytics) under a fixed, env-supplied consumer
// group, projects each event into the "analytics" schema of the analytical
// database (embedded migrations, its own role) and serves only /healthz and
// /readyz on an admin port. It is the only writer of that schema; the
// read-only side is cmd/yard-reports.
//
// It never touches the OLTP tables and calls no sibling context. Kafka is
// dialled lazily (the reader connects inside its first fetch), so a broker
// outage at boot does not crash the process; the analytical database dial and
// migrations happen at boot and a failure is a boot error naming the variable.
// There is no in-memory mode: a missing variable is a boot error naming it.
//
// Environment:
//
//	ANALYTICS_DATABASE_URL    REQUIRED. Analytical Postgres DSN of the projector's role, DIRECT
//	KAFKA_BROKERS             REQUIRED. Comma-separated brokers
//	ANALYTICS_CONSUMER_GROUP  REQUIRED. The projector's fixed consumer group
//	ADMIN_ADDR                /healthz and /readyz listen address (default :8091)
//	LOG_LEVEL                 debug | info | warn (or warning) | error (default info)
//
// Shutdown: /readyz flips to 503, the admin server drains, the in-flight
// message is committed or left uncommitted, the reader closes, then the pool.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/claudioed/yard-management/internal/adapters/inbound/http"
	inboundkafka "github.com/claudioed/yard-management/internal/adapters/inbound/kafka"
	"github.com/claudioed/yard-management/internal/adapters/outbound/analyticsstore"
	outboundkafka "github.com/claudioed/yard-management/internal/adapters/outbound/kafka"
)

// shutdownTimeout bounds the admin server drain and the wait for the consumer
// loop to finish the message it was handling.
const shutdownTimeout = 10 * time.Second

// Environment variables of the projector.
const (
	envAnalyticsURL = "ANALYTICS_DATABASE_URL"
	envBrokers      = "KAFKA_BROKERS"
	envGroup        = inboundkafka.EnvAnalyticsGroup
	envAdminAddr    = "ADMIN_ADDR"
	envLogLevel     = "LOG_LEVEL"
	defaultAdmin    = ":8091"
)

// config is the projector's environment.
type config struct {
	analyticsURL string
	brokers      []string
	group        string
	adminAddr    string
	logLevel     string
}

// loadConfig reads the environment through getenv (os.Getenv in main). Every
// problem is reported together, each naming its variable.
func loadConfig(getenv func(string) string) (config, error) {
	c := config{
		analyticsURL: strings.TrimSpace(getenv(envAnalyticsURL)),
		group:        strings.TrimSpace(getenv(envGroup)),
		adminAddr:    defaultAdmin,
		logLevel:     getenv(envLogLevel),
	}
	if v := getenv(envAdminAddr); v != "" {
		c.adminAddr = v
	}
	for _, b := range strings.Split(getenv(envBrokers), ",") {
		if b = strings.TrimSpace(b); b != "" {
			c.brokers = append(c.brokers, b)
		}
	}
	var problems []error
	if c.analyticsURL == "" {
		problems = append(problems, fmt.Errorf("%s is required: the projector writes only to the analytical database", envAnalyticsURL))
	}
	if len(c.brokers) == 0 {
		problems = append(problems, fmt.Errorf("%s is required: the projector consumes the analytics topic", envBrokers))
	}
	if c.group == "" {
		problems = append(problems, fmt.Errorf("%s is required: the projector's consumer group comes from the environment", envGroup))
	}
	return c, errors.Join(problems...)
}

func main() { os.Exit(realMain()) }

// realMain is main without os.Exit, so its deferred stop runs.
func realMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Getenv); err != nil {
		slog.Error("yard-projector exited with error", "error", err)
		return 1
	}
	return 0
}

// run boots the projector and blocks until ctx is cancelled (a clean stop,
// nil) or the consumer or the admin server dies (an error: a projector whose
// loop died must not keep answering probes while consuming nothing).
func run(ctx context.Context, getenv func(string) string) error {
	cfg, err := loadConfig(getenv)
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	logger := newLogger(cfg.logLevel)
	slog.SetDefault(logger)

	pool, err := openAnalyticsPool(ctx, cfg.analyticsURL)
	if err != nil {
		return err
	}
	defer pool.Close() // runs last: after the consumer loop stopped touching it

	consumer, err := inboundkafka.NewAnalyticsConsumer(inboundkafka.NewFactory(cfg.brokers, logger), cfg.group, analyticsstore.NewProjection(pool), logger)
	if err != nil {
		return err
	}
	defer func() {
		if err := consumer.Close(); err != nil {
			logger.Error("closing the analytics consumer failed", "error", err)
		}
	}()

	readiness := inboundhttp.NewReadiness(inboundhttp.DefaultReadinessTTL, inboundhttp.DefaultReadinessTimeout,
		inboundhttp.Dependency{Name: "postgres", Check: pool.Ping},
		inboundhttp.Dependency{Name: "kafka", Check: outboundkafka.NewBrokerCheck(cfg.brokers).Check},
	)
	admin := &http.Server{Addr: cfg.adminAddr, Handler: inboundhttp.NewAdminRouter(readiness), ReadHeaderTimeout: 5 * time.Second}
	adminErr := make(chan error, 1)
	go func() {
		logger.Info("projector admin server listening", "addr", cfg.adminAddr)
		if err := admin.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			adminErr <- err
		}
	}()

	consumerCtx, cancelConsumer := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelConsumer()
	consumerDone := make(chan error, 1)
	go func() {
		logger.Info("analytics consumer running", "topic", consumer.Topic, "group_id", cfg.group, "brokers", cfg.brokers)
		consumerDone <- consumer.Run(consumerCtx)
	}()

	var runErr error
	consumerExited := false
	select {
	case <-ctx.Done():
	case err := <-consumerDone:
		// The consumer returns only on cancellation or an unrecoverable
		// reader failure: exit non-zero so the pod restarts.
		consumerExited = true
		runErr = fmt.Errorf("analytics consumer stopped unexpectedly: %w", err)
	case err := <-adminErr:
		runErr = fmt.Errorf("admin server failed: %w", err)
	}

	// Graceful shutdown: flip readiness first, drain the admin server, then
	// let the consumer finish the message in flight before the pool closes.
	readiness.SetNotReady()
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	runErr = errors.Join(runErr, admin.Shutdown(shutdownCtx))
	cancelConsumer()
	if !consumerExited {
		select {
		case err := <-consumerDone:
			// Cancellation is the expected end; anything else deserves a line.
			if !errors.Is(err, context.Canceled) {
				logger.Warn("analytics consumer returned an error at shutdown", "error", err)
			}
		case <-shutdownCtx.Done():
			logger.Warn("analytics consumer did not stop before the shutdown deadline")
		}
	}
	return runErr
}

// openAnalyticsPool applies the embedded analytical migrations over the
// direct DSN, opens the writer pool and pings it.
func openAnalyticsPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	if err := analyticsstore.RunMigrations(ctx, url); err != nil {
		return nil, fmt.Errorf("run analytics migrations on %s: %w", envAnalyticsURL, err)
	}
	pool, err := analyticsstore.NewPool(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("open analytics pool on %s: %w", envAnalyticsURL, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping %s: %w", envAnalyticsURL, err)
	}
	return pool, nil
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
