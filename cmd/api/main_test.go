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

// required is a complete valid environment; tests drop or change one entry.
func required(overrides map[string]string) map[string]string {
	m := map[string]string{
		"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "k:9092",
		"DOCK_SCHEDULING_CONSUMER_GROUP": "yard-management-dock", "FACILITY_CONSUMER_GROUP": "yard-management-facility",
		"INBOUND_RECEIVING_CONSUMER_GROUP": "yard-management-inbound",
	}
	for k, v := range overrides {
		m[k] = v
	}
	return m
}

var requiredVariables = []string{"DATABASE_URL", "KAFKA_BROKERS", "DOCK_SCHEDULING_CONSUMER_GROUP", "FACILITY_CONSUMER_GROUP", "INBOUND_RECEIVING_CONSUMER_GROUP"}

func TestLoadConfigNamesEveryMissingRequiredVariableTogether(t *testing.T) {
	_, err := loadConfig(env(nil))
	if err == nil {
		t.Fatal("an empty environment is refused")
	}
	for _, name := range requiredVariables {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the boot error does not name %s: %v", name, err)
		}
	}
}

// assertOnlyNames fails unless err names want and none of the other required
// variables.
func assertOnlyNames(t *testing.T, label, want string, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("%s: want a boot error naming %s: %v", label, want, err)
		return
	}
	for _, other := range requiredVariables {
		if other != want && strings.Contains(err.Error(), other) {
			t.Errorf("%s: the error also names %s: %v", label, other, err)
		}
	}
}

func TestLoadConfigNamesExactlyTheMissingOrBlankVariable(t *testing.T) {
	for _, name := range requiredVariables {
		for label, blank := range map[string]string{"missing": "", "blank": "  "} {
			_, err := loadConfig(env(required(map[string]string{name: blank})))
			assertOnlyNames(t, label+" "+name, name, err)
		}
	}
	_, err := loadConfig(env(required(map[string]string{"KAFKA_BROKERS": " , "})))
	assertOnlyNames(t, "blank brokers", "KAFKA_BROKERS", err)
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := loadConfig(env(required(map[string]string{"KAFKA_BROKERS": "a:9092, b:9092"})))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.httpAddr != ":8080" || cfg.drainDelay != 5*time.Second || len(cfg.brokers) != 2 || cfg.brokers[1] != "b:9092" {
		t.Fatalf("defaults %+v", cfg)
	}
	if cfg.dockGroup != "yard-management-dock" || cfg.facilityGroup != "yard-management-facility" || cfg.inboundGroup != "yard-management-inbound" {
		t.Fatalf("groups %+v", cfg)
	}
}

func TestLoadConfigOverrides(t *testing.T) {
	cfg, err := loadConfig(env(required(map[string]string{
		"KAFKA_BROKERS": "a:9092", "HTTP_ADDR": "127.0.0.1:9", "SHUTDOWN_DRAIN_DELAY": "0s", "LOG_LEVEL": "debug",
	})))
	if err != nil || cfg.httpAddr != "127.0.0.1:9" || cfg.drainDelay != 0 || cfg.logLevel != "debug" {
		t.Fatalf("overrides %+v %v", cfg, err)
	}
	for _, bad := range []string{"soon", "-1s"} {
		_, err = loadConfig(env(required(map[string]string{"SHUTDOWN_DRAIN_DELAY": bad})))
		if err == nil || !strings.Contains(err.Error(), "SHUTDOWN_DRAIN_DELAY") {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestRunRefusesAMissingConfigurationBeforeOpeningAnything(t *testing.T) {
	for _, name := range []string{"DATABASE_URL", "KAFKA_BROKERS", "DOCK_SCHEDULING_CONSUMER_GROUP", "FACILITY_CONSUMER_GROUP", "INBOUND_RECEIVING_CONSUMER_GROUP"} {
		t.Setenv(name, "")
	}
	err := run(context.Background())
	for _, name := range []string{"DATABASE_URL", "KAFKA_BROKERS", "DOCK_SCHEDULING_CONSUMER_GROUP", "FACILITY_CONSUMER_GROUP", "INBOUND_RECEIVING_CONSUMER_GROUP"} {
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("run = %v, want it to name %s", err, name)
		}
	}
}

func TestRunFailsWhenPostgresIsUnreachable(t *testing.T) {
	old, oldBackoff := bootAttempts, bootBackoff
	bootAttempts, bootBackoff = 2, time.Millisecond
	defer func() { bootAttempts, bootBackoff = old, oldBackoff }()
	t.Setenv("DATABASE_URL", "postgres://yard:yard@127.0.0.1:1/yard?sslmode=disable&connect_timeout=1")
	t.Setenv("KAFKA_BROKERS", "127.0.0.1:1")
	t.Setenv("DOCK_SCHEDULING_CONSUMER_GROUP", "g1")
	t.Setenv("FACILITY_CONSUMER_GROUP", "g2")
	t.Setenv("INBOUND_RECEIVING_CONSUMER_GROUP", "g3")
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
	go func() { done <- serve(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), srv, ln, readiness, 0, nil) }()

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
	err = serve(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), &http.Server{}, ln, inboundhttp.NewReadiness(0, 0), 0, nil)
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
