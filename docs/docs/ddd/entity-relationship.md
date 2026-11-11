---
id: entity-relationship
title: Entity Relationship
sidebar_label: Entity Relationship
description: "The Postgres schema of Yard Management as built, with one table per aggregate root, read-model tables, the occupancy indexes, the outbox, inbox and idempotency tables and the analytics schema."
---

# Entity Relationship


The Postgres schema **as built**, from the service's migrations `migrations/0001_yard-management` and
`0002_idempotency_keys` (OLTP) and `analytics/migrations/0001_yard_analytics` and `0002_kpi_standard` (the `analytics`
schema). This context owns its database; no table is shared with another context
(`conventions.integration_rules`). Postgres and Kafka are the only runtime. Columns are drawn with the types
of the migration, simplified (`COLLATE "C"` and defaults are left out). The earlier version of this page proposed a
different shape: child tables for the visit's trailers, the check's observations and the policy's lists, and a
`schema_migrations` table; as built those lists are array and `JSONB` columns of the root row, and the
outbox is a smaller table.

## Aggregate tables

### YardVisit

```mermaid
erDiagram
    direction LR
    yard_visits {
        TEXT visit_id PK
        TEXT site_id
        TEXT state "CHECKED_IN, CHECKED_OUT, REJECTED"
        TEXT carrier
        TEXT carrier_scac "empty string when none"
        TEXT tractor_plate
        TEXT driver_ref "opaque, no PII"
        TEXT purpose "empty or a Purpose"
        TEXT appointment_id "empty string when none"
        TEXT rejection_reason "empty string when none"
        TEXT_ARRAY trailers_in "trailer ids, values"
        TEXT_ARRAY trailers_out "trailer ids, values"
        TIMESTAMPTZ arrived_at
        TIMESTAMPTZ departed_at "nullable"
        BIGINT version "optimistic lock, 1 or more"
        TIMESTAMPTZ updated_at
    }
```

Source: `migrations/0001_yard-management.up.sql` (`yard_visits`).

### Trailer

```mermaid
erDiagram
    direction LR
    trailers {
        TEXT trailer_id PK
        TEXT site_id
        TEXT scac "natural key part"
        TEXT unit_number "natural key part"
        TEXT kind "DRY_VAN, REEFER, FLATBED, CONTAINER"
        TEXT state "PARKED, IN_MOVE, DOCKED, DEPARTED"
        TEXT load_state "EMPTY, LOADED, PARTIAL"
        TEXT_ARRAY asn_numbers "Contents, inbound"
        TEXT outbound_ref "empty string when none"
        TEXT purpose "DROP, LIVE_UNLOAD, LIVE_LOAD, PICKUP, BOBTAIL"
        TEXT seal_number "empty string when none"
        TEXT seal_state "NONE, INTACT, BROKEN, APPLIED, VERIFIED, MISMATCH"
        TEXT seal_class "I, S, H or empty (ISO 17712)"
        TEXT unit_number_scheme "ISO6346 or empty for a free-form number"
        TEXT hold "empty or a HoldReason"
        TEXT location_kind "SPOT or DOOR, empty while IN_MOVE"
        TEXT location_code "empty while IN_MOVE"
        TEXT door_work "WAITING, IN_PROGRESS, DONE, empty off a door"
        TEXT origin_kind "where StartMoving found it"
        TEXT origin_code "where StartMoving found it"
        TEXT origin_work "door work at the origin"
        TEXT visit_id "value, no FK"
        TEXT appointment_id "empty string when none"
        TIMESTAMPTZ arrived_at "dwell clock start"
        TIMESTAMPTZ departed_at "nullable"
        TIMESTAMPTZ dwell_alerted_at "nullable, the sweep marker"
        BIGINT version "optimistic lock"
        TIMESTAMPTZ updated_at
    }
```

Source: `migrations/0001_yard-management.up.sql` (`trailers`).

### YardMove

```mermaid
erDiagram
    direction LR
    yard_moves {
        TEXT move_id PK
        TEXT site_id
        TEXT trailer_id "value, no FK"
        TEXT appointment_id "empty string when none"
        TEXT performer "HOSTLER or VISITING_DRIVER"
        TEXT from_kind "empty at the gate"
        TEXT from_code "empty at the gate"
        TEXT to_kind "SPOT or DOOR"
        TEXT to_code
        TEXT actual_kind "set at completion"
        TEXT actual_code "set at completion"
        TEXT reason "TO_DOOR, FROM_DOOR, TO_SPOT, RELOCATE, TO_HOLDING"
        INTEGER priority_band "1 to 9"
        TIMESTAMPTZ due_by "nullable"
        TEXT state "REQUESTED, CLAIMED, COMPLETED, CANCELLED"
        TEXT hostler_ref "empty string when none"
        TIMESTAMPTZ lease_expires_at "nullable"
        TIMESTAMPTZ requested_at
        TIMESTAMPTZ claimed_at "nullable"
        TIMESTAMPTZ completed_at "nullable"
        BIGINT version "optimistic lock"
        TIMESTAMPTZ updated_at
    }
```

