---
id: observability
title: Observability
sidebar_label: Observability
sidebar_position: 3
description: "What yard-management exposes for operators: OpenTelemetry traces and metrics over OTLP, trace-correlated structured logs, probes, one business counter, the freshness report and outbox state. The dashboard and alert rules registered in warehouse-infra develop are described too (the api and relay run on k3s; the rest is switched off)."
---

# Observability

`yard-management` follows the fleet observability standard ([ADR 0017](/docs/adr/0017-fleet-observability-standard)): every
long-running binary (`cmd/api`, `cmd/mcp`, `cmd/relay`, `cmd/yard-projector`, `cmd/yard-reports`) calls `telemetry.Setup`, which exports
traces and metrics over **OTLP/gRPC** to the OpenTelemetry Collector, and writes JSON logs whose records carry `trace_id` and `span_id`
when they are written under a span. The service has **no Prometheus endpoint** of its own: the Collector re-publishes the metrics.
Export never blocks: an unreachable Collector costs telemetry, not availability (the exporters connect lazily, and a failed flush at shutdown is
logged, not an exit code).

What this page describes is the code on `develop`. Whether the image that runs on the k3s cluster contains it, and whether the series
reach Prometheus, was not checked against a running pod.

## Signals

| Instrument | Kind, unit | Prometheus series (after the Collector) | Emitted by | Meaning |
| --- | --- | --- | --- | --- |
| `http.server.request.duration` | histogram, `s` | `http_server_request_duration_seconds_{bucket,sum,count}` | api, mcp, yard-reports, yard-projector (admin port) | Request duration, labelled `http.route` (the chi route **pattern**, `/trailers/{trailerId}`, never the raw path), `http.method` and `http.scheme`. It has no status attribute: request rate and duration come from it, an error rate from the gateway panels and the `http request` log lines. |
| `yard.visits.checked_in` | counter, `{visit}` | `yard_visits_checked_in_total` | api | Gate check-ins by `outcome` (`admitted`, `rejected`, `refused`) and `reason`, see below. |
| Go runtime metrics | gauges and counters | `go_*` (for example `go_goroutine_count`) | every binary | Goroutines, heap, GC and scheduler latency, from `go.opentelemetry.io/contrib/instrumentation/runtime`. |

Traces: every request to the api, mcp and reports routers (and the admin port of the projector) produces one server span named after the
route pattern. The relay and the consumers start no span of their own.

### `yard.visits.checked_in`

Counts every gate check-in (`CheckInVisit`, behind `POST /visits`; the MCP server has no check-in tool), once, after its unit of work ended.

| `outcome` | `reason` | Meaning |
| --- | --- | --- |
| `admitted` | none | a `CHECKED_IN` visit was recorded |
| `rejected` | `seal-mismatch`, `carrier-suspended` | a `REJECTED` visit was recorded: the site policy turned the truck away |
| `refused` | the problem slug of the failure: `visit-already-open`, `appointment-required`, `unknown-appointment`, `trailer-already-on-site`, `no-free-spot`, `invalid-carrier`, `validation`, ... or `internal-error` | nothing was persisted |

A rising `rejected` share means the policy is turning trucks away; a rising `refused` with a client reason means the gate clients send
check-ins the rules refuse; a rising `internal-error` or `concurrent-modification` means the service itself is failing. A request replayed by
the idempotency middleware does not run the use case and is not counted; a rejection recorded by hand (`POST /visit-rejections`) is another
command and is not counted by this instrument.

### Resource attributes and environment

Every signal carries `service.name`, `service.version` and `deployment.environment.name`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `OTEL_SERVICE_NAME` | `yard-management` (api), `yard-management-mcp`, `yard-management-relay`, `yard-management-projector`, `yard-management-reports` | `service.name` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | Collector OTLP/gRPC endpoint, `host:port` or a URL |
| `SERVICE_VERSION` | `dev` | `service.version` |
| `ENVIRONMENT` | `local` | `deployment.environment.name` |

The chart renders all four on every Deployment that runs a binary (`otel.enabled`, `otel.endpoint`, `otel.serviceName`, `environment`,
`serviceVersion`); the sweeps CronJobs run `wget`, not a binary, and render none. See [Configuration](/docs/operations/configuration).

### What is not instrumented

A trace started by a REST or MCP request ends at the outbox insert: the `outbox` table has no column for the trace context, so no
`traceparent` travels in the Kafka headers (ADR 0017, section 4). The Kafka consumers and the outbox relay have no spans and no metrics of
their own (the data-level signals below cover them), and `http.server.active_requests` is not wired.

## Logs

