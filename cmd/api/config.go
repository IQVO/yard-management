package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Environment variables of the API process.
const (
	envHTTPAddr       = "HTTP_ADDR"
	envDatabaseURL    = "DATABASE_URL"
	envKafkaBrokers   = "KAFKA_BROKERS"
	envDrainDelay     = "SHUTDOWN_DRAIN_DELAY"
	envLogLevel       = "LOG_LEVEL"
	envDockGroup      = "DOCK_SCHEDULING_CONSUMER_GROUP"
	envFacilityGroup  = "FACILITY_CONSUMER_GROUP"
	envInboundRecvGrp = "INBOUND_RECEIVING_CONSUMER_GROUP"
	defaultHTTPAddr   = ":8080"
	defaultDrainWait  = 5 * time.Second
)

// config is the validated environment of the API process.
type config struct {
	httpAddr   string
	brokers    []string
	drainDelay time.Duration
	logLevel   string

	// The three consumer groups (ADR 0009). Mandatory and never defaulted: a
	// process started by hand must not join a live group by accident.
	dockGroup, facilityGroup, inboundGroup string
}

// loadConfig reads and validates the environment. DATABASE_URL, KAFKA_BROKERS
// and the three consumer groups are required (there is no in-memory mode and
// no log sink): every missing one is named in ONE boot error. DATABASE_URL
// itself is read by the Postgres adapter (it also honours
// MIGRATIONS_DATABASE_URL); only its presence is checked here.
func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{httpAddr: defaultHTTPAddr, drainDelay: defaultDrainWait, logLevel: getenv(envLogLevel)}
	var problems []error
	if strings.TrimSpace(getenv(envDatabaseURL)) == "" {
		problems = append(problems, fmt.Errorf("%s is required: the API runs on Postgres only", envDatabaseURL))
	}
	cfg.brokers = splitList(getenv(envKafkaBrokers))
	if len(cfg.brokers) == 0 {
		problems = append(problems, fmt.Errorf("%s is required: the readiness check and the consumers need Kafka", envKafkaBrokers))
	}
	for _, g := range []struct {
		env, what string
		dst       *string
	}{
		{envDockGroup, "the dock-scheduling consumer", &cfg.dockGroup},
		{envFacilityGroup, "the facility-layout consumer", &cfg.facilityGroup},
		{envInboundRecvGrp, "the inbound-receiving consumer", &cfg.inboundGroup},
	} {
		if *g.dst = strings.TrimSpace(getenv(g.env)); *g.dst == "" {
			problems = append(problems, fmt.Errorf("%s is required: the consumer group of %s", g.env, g.what))
		}
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
