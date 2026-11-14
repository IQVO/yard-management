---
id: configuration
title: Configuration
sidebar_label: Configuration
sidebar_position: 1
description: "Every environment variable read by each yard-management binary: name, default, required, meaning."
---

# Configuration

`yard-management` ships **five binaries** (`cmd/api`, `cmd/mcp`, `cmd/relay`,
`cmd/yard-projector`, `cmd/yard-reports`), all in one image. Each is configured
only through environment variables: there is no config file, no flag and no
secret-manager integration. REST, MCP and the reports are unauthenticated
fleet-wide, so there is no token or key to configure.

There is **no in-memory mode and no log sink**. Every binary refuses to boot
without its mandatory variables and names each missing one in a single boot
error, raised before any connection is opened or any port is bound
([ADR 0005](/docs/adr/0005-postgres-persistence-and-outbox),
[ADR 0008](/docs/adr/0008-rest-adapter-and-api-process)). Test-only variables do
not exist: the integration tests start their own containers with testcontainers.

Every binary also reads the four observability variables of the fleet standard
([ADR 0016](/docs/adr/0016-fleet-observability-standard)); none is required.

Where the chart sets a variable, the Helm value is named in the last column
(`charts/yard-management/values.yaml`). Source files are under `cmd/<binary>/`.

## `cmd/api`

Source: `cmd/api/config.go`, `cmd/api/wire.go`, `cmd/api/main.go`.

| Variable | Default | Required? | Meaning | Chart value |
| --- | --- | --- | --- | --- |
| `HTTP_ADDR` | `:8080` | no | Listen address of the REST server (also serves `/healthz` and `/readyz`). | `config.httpAddr` |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the OLTP database. The embedded migrations run at boot; repositories, the outbox, the idempotency store and the processed-events table all live here. | `database.url` or `database.existingSecret` |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct (non-pooled, session-mode) DSN used only for the boot migrations, because golang-migrate's advisory lock does not survive PgBouncer transaction pooling. Empty falls back to `DATABASE_URL`. | `database.migrationsExistingSecretKey` |
| `KAFKA_BROKERS` | none | **yes** | Comma-separated broker list (trimmed, blanks dropped). Used by the three consumers and by the `/readyz` metadata check. | `kafka.brokers` |
| `DOCK_SCHEDULING_CONSUMER_GROUP` | none | **yes** | Consumer group of the `warehouse.dock-scheduling.events` consumer. Never defaulted: a process started by hand must not join a live group by accident. | `config.dockSchedulingConsumerGroup` |
| `FACILITY_CONSUMER_GROUP` | none | **yes** | Consumer group of the `warehouse.facility.events` consumer. | `config.facilityConsumerGroup` |
| `INBOUND_RECEIVING_CONSUMER_GROUP` | none | **yes** | Consumer group of the `warehouse.inbound-receiving.events` consumer. | `config.inboundReceivingConsumerGroup` |
| `ANALYTICS_READER_DATABASE_URL` | none | no | DSN of the read-only analytics reports role. Set: `GET /reports/*` on the API answers from the `analytics` schema. Unset: the report routes answer the contract shape with no rows. | `analytics.readerUrl` (secret key `analytics.readerExistingSecretKey`, `optional: true`) |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | no | After SIGINT/SIGTERM `/readyz` flips to 503 and the process waits this long before closing the listener. Go duration, `0` disables the wait. A negative or unparsable value is a **boot error**. | `config.shutdownDrainDelay` |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn` (or `warning`), `error`, case-insensitive. Anything else silently falls back to `info`. Logs are JSON on stdout. | `config.logLevel` |
| `OTEL_SERVICE_NAME` | `yard-management` | no | `service.name` of this binary's spans and metrics. | `otel.serviceName` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | OTLP/gRPC endpoint of the Collector, `host:port` or a URL. Export never blocks: an unreachable Collector costs telemetry, not availability ([ADR 0016](/docs/adr/0016-fleet-observability-standard)). | `otel.endpoint` (rendered only while `otel.enabled`) |
| `SERVICE_VERSION` | `dev` | no | `service.version` of the resource. | `serviceVersion`, else `image.tag`, else the chart `appVersion` |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` of the resource (read by `telemetry.Setup`). | `environment` |

Boot errors are reported together, for example with an empty environment the
process exits 1 with `invalid configuration:` followed by one line per missing
variable (`DATABASE_URL is required: the API runs on Postgres only`,
`KAFKA_BROKERS is required: …`, and one `<GROUP> is required: the consumer group
of …` line per consumer).

