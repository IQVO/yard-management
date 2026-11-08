---
paths:
  - "internal/adapters/**/kafka/**"
  - "internal/adapters/outbound/events/**"
  - "apis/asyncapi*"
---

<!-- TEMPLATE (warehouse-harness-template v2): fill in every "FILL IN" for
     THIS repo, or delete this file if the repo publishes/consumes no Kafka
     events. The "CloudEvents 1.0 is MANDATORY" section is NOT a
     placeholder: keep it verbatim, only substitute the per-repo values. -->
# Cross-service integration events (Kafka)

This service BOTH publishes and consumes. It publishes its own domain
events to `warehouse.yard-management.events` and mirrors every one of
them to `warehouse.yard-management.analytics` (consumed only by this
service's own projector, ADR 0006). It consumes
`warehouse.dock-scheduling.events`, `warehouse.facility.events` and
`warehouse.inbound-receiving.events` (ADR 0003) into local copies.

## Events: CloudEvents 1.0 is MANDATORY

Every Kafka message this service produces or consumes (integration
`warehouse.<ctx>.events` AND analytics `warehouse.<ctx>.analytics`) is a
CloudEvents 1.0 event in structured content mode. This is a hard fleet rule,
not a preference — there is nothing to "choose" here:

- No flat envelope (`event_id`/`event_type`/`occurred_at`), no dual-write,
  no dual-read, no envelope toggle env var (`EVENT_ENVELOPE_MODE` is gone
  fleet-wide). `internal/architecture/fitness_test.go`'s
  `TestNoEventEnvelopeToggleOrFlatEnvelope` fails CI on any of them.
- Build/validate/(un)marshal with `github.com/cloudevents/sdk-go/v2/event`
  (latest v2) via ONE helper package,
  `internal/adapters/kafka/cloudevents/` — copy it from this template's
  `templates/cloudevents/cloudevents.go.tmpl` (+ its test) and change only
  the per-repo constants. Transport stays `segmentio/kafka-go` (no sdk-go
  protocol/client packages, no hand-rolled CloudEvent structs).
- Every produced message carries the Kafka header
  `content-type: application/cloudevents+json; charset=UTF-8`
  (`cloudevents.ContentTypeHeader()`), next to the W3C trace headers
  (`traceparent`/`tracestate` stay in headers, never duplicated into
  extension attributes). Message key = aggregate id, `kafkago.Hash{}`
  balancer.
- Required attributes: `specversion=1.0`; `id` (UUID v4 minted ONCE per
  domain event and persisted with the outbox row, so redelivery carries
  the same id); `source=/warehouse/<repo>`; `type`; `subject` (aggregate
  instance id, never empty); `time` (domain occurred-at, UTC);
  `datacontenttype=application/json`;
  `dataschema=urn:warehouse:<repo>:<events|analytics>:<EventName>:v<N>`.
  No custom extension attributes without an ADR.
- `type` = `com.warehouse.<subdomain>.<bounded-context>.<entity>.<EventName>`
  (this repo's exact prefix: `com.warehouse.wms.yard-management`, e.g.
  `com.warehouse.wms.yard-management.trailer.TrailerDocked`). The SAME
  `type` names the occurrence on both the integration and the analytics
  topic; `dataschema` names the payload shape. Breaking payload change =>
  new `.v2` type + new dataschema version, never mutate an existing one.
- Consumers decode with `cloudevents.Decode` (validates), dispatch on the
  FULL `type` string (never a short name or suffix match), ignore unknown
  types, read `time`/`subject` from attributes and the payload via
  `DataAs`, dedupe on `id`, and DLQ/skip — WARN log + commit past, never
  crash, never block the partition, never fall back to parsing a legacy
  shape — anything that fails CloudEvents validation.
- Tests: a golden exact-JSON test per published `type` (all attributes +
  the `content-type` header); a legacy-flat-message-rejected test per
  consumer; Kafka integration tests via testcontainers only.

This repo's own record of the standard is
`docs/adr/0004-cloudevents-envelope-and-type-catalogue.md` (envelope,
dataschema URNs and the full type catalogue); the consumed contracts and
their local copies live in `docs/adr/0003-consumed-contracts-and-local-copies.md`.

### Published types

One row per published event, exact strings (ADR 0004 / `apis/asyncapi.yaml`).

| `type` | topic(s) | `subject` | `dataschema` |
| --- | --- | --- | --- |
| `com.warehouse.wms.yard-management.yardvisit.VisitCheckedIn` | `warehouse.yard-management.events` + `.analytics` | visit id | `urn:warehouse:yard-management:events:VisitCheckedIn:v1` |
| `com.warehouse.wms.yard-management.yardvisit.VisitRejected` | `warehouse.yard-management.events` + `.analytics` | visit id | `urn:warehouse:yard-management:events:VisitRejected:v1` |
| `com.warehouse.wms.yard-management.yardvisit.VisitCheckedOut` | `warehouse.yard-management.events` + `.analytics` | visit id | `urn:warehouse:yard-management:events:VisitCheckedOut:v1` |
| `com.warehouse.wms.yard-management.trailer.TrailerParked` | `warehouse.yard-management.events` + `.analytics` | trailer id | `urn:warehouse:yard-management:events:TrailerParked:v1` |
| `com.warehouse.wms.yard-management.trailer.TrailerDocked` | `warehouse.yard-management.events` + `.analytics` | trailer id | `urn:warehouse:yard-management:events:TrailerDocked:v1` |
| `com.warehouse.wms.yard-management.trailer.TrailerUndocked` | `warehouse.yard-management.events` + `.analytics` | trailer id | `urn:warehouse:yard-management:events:TrailerUndocked:v1` |
| `com.warehouse.wms.yard-management.trailer.DoorWorkStarted` | `warehouse.yard-management.events` + `.analytics` | trailer id | `urn:warehouse:yard-management:events:DoorWorkStarted:v1` |
| `com.warehouse.wms.yard-management.trailer.DoorWorkCompleted` | `warehouse.yard-management.events` + `.analytics` | trailer id | `urn:warehouse:yard-management:events:DoorWorkCompleted:v1` |
| `com.warehouse.wms.yard-management.trailer.TrailerDeparted` | `warehouse.yard-management.events` + `.analytics` | trailer id | `urn:warehouse:yard-management:events:TrailerDeparted:v1` |
| `com.warehouse.wms.yard-management.trailer.SealExceptionRaised` | `warehouse.yard-management.events` + `.analytics` | trailer id | `urn:warehouse:yard-management:events:SealExceptionRaised:v1` |
| `com.warehouse.wms.yard-management.trailer.TrailerDwellExceeded` | `warehouse.yard-management.events` + `.analytics` | trailer id | `urn:warehouse:yard-management:events:TrailerDwellExceeded:v1` |
| `com.warehouse.wms.yard-management.trailer.TrailerHeld` | `warehouse.yard-management.events` + `.analytics` | trailer id | `urn:warehouse:yard-management:events:TrailerHeld:v1` |
| `com.warehouse.wms.yard-management.trailer.TrailerHoldReleased` | `warehouse.yard-management.events` + `.analytics` | trailer id | `urn:warehouse:yard-management:events:TrailerHoldReleased:v1` |
| `com.warehouse.wms.yard-management.yardmove.YardMoveRequested` | `warehouse.yard-management.events` + `.analytics` | move id | `urn:warehouse:yard-management:events:YardMoveRequested:v1` |
| `com.warehouse.wms.yard-management.yardmove.YardMoveClaimed` | `warehouse.yard-management.events` + `.analytics` | move id | `urn:warehouse:yard-management:events:YardMoveClaimed:v1` |
| `com.warehouse.wms.yard-management.yardmove.YardMoveCompleted` | `warehouse.yard-management.events` + `.analytics` | move id | `urn:warehouse:yard-management:events:YardMoveCompleted:v1` |
| `com.warehouse.wms.yard-management.yardmove.YardMoveCancelled` | `warehouse.yard-management.events` + `.analytics` | move id | `urn:warehouse:yard-management:events:YardMoveCancelled:v1` |
| `com.warehouse.wms.yard-management.yardmove.YardMoveLeaseExpired` | `warehouse.yard-management.events` + `.analytics` | move id | `urn:warehouse:yard-management:events:YardMoveLeaseExpired:v1` |
| `com.warehouse.wms.yard-management.yardcheck.YardCheckClosed` | `warehouse.yard-management.events` + `.analytics` | yard check id | `urn:warehouse:yard-management:events:YardCheckClosed:v1` |

### Consumed types

One row per consumed event — the EXACT `type` string from the producer's
catalogue (byte-identical; see ADR 0003 and `docs/adr/0009-kafka-consumers.md`).

| `type` | topic | producer |
| --- | --- | --- |
| `com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentBooked` | `warehouse.dock-scheduling.events` | dock-scheduling |
| `com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentRescheduled` | `warehouse.dock-scheduling.events` | dock-scheduling |
| `com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentCancelled` | `warehouse.dock-scheduling.events` | dock-scheduling |
| `com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentNoShow` | `warehouse.dock-scheduling.events` | dock-scheduling |
| `com.warehouse.wms.dock-scheduling.dockcalendar.DoorClosureScheduled` | `warehouse.dock-scheduling.events` | dock-scheduling |
| `com.warehouse.wms.dock-scheduling.dockcalendar.DoorClosureLifted` | `warehouse.dock-scheduling.events` | dock-scheduling |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered` | `warehouse.facility.events` | facility-layout |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned` | `warehouse.facility.events` | facility-layout |
| `com.warehouse.wms.facility-layout.zone.ZoneRegistered` | `warehouse.facility.events` | facility-layout |
| `com.warehouse.wms.inbound-receiving.asn.ASNRegistered` | `warehouse.inbound-receiving.events` | inbound-receiving |
| `com.warehouse.wms.inbound-receiving.asn.ASNCancelled` | `warehouse.inbound-receiving.events` | inbound-receiving |
| `com.warehouse.wms.inbound-receiving.receipt.ReceiptOpened` | `warehouse.inbound-receiving.events` | inbound-receiving |
| `com.warehouse.wms.inbound-receiving.receipt.ReceiptClosed` | `warehouse.inbound-receiving.events` | inbound-receiving |

## Consumer group id

This service consumes Kafka through three consumers (plus the analytics
projector on its own topic): each group id comes from a MANDATORY env var,
never a hardcoded string literal — `internal/architecture/fitness_test.go`'s
TestKafkaConsumerGroupNeverHardcodedInline enforces this (a real incident:
wes-work-planning's hardcoded group id let a locally-run e2e-tests process
silently collide with the live in-cluster Deployment's consumer group on
the shared fleet Kafka broker). The variables are
`DOCK_SCHEDULING_CONSUMER_GROUP`, `FACILITY_CONSUMER_GROUP` and
`INBOUND_RECEIVING_CONSUMER_GROUP` (cmd/api; the boot error names a missing
one) and `ANALYTICS_CONSUMER_GROUP` (the projector). Every group resumes
from committed offsets; none replays from FirstOffset.
