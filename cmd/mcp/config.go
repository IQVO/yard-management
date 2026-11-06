package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Environment variables of the MCP process.
const (
	envMCPAddr      = "MCP_ADDR"
	envDatabaseURL  = "DATABASE_URL"
	envKafkaBrokers = "KAFKA_BROKERS"
	envDrainDelay   = "SHUTDOWN_DRAIN_DELAY"
	envLogLevel     = "LOG_LEVEL"

	defaultMCPAddr   = ":8090"
	defaultDrainWait = 5 * time.Second
)

// config is the validated environment of the MCP process.
type config struct {
	addr       string
	brokers    []string
	drainDelay time.Duration
	logLevel   string
}

// loadConfig reads and validates the environment before anything is opened.
// DATABASE_URL and KAFKA_BROKERS are required (fleet decision 2026-10-17: no
// in-memory mode, no log sink): a missing one is a boot error that names it,
// and both are reported together. KAFKA_BROKERS is required although this
// binary starts no relay and no consumer: its writes enqueue outbox rows that
// cmd/relay drains, and /readyz reports the broker like the API's, so a
// deployment without Kafka refuses to boot instead of accepting writes whose
// events never leave. DATABASE_URL itself is read by the Postgres adapter (it
// also honours MIGRATIONS_DATABASE_URL); only its presence is checked here.
func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{addr: defaultMCPAddr, drainDelay: defaultDrainWait, logLevel: getenv(envLogLevel)}
	var problems []error
	if strings.TrimSpace(getenv(envDatabaseURL)) == "" {
		problems = append(problems, fmt.Errorf("%s is required: the MCP server runs on Postgres only (there is no in-memory mode)", envDatabaseURL))
	}
	cfg.brokers = splitList(getenv(envKafkaBrokers))
	if len(cfg.brokers) == 0 {
		problems = append(problems, fmt.Errorf("%s is required: the readiness check needs Kafka and the outbox relay publishes the events of these writes there", envKafkaBrokers))
	}
	if v := getenv(envMCPAddr); v != "" {
		cfg.addr = v
	}
	if v := getenv(envDrainDelay); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			problems = append(problems, fmt.Errorf("%s %q is not a non-negative duration", envDrainDelay, v))
		} else {
			cfg.drainDelay = d
		}
	}
	return cfg, errors.Join(problems...)
}

// splitList splits a comma-separated list, dropping blanks.
func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