## `cmd/mcp`

Source: `cmd/mcp/config.go`, `cmd/mcp/wire.go`.

| Variable | Default | Required? | Meaning | Chart value |
| --- | --- | --- | --- | --- |
| `MCP_ADDR` | `:8090` | no | Listen address of the Streamable HTTP MCP server (served at `/` and `/mcp`, plus `/healthz` and `/readyz`). | `mcp.httpAddr` |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the same OLTP database as the API. | `database.url` or `database.existingSecret` |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the boot migrations. `cmd/mcp` migrates on start like the API, so it can boot against a fresh database; golang-migrate's advisory lock makes concurrent starts safe. | `database.migrationsExistingSecretKey` |
| `KAFKA_BROKERS` | none | **yes** | Brokers. This binary starts no relay and no consumer, but its writes enqueue outbox rows that `cmd/relay` drains and `/readyz` reports the broker, so a deployment without Kafka refuses to boot. | `kafka.brokers` |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | no | As for the API. | `config.shutdownDrainDelay` |
| `LOG_LEVEL` | `info` | no | As for the API. | `config.logLevel` |
| `OTEL_SERVICE_NAME` | `yard-management-mcp` | no | `service.name` of this binary's spans and metrics. | `otel.serviceName` with the suffix `-mcp` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | OTLP/gRPC endpoint of the Collector, `host:port` or a URL. Export never blocks: an unreachable Collector costs telemetry, not availability ([ADR 0016](/docs/adr/0016-fleet-observability-standard)). | `otel.endpoint` (rendered only while `otel.enabled`) |
| `SERVICE_VERSION` | `dev` | no | `service.version` of the resource. | `serviceVersion`, else `image.tag`, else the chart `appVersion` |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` of the resource (read by `telemetry.Setup`). | `environment` |

`cmd/mcp` has no consumer-group variable and no analytics variable: its
`get_yard_dwell_report` tool answers no rows because the reader is not wired
(`cmd/mcp/wire.go`, `mcpDeps`).

## `cmd/relay`

Source: `cmd/relay/main.go`. The relay does not run migrations: its poll fails
and is retried until the API (or MCP) has created the schema.

| Variable | Default | Required? | Meaning | Chart value |
| --- | --- | --- | --- | --- |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the OLTP database holding the `outbox` table. Missing: exit code 2, `DATABASE_URL is required`. | `database.url` or `database.existingSecret` |
| `KAFKA_BROKERS` | none | **yes** | Brokers the relay publishes to. Missing: exit code 2, `KAFKA_BROKERS is required`. | `kafka.brokers` |
| `OUTBOX_POLL_INTERVAL` | `1s` | no | Pause between polls. Go duration, must be positive: anything else is a boot error naming the variable. | `relay.outboxPollInterval` |
| `OUTBOX_MAX_ATTEMPTS` | `20` | no | Failures after which one outbox row is no longer claimed (a poison row cannot block the relay). `0` = unlimited. Negative or unparsable: WARN log and the default `20`. | `relay.outboxMaxAttempts` |
| `RELAY_HEALTH_ADDR` | `:8081` | no | Listen address of the `/healthz` liveness endpoint (the relay has no `/readyz`). | `relay.healthAddr` |
| `OTEL_SERVICE_NAME` | `yard-management-relay` | no | `service.name` of this binary's spans and metrics. | `otel.serviceName` with the suffix `-relay` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | OTLP/gRPC endpoint of the Collector, `host:port` or a URL. Export never blocks: an unreachable Collector costs telemetry, not availability ([ADR 0016](/docs/adr/0016-fleet-observability-standard)). | `otel.endpoint` (rendered only while `otel.enabled`) |
| `SERVICE_VERSION` | `dev` | no | `service.version` of the resource. | `serviceVersion`, else `image.tag`, else the chart `appVersion` |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` of the resource (read by `telemetry.Setup`). | `environment` |

The relay has no `LOG_LEVEL`: it always logs JSON at the default (info) level.

## `cmd/yard-projector`

Source: `cmd/yard-projector/main.go`. The only writer of the `analytics` schema.

