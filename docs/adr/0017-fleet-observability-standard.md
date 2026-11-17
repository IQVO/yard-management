# ADR 0017: Adopt the fleet observability standard: OpenTelemetry on every binary, trace-correlated logs and one business counter

## Status

Accepted (2026-11-13). Adopts, for this context, the fleet's standard metrics convention (inventory-storage ADR 0016, wes-work-planning
ADR 0013) and its OpenTelemetry layout. Additive: no route, request, response, event, topic or table changes, and `/healthz` and `/readyz`
answer what they answered before. Code: `internal/adapters/outbound/telemetry`, `internal/adapters/inbound/requestlog`,
`internal/adapters/inbound/http/server.go`, `internal/application/usecases/checkin_metrics.go`, `cmd/*/telemetry.go`,
`charts/yard-management` (the `otel`, `environment` and `serviceVersion` values).

Numbering: `docs/adr` has no 0016, but the code and `apis/asyncapi.yaml` cite "ADR 0016" for the carrier-suspension rule (for example
`internal/application/usecases/carriers.go`). This ADR therefore takes 0017 and leaves 0016 to that decision.

## Context

Until now the service exported nothing about itself. `go.mod` carried the OpenTelemetry modules only as indirect dependencies, no package
under `cmd/` or `internal/` imported them, no binary read an `OTEL_*` variable, the HTTP adapter had no access log, and a log line carried
no trace or span id. The Grafana panels that select `http_server_request_duration_seconds` and the Go runtime series by `service_name`
(see [Observability](/docs/operations/observability)) had nothing producing those series. The only business-level signals were SQL
queries on the outbox and the freshness report.

The executive deck's top ask for the repo was: a Prometheus registry (or the fleet OTel standard) as the prerequisite for any dashboard
or alert that can see the application itself. The fleet already settled the question: every service pushes OTLP/gRPC to the Collector, and
the Collector re-publishes metrics to Prometheus. A per-service Prometheus registry would be a second pipeline, so it is not used.

## Decision

### 1. The adapter is copied from node-topology, one source for everything

`internal/adapters/outbound/telemetry/{telemetry.go,slogotel.go}` and `internal/adapters/inbound/requestlog/requestlog.go` are copied
from node-topology `origin/develop` with their tests, together with its `cmd/*/telemetry.go` layout (one file per binary). node-topology
was chosen over inventory-storage as the single copy source because it already has the multi-binary layout and the `requestlog` package,
and the `telemetry.Setup` of the two repos differs only in one comment. No file comes from a second repo.

`telemetry.Setup(ctx, serviceName, serviceVersion, endpoint)` builds a TracerProvider and a MeterProvider that export over OTLP/gRPC,
installs the W3C trace-context and baggage propagators, starts the Go runtime metrics (`go.opentelemetry.io/contrib/instrumentation/runtime`),
and returns one flush func. Export never blocks: the exporters connect lazily, so an unreachable Collector costs telemetry, never
availability, and a failed flush at shutdown is logged, not turned into a non-zero exit. An empty endpoint falls back to
`localhost:4317`; a bare `host:port` becomes a plaintext URL and an explicit URL (`https://...`) is left alone.

`telemetry.WithTraceContext` wraps the JSON slog handler so a record written with a span-carrying context (the `*Context` slog variants)
carries `trace_id` and `span_id`.

### 2. Tier 1 on every long-running binary

| Binary | `service.name` default | Setup | HTTP middleware | Trace-correlated logs |
| --- | --- | --- | --- | --- |
| `cmd/api` | `yard-management` | yes | REST router | yes |
| `cmd/mcp` | `yard-management-mcp` | yes | MCP router | yes |
| `cmd/relay` | `yard-management-relay` | yes | none (health port only) | yes |
| `cmd/yard-projector` | `yard-management-projector` | yes | admin router (`/healthz`, `/readyz`) | yes |
| `cmd/yard-reports` | `yard-management-reports` | yes | reports router | yes |

