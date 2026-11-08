package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestLoadConfigNamesTheMissingVariable(t *testing.T) {
	if _, err := loadConfig(env(map[string]string{"KAFKA_BROKERS": "k:9092"}), quiet()); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("err = %v, want it to name DATABASE_URL", err)
	}
	if _, err := loadConfig(env(map[string]string{"DATABASE_URL": "postgres://x"}), quiet()); err == nil || !strings.Contains(err.Error(), "KAFKA_BROKERS") {
		t.Fatalf("err = %v, want it to name KAFKA_BROKERS", err)
	}
}

func TestLoadConfigDefaultsAndOverrides(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "k:9092"}
	cfg, err := loadConfig(env(base), quiet())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.maxAttempts != 20 || cfg.interval != time.Second || cfg.healthAddr != ":8081" || cfg.databaseURL != "postgres://x" || cfg.brokers != "k:9092" {
		t.Fatalf("defaults = %+v", cfg)
	}
	base["OUTBOX_POLL_INTERVAL"], base["RELAY_HEALTH_ADDR"], base["OUTBOX_MAX_ATTEMPTS"] = "250ms", ":9999", "7"
	cfg, err = loadConfig(env(base), quiet())
	if err != nil || cfg.interval != 250*time.Millisecond || cfg.healthAddr != ":9999" || cfg.maxAttempts != 7 {
		t.Fatalf("overrides = %+v, %v", cfg, err)
	}
	for _, bad := range []string{"soon", "0s", "-1s"} {
		base["OUTBOX_POLL_INTERVAL"] = bad
		if _, err := loadConfig(env(base), quiet()); err == nil || !strings.Contains(err.Error(), "OUTBOX_POLL_INTERVAL") {
			t.Errorf("interval %q: err = %v, want it to name OUTBOX_POLL_INTERVAL", bad, err)
		}
	}
}

func TestOutboxMaxAttempts(t *testing.T) {
	cases := map[string]int{"": 20, "0": 0, "3": 3, "x": 20, "-2": 20, "1.5": 20}
	for in, want := range cases {
		if got := outboxMaxAttempts(env(map[string]string{"OUTBOX_MAX_ATTEMPTS": in}), quiet()); got != want {
			t.Errorf("OUTBOX_MAX_ATTEMPTS=%q -> %d, want %d", in, got, want)
		}
	}
}

func TestHealthz(t *testing.T) {
	srv := httptest.NewServer(healthMux())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("healthz = %d %q", resp.StatusCode, body)
	}
	other, err := http.Get(srv.URL + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Body.Close() }()
	if other.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown path = %d", other.StatusCode)
	}
}