| Variable | Default | Required? | Meaning | Chart value |
| --- | --- | --- | --- | --- |
| `ANALYTICS_DATABASE_URL` | none | **yes** | DSN of the projector's role on the analytical database. Must be a **direct** connection: the projector runs the embedded analytical migrations through it at boot. | `analytics.url` or `analytics.existingSecret` |
| `KAFKA_BROKERS` | none | **yes** | Brokers of the analytics topic. Kafka is dialled lazily, so a broker outage at boot does not crash the process. | `kafka.brokers` |
| `ANALYTICS_CONSUMER_GROUP` | none | **yes** | Fixed consumer group of the `warehouse.yard-management.analytics` consumer. A brand-new group replays from the earliest retained offset (the backfill). | `analytics.consumerGroup` (chart default `yard-management-analytics`) |
| `ADMIN_ADDR` | `:8091` | no | Listen address of `/healthz` and `/readyz`. | `analytics.projector.adminAddr` |
| `LOG_LEVEL` | `info` | no | As for the API. | `config.logLevel` |
| `OTEL_SERVICE_NAME` | `yard-management-projector` | no | `service.name` of this binary's spans and metrics. | `otel.serviceName` with the suffix `-projector` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | OTLP/gRPC endpoint of the Collector, `host:port` or a URL. Export never blocks: an unreachable Collector costs telemetry, not availability ([ADR 0016](/docs/adr/0016-fleet-observability-standard)). | `otel.endpoint` (rendered only while `otel.enabled`) |
| `SERVICE_VERSION` | `dev` | no | `service.version` of the resource. | `serviceVersion`, else `image.tag`, else the chart `appVersion` |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` of the resource (read by `telemetry.Setup`). | `environment` |

## `cmd/yard-reports`

Source: `cmd/yard-reports/main.go`. Read-only: it never writes and never opens
the OLTP tables.

| Variable | Default | Required? | Meaning | Chart value |
| --- | --- | --- | --- | --- |
| `ANALYTICS_READER_DATABASE_URL` | none | **yes**, unless `ANALYTICS_DATABASE_URL` is set | DSN of the reports role (read-only grants). | `analytics.readerUrl` |
| `ANALYTICS_DATABASE_URL` | none | fallback | Used when `ANALYTICS_READER_DATABASE_URL` is empty. One of the two is required. | `analytics.url` or `analytics.existingSecret` |
| `HTTP_ADDR` | `:8092` | no | Listen address of the `/reports/*` routes plus `/healthz` and `/readyz`. | `analytics.reports.httpAddr` |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | no | As for the API. | `config.shutdownDrainDelay` |
| `LOG_LEVEL` | `info` | no | As for the API. | `config.logLevel` |
| `OTEL_SERVICE_NAME` | `yard-management-reports` | no | `service.name` of this binary's spans and metrics. | `otel.serviceName` with the suffix `-reports` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | OTLP/gRPC endpoint of the Collector, `host:port` or a URL. Export never blocks: an unreachable Collector costs telemetry, not availability ([ADR 0016](/docs/adr/0016-fleet-observability-standard)). | `otel.endpoint` (rendered only while `otel.enabled`) |
| `SERVICE_VERSION` | `dev` | no | `service.version` of the resource. | `serviceVersion`, else `image.tag`, else the chart `appVersion` |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` of the resource (read by `telemetry.Setup`). | `environment` |

## Readiness and liveness

| Binary | `/healthz` | `/readyz` is 200 only while |
| --- | --- | --- |
| `cmd/api` | liveness, always 200 | not draining **and** Postgres answers a ping **and** a Kafka broker answers a topic-less metadata request |
| `cmd/mcp` | liveness | same as the API |
| `cmd/yard-projector` (admin port) | liveness | not draining and Postgres (analytical) pings and Kafka answers metadata |
| `cmd/yard-reports` | liveness | not draining and the read of the `analytics` freshness table succeeds: **not ready until the projector has created the schema** |
| `cmd/relay` | liveness only (no `/readyz`) | n/a |

The readiness verdict is cached for 3 s and each check is bounded by 2 s
(`DefaultReadinessTTL`, `DefaultReadinessTimeout` in
`internal/adapters/inbound/http/readiness.go`).

## Shutdown order

SIGINT/SIGTERM on the API: `/readyz` flips to 503, the drain delay passes, the
HTTP server drains (10 s bound), the three consumers stop (an interrupted
message stays uncommitted and is redelivered), and the Postgres pool closes
last. The chart's `terminationGracePeriodSeconds` (40 s for the API, refused
below 20) leaves room for this sequence.