Every binary reads `OTEL_SERVICE_NAME`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `SERVICE_VERSION` (default `dev`) and, inside `telemetry.Setup`,
`ENVIRONMENT` (default `local`). Telemetry comes up after the configuration is accepted and before any router is built, and is flushed last.
The sweeps CronJobs run busybox `wget`, not a binary of this repo, so they are not instrumented.

The four chi routers (`NewRouter`, `NewReportsRouter`, `NewAdminRouter` and the one of `cmd/mcp`) use the fleet middleware order:
`RequestID`, `otelchi.Middleware` (server span named after the route pattern), `otelchimetric.NewServerRequestDuration`
(`http.server.request.duration`, labelled `http.route` with the route pattern, plus `http.method` and `http.scheme`; there is no status
attribute, so an error rate comes from the gateway panels and the access log, not from this histogram), `RequestLogger`, `Recoverer`. The NUL guard (`rejectNUL`) now sits after them so a refused request still
shows up in the request-duration histogram and the log.

Three behaviour notes, none of them a contract change:

- `Recoverer` is new: a panic in a handler now answers 500 instead of aborting the connection.
- `cmd/mcp` moved from `http.ServeMux` to a chi router so the same middleware applies. `GET /healthz` and `GET /readyz` answer the same
  bodies, and every other method and path still reaches the MCP handler (the route pattern is `/*`), as it did under the mux.
- The access log is new: one `http request` line per request with `method`, the matched route pattern (never the raw path, which holds ids),
  `status`, `duration_ms`, `bytes` and `request_id`; INFO below 400, WARN for 4xx, ERROR for 5xx, and `/healthz` and `/readyz` are not logged.
  It lives in its own package so `cmd/mcp` can use it without importing the REST handlers.

`/healthz` is unchanged.

### 3. Tier 2: one counter, on the gate check-in

The convention names the instrument `<context>.<noun>.<verb>`, attributes with dotted keys, `outcome` and `reason` reserved, and a UCUM
`{noun}` unit; the instrument lives in an outbound adapter behind a port and is never called from `internal/domain`.

| | |
| --- | --- |
| Instrument | `yard.visits.checked_in`, Int64 counter, unit `{visit}`; Prometheus series `yard_visits_checked_in_total` |
| `outcome=admitted` | a `CHECKED_IN` visit was recorded; no `reason` |
| `outcome=rejected` | a `REJECTED` visit was recorded (the gate turned the truck away); `reason` is `seal-mismatch` or `carrier-suspended` |
| `outcome=refused` | nothing was persisted; `reason` is the problem slug of the failure (`visit-already-open`, `appointment-required`, `unknown-appointment`, `trailer-already-on-site`, `no-free-spot`, `invalid-carrier`, `validation`, ...), `internal-error` when no slug names it |

Why `CheckInVisit`: the context exists to know what is on the yard; the visit is the aggregate every trailer, move and event starts from,
and the check-in is the one command where the site's policy (appointment required, seal mismatch, suspended carrier) decides between three
different business results. The rate of `rejected` against `admitted` says whether the policy is turning trucks away; `refused` with a client
reason says gate clients send check-ins the rules refuse; `internal-error` and `concurrent-modification` say the service itself is failing.

Shape: the port `ports.CheckInMetrics` (`CheckInAdmitted`, `CheckInRejected`, `CheckInRefused`) lives in `internal/application/ports`; the
implementation is `telemetry.CheckInMetrics`; `usecases.Deps.Metrics` is optional and a nil value records nothing. The use case calls it once
per attempt, after its unit of work ended, so a rolled-back check-in is never counted as admitted. `cmd/api` wires it; the MCP adapter has no check-in tool (outside tests, the only caller of `CheckInVisit` is the REST handler of
`POST /visits`), so `cmd/mcp` does not. A request replayed by the idempotency middleware does not run the use case and is not counted.
`RejectAtGate` (a rejection the gate staff records by hand) is a different command and is not counted by this instrument.

The use case keeps its own table from errors to problem slugs because it may not import the REST adapter. The `http` package asserts that
the table and the problem catalogue agree (`TestCheckInMetricReasonsAreTheProblemSlugsOfTheCatalogue`).

