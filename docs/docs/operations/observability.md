---
id: observability
title: Observability
sidebar_label: Observability
sidebar_position: 3
description: "What yard-management exposes for operators today: structured logs, probes, the freshness report and outbox state; it has no OTel instruments."
---

# Observability

**Honest summary:** `yard-management` has **no OpenTelemetry instrument, no
span and no Prometheus endpoint** of its own. The only `go.opentelemetry.io`
entries in `go.mod` are indirect dependencies of other libraries (`// indirect`
for `otelhttp`, `otel`, `otel/metric`, `otel/trace`), and no package under
`cmd/` or `internal/` imports them; the binaries read no `OTEL_*` variable.
What an operator has is structured logs, the probes, a handful of data-level
signals, and, at the edge, whatever the Kong plugins of the chart emit.

## Logs

Every binary writes JSON (`log/slog`) to stdout. `LOG_LEVEL` selects
`debug`/`info`/`warn`/`error` for the api, mcp, projector and reports; the relay
always logs at info. There is no request-access log and no trace/correlation id
in the log lines. Fields are those of the individual calls:

| Message (abridged) | Level | Fields | Binary |
| --- | --- | --- | --- |
| `http server listening` / `mcp server listening (Streamable HTTP)` / `reports server listening` / `projector admin server listening` | info | `addr` | api, mcp, reports, projector |
| `shutdown: readiness flipped to not-ready; waiting for traffic to drain` | info | `drain_delay` | api, mcp, reports |
| `consumer starting` | info | `consumer`, `topic` | api |
| `event consumed` | info | `consumer`, `type`, `event_id`, `subject` | api, projector |
| `skipping a message that is not a valid CloudEvent` | warn | `consumer`, `topic`, `error` | api, projector |
| `an event that can never be applied; dead-lettering it` | warn | `consumer`, `type`, `event_id`, `error` | api, projector |
| `<consumer> message can never be applied; dead-lettering it without retry` | error | `error`, `partition`, `offset` | api, projector |
| `<consumer> handling failed; retrying the same message` (also `offset commit`, `dead-letter publish`) | error | `error`, `partition`, `offset`, `attempt`, `retry_in` | api, projector |
| `kafka reader error; retrying` | warn | `topic`, `error` | api, projector |
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
`internal/adapters/inbound/http/response.go` and `readiness.go`, the five
`cmd/*/main.go`.

## Probes

See [Runbook](/docs/operations/runbook#probes-and-what-readiness-waits-for).
`/readyz` is the single best health signal: a 503 problem of type
`dependency-unavailable` names the failing dependency.

## Data-level signals

With no metrics, these are what answers "is it healthy":

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
`kongPlugins.opentelemetry`, `kongPlugins.cors`, all **off by default**;
`templates/kong-plugins.yaml`). When switched on they emit request metrics and
traces at the gateway, not from the service. There is deliberately no auth
plugin.

## Dashboards and alerts

None exist for this service. `warehouse-infra` `origin/develop` has no
reference to `yard-management` (checked with `git grep`), so no scrape job,
dashboard or alert rule is provisioned. The log lines above are the stable
substrings to alert on once the service is deployed.
