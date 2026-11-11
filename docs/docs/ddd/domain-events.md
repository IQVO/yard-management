---
id: domain-events
title: Domain Events
sidebar_label: Domain Events
description: "Every event Yard Management publishes and consumes, with full CloudEvents types, topics, keys, payload fields, producers and consumers."
---

# Domain Events


Every event this context publishes and consumes, from
`contexts[yard-management].publishes` and `.consumes`. The envelope follows the
yard [CloudEvents standard](https://iqvo.github.io/warehouse-docs/yard/strategic-design/event-standard-cloudevents).
There are **twenty-one published events** (eleven integration, ten analytics only)
and **thirteen consumed events**. The split is as built (service ADR 0004, refined by ADR 0010 on
2026-11-09, which moved `DoorWorkCompleted` and `TrailerDeparted` to the integration topic; ADR 0013 added `TrailerSecured` and `TrailerSecuringReleased`); the AsyncAPI is
version 3.0.0 and the topic of each channel is in its `address`.

## Envelope

| Attribute | Value |
| --- | --- |
| `specversion` | `1.0` (structured JSON mode, mandatory on every Kafka message) |
| `id` | unique per event; a redelivery carries the same `id` (the inbox dedupes on it) |
| `source` | `/warehouse/yard-management` |
| `type` | `com.warehouse.wms.yard-management.<entity>.<EventName>`; entity is the lower-case aggregate that raises it: `yardvisit`, `trailer`, `yardmove`, `yardcheck` |
| `subject` | the aggregate root id: `visitId` for `yardvisit`, `trailerId` for `trailer`, `moveId` for `yardmove`, `yardCheckId` for `yardcheck` |
| `time` | the instant the fact happened |
| `datacontenttype` | `application/json`, payload fields in `snake_case` |
| `dataschema` | `urn:warehouse:yard-management:events:<EventName>:v1` on the integration topic, `urn:warehouse:yard-management:analytics:<EventName>:v1` on its analytics twin |
| Kafka topic | integration events: `warehouse.yard-management.events` and, as a twin with the same `id`, `type`, key and byte-identical `data`, `warehouse.yard-management.analytics`; analytics-only events: `warehouse.yard-management.analytics` only |
| Kafka key | the same aggregate root id as `subject`, so one visit's (or trailer's, or move's) events stay in order on one partition |
| `version` | every event on an aggregate with a `version` carries it in `data` (starts at 1, +1 per accepted change); readers apply only `version` greater than the stored one |

Every event leaves through the transactional outbox, in the same transaction as the
aggregate change, with an attempt cap; one outbox row becomes up to two Kafka messages, and a row that can
never be encoded goes verbatim to `warehouse.yard-management.events.dlq`.

Because events are keyed by visit or trailer id and not by appointment,
`dock-scheduling` can receive them in any order across partitions (its policy
`OutOfOrderYardFacts`).

## Published: integration events

Topic `warehouse.yard-management.events` (each message also goes, as a twin, to the analytics topic).

### Visit events (key `visitId`)

| Event | Full `type` | `data` fields | Producing command / use case | Consumers |
| --- | --- | --- | --- | --- |
| `VisitCheckedIn` | `com.warehouse.wms.yard-management.yardvisit.VisitCheckedIn` | `visit_id`, `site_id`, `carrier`, `carrier_scac`, `purpose`, `appointment_id`, `trailers`, `arrived_at`, `version` | `CheckInVisit` | `dock-scheduling` |
| `VisitRejected` | `com.warehouse.wms.yard-management.yardvisit.VisitRejected` | `visit_id`, `site_id`, `carrier_scac`, `appointment_id`, `reason`, `rejected_at` | `RejectAtGate` (also the `REJECT` outcome of `SealCheckAtGate`) | `dock-scheduling`, `exception-management` |

`reason` is a `RejectionReason`: `APPOINTMENT_REQUIRED`, `UNKNOWN_APPOINTMENT`,
`SEAL_MISMATCH`, `TRAILER_ALREADY_ON_SITE` or `SITE_REQUEST`.

### Trailer events (key `trailerId`)

| Event | Full `type` | `data` fields | Producing command / use case | Consumers |
| --- | --- | --- | --- | --- |
| `TrailerDocked` | `com.warehouse.wms.yard-management.trailer.TrailerDocked` | `trailer_id`, `site_id`, `door_code`, `appointment_id`, `asn_numbers`, `visit_id`, `docked_at`, `version` | `ArriveAt` a door, via `CompleteMove` (the move and the trailer change in one unit of work) | `dock-scheduling`, `outbound-shipping` |
| `TrailerUndocked` | `com.warehouse.wms.yard-management.trailer.TrailerUndocked` | `trailer_id`, `site_id`, `door_code`, `appointment_id`, `undocked_at`, `version` | the trailer leaves a door: `StartMoving` or `Depart` from `DOCKED` | `dock-scheduling` |
| `SealExceptionRaised` | `com.warehouse.wms.yard-management.trailer.SealExceptionRaised` | `trailer_id`, `site_id`, `appointment_id`, `expected_seal_number`, `found_seal_number`, `raised_at` | policy `SealCheckAtGate` | `exception-management` |
| `TrailerDwellExceeded` | `com.warehouse.wms.yard-management.trailer.TrailerDwellExceeded` | `trailer_id`, `site_id`, `kind`, `dwell_minutes`, `threshold_minutes`, `location_code` | `DwellSweep` | `exception-management` |
| `DoorWorkCompleted` | `com.warehouse.wms.yard-management.trailer.DoorWorkCompleted` | `trailer_id`, `site_id`, `door_code`, `appointment_id`, `completed_at`, `load_state`, `version` | `CompleteDoorWork` (also `AutoCompleteUnload`) | `outbound-shipping` |
| `TrailerDeparted` | `com.warehouse.wms.yard-management.trailer.TrailerDeparted` | `trailer_id`, `site_id`, `visit_id`, `departed_at`, `dwell_minutes`, `load_state`, `seal_number`, `version` | `Depart` | `outbound-shipping` |
| `TrailerSecured` | `com.warehouse.wms.yard-management.trailer.TrailerSecured` | `trailer_id`, `site_id`, `door_code`, `method`, `secured_at`, `version` | `SecureTrailer` (not raised again for the same method) | none yet |
| `TrailerSecuringReleased` | `com.warehouse.wms.yard-management.trailer.TrailerSecuringReleased` | `trailer_id`, `site_id`, `door_code`, `released_at`, `version` | `ReleaseTrailerSecuring` | none yet |

`method` in `TrailerSecured` is `RESTRAINT` (a vehicle restraint engaged) or `WHEEL_CHOCKS` (ADR 0013). `kind` in `TrailerDwellExceeded` is the `TrailerKind` whose threshold was crossed. `DoorWorkCompleted` and
`TrailerDeparted` reached this topic on 2026-11-09 (service ADR 0010) because `outbound-shipping` confirms a shipment
when the trailer physically leaves; messages raised before that stayed on the analytics topic. `SealExceptionRaised.trailer_id`
is null when the visit was rejected at the gate (the CloudEvents subject is then the visit id).

### Yard check events (key `yardCheckId`)

| Event | Full `type` | `data` fields | Producing command / use case | Consumers |
| --- | --- | --- | --- | --- |
| `YardCheckClosed` | `com.warehouse.wms.yard-management.yardcheck.YardCheckClosed` | `yard_check_id`, `site_id`, `closed_at`, `discrepancies` | `CloseYardCheck` | `exception-management` |

`discrepancies` is a list of findings (kind `MISSING`, `UNEXPECTED` or `WRONG_LOCATION`, location, trailer), not a count.

## Published: analytics-only events

Topic `warehouse.yard-management.analytics` only. "Analytics only" means no other context reads the event and it
does not go to the integration topic; no context may depend on it for a decision without a model change. The
the analytics reports read model is built from them and from the twins of the integration events.

| Event | Full `type` | `data` fields | Producing command / use case |
| --- | --- | --- | --- |
| `VisitCheckedOut` | `com.warehouse.wms.yard-management.yardvisit.VisitCheckedOut` | `visit_id`, `site_id`, `trailers_out`, `departed_at`, `on_site_minutes`, `version` | `CheckOutVisit` |
| `TrailerParked` | `com.warehouse.wms.yard-management.trailer.TrailerParked` | `trailer_id`, `site_id`, `spot_code`, `version` | `ArriveAt` a spot, via `CompleteMove`; also `ReturnToOrigin` |
| `DoorWorkStarted` | `com.warehouse.wms.yard-management.trailer.DoorWorkStarted` | `trailer_id`, `site_id`, `door_code`, `appointment_id`, `started_at`, `version` | `StartDoorWork` |
| `TrailerHeld` | `com.warehouse.wms.yard-management.trailer.TrailerHeld` | `trailer_id`, `site_id`, `reason`, `version` | `PlaceHold` (also `RecordSeal` with state `BROKEN`) |
| `TrailerHoldReleased` | `com.warehouse.wms.yard-management.trailer.TrailerHoldReleased` | `trailer_id`, `site_id`, `version` | `ReleaseHold` |
| `YardMoveRequested` | `com.warehouse.wms.yard-management.yardmove.YardMoveRequested` | `move_id`, `site_id`, `trailer_id`, `appointment_id`, `performer`, `from_location`, `to_location`, `reason`, `due_by`, `version` | `RequestMove` (also the first move at the gate, created `CLAIMED`) |
| `YardMoveClaimed` | `com.warehouse.wms.yard-management.yardmove.YardMoveClaimed` | `move_id`, `site_id`, `hostler_ref`, `claimed_at`, `version` | `ClaimNextMove` |
| `YardMoveLeaseExpired` | `com.warehouse.wms.yard-management.yardmove.YardMoveLeaseExpired` | `move_id`, `site_id`, `hostler_ref`, `version` | `ExpireLease` (the lease sweep) |
| `YardMoveCompleted` | `com.warehouse.wms.yard-management.yardmove.YardMoveCompleted` | `move_id`, `site_id`, `trailer_id`, `performer`, `hostler_ref`, `from_location`, `to_location`, `actual_location`, `reason`, `completed_at`, `duration_seconds`, `version` | `CompleteMove` |
| `YardMoveCancelled` | `com.warehouse.wms.yard-management.yardmove.YardMoveCancelled` | `move_id`, `site_id`, `reason`, `version` | `CancelMove` |

`reason` in `TrailerHeld` is a `HoldReason`: `DAMAGED`, `SEAL_BROKEN`, `NOT_FOUND`,
`QUALITY` or `SECURITY`. `YardMoveLeaseExpired` is new as built (decision Y-10): `ExpireLease`
used to raise nothing. `DeclareYardPolicy` raises no event; neither do `OpenYardCheck`, `RecordObservation`, `RecordArrival`, `RecordSeal`,
`BreakSeal`, `RecordLoadState`, `StartMoving` or `ArriveAt` on their own account.

### Example: a trailer docked

`TrailerDocked` is the most important event: it is how the physical door reaches
`dock-scheduling` (and, for ship-confirm, `outbound-shipping`). Ids and times are illustrative.

```json
{
  "specversion": "1.0",
  "id": "0d3e7a52-5a63-4f0b-8f6e-1c2d9b7a4e11",
  "source": "/warehouse/yard-management",
  "type": "com.warehouse.wms.yard-management.trailer.TrailerDocked",
  "subject": "trl-0001",
  "time": "2026-11-12T14:07:31Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:warehouse:yard-management:events:TrailerDocked:v1",
  "data": {
    "trailer_id": "trl-0001",
    "site_id": "SITE-A",
    "door_code": "D07",
    "appointment_id": "apt-0042",
    "asn_numbers": ["ASN-1001"],
    "visit_id": "vis-0007",
    "docked_at": "2026-11-12T14:07:31Z",
    "version": 3
  }
}
```

### Example: a move completed

`YardMoveCompleted` carries the `actual_location` next to the planned `to_location` and the move's
`reason`. A location other than the destination needs a reason (`ErrUnexplainedDestination`); as built the
payload has both fields (the model first lacked them, gap YM-G15). Ids and times are illustrative.

```json
{
  "specversion": "1.0",
  "id": "5b8c1f90-2e47-4d36-a1c8-77e0a9d35b02",
  "source": "/warehouse/yard-management",
  "type": "com.warehouse.wms.yard-management.yardmove.YardMoveCompleted",
  "subject": "mov-0105",
  "time": "2026-11-12T14:07:30Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:warehouse:yard-management:events:YardMoveCompleted:v1",
  "data": {
    "move_id": "mov-0105",
    "site_id": "SITE-A",
    "trailer_id": "trl-0001",
    "performer": "HOSTLER",
    "hostler_ref": "host-12",
    "from_location": { "kind": "SPOT", "code": "P14" },
    "to_location": { "kind": "DOOR", "code": "D07" },
    "actual_location": { "kind": "DOOR", "code": "D07" },
    "reason": "TO_DOOR",
    "completed_at": "2026-11-12T14:07:30Z",
    "duration_seconds": 214,
    "version": 3
  }
}
```

## Consumed

Every consumer is idempotent on the CloudEvents `id` (a `processed_events` row written in the
same transaction as the effect) and dispatches on the full `type` string, never on
a suffix. A policy refusal is logged and counted and the event is marked processed;
an infrastructure failure rolls back and the message is redelivered. The three consumers
(dock-scheduling, facility-layout, inbound-receiving) each run under a group named by an environment variable
(`DOCK_SCHEDULING_CONSUMER_GROUP`, `FACILITY_CONSUMER_GROUP`, `INBOUND_RECEIVING_CONSUMER_GROUP`); the analytics
consumer has its own (`ANALYTICS_CONSUMER_GROUP`).

### From `dock-scheduling` (topic `warehouse.dock-scheduling.events`)

| Full `type` | `data` fields (producer's contract) | What this context does with it | Idempotency beyond the inbox |
| --- | --- | --- | --- |
| `com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentBooked` | `appointment_id`, `site_id`, `direction`, `door_code`, `carrier`, `carrier_scac`, `window_start`, `window_end`, `asn_numbers`, `cpt_id`, `shipment_refs`, `expected_seal_number`, `arrival_tolerance_minutes`, `provenance`, `version` | Inserts the appointment into `AppointmentView` (door, window, expected seal, tolerance), status `BOOKED` | Apply only `version` above the stored one |
| `com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentRescheduled` | `appointment_id`, `site_id`, `door_code`, `previous_door_code`, `window_start`, `window_end`, `previous_window_start`, `previous_window_end`, `arrival_tolerance_minutes`, `version` | Updates door, window and tolerance in `AppointmentView` | As above |
| `com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentCancelled` | `appointment_id`, `site_id`, `door_code`, `reason`, `version` | Marks it `CANCELLED`; `NoShowFreesHolding` cancels an open `TO_DOOR` move for it | As above; cancel a move only if one is open |
| `com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentNoShow` | `appointment_id`, `site_id`, `door_code`, `direction`, `carrier_scac`, `window_end`, `version` | Marks it `NO_SHOW`; `NoShowFreesHolding` as above | As above |
| `com.warehouse.wms.dock-scheduling.dockcalendar.DoorClosureScheduled` | `site_id`, `closure_id`, `door_code`, `from`, `to`, `reason`, `version` | Inserts the closure into `DoorClosureView` (`ErrDoorClosed`) | Upsert on `closure_id` |
| `com.warehouse.wms.dock-scheduling.dockcalendar.DoorClosureLifted` | `site_id`, `closure_id`, `door_code`, `version` | Removes the closure from `DoorClosureView` | As above |

### From `facility-layout` (topic `warehouse.facility.events`)

| Full `type` | `data` fields used | What this context does with it | Idempotency beyond the inbox |
| --- | --- | --- | --- |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered` | `locationCode`, `zoneId`, `role`, `dockFlow` | Adds a Yard or Dock slot to `LocationView` (`locations`); a slot whose zone has not arrived is held pending and is `unknown-location` until it does | Upsert on `locationCode` |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned` | `locationCode` | Removes or deactivates the slot in `LocationView` (`ErrUnknownLocation` for new arrivals) | Repeatable |
| `com.warehouse.wms.facility-layout.zone.ZoneRegistered` | `zoneId`, `siteCode` | Maps `zoneId` to `siteCode` so a slot has a site; never by parsing a location code | Upsert on `zoneId` |

### From `inbound-receiving` (topic `warehouse.inbound-receiving.events`)

| Full `type` | `data` fields used | What this context does with it | Idempotency beyond the inbox |
| --- | --- | --- | --- |
| `com.warehouse.wms.inbound-receiving.asn.ASNRegistered` | `asn_number`, `lines` | Adds the ASN to `AsnContentView` (what an inbound trailer holds) | Upsert on `asn_number` |
| `com.warehouse.wms.inbound-receiving.asn.ASNCancelled` | `asn_number` | Marks it cancelled in `AsnContentView` | Status overwrite |
| `com.warehouse.wms.inbound-receiving.receipt.ReceiptOpened` | `receipt_id`, `asn_number`, `appointment_id`, `door_code` | Records the receipt at the door in `ReceiptAtDoorView` | Upsert on `receipt_id` |
| `com.warehouse.wms.inbound-receiving.receipt.ReceiptClosed` | `receipt_id`, `asn_number`, `closed_at` | `AutoCompleteUnload`: `CompleteDoorWork` and `load_state` `EMPTY` if the trailer is `DOCKED` | Complete door work once |

Source: `internal/domain/trailer/events.go`, `internal/adapters/outbound/kafka/encoder.go`, `internal/adapters/inbound/kafka/consumer.go`, `internal/application/views/views.go`, `apis/asyncapi.yaml`.

The `type` strings of all three producers are read from their AsyncAPI (the `dock-scheduling` copy is under
`yard/apis/dock-scheduling/`, the others under `apis/`). The "idempotency beyond the inbox" column is as built where
the service's ADR 0009 states it and otherwise this page's reading: versioned dock-scheduling facts are
guarded by `version`, a replay of an older version never overwrites a newer row.

## Notes

The gaps this page used to list are closed by the service's decisions (see the
[Model Gap Register](https://iqvo.github.io/warehouse-docs/yard/strategic-design/model-gap-register)):

- `VisitRejected`, `SealExceptionRaised`, `TrailerDwellExceeded` and `YardCheckClosed` carry no `version`, as modelled
  (decision Y-10 documents the exemptions); the `trailers` array of `VisitCheckedIn` and `discrepancies` are spelled
  out in the AsyncAPI.
- `ExpireLease` now raises `YardMoveLeaseExpired` (analytics only, decision Y-10, `YM-G14`).
- `StartMoving` and `ArriveAt` run inside the move's use cases and raise `TrailerParked`, `TrailerDocked` and
  `TrailerUndocked`; a live visit that leaves from the door raises `TrailerUndocked` then `TrailerDeparted` (decision
  Y-6, `YM-G8`).
- `VisitRejected` is consumed by `dock-scheduling`, which cancels the appointment with `TURNED_AWAY` (decision X2,
  `YM-G17`), and by `exception-management`.
- The AsyncAPI description of `TrailerDocked` says `inbound-receiving` consumes it; on `develop` it does not (the optional
  confirm of the door on an open receipt was not built), while `outbound-shipping` does. The upstream spec text is to be
  corrected in the producer's repository.
- The consumed `lines` of `ASNRegistered` is used by no rule on these pages (gap `YM-G26`, accepted).
