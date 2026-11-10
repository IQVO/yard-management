---
id: events
title: Event catalogue
sidebar_label: Events
sidebar_position: 2
description: "The Kafka events yard-management publishes and consumes: topics, CloudEvents types, keys, consumers and delivery semantics."
---

# Event catalogue

Contract: `apis/asyncapi.yaml` (AsyncAPI **3.0**; channels have friendly keys
and the topic is in `address:`). Every message is a **CloudEvents 1.0** structured
JSON message (`application/cloudevents+json`) built only by
`internal/adapters/kafka/cloudevents`: `id`, `source` `/warehouse/yard-management`,
`type`, `subject`, `time`, `datacontenttype` and `dataschema`
(`urn:warehouse:yard-management:<events|analytics>:<EventName>:v1`) are required.
`type` = `com.warehouse.wms.yard-management.<entity>.<EventName>`; `data` is
snake_case, times are RFC 3339 UTC, `version` is the aggregate version. The Kafka
key and the CloudEvents `subject` are the aggregate root id. A breaking change is
a new `.v2` type. Every field of every payload is on
[Domain events](/docs/ddd/domain-events).

## Channels

| Channel key (AsyncAPI) | Topic (`address`) | Direction | Written or read by |
| --- | --- | --- | --- |
| `yardManagementEvents` | `warehouse.yard-management.events` | produced | `cmd/relay`; the **nine integration types** |
| `yardManagementAnalytics` | `warehouse.yard-management.analytics` | produced | `cmd/relay` in the same pass: the nine integration types again (same `id`, byte-identical `data`, analytics `dataschema`) plus **ten analytics-only types**; consumed only by `cmd/yard-projector` |
| (none in AsyncAPI) | `warehouse.yard-management.events.dlq` | produced | outbox rows that can never be encoded, verbatim (header `dlq-reason`) |
| `dockSchedulingEvents` | `warehouse.dock-scheduling.events` | consumed | `cmd/api` consumer, group `DOCK_SCHEDULING_CONSUMER_GROUP` |
| `facilityEvents` | `warehouse.facility.events` | consumed | `cmd/api`, group `FACILITY_CONSUMER_GROUP` |
| `inboundReceivingEvents` | `warehouse.inbound-receiving.events` | consumed | `cmd/api`, group `INBOUND_RECEIVING_CONSUMER_GROUP` |

## Published types (19)

Rows are in the order of the encoder's catalogue (`published` in
`internal/adapters/outbound/kafka/encoder.go`); a row outside it is poison and is
dead-lettered. The consumers named are the ones whose code reads the type on
`origin/develop` of the sibling repository.

| CloudEvents `type` | Event | Topics | Consumers |
| --- | --- | --- | --- |
| `com.warehouse.wms.yard-management.yardvisit.VisitCheckedIn` | `VisitCheckedIn` | events + analytics | dock-scheduling |
| `com.warehouse.wms.yard-management.yardvisit.VisitRejected` | `VisitRejected` | events + analytics | dock-scheduling, exception-management |
| `com.warehouse.wms.yard-management.yardvisit.VisitCheckedOut` | `VisitCheckedOut` | analytics only | this service's projector |
| `com.warehouse.wms.yard-management.trailer.TrailerDocked` | `TrailerDocked` | events + analytics | dock-scheduling, outbound-shipping |
| `com.warehouse.wms.yard-management.trailer.TrailerUndocked` | `TrailerUndocked` | events + analytics | dock-scheduling |
| `com.warehouse.wms.yard-management.trailer.SealExceptionRaised` | `SealExceptionRaised` | events + analytics | exception-management |
| `com.warehouse.wms.yard-management.trailer.TrailerDwellExceeded` | `TrailerDwellExceeded` | events + analytics | exception-management |
| `com.warehouse.wms.yard-management.trailer.TrailerParked` | `TrailerParked` | analytics only | this service's projector |
| `com.warehouse.wms.yard-management.trailer.DoorWorkStarted` | `DoorWorkStarted` | analytics only | this service's projector |
| `com.warehouse.wms.yard-management.trailer.DoorWorkCompleted` | `DoorWorkCompleted` | events + analytics | outbound-shipping |
| `com.warehouse.wms.yard-management.trailer.TrailerDeparted` | `TrailerDeparted` | events + analytics | outbound-shipping |
| `com.warehouse.wms.yard-management.trailer.TrailerHeld` | `TrailerHeld` | analytics only | this service's projector |
| `com.warehouse.wms.yard-management.trailer.TrailerHoldReleased` | `TrailerHoldReleased` | analytics only | this service's projector |
| `com.warehouse.wms.yard-management.yardmove.YardMoveRequested` | `YardMoveRequested` | analytics only | this service's projector |
| `com.warehouse.wms.yard-management.yardmove.YardMoveClaimed` | `YardMoveClaimed` | analytics only | this service's projector |
| `com.warehouse.wms.yard-management.yardmove.YardMoveLeaseExpired` | `YardMoveLeaseExpired` | analytics only | this service's projector |
| `com.warehouse.wms.yard-management.yardmove.YardMoveCompleted` | `YardMoveCompleted` | analytics only | this service's projector |
| `com.warehouse.wms.yard-management.yardmove.YardMoveCancelled` | `YardMoveCancelled` | analytics only | this service's projector |
| `com.warehouse.wms.yard-management.yardcheck.YardCheckClosed` | `YardCheckClosed` | events + analytics | exception-management |

