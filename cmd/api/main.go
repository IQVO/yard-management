// Command api is yard-management's composition root for the REST API: it
// wires env config into the Postgres adapters, the adapters into the use
// cases and the use cases into the chi router (ADR 0008), on real Postgres and
// with a real Kafka broker behind /readyz: there is no in-memory mode.
//
// Environment:
//
//	HTTP_ADDR               listen address (default :8080)
//	DATABASE_URL            Postgres DSN (REQUIRED; boot error naming it)
//	MIGRATIONS_DATABASE_URL direct DSN for the boot migrations (default DATABASE_URL)
//	KAFKA_BROKERS           comma-separated brokers (REQUIRED; /readyz asks one for metadata)
//	SHUTDOWN_DRAIN_DELAY    wait after flipping /readyz before closing (default 5s)
//	LOG_LEVEL               debug|info|warn|error (default info)
//
// The transactional outbox rows the use cases write are relayed to Kafka by
// cmd/relay, not by this process; the Kafka consumers are a later binary.
// GET /readyz is 200 only while the process is not draining AND Postgres
// answers a ping AND a Kafka broker answers a topic-less metadata request
// (verdict cached 3 s, bounded 2 s); /healthz is liveness only.
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
)

// shutdownTimeout bounds the HTTP server's graceful drain.
const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("service exited with error", "error", err)
		os.Exit(1)
	}
}

// run boots the service and serves until SIGINT/SIGTERM or until ctx is
// cancelled (the integration test stops the real entry point that way). A
// misconfiguration is refused before anything is opened or bound.
func run(ctx context.Context) error {
	logger := newLogger(os.Getenv(envLogLevel))
	slog.SetDefault(logger)
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	rt, err := wire(ctx, cfg, os.Getenv, logger)
	if err != nil {
		return err
	}
	// The pool closes LAST, after the HTTP drain.
	defer rt.pool.Close()

	ln, err := net.Listen("tcp", cfg.httpAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.httpAddr, err)
	}
	httpServer := &http.Server{Handler: inboundhttp.NewRouter(rt.server), ReadHeaderTimeout: 5 * time.Second}
	return serve(ctx, logger, httpServer, ln, rt.readiness, cfg.drainDelay)
}

// serve runs httpServer on ln until SIGINT/SIGTERM, ctx is cancelled or the
// listener fails, then shuts down in the fleet order: /readyz flips to 503,
// the drain delay passes, the server drains in-flight requests.
func serve(ctx context.Context, logger *slog.Logger, httpServer *http.Server, ln net.Listener, readiness *inboundhttp.Readiness, drainDelay time.Duration) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", ln.Addr().String())
		if err := httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-errCh:
		return err
	case <-sigCtx.Done():
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

// newLogger builds the JSON logger; LOG_LEVEL debug|info|warn|warning|error
// (case-insensitive).
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
