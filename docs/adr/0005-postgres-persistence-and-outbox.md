# ADR 0005: Postgres persistence and the transactional outbox

## Status

Accepted (2026-11-06). Implements the persistence side of ADR 0002 (aggregates, invariants), ADR 0003 (local copies and the inbox)
and ADR 0004 (CloudEvents catalogue). Code: `migrations/`, `internal/adapters/outbound/postgres`,
`internal/adapters/outbound/kafka`, `internal/application/outbox`, `cmd/relay`.

## Context

The domain decides every invariant (ADR 0002) and is pure. What it cannot decide is the race between two writers: two hostlers
claiming the same move, two arrivals to one spot, two check-ins of one tractor. The database is the backstop that refuses the
loser, and the outbox is how a fact leaves the service without a dual write. Postgres and Kafka are the only runtime: there is no
in-memory mode in any `cmd/*` (the `memory` adapter exists for the unit-test repocontract suite only).

## Decision

### Schema

golang-migrate, embedded in the binary (`migrations/*.sql`, `migrations.FS`), additive numbered pairs; every `down` recreates the
previous schema. DDL runs on `MIGRATIONS_DATABASE_URL` (a direct session connection) and falls back to `DATABASE_URL`
(`postgres.MigrationsURL`), because a pooler in transaction mode cannot hold golang-migrate's advisory lock.

| Table | Holds |
|---|---|
| `yard_visits` | the YardVisit aggregate (state CHECKED_IN, CHECKED_OUT, REJECTED); `version` guards every UPDATE |
| `trailers` | the Trailer aggregate: state, location, door work, seal, hold, last alerted dwell threshold |
| `yard_moves` | the YardMove aggregate: performer, lease, priority band, destination |
| `yard_checks` | the YardCheck aggregate and its observations |
| `yard_policies` | the per-site YardPolicy (defaults are applied by the domain when no row exists) |
| `zones`, `locations` | event-fed copies of facility-layout (doors = role Dock, yard spots = role Yard); `site_code` is null until the zone arrives (X5) |
| `appointments`, `door_closures` | event-fed copies of dock-scheduling |
| `asns`, `receipts` | event-fed copies of inbound-receiving |
| `processed_events` | the inbox: columns `event_id` (primary key) and `processed_at` |
| `outbox` | the transactional outbox |
| `idempotency_keys` | stored answers of requests made with an Idempotency-Key (2xx and 4xx only; migration 0002) |

### Backstops: the domain decides, the database refuses the loser

Each backstop is a partial unique index and surfaces as the domain error the use case would have raised
(`postgres.uniqueViolation` maps the constraint name, SQLSTATE `23505`):

| Index | Guarantees | Error |
|---|---|---|
| `uq_yard_visits_one_open_per_plate` (site, tractor plate) where CHECKED_IN | one open visit per tractor | `ErrVisitAlreadyOpen` |
| `uq_trailers_one_per_location` (location kind, code) where PARKED or DOCKED | one trailer per spot or door | `ErrLocationOccupied` |
| `uq_trailers_unit_on_site` (site, SCAC, unit) where not DEPARTED | one live trailer per unit | `ErrTrailerAlreadyOnSite` |
| `uq_yard_moves_one_open_per_trailer` | one open move per trailer | `ErrTrailerHasOpenMove` |
| `uq_yard_moves_open_destination` | a destination is reserved by one open move | `ErrDestinationReserved` |
| `uq_yard_moves_one_claim_per_hostler` | a hostler holds one claim at a time | `ErrHostlerBusy` |
| `uq_yard_checks_one_open_per_site` | one open YardCheck per site | `ErrYardCheckOpen` |

- **Per-location advisory locks.** `Locks.Lock(ctx, codes...)` takes `pg_advisory_xact_lock(hashtextextended('yard-management:location:' || code, 0))`
  for the sorted, de-duplicated codes (two writers needing the same set never deadlock). Transaction-scoped, so they release at
  commit or rollback and are safe behind a pooler; outside a unit of work the call is `ErrNoTransaction`. The use cases take them
  where an occupancy decision is made (`targeting.go`, `moves.go`: a move's destination at request time, the actual location at
  completion, the origin at start).
- **Optimistic version.** `Save` inserts when the aggregate's version is 1 and otherwise updates `WHERE version = version - 1`; a
  stale version, a missing row or a second insert of one id is `ports.ErrConcurrentModification` (409 `concurrent-update`).
- **ClaimNext.** `YardMove` claiming selects the best REQUESTED hostler move of the site (lowest band, earliest `due_by`, then
  request time; index `idx_yard_moves_claimable`) with `FOR UPDATE SKIP LOCKED`, so concurrent claims never take the same move.
  Lease expiry is found through `idx_yard_moves_expiry` and returned by the `POST /move-lease-sweeps` CronJob.

### Unit of work

`UnitOfWork.Do` begins one pgx transaction, carries it in the context (`pgtx.With`) and commits on nil; a nested `Do` joins. Every
repository reads the transaction back with `pgtx.From` and falls back to the pool outside one. The aggregate rows, the outbox rows,
the consumers' `processed_events` claim and the idempotency answer commit or roll back together. A cancelled context still rolls
back (`context.WithoutCancel`).