Source: `migrations/0001_yard-management.up.sql` (`yard_moves`).

### YardCheck

```mermaid
erDiagram
    direction LR
    yard_checks {
        TEXT yard_check_id PK
        TEXT site_id
        TEXT state "OPEN, CLOSED"
        JSONB observations "the observed locations"
        JSONB discrepancies "written at close"
        TIMESTAMPTZ opened_at
        TIMESTAMPTZ closed_at "nullable"
        BIGINT version
        TIMESTAMPTZ updated_at
    }
```

Source: `migrations/0001_yard-management.up.sql` (`yard_checks`).

### YardPolicy

```mermaid
erDiagram
    direction LR
    yard_policies {
        TEXT site_id PK
        BOOLEAN require_appointment
        TEXT_ARRAY holding_spot_codes
        TEXT_ARRAY parking_zone_ids
        JSONB dwell_threshold_minutes "by trailer kind"
        INTEGER lease_seconds "1 or more"
        TEXT seal_mismatch_action "FLAG, REJECT"
        INTEGER free_time_minutes "0 means none"
        TEXT minimum_seal_class "I, S, H or empty for no minimum"
        BOOLEAN declared "false for the defaults"
        BIGINT version "2 or more"
        TIMESTAMPTZ updated_at
    }
```

Source: `internal/domain/yardvisit/visit.go`, `internal/domain/trailer/trailer.go`, `internal/domain/trailer/work.go`, `internal/domain/yardmove/yardmove.go`, `internal/domain/yardcheck/yardcheck.go`, `internal/domain/yardpolicy/yardpolicy.go`, `internal/domain/trailer/events.go`, `internal/adapters/outbound/kafka/encoder.go`, `migrations/0001_yard-management.up.sql`, `migrations/0002_idempotency_keys.up.sql`.

Omitted: the rest of the indexes (`idx_*` lookups by site, arrival, appointment and expiry). The invariants a
database can hold are held by these:

| Invariant | Error | Index or lock as built |
| --- | --- | --- |
| A spot or door holds at most one trailer | `ErrLocationOccupied` | Per-location advisory lock inside `Occupancy.CheckFree` plus the partial unique index `uq_trailers_one_per_location` on `trailers (location_kind, location_code)` where the state is `PARKED` or `DOCKED` |
| The natural key is unique among trailers on site | `ErrTrailerAlreadyOnSite` | `uq_trailers_unit_on_site` on `trailers (site_id, scac, unit_number)` where state is not `DEPARTED` |
| A trailer has at most one open move | `ErrTrailerHasOpenMove` | `uq_yard_moves_one_open_per_trailer` on `yard_moves (trailer_id)` where state is `REQUESTED` or `CLAIMED` |
| No two open moves share a destination | `ErrDestinationReserved` | `uq_yard_moves_open_destination` on `yard_moves (to_kind, to_code)` where state is `REQUESTED` or `CLAIMED` |
| A hostler holds at most one `CLAIMED` move | `ErrHostlerBusy` | `uq_yard_moves_one_claim_per_hostler` on `yard_moves (site_id, hostler_ref)` where state is `CLAIMED` |
| At most one `CHECKED_IN` visit per plate per site | `ErrVisitAlreadyOpen` | `uq_yard_visits_one_open_per_plate` on `yard_visits (site_id, tractor_plate)` where state is `CHECKED_IN` |
| At most one `OPEN` yard check per site | `ErrYardCheckOpen` | `uq_yard_checks_one_open_per_site` on `yard_checks (site_id)` where state is `OPEN` |
| A location is observed once per check | `ErrLocationAlreadyObserved` | The use case, over the `observations` column (no table) |
| A parked or docked trailer has a place | none | `CHECK` on `trailers`: state `PARKED` or `DOCKED` if and only if `location_code` is not empty |
| `ClaimNextMove` finds the next move | none | `idx_yard_moves_claimable` on `yard_moves (site_id, priority_band, due_by NULLS LAST, requested_at, move_id)` where state is `REQUESTED` and performer is `HOSTLER` |
| Lease sweep | none | `idx_yard_moves_expiry` on `yard_moves (lease_expires_at)` where state is `CLAIMED` and performer is `HOSTLER` |

