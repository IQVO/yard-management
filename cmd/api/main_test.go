package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/yard-management/internal/adapters/inbound/http"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadConfigNamesEveryMissingRequiredVariable(t *testing.T) {
	_, err := loadConfig(env(nil))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") || !strings.Contains(err.Error(), "KAFKA_BROKERS") {
		t.Fatalf("both variables are named together: %v", err)
	}
	_, err = loadConfig(env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err == nil || strings.Contains(err.Error(), "DATABASE_URL") || !strings.Contains(err.Error(), "KAFKA_BROKERS") {
		t.Fatalf("only KAFKA_BROKERS is missing: %v", err)
	}
	_, err = loadConfig(env(map[string]string{"KAFKA_BROKERS": " , ", "DATABASE_URL": "postgres://x"}))
	if err == nil || !strings.Contains(err.Error(), "KAFKA_BROKERS") {
		t.Fatalf("blank brokers are missing brokers: %v", err)
	}
	_, err = loadConfig(env(map[string]string{"DATABASE_URL": "  ", "KAFKA_BROKERS": "k:9092"}))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("a blank DATABASE_URL is missing: %v", err)
	}
}

func TestLoadConfigDefaultsAndOverrides(t *testing.T) {
	cfg, err := loadConfig(env(map[string]string{"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "a:9092, b:9092"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.httpAddr != ":8080" || cfg.drainDelay != 5*time.Second || len(cfg.brokers) != 2 || cfg.brokers[1] != "b:9092" {
		t.Fatalf("defaults %+v", cfg)
	}
	cfg, err = loadConfig(env(map[string]string{
		"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "a:9092", "HTTP_ADDR": "127.0.0.1:9", "SHUTDOWN_DRAIN_DELAY": "0s", "LOG_LEVEL": "debug",
	}))
	if err != nil || cfg.httpAddr != "127.0.0.1:9" || cfg.drainDelay != 0 || cfg.logLevel != "debug" {
		t.Fatalf("overrides %+v %v", cfg, err)
	}
	for _, bad := range []string{"soon", "-1s"} {
		_, err = loadConfig(env(map[string]string{"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "a:9092", "SHUTDOWN_DRAIN_DELAY": bad}))
		if err == nil || !strings.Contains(err.Error(), "SHUTDOWN_DRAIN_DELAY") {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestRunRefusesAMissingConfigurationBeforeOpeningAnything(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("KAFKA_BROKERS", "")
	err := run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") || !strings.Contains(err.Error(), "KAFKA_BROKERS") {
		t.Fatalf("run = %v", err)
	}
}

func TestRunFailsWhenPostgresIsUnreachable(t *testing.T) {
	old, oldBackoff := bootAttempts, bootBackoff
	bootAttempts, bootBackoff = 2, time.Millisecond
	defer func() { bootAttempts, bootBackoff = old, oldBackoff }()
	t.Setenv("DATABASE_URL", "postgres://yard:yard@127.0.0.1:1/yard?sslmode=disable&connect_timeout=1")
	t.Setenv("KAFKA_BROKERS", "127.0.0.1:1")
	err := run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "run migrations") {
		t.Fatalf("run = %v", err)
	}
}

func TestRetryReturnsTheLastErrorAndStopsOnACancelledContext(t *testing.T) {
	old, oldBackoff := bootAttempts, bootBackoff
	bootAttempts, bootBackoff = 3, time.Millisecond
	defer func() { bootAttempts, bootBackoff = old, oldBackoff }()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	calls := 0
	err := retry(context.Background(), logger, "step", func() error { calls++; return errors.New("down") })
	if calls != 3 || err == nil || !strings.Contains(err.Error(), "step: down") {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	calls = 0
	if err := retry(context.Background(), logger, "step", func() error {
		if calls++; calls < 2 {
			return errors.New("not yet")
		}
		return nil
	}); err != nil || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := retry(ctx, logger, "step", func() error { return errors.New("down") }); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled context stops the retries: %v", err)
	}
}

func TestServeDrainsAndStopsWhenTheContextIsCancelled(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	readiness := inboundhttp.NewReadiness(0, 0)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), srv, ln, readiness, 0) }()

	resp, err := http.Get("http://" + ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil || readiness.Ready() {
			t.Fatalf("serve = %v, ready=%v", err, readiness.Ready())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return")
	}
}

func TestServeReturnsAListenerFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()
	err = serve(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), &http.Server{}, ln, inboundhttp.NewReadiness(0, 0), 0)
	if err == nil {
		t.Fatal("a closed listener is a serve error")
	}
}

func TestNewLoggerLevels(t *testing.T) {
	for level, want := range map[string]slog.Level{"debug": slog.LevelDebug, "DEBUG": slog.LevelDebug, "warn": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError, "": slog.LevelInfo, "nope": slog.LevelInfo} {
		l := newLogger(level)
		for _, probe := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
			if got := l.Enabled(context.Background(), probe); got != (probe >= want) {
				t.Errorf("level %q: Enabled(%v) = %v", level, probe, got)
			}
		}
	}
}

func TestDepsWiresEveryPortOfTheUseCases(t *testing.T) {
	d := deps(nil, nil, nil, nil)
	for name, port := range map[string]any{
		"Visits": d.Visits, "Trailers": d.Trailers, "Moves": d.Moves, "Checks": d.Checks, "Policies": d.Policies, "Locations": d.Locations,
		"Appointments": d.Appointments, "Closures": d.Closures, "Asns": d.Asns, "Receipts": d.Receipts, "Processed": d.Processed,
		"ApptFeed": d.ApptFeed, "LocationFeed": d.LocationFeed, "ClosureFeed": d.ClosureFeed, "AsnFeed": d.AsnFeed, "ReceiptFeed": d.ReceiptFeed,
		"Events": d.Events, "IDs": d.IDs,
	} {
		if port == nil {
			t.Errorf("port %s is not wired", name)
		}
	}
}