### Inbox and idempotency

- `ProcessedEvents.Claim` is `INSERT ... ON CONFLICT (event_id) DO NOTHING` inside the consumer's unit of work: a rollback
  un-claims the id, so the redelivery is processed, and a duplicate is skipped (ADR 0009).
- `idempotency_keys(key pk, fingerprint, status, content_type, body, created_at)`: the first writer of a key wins; the
  fingerprint (method, path, body hash) turns a reuse of a key with another request into `409 idempotency-key-reused`.

### Outbox and relay

- `postgres.Outbox` (the `ports.EventPublisher`) only INSERTs, in the same transaction as the aggregate. `outbox(id bigserial,
  event_id unique, aggreg_type, aggreg_id, payload json, occurred_at, attempts, next_attempt_at, last_error, created_at)`. The
  CloudEvents id is minted once at enqueue and persisted, so a relay retry republishes the same id and consumers dedupe on it. The
  payload is a self-describing `{type, data}` envelope in a `JSON` (not `JSONB`) column on purpose: the exact text is preserved, so
  every retry publishes byte-identical data. The relay never re-derives domain state.
- **One row, up to two messages.** `kafka.Encoder` maps ONE outbox row to its wire form by the catalogue of ADR 0004: the nine
  integration types go (seven at acceptance, nine since ADR 0010) to `warehouse.yard-management.events` and, as a twin with the same id, type, subject and data, to
  `warehouse.yard-management.analytics`; the ten analytics-only types go only to the analytics topic. Key and subject are the
  aggregate id (visit, trailer, move, yard check). `kafka.Topics()` (events, analytics, `<events>.dlq`, 8 partitions) is what the
  deployment pre-creates.
- **Claiming.** `OutboxRelay` claims up to 100 due rows in id order under `FOR UPDATE SKIP LOCKED` (overlapping relays claim
  disjoint sets), sends each through the sink inside the pass's transaction and deletes it only after the broker accepted it:
  delivery is at-least-once. A send failure stops the pass: `attempts + 1`, `last_error`, and `next_attempt_at` pushed out with a
  capped exponential backoff (base `OUTBOX_POLL_INTERVAL`, ceiling 5 minutes); the rows before it stay deleted.
- **Attempt cap.** `OUTBOX_MAX_ATTEMPTS` (default 20, `0` = unlimited; a typo falls back to the default with a WARN) excludes a row
  from claims once it has failed that many times. The failure that reaches the cap is logged once at ERROR (id, type, key,
  attempts) and the row stays in the outbox for inspection; an operator re-arms it with
  `UPDATE outbox SET attempts = 0, next_attempt_at = now() WHERE id = ...`. The architecture test `TestOutboxRelaysPassAttemptCap`
  requires every `cmd/*` that builds a relay to pass the cap.
- **Ordering trade-off (deliberate, fleet-wide).** While a row backs off, later rows, including later rows of the same aggregate,
  are claimed by the next passes, and once the cap excludes a poison row the rows behind it publish. Per-key order is guaranteed
  only while every row publishes; consumers must tolerate an out-of-order version (ADR 0003, ADR 0009).
- **Dead letters.** A row the encoder cannot encode (an event outside the catalogue, a payload that is not the envelope) can never
  publish: it is written verbatim to `warehouse.yard-management.events.dlq` (key = aggregate id, headers `content-type` and `dlq-reason`) and
  deleted. A failed DLQ write is an ordinary send failure (retried and counted). A cancelled context never dead-letters.
- **Shutdown.** `cmd/relay` stops the loop first (the pass in flight finishes or rolls back, so each row is either published and
  removed or still in the outbox), then closes the Kafka writer, then the pool. It serves `/healthz` on `RELAY_HEALTH_ADDR`
  (default `:8081`) for the liveness probe.

### Process split

The relay is its own binary (`cmd/relay`, one replica or more, safe to overlap). `cmd/api` and `cmd/mcp` only enqueue; their
`/readyz` includes the Kafka broker check so a deployment without Kafka refuses to take writes whose events would never leave.

## Consequences

- A write is visible in Postgres before its event is on Kafka: consumers see it after the next relay pass (default poll 1 s).
- A stuck broker grows the `outbox` table; the capped row stays visible with `last_error` instead of blocking the relay.
- A new published event type must be added to the encoder catalogue and ADR 0004, or its rows are dead-lettered.
- The OLTP database is one Postgres; the analytical schema of ADR 0006 is separate from it and shares no table.

## Alternatives considered and rejected

- **Publish to Kafka inside the use case**: a dual write; a crash between commit and publish loses the event.
- **Two outbox rows per event** (one per topic): the encoder's one-row-two-messages route keeps a single persisted id and a single
  row to inspect or re-arm.
- **Block the relay on a poison row**: preserves per-key order at the price of a total outage; the cap and the DLQ are the fleet
  choice.
