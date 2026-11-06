// Command yard-reports is the READ side of the yard-management analytics
// (ADR 0006): GET /reports/yard-dwell, /reports/door-utilisation,
// /reports/hostler-productivity and /reports/freshness over the "analytics"
// schema, through a read-only pool. It never writes and never opens the OLTP
// tables; the writer is cmd/yard-projector, which also owns the migrations: this
// process is not ready until the projector has created the schema.
//
// Environment:
//
//	ANALYTICS_READER_DATABASE_URL  DSN of the reports role (read-only grants);
//	                               falls back to ANALYTICS_DATABASE_URL. One of the two is REQUIRED
//	HTTP_ADDR                      listen address (default :8092)
//	SHUTDOWN_DRAIN_DELAY           wait after flipping /readyz before closing (default 5s)
//	LOG_LEVEL                      debug | info | warn (or warning) | error (default info)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	inboundhttp "github.com/claudioed/yard-management/internal/adapters/inbound/http"
	"github.com/claudioed/yard-management/internal/adapters/outbound/analyticsstore"
)

// shutdownTimeout bounds the HTTP server's graceful drain.
const shutdownTimeout = 10 * time.Second

// Environment variables of the reports process.
const (
	envReaderURL    = "ANALYTICS_READER_DATABASE_URL"
	envAnalyticsURL = "ANALYTICS_DATABASE_URL"
	envHTTPAddr     = "HTTP_ADDR"
	envDrainDelay   = "SHUTDOWN_DRAIN_DELAY"
	envLogLevel     = "LOG_LEVEL"
	defaultHTTPAddr = ":8092"
	defaultDrain    = 5 * time.Second
)

// config is the reports process's environment.
type config struct {
	databaseURL string
	httpAddr    string
	drainDelay  time.Duration
	logLevel    string
}

// loadConfig reads the environment through getenv; every problem names its
// variable and all are reported together.
func loadConfig(getenv func(string) string) (config, error) {
	c := config{httpAddr: defaultHTTPAddr, drainDelay: defaultDrain, logLevel: getenv(envLogLevel)}
	var problems []error
	if c.databaseURL = strings.TrimSpace(getenv(envReaderURL)); c.databaseURL == "" {
		c.databaseURL = strings.TrimSpace(getenv(envAnalyticsURL))
	}
	if c.databaseURL == "" {
		problems = append(problems, fmt.Errorf("%s (or %s) is required: the reports read only the analytical database", envReaderURL, envAnalyticsURL))
	}
	if v := getenv(envHTTPAddr); v != "" {
		c.httpAddr = v
	}
	if v := getenv(envDrainDelay); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			problems = append(problems, fmt.Errorf("%s %q is not a non-negative duration", envDrainDelay, v))
		} else {
			c.drainDelay = d
		}
	}
	return c, errors.Join(problems...)
}

func main() { os.Exit(realMain()) }

// realMain is main without os.Exit, so its deferred stop runs.
func realMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Getenv); err != nil {
		slog.Error("yard-reports exited with error", "error", err)
		return 1
	}
	return 0
}

// run serves the reports until ctx is cancelled (nil) or the listener fails.
func run(ctx context.Context, getenv func(string) string) error {
	cfg, err := loadConfig(getenv)
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	logger := newLogger(cfg.logLevel)
	slog.SetDefault(logger)

	pool, err := analyticsstore.NewReadOnlyPool(ctx, cfg.databaseURL)
	if err != nil {
		return fmt.Errorf("open the read-only analytics pool: %w", err)
	}
	defer pool.Close()

	// Ready only once the projector has created the schema: the probe reads
	// the freshness table itself.
	readiness := inboundhttp.NewReadiness(inboundhttp.DefaultReadinessTTL, inboundhttp.DefaultReadinessTimeout,
		inboundhttp.Dependency{Name: "analytics", Check: func(ctx context.Context) error {
			_, _, err := analyticsstore.NewReader(pool).LastEventAt(ctx)
			return err
		}},
	)
	server := &inboundhttp.Server{Reports: analyticsstore.NewReader(pool), Readiness: readiness}
	ln, err := net.Listen("tcp", cfg.httpAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.httpAddr, err)
	}
	httpServer := &http.Server{Handler: inboundhttp.NewReportsRouter(server), ReadHeaderTimeout: 5 * time.Second}
	return serve(ctx, logger, httpServer, ln, readiness, cfg.drainDelay)
}

// serve runs httpServer on ln until ctx is cancelled or the listener fails,
// then shuts down in the fleet order: /readyz flips to 503, the drain delay
// passes, the server drains in-flight requests.
func serve(ctx context.Context, logger *slog.Logger, httpServer *http.Server, ln net.Listener, readiness *inboundhttp.Readiness, drainDelay time.Duration) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("reports server listening", "addr", ln.Addr().String())
		if err := httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	readiness.SetNotReady()
	if drainDelay > 0 {
		logger.Info("shutdown: readiness flipped to not-ready; waiting for traffic to drain", "drain_delay", drainDelay.String())
		time.Sleep(drainDelay)
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
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