The location columns are empty while the trailer is `IN_MOVE`, so a moving trailer holds no place; the origin is
kept in `origin_*` for `ReturnToOrigin` (decision Y-1). `appointment_id` and `dwell_alerted_at` are columns of
`trailers` (decisions X3 and Y-13). The migration gives `yard_policies.version` a floor of 2 and the table a
`declared` flag: a site that never declared a policy is answered by the defaults, as version 1.

## Read-model tables

```mermaid
erDiagram
    direction LR
    zones {
        TEXT zone_id PK
        TEXT site_code
    }
    locations {
        TEXT location_code PK
        TEXT zone_id
        TEXT site_code "nullable"
        TEXT kind "SPOT or DOOR"
        TEXT dock_flow "empty, or the flow"
        BOOLEAN active "false after decommission"
    }
    appointments {
        TEXT appointment_id PK
        TEXT site_id
        TEXT direction
        TEXT door_code
        TEXT carrier_scac
        TIMESTAMPTZ window_start "nullable"
        TIMESTAMPTZ window_end "nullable"
        TEXT_ARRAY asn_numbers
        TEXT_ARRAY shipment_refs
        TEXT expected_seal_number
        INTEGER arrival_tolerance_minutes
        TEXT status "BOOKED, CHECKED_IN, USED, CANCELLED, NO_SHOW"
        BIGINT version "apply only if greater"
    }
    door_closures {
        TEXT closure_id PK
        TEXT site_id
        TEXT door_code
        TIMESTAMPTZ closed_from "nullable"
        TIMESTAMPTZ closed_to "nullable"
        BOOLEAN lifted
    }
    asns {
        TEXT asn_number PK
        BOOLEAN cancelled
    }
    receipts {
        TEXT receipt_id PK
        TEXT asn_number
        TEXT appointment_id "from ReceiptOpened"
        TEXT door_code "from ReceiptOpened"
        TIMESTAMPTZ closed_at "nullable, from ReceiptClosed"
    }
```

Source: `internal/adapters/inbound/kafka/consumer.go`, `internal/application/views/views.go`.

Omitted: the event fields not used by a decision here, and the lookup indexes. The site of a door or spot is
derived by joining `locations.zone_id` to `zones.zone_id` (`site_code` is
`facility-layout`'s `siteCode`, the `site_id` on every yard event; the migration also keeps a `site_code` on the
location); it is never parsed from a location code. The appointment copy marks itself `CHECKED_IN` from the yard's
own `VisitCheckedIn` and `USED` from its own `TrailerUndocked` (decision X4). The `asns` copy keeps only the number
and a cancelled flag; the ASN lines are not stored. `YardBoard` has no table: it is a query
over `trailers` and `yard_moves`. The KPI reports are built from the analytics topic into the `analytics` schema
below, not from these tables.

## Infrastructure tables

```mermaid
erDiagram
    direction LR
    outbox {
        BIGSERIAL id PK
        TEXT event_id UK "CloudEvents id"
        TEXT aggreg_type
        TEXT aggreg_id "aggregate root id"
        JSON payload "the CloudEvent"
        TIMESTAMPTZ occurred_at
        INTEGER attempts "0 or more"
        TIMESTAMPTZ next_attempt_at
        TEXT last_error
        TIMESTAMPTZ created_at
    }
    processed_events {
        TEXT event_id PK "CloudEvents id, inbox"
        TIMESTAMPTZ processed_at
    }
    idempotency_keys {
        TEXT key PK
        TEXT fingerprint "of the request"
        INTEGER status "200 to 499"
        TEXT content_type
        BYTEA body "the first response"
        TIMESTAMPTZ created_at
    }
```

Source: `migrations/0001_yard-management.up.sql`, `migrations/0002_idempotency_keys.up.sql`, `internal/adapters/kafka/cloudevents/cloudevents.go`.

**All three are infrastructure, not domain.** `outbox` is written in the same transaction as the aggregate and
drained by the `relay` binary to Kafka; `attempts`, `next_attempt_at` and `last_error` track the retries. The table
has no topic column; which topic an event goes to is decided by the relay, not by the schema (19 types on the
analytics topic, 9 of them also on the events topic). `processed_events` is the inbox: one row per consumed
CloudEvents `id`, written in the same transaction as the effect. `idempotency_keys` stores the first response to a
REST `Idempotency-Key`, with a request fingerprint. The migration tool's own bookkeeping table is not part of the SQL.

## Analytics schema

