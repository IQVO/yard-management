package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Environment variables of the API process.
const (
	envHTTPAddr      = "HTTP_ADDR"
	envDatabaseURL   = "DATABASE_URL"
	envKafkaBrokers  = "KAFKA_BROKERS"
	envDrainDelay    = "SHUTDOWN_DRAIN_DELAY"
	envLogLevel      = "LOG_LEVEL"
	defaultHTTPAddr  = ":8080"
	defaultDrainWait = 5 * time.Second
)

// config is the validated environment of the API process.
type config struct {
	httpAddr   string
	brokers    []string
	drainDelay time.Duration
	logLevel   string
}

// loadConfig reads and validates the environment. DATABASE_URL and
// KAFKA_BROKERS are required (there is no in-memory mode and no log sink): a
// missing one is a boot error that names it, and both are reported together.
// DATABASE_URL itself is read by the Postgres adapter (it also honours
// MIGRATIONS_DATABASE_URL); only its presence is checked here.
func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{httpAddr: defaultHTTPAddr, drainDelay: defaultDrainWait, logLevel: getenv(envLogLevel)}
	var problems []error
	if strings.TrimSpace(getenv(envDatabaseURL)) == "" {
		problems = append(problems, fmt.Errorf("%s is required: the API runs on Postgres only", envDatabaseURL))
	}
	cfg.brokers = splitList(getenv(envKafkaBrokers))
	if len(cfg.brokers) == 0 {
		problems = append(problems, fmt.Errorf("%s is required: the readiness check and the outbox relay need Kafka", envKafkaBrokers))
	}
	if v := getenv(envHTTPAddr); v != "" {
		cfg.httpAddr = v
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
