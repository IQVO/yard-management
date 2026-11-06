// Command mcp is the composition root for the yard-management MCP server
// (docs/adr/0007-mcp-server.md): it wires env config into the Postgres
// adapters, the adapters into the SAME use cases the REST API runs and those
// into the inbound MCP adapter, then serves MCP over Streamable HTTP (stateless;
// no stdio, no SSE-only transport) on real Postgres: there is no in-memory mode.
// It is a second, independent deployable alongside cmd/api.
//
// The surface is eight L1 read tools and six L3 write tools. A write runs in
// one unit of work with the idempotency key and the transactional outbox rows
// it raises, so this binary never needs Kafka: the events leave through the
// outbox, which cmd/relay publishes. It never runs the relay or the consumers;
// KAFKA_BROKERS is required only for the /readyz broker check (a deployment
// without Kafka refuses to boot instead of accepting writes whose events never
// leave).
//
// There is NO auth of any kind (fleet-wide revert 2026-09-11): no keys, no
// bearer checks. Access control is the in-cluster ClusterIP boundary.
//
// Environment:
//
//	MCP_ADDR                listen address (default :8090)
//	DATABASE_URL            Postgres DSN (REQUIRED; boot error naming it)
//	MIGRATIONS_DATABASE_URL direct DSN for the boot migrations (default DATABASE_URL)
//	KAFKA_BROKERS           comma-separated brokers (REQUIRED; boot error naming it; /readyz asks one for metadata)
//	SHUTDOWN_DRAIN_DELAY    wait after flipping /readyz before closing (default 5s)
//	LOG_LEVEL               debug|info|warn|error (default info)
//
// GET /healthz is liveness only; GET /readyz is 200 only while the process is
// not draining AND Postgres answers a ping AND a Kafka broker answers a
// topic-less metadata request. The MCP endpoint is served at / and at /mcp.
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
		slog.Error("mcp server exited with error", "error", err)
		os.Exit(1)
	}
}

// run boots the server and serves until SIGINT/SIGTERM or until ctx is
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

	ln, err := net.Listen("tcp", cfg.addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.addr, err)
	}
	srv := &http.Server{Handler: newRouter(rt), ReadHeaderTimeout: 5 * time.Second}
	return serve(ctx, logger, srv, ln, rt.readiness, cfg.drainDelay)
}

// serve runs srv on ln until SIGINT/SIGTERM, ctx is cancelled or the listener
// fails, then shuts down in the fleet order: /readyz flips to 503, the drain
// delay passes, the server drains in-flight requests.
func serve(ctx context.Context, logger *slog.Logger, srv *http.Server, ln net.Listener, readiness *inboundhttp.Readiness, drainDelay time.Duration) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("mcp server listening (Streamable HTTP)", "addr", ln.Addr().String())
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
	return srv.Shutdown(shutdownCtx)
}

// newLogger builds the process-wide JSON logger. LOG_LEVEL maps
// debug|info|warn|error (case-insensitive), defaulting to Info.
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
