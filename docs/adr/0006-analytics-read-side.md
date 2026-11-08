# ADR 0006: Analytics read side: an analytics stream, a projector and yard reports over a separate analytical schema

## Status

Accepted (2026-11-06). Additive: the OLTP API, the integration topic `warehouse.yard-management.events` and its payloads are
unchanged. Fills the "analytics topic" of ADR 0004. Code: `internal/analytics/report`, `internal/adapters/inbound/kafka/analytics_consumer.go`,
`internal/adapters/outbound/analyticsstore`, `analytics/`, `cmd/yard-projector`, `cmd/yard-reports`, `internal/adapters/inbound/http/reports.go`.

## Context

Every OLTP bounded context of the fleet ships an analytics read side: a separate analytical store fed by a **projector** that
consumes the context's own analytics topic, and a read-only **reports** process over it (inbound-receiving ADR 0006,
dock-scheduling's analytics ADR). This ADR mirrors them and records where yard-management differs.

The questions the yard answers are throughput and idle time: how long a trailer stays on the yard (and how long the driver waits for
the first door work), how busy each dock door is, and how productive each hostler is.

## Decision

### 1. What is published and what is projected

ADR 0004 already routes EVERY published type to `warehouse.yard-management.analytics`: the seven integration types mirrored (same
id, type, subject and byte-identical data, only the `dataschema` stream segment differs) plus the twelve analytics-only types. The
twin is produced by the same encoder from the same outbox row (ADR 0005), so no OLTP change was needed.

The projector folds seven of them (the others are acknowledged and ignored, so a new published type is invisible to the reports until
the kind table in `analytics_consumer.go` is extended): `VisitCheckedIn`, `TrailerDocked`, `TrailerUndocked`, `DoorWorkStarted`,
`DoorWorkCompleted`, `TrailerDeparted`, `YardMoveCompleted`. Dispatch is on the FULL type string; the payloads are the snake_case
`data` of `apis/asyncapi.yaml`, decoded tolerantly.

### 2. The analytical model

Facts, never aggregates, so any window can be recomputed. Everything is in the Postgres schema `analytics`
(`analytics/migrations/0001_yard_analytics`), including golang-migrate's own table, so the OLTP migrations never see it:

- `analytics.processed_events(event_id pk, event_type, occurred_at, applied_at)`: the dedupe set and the freshness source
  (`max(occurred_at)`).
- `analytics.trailer_facts`: one row per trailer; kind, unit, visit and `arrived_at` from `VisitCheckedIn` (the only event carrying
  the kind), `first_door_work_started_at`, `departed_at` and `dwell_minutes` from `TrailerDeparted`. A trailer whose
  `VisitCheckedIn` was not seen has a null kind and is left out of the dwell report (replay from the earliest offset is the backfill).
- `analytics.door_dockings(trailer_id, docked_at, site_id, door_code, undocked_at)`: opened by `TrailerDocked`, closed by the next
  `TrailerUndocked` of the same trailer.