### 4. Trace propagation across Kafka is not done

The convention stamps `traceparent` and `tracestate` into the outbox row where it is appended, because the CloudEvents encoder has no
context. The `outbox` table (`migrations/0001_yard-management.up.sql`) has no column for headers: its columns are `id`, `event_id`,
`aggreg_type`, `aggreg_id`, `payload`, `occurred_at`, `attempts`, `next_attempt_at`, `last_error` and `created_at`. Carrying the trace
context would need a migration, which this change does not add, so a trace started by a REST or MCP request ends at the outbox insert and the
consumers of the emitted events start no span from it. The CloudEvents envelope and every encoder golden are untouched.

### 5. Chart and operations

`values.yaml` gains `otel.{enabled,endpoint,serviceName}`, `environment` and `serviceVersion` (default empty: it falls back to `image.tag`, then the
chart `appVersion`). Every Deployment that runs a binary renders `ENVIRONMENT`, `SERVICE_VERSION` and, while `otel.enabled`,
`OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_SERVICE_NAME` (`<otel.serviceName>` for the api, `<otel.serviceName>-mcp`, `-relay`, `-projector`,
`-reports` for the others). Those four names belong to the chart: every `extraEnv` list refuses them. `charts/yard-management/tests/render_test.sh`
asserts the rendering, scans the names each `cmd/*` reads, and compares them with the env table in
[Configuration](/docs/operations/configuration).

### 6. Dependencies and two deviations from the copy source

- The OTLP/gRPC dependencies raise `google.golang.org/grpc` (v1.83.2), `google.golang.org/protobuf`, the genproto modules, `grpc-gateway` and the
  OpenTelemetry modules (v1.47.0). `golang.org/x/net` was already v0.62.0 in `go.mod` and stays there. `govulncheck ./...` on the final tree
  reports no vulnerability that the code calls.
- The fleet `normalizeEndpoint` builds the plaintext prefix with the literal `"http://"`. No fitness sensor in this repo rejects a URL literal
  (`internal/architecture` and `scripts/harness/repo_lint.py` were searched), so the code is kept identical to the fleet copy.

## Consequences

- A request to the API produces `http.server.request.duration` (series `http_server_request_duration_seconds`), a server span named after the
  route pattern and a log line carrying that span's `trace_id` and `span_id`. The router tests prove the three through an in-memory
  `ManualReader` and `SpanRecorder`.
- The Go runtime metrics of every binary reach the Collector, so the Go-runtime panels have a producer. Whether a Collector, Prometheus and
  the dashboards are reachable from a running pod was not checked here.
- Existing dashboard panels that select `service_name` matching `yard-management.*` now match five names.
- A new business instrument must follow the Tier 2 convention and is reviewed like an API contract: a metric name is a public contract with
  every dashboard and alert built on it.

## Follow-ups (documented, not implemented)

- Propagate the trace context through Kafka: needs a nullable `headers` column on `outbox` (a migration), stamped at append and read by the encoder.
- Spans and metrics for the Kafka consumers and the outbox relay (backlog, attempts, dead letters): only the runtime metrics and the logs cover them today.
- Dashboard and alert panels for `yard_visits_checked_in_total` in warehouse-infra.
- `otelchimetric.NewServerActiveRequests` (`http.server.active_requests`) is not wired.

## Alternatives considered and rejected

- A Prometheus registry and a `/metrics` endpoint per binary: a second pipeline next to the OTLP one every other service uses.
- Instrumenting only `cmd/api`: the MCP server runs the same write use cases, and the relay, projector and reports are where the data-level
  signals of [Observability](/docs/operations/observability) live.
- One counter per outcome (`yard.visits.admitted`, `yard.visits.rejected`): the convention prefers one counter with an `outcome` attribute.

## Addendum 2026-11-13: current state

ADR 0016 now exists: `docs/adr/0016-carrier-registry-copy-and-suspended-carrier-gate.md`, written retroactively from the code. The "Numbering" paragraph under
Status, which says `docs/adr` has no 0016, described the tree when this ADR was written.
