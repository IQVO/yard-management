# ADR 0004: CloudEvents envelope and type catalogue

## Status

Accepted (2026-11-05).

## Context

Every Kafka message in the fleet is a CloudEvents 1.0 event in structured mode (fleet rule
`.claude/rules/fleet/cloudevents.md`). This ADR is the catalogue of every type yard-management publishes or consumes;
`TestEventCatalogueMatchesContract` checks it against `apis/asyncapi.yaml`.

## Decision

### Envelope

- `specversion` `1.0`; `id` a UUID persisted with the outbox row (stable across relay retries; consumers dedupe on it);
  `source` `/warehouse/yard-management`; `subject` the aggregate root id; `time` the occurred-at instant in UTC;
  `datacontenttype` `application/json`; `dataschema` `urn:warehouse:yard-management:<events|analytics>:<EventName>:v1`.
- Kafka value = the JSON event format; header `content-type: application/cloudevents+json; charset=UTF-8`; Kafka key = the
  subject, so all events of one aggregate stay ordered on one partition.
- `data` is snake_case; nullable fields are `null` (never omitted); timestamps are RFC 3339 UTC; `version` is the aggregate
  version after the change (the advisory events VisitRejected, SealExceptionRaised, TrailerDwellExceeded and
  YardCheckClosed carry none).
- Built and decoded only through `internal/adapters/kafka/cloudevents`.

### Streams

Seven types are integration events on `warehouse.yard-management.events` and are mirrored on
`warehouse.yard-management.analytics` (same `id`, same `type`, byte-identical `data`, analytics `dataschema`). The other
twelve are analytics-only: they go ONLY to `warehouse.yard-management.analytics`. A row the encoder cannot encode is written
verbatim to `warehouse.yard-management.events.dlq`. Both topics have 8 partitions.

### Published

`<entity>` is the raising aggregate, lowercase: `yardvisit`, `trailer`, `yardmove` or `yardcheck`.

| type | stream | raised when | key |
|---|---|---|---|
| `com.warehouse.wms.yard-management.yardvisit.VisitCheckedIn` | events + analytics | a visit passes the gate (consumed by dock-scheduling) | `visit_id` |
| `com.warehouse.wms.yard-management.yardvisit.VisitRejected` | events + analytics | a visit is turned away (dock-scheduling, exception-management) | `visit_id` |
| `com.warehouse.wms.yard-management.yardvisit.VisitCheckedOut` | analytics | the visit leaves | `visit_id` |
| `com.warehouse.wms.yard-management.trailer.TrailerDocked` | events + analytics | a trailer arrives at a door (dock-scheduling, inbound-receiving) | `trailer_id` |
| `com.warehouse.wms.yard-management.trailer.TrailerUndocked` | events + analytics | a trailer leaves a door (dock-scheduling) | `trailer_id` |
| `com.warehouse.wms.yard-management.trailer.TrailerParked` | analytics | a trailer comes to rest on a spot | `trailer_id` |
| `com.warehouse.wms.yard-management.trailer.DoorWorkStarted` | analytics | unloading or loading begins | `trailer_id` |
| `com.warehouse.wms.yard-management.trailer.DoorWorkCompleted` | analytics | door work is done | `trailer_id` |
| `com.warehouse.wms.yard-management.trailer.TrailerDeparted` | analytics | a trailer leaves the site | `trailer_id` |
| `com.warehouse.wms.yard-management.trailer.SealExceptionRaised` | events + analytics | gate seal differs from the expected one (exception-management) | `trailer_id` (visit id when no trailer) |
| `com.warehouse.wms.yard-management.trailer.TrailerDwellExceeded` | events + analytics | dwell crossed the kind's threshold (exception-management) | `trailer_id` |
| `com.warehouse.wms.yard-management.trailer.TrailerHeld` | analytics | a hold is placed | `trailer_id` |
| `com.warehouse.wms.yard-management.trailer.TrailerHoldReleased` | analytics | a hold is released | `trailer_id` |
| `com.warehouse.wms.yard-management.yardmove.YardMoveRequested` | analytics | a move is requested | `move_id` |
| `com.warehouse.wms.yard-management.yardmove.YardMoveClaimed` | analytics | a hostler claims a move | `move_id` |
| `com.warehouse.wms.yard-management.yardmove.YardMoveLeaseExpired` | analytics | a lease ran out | `move_id` |
| `com.warehouse.wms.yard-management.yardmove.YardMoveCompleted` | analytics | a move is completed | `move_id` |
| `com.warehouse.wms.yard-management.yardmove.YardMoveCancelled` | analytics | a move is cancelled | `move_id` |
| `com.warehouse.wms.yard-management.yardcheck.YardCheckClosed` | events + analytics | an audit closes (exception-management) | `yard_check_id` |

Payload fields are in `apis/asyncapi.yaml` (one message per type with schema and example) and equal `contracts-delta.md` /
`model.yaml` `publishes`, plus the final gap decisions: `appointment_id` on YardMoveRequested, `to_location` and `reason` on
YardMoveCompleted, `trailer_id` nullable on SealExceptionRaised, and YardMoveLeaseExpired. A breaking payload change is a
new `.v2` type and dataschema, never a mutation of v1.

### Consumed (ADR 0003)

| type | topic | consumer group env |
|---|---|---|
| `com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentBooked` | `warehouse.dock-scheduling.events` | `DOCK_SCHEDULING_CONSUMER_GROUP` |
| `com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentRescheduled` | `warehouse.dock-scheduling.events` | `DOCK_SCHEDULING_CONSUMER_GROUP` |
| `com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentCancelled` | `warehouse.dock-scheduling.events` | `DOCK_SCHEDULING_CONSUMER_GROUP` |
| `com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentNoShow` | `warehouse.dock-scheduling.events` | `DOCK_SCHEDULING_CONSUMER_GROUP` |
| `com.warehouse.wms.dock-scheduling.dockcalendar.DoorClosureScheduled` | `warehouse.dock-scheduling.events` | `DOCK_SCHEDULING_CONSUMER_GROUP` |
| `com.warehouse.wms.dock-scheduling.dockcalendar.DoorClosureLifted` | `warehouse.dock-scheduling.events` | `DOCK_SCHEDULING_CONSUMER_GROUP` |
| `com.warehouse.wms.facility-layout.zone.ZoneRegistered` | `warehouse.facility.events` | `FACILITY_CONSUMER_GROUP` |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered` | `warehouse.facility.events` | `FACILITY_CONSUMER_GROUP` |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned` | `warehouse.facility.events` | `FACILITY_CONSUMER_GROUP` |
| `com.warehouse.wms.inbound-receiving.asn.ASNRegistered` | `warehouse.inbound-receiving.events` | `INBOUND_RECEIVING_CONSUMER_GROUP` |
| `com.warehouse.wms.inbound-receiving.asn.ASNCancelled` | `warehouse.inbound-receiving.events` | `INBOUND_RECEIVING_CONSUMER_GROUP` |
| `com.warehouse.wms.inbound-receiving.receipt.ReceiptOpened` | `warehouse.inbound-receiving.events` | `INBOUND_RECEIVING_CONSUMER_GROUP` |
| `com.warehouse.wms.inbound-receiving.receipt.ReceiptClosed` | `warehouse.inbound-receiving.events` | `INBOUND_RECEIVING_CONSUMER_GROUP` |

The projector reads this service's own analytics topic with `ANALYTICS_CONSUMER_GROUP`.

## Consequences

- Consumers need nothing but this catalogue and `apis/asyncapi.yaml`; there is no shared Go code.
- The analytics stream is the only source of the KPI reports; the integration topic stays small.