Every binary writes JSON (`log/slog`) to stdout. `LOG_LEVEL` selects
`debug`/`info`/`warn`/`error` for the api, mcp, projector and reports; the relay
always logs at info. A record written with a span-carrying context (the `*Context`
slog variants, which the access log and the `request failed` line use) also carries
`trace_id` and `span_id`; the others (startup, shutdown, the consumers and the relay
loops) carry none. Fields are those of the individual calls:

| Message (abridged) | Level | Fields | Binary |
| --- | --- | --- | --- |
| `http request` (one per request; INFO, WARN for 4xx, ERROR for 5xx; `/healthz` and `/readyz` are not logged) | info/warn/error | `method`, `route` (the route pattern, not the raw path), `status`, `duration_ms`, `bytes`, `request_id`, `trace_id`, `span_id` | api, mcp, reports, projector (admin) |
| `telemetry configured` | info | `service_name`, `otlp_endpoint` | all |
| `telemetry flush failed on shutdown` | warn | `error` | all |
| `http server listening` / `mcp server listening (Streamable HTTP)` / `reports server listening` / `projector admin server listening` | info | `addr` | api, mcp, reports, projector |
| `shutdown: readiness flipped to not-ready; waiting for traffic to drain` | info | `drain_delay` | api, mcp, reports |
| `consumer starting` | info | `consumer`, `topic` | api |
| `event consumed` | info | `consumer`, `type`, `event_id`, `subject` | api, projector |
| `skipping a message that is not a valid CloudEvent` | warn | `consumer`, `topic`, `error` | api, projector |
| `an event that can never be applied; dead-lettering it` | warn | `consumer`, `type`, `event_id`, `error` | api, projector |
| `<consumer> message can never be applied; dead-lettering it without retry` | error | `error`, `partition`, `offset` | api, projector |
| `<consumer> handling failed; retrying the same message` (also `offset commit`, `dead-letter publish`) | error | `error`, `partition`, `offset`, `attempt`, `retry_in` | api, projector |
| `kafka reader error; retrying` | error | `topic`, `detail` | api, projector |
| `a consumer died; shutting down` | error | `error` | api |
| `analytics consumer running` | info | `topic`, `group_id`, `brokers` | projector |
| `readiness: dependency is not ready` | warn | `dependency` (`postgres`, `kafka`, `analytics`), `error` | all with `/readyz` |
| `request failed` (any unexpected 5xx) | error | `error`, `method`, `path` | api, reports |
| `mcp tool failed with an unexpected error` | error | `error` | mcp |
| `boot step failed; retrying` | warn | `step`, `attempt`, `error` | api, mcp |
| `outbox relay: send failed, stopping this pass at the failed row` | warn | `outbox_id`, `topic`, `event_type`, `key`, `attempts`, `error` | relay |
| `outbox relay: row reached the attempt cap and is excluded from future claims…` | error | `outbox_id`, `topic`, `event_type`, `key`, `attempts`, `max_attempts`, `error` | relay |
| `outbox relay: dead-lettered a row the encoder refuses…` | warn | row and cause | relay |
| `outbox relay pass failed` | error | `error` | relay |
| `relay: OUTBOX_MAX_ATTEMPTS is not a non-negative integer, using the default` | warn | `value`, `default` | relay |
| `<binary> exited with error` / `relay: stopped with an error` | error | `error` | all |

`method` and `path` in `request failed` are sanitised against log injection
(`internal/adapters/inbound/http/logsafe.go`). A 5xx never leaks its cause to
the client: the problem detail is `an unexpected error occurred`.

Sources: `internal/adapters/inbound/kafka/consumer.go` and `loop.go`,
`internal/adapters/outbound/postgres/outbox_relay.go`,
`internal/adapters/inbound/http/response.go` and `readiness.go`,
`internal/adapters/inbound/requestlog/requestlog.go`, the five `cmd/*/main.go` and `cmd/*/telemetry.go`.

## Probes