Totals: 9 integration types (events and analytics), 10 analytics-only.
`TestEventCatalogueMatchesContract` ties this list to the AsyncAPI document, and
the integration/analytics split is decided by the encoder, not by the emitter.

## Consumed types (13)

| Topic | CloudEvents `type` | Effect here |
| --- | --- | --- |
| `warehouse.dock-scheduling.events` | `com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentBooked`, `…DockAppointmentRescheduled`, `…DockAppointmentCancelled`, `…DockAppointmentNoShow` | appointment copy; `NoShowFreesHolding` on no-show and cancellation |
| `warehouse.dock-scheduling.events` | `com.warehouse.wms.dock-scheduling.dockcalendar.DoorClosureScheduled`, `…DoorClosureLifted` | door-closure copy |
| `warehouse.facility.events` | `com.warehouse.wms.facility-layout.zone.ZoneRegistered`, `…locationslot.LocationSlotRegistered`, `…locationslot.LocationSlotDecommissioned` | location copy (Yard slots are spots, Dock slots are doors with a dock flow) |
| `warehouse.inbound-receiving.events` | `com.warehouse.wms.inbound-receiving.asn.ASNRegistered`, `…asn.ASNCancelled`, `…receipt.ReceiptOpened`, `…receipt.ReceiptClosed` | ASN content and receipt-at-door copies; `AutoCompleteUnload` on `ReceiptClosed` |

Consumers dispatch on the full `type`; an unknown type is ignored, a message that
is not a valid CloudEvent is skipped with a WARN, a poison message of a handled
type goes to `<topic>.dlq`. Effects are idempotent on the CloudEvents `id`
(`processed_events`). See [Runbook](/docs/operations/runbook#kafka-topics-and-consumer-groups).

## Delivery semantics

- **Transactional outbox.** The aggregate change and its outbox rows commit in one
  transaction; `cmd/relay` publishes at least once with `acks=all`, keyed by the
  aggregate id (the 8 partitions keep one aggregate's events ordered), and the
  persisted `id` makes a republish dedupe-able.
- **Ordering.** Per aggregate, per partition. A row that reaches the attempt cap is
  excluded and later rows overtake it ([ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue),
  [ADR 0005](/docs/adr/0005-postgres-persistence-and-outbox)).
- **MCP attribution.** An event raised by an MCP write carries
  `initiated_by {via, proposal_id, approved_by, agent_id}` in its `data`.
- **Integration events** added by [ADR 0010](/docs/adr/0010-door-work-and-departure-on-the-integration-topic):
  `DoorWorkCompleted` and `TrailerDeparted` on the integration topic.

## Discrepancies

- The `publishYardManagementEvents` operation description in `apis/asyncapi.yaml`
  lists `inbound-receiving` as a consumer of `TrailerDocked`. No consumer of the
  yard topic exists in `inbound-receiving` on `origin/develop` (checked by
  `git grep warehouse.yard-management`); the table above omits it.