The KPI reports (service ADR 0011) read a separate `analytics` schema, written only by the `yard-projector` binary from
the analytics topic and read by `yard-reports` (that split is from the service's binary layout, not checked in the
code). The migrations grant read access to a `yard_analytics_reader` role when it exists.

```mermaid
erDiagram
    direction LR
    analytics_processed_events {
        TEXT event_id PK
        TEXT event_type
        TIMESTAMPTZ occurred_at
        TIMESTAMPTZ applied_at
    }
    trailer_facts {
        TEXT trailer_id PK
        TEXT site_id
        TEXT kind
        TEXT unit_number
        TEXT visit_id
        TIMESTAMPTZ arrived_at
        TIMESTAMPTZ first_door_work_started_at
        TIMESTAMPTZ departed_at
        BIGINT dwell_minutes
    }
    visit_facts {
        TEXT visit_id PK
        TEXT site_id
        TEXT purpose
        TIMESTAMPTZ arrived_at
        TIMESTAMPTZ departed_at
        BIGINT on_site_minutes
    }
    door_dockings {
        TEXT trailer_id PK
        TIMESTAMPTZ docked_at PK
        TEXT site_id
        TEXT door_code
        TIMESTAMPTZ undocked_at
    }
    door_work {
        TEXT trailer_id PK
        TIMESTAMPTZ started_at PK
        TEXT site_id
        TEXT door_code
        TIMESTAMPTZ completed_at
    }
    hostler_moves {
        TEXT move_id PK
        TEXT site_id
        TEXT trailer_id
        TEXT hostler_ref
        TIMESTAMPTZ completed_at
        BIGINT duration_seconds
    }
    gate_exceptions {
        TEXT kind PK "VISIT_REJECTED, SEAL_EXCEPTION"
        TEXT subject_id PK
        TIMESTAMPTZ occurred_at PK
        TEXT site_id
        TEXT reason
    }
    trailer_holds {
        TEXT trailer_id PK
        BIGINT version PK
        TEXT site_id
        TEXT reason
        TIMESTAMPTZ held_at
    }
    yard_check_results {
        TEXT yard_check_id PK
        TEXT site_id
        TIMESTAMPTZ closed_at
        INTEGER discrepancies
    }
```

Source: `analytics/migrations/0001_yard_analytics.up.sql`, `analytics/migrations/0002_kpi_standard.up.sql`.

Omitted: the indexes. No table here has a foreign key. Each is a projection of the analytics topic.

## Why no foreign key crosses an aggregate or a context

- **Across aggregates.** `yard_visits.trailers_in`, `yard_moves.trailer_id`, `trailers.visit_id`,
  `trailers.appointment_id` and `yard_visits.appointment_id` are values, not foreign keys, and the migration has no
  `REFERENCES` clause at all. `YardVisit`, `Trailer` and `YardMove` are separate consistency boundaries; the
  occupancy rule that spans them is held by a domain service, a lock and a partial
  unique index, not by a foreign key. A foreign key would couple lifecycles (a departed
  trailer could not be archived without its visit and moves).
- **To other contexts.** `site_id`, `appointment_id`, `door_code`, `asn_number`,
  `receipt_id` and the location codes name things owned by `facility-layout`,
  `dock-scheduling` and `inbound-receiving`. Those live in other databases; this context
  only holds event-fed copies (the read-model tables above).

## Table ≠ aggregate

| Table | Aggregate / role | Notes |
| --- | --- | --- |
| `yard_visits` | **YardVisit** (root) | One row per visit, including a rejected one; the trailers in and out are array columns. |
| `trailers` | **Trailer** (root) | One row per trailer; the occupancy and natural-key indexes live here. |
| `yard_moves` | **YardMove** (root) | One row per move; the open-move, destination and hostler indexes live here. |
| `yard_checks` | **YardCheck** (root) | One row per check; observations and discrepancies are `JSONB` columns. |
| `yard_policies` | **YardPolicy** (root) | One row per site that declared a policy; the lists are array and `JSONB` columns replaced as a set by `DeclareYardPolicy`. |
| `locations`, `zones` | read model `LocationView` | From `facility-layout`. |
| `appointments` | read model `AppointmentView` | From `dock-scheduling`. |
| `door_closures` | read model `DoorClosureView` | From `dock-scheduling`. |
| `asns` | read model `AsnContentView` | From `inbound-receiving`. |
| `receipts` | read model `ReceiptAtDoorView` | From `inbound-receiving`. |
| `outbox` | none (infrastructure) | Transactional outbox. |
| `processed_events` | none (infrastructure) | Inbox. |
| `idempotency_keys` | none (infrastructure) | REST replay store. |
| `analytics.*` (9 tables) | read model the analytics reports | Projected from the analytics topic. |