See [Runbook](/docs/operations/runbook#probes-and-what-readiness-waits-for).
`/readyz` is the single best health signal: a 503 problem of type
`dependency-unavailable` names the failing dependency.

## Data-level signals

These are what answers the questions no instrument covers (the outbox, consumer lag, the read side):

| Question | Where to look |
| --- | --- |
| Are events leaving? | `SELECT count(*), min(created_at) FROM outbox;` should stay near zero; the relay deletes a row once it is sent |
| Is a row stuck? | `outbox.attempts >= OUTBOX_MAX_ATTEMPTS` (default 20), `outbox.last_error`; the ERROR log with `outbox_id` |
| Is the analytics read side fresh? | `GET /reports/freshness` (last event time folded into the `analytics` schema) |
| Is a consumer wedged? | repeated ERROR `handling failed; retrying the same message` with a growing `attempt`; consumer-group lag on the broker |
| Poison traffic? | messages on `warehouse.yard-management.events.dlq` (header `dlq-reason`) and on `<consumed topic>.dlq` (headers `x-dlq-source-topic`, `x-dlq-cause`) |
| Are sweeps running? | `kubectl get cronjob,jobs` for `…-sweep-dwell` and `…-sweep-move-leases`; failed Jobs keep their logs |

## Edge telemetry (Kong)

The chart can render Kong plugins on the REST route (`kongPlugins.prometheus`,
`kongPlugins.opentelemetry`, `kongPlugins.cors`, all **off by default** in the
chart; `templates/kong-plugins.yaml`). `warehouse-infra`'s
`helm-values/yard-management.yaml` switches all three on. When on they emit
request metrics and traces at the gateway, not from the service. There is
deliberately no auth plugin.

## Dashboards and alerts

Registered in `warehouse-infra` `origin/develop` by PR 193 (commit `b4b827e`). The api and
relay pods run (read-only on 2026-11-10), so the API-route Kong panels and the log panels
can have data; the MCP and reports panels select Kong routes (`-mcp`, `-reports`) and
processes that do not exist while those components are off, so expect them empty. Whether
the dashboard and the rules are loaded in the observability stack was not checked.

**Dashboard.** `terraform/dashboards/contexts/yard-management.json` (title
"Warehouse — Yard Management", uid `warehouse-yard-management`, generated by
`scripts/gen-context-dashboards.py`). Four rows:

| Row | Panels |
| --- | --- |
| API Gateway (Kong), the north-south view of this context's routes | Request rate by status code; 5xx error ratio; p95 latency, Kong request against upstream. All select the Kong services of the API, MCP and reports routes (`httproute.warehouse-systems.yard-management`, `-mcp`, `-reports`). |
| Service HTTP RED (OLTP, projector, reports, MCP) | Request rate by route; 5xx rate by Kong route (API, MCP, reports); request duration p95 by process. The service panels select `service_name` matching `yard-management.*`. |
| Go runtime | Goroutines; Go memory used. |
| Logs | Live log stream, log volume by level, errors and warnings only, from Loki, queried by `app="yard-management"` (the Istio containers are excluded). |

The service panels of rows two and three query OpenTelemetry-style names
(`http_server_request_duration_seconds`, `go_goroutine_count` or `runtime_go_*`,
keyed by `service_name`). The instruments above are the ones that produce those names, with the
five `service_name` values of the table in [Resource attributes](#resource-attributes-and-environment), all matching `yard-management.*`.
Until a release carrying this code is deployed and the Collector pipeline is confirmed, treat them as unverified: this was not checked
against a running pod. The Kong and log panels do not depend on it. No panel or alert reads `yard_visits_checked_in_total` yet.

**Alert rules.** Six, in `terraform/alerting/warehouse-alert-rules.json`, all labelled
`context=yard-management`, each linking this site's [runbook](/docs/operations/runbook)
and the dashboard:

| Group | Alert | Severity | Fires when |
| --- | --- | --- | --- |
| `warehouse-yard-management` | `WarehouseKong5xxRatioHigh` | warning | A Kong route of this context answers 5xx for more than 5 % of its requests over 5 minutes, held for 5 minutes. |
| `warehouse-yard-management` | `WarehousePodRestarting` | warning | A container of a pod named `yard-management-*` in `warehouse-systems` restarted 2 or more times in 15 minutes. |
| `warehouse-yard-management-slo` | `WarehouseApiAvailabilityBurnFast` | critical | The 5xx ratio burns the one-day 99 % availability budget at 14.4 times the allowed rate or more, over both 1 hour and 5 minutes, with at least 30 requests in the hour, held for 2 minutes. |
| `warehouse-yard-management-slo` | `WarehouseApiLatencyBurnFast` | critical | The share of upstream responses slower than 400 ms (the nearest Kong bucket to the 500 ms target of the one-day 95 % latency objective) burns the budget at 14.4 times or more, over 1 hour and 5 minutes, with at least 30 requests in the hour, held for 2 minutes. |
| `warehouse-yard-management-slo` | `WarehouseApiAvailabilityBurnSlow` | warning | The same availability budget burns at 3 times or more over both 6 hours and 30 minutes, with at least 30 requests in 6 hours, held for 15 minutes. |
| `warehouse-yard-management-slo` | `WarehouseApiLatencyBurnSlow` | warning | The same latency budget burns at 3 times or more over 6 hours and 30 minutes, with at least 30 requests in 6 hours, held for 15 minutes. |

The rules read Kong (`kong_http_requests_total`, `kong_upstream_latency_ms_*`) and
kube-state-metrics (`kube_pod_container_status_restarts_total`). There is still no
rule on the log lines above, outbox backlog or consumer lag; those log lines remain
the stable substrings to alert on.