- `analytics.door_work(trailer_id, started_at, ..., completed_at)`: opened by `DoorWorkStarted`, closed by `DoorWorkCompleted`.
- `analytics.hostler_moves(move_id pk, site_id, trailer_id, hostler_ref, completed_at, duration_seconds)`: one row per
  `YardMoveCompleted` whose performer is HOSTLER (a visiting driver's move is not yard-truck productivity).

Rows are upserted by their natural key and the arrival and first-door-work instants keep the earliest (`LEAST`), so arrival order
across keys never matters. The migration grants SELECT to the role
`yard_analytics_reader` when it exists.

### 3. The projector (`cmd/yard-projector`)

- Consumes the analytics topic under a fixed consumer group read from `ANALYTICS_CONSUMER_GROUP` (never defaulted; boot error naming
  it). At-least-once; a brand-new group starts at the earliest offset, so the model is rebuilt by replaying retained history.
- **Dedupe and effect in one transaction**: `Projection.Apply` inserts the CloudEvents id into `analytics.processed_events` (`ON
  CONFLICT DO NOTHING`) and only if it was inserted folds the event into the fact tables, in ONE analytical-database transaction.
  The offset is committed after `Apply` returned.
- **Policy**: not a CloudEvents 1.0 message: WARN, committed past; a valid event of another type: ignored; a known type with an
  unusable payload or one the store deterministically rejects (SQLSTATE class 22 or 23, `report.ErrRejected`): dead-lettered at once
  to `warehouse.yard-management.analytics.dlq`, then committed past (a failed DLQ write retries the message); a transient failure
  (database down, timeout): the same message is retried with capped exponential backoff and never dead-lettered (the same rule as
  ADR 0009).
- Boot: the embedded analytical migrations run on `ANALYTICS_DATABASE_URL` (a DIRECT DSN of the projector's own role; it creates the
  schema when missing) and a failure is a boot error naming the variable. Kafka is dialled lazily. It never opens the OLTP database.
- `/healthz` and `/readyz` on `ADMIN_ADDR` (default `:8091`); shutdown flips readiness, drains, lets the in-flight message finish,
  then closes the pool. A consumer loop that died makes the process exit rather than keep answering probes.

### 4. The reports (`cmd/yard-reports`, `:8092`) and the API routes

Read-only HTTP through a read-only pool (`default_transaction_read_only=on`, 15 s statement timeout); RFC 7807 errors; no auth.
`GET /reports/yard-dwell`, `/reports/door-utilisation`, `/reports/hostler-productivity` take `from` and `to` (required RFC 3339,
`to` after `from`, at most 92 days) and an optional `siteId`; `GET /reports/freshness` answers `{lastEventAt, asOf}`.

| report | rows |
|---|---|
| yard-dwell | per trailer kind among trailers that DEPARTED in the window: `trailers`, `avgDwellMinutes`, `maxDwellMinutes`, `avgDriverWaitMinutes` (arrival to first door work; null when none had door work) |
| door-utilisation | per dock door with a docking in the window: `dockings`, `occupiedMinutes` (clipped to the window, an open docking counts up to now), `utilisation` (0..1) |
| hostler-productivity | per hostler: `moves`, `avgMoveSeconds`, `movesPerHour` (hours in which the hostler completed a move) |

SQL only sums and averages; the ratios live in `internal/analytics/report` (pure, imports nothing from the module). `cmd/yard-reports`
is not ready until the projector created the schema (its probe reads the freshness table). `cmd/api` serves the same three routes
when `ANALYTICS_READER_DATABASE_URL` is set (a read-only pool on the reports role); unset, it answers the contract shape with no
rows. `cmd/mcp` leaves its report tool answering no rows (ADR 0007).

### 5. Why a separate analytical schema and role

The projection is a derived copy that can be dropped and rebuilt from the topic. Keeping it in its own schema under its own writer
role and a read-only reader role means a heavy report cannot take OLTP connections or locks, the two migration histories evolve
independently, the reports role cannot read a single OLTP table, and the projector holds no credentials on OLTP data. The schema may
live in the same Postgres database as the OLTP tables or in another (the DSNs decide). No OLTP package imports `analyticsstore`
(a rule stated in its package doc).

### 6. Packaging

The one image builds every `cmd/*`, so `/app/yard-projector` and `/app/yard-reports` ship in it. The chart gets a `projector`
Deployment (health on `ADMIN_ADDR`) and a `reports` Deployment and Service, gated by their DSNs.

## Consequences

- A trailer that arrived before the stream existed is only partly visible; replaying from the earliest retained offset is the backfill.
- The reports are eventually consistent; `GET /reports/freshness` is how a caller sees the lag.
- Two more processes and one more role to provision; the reader role may be omitted by pointing the reports at the projector DSN
  (`ANALYTICS_READER_DATABASE_URL` falls back to `ANALYTICS_DATABASE_URL`).

## Alternatives considered and rejected

- **Project from the integration topic**: only seven of nineteen types are there, and an analytics-only field could not be added
  without touching the integration contract.
- **Compute the reports on the OLTP tables**: a caller-chosen 92-day window over `trailers` and `yard_moves` would compete with the
  floor's writes.
- **Two ids per occurrence**: breaks the "same id on both topics" rule that lets a consumer correlate the streams.
