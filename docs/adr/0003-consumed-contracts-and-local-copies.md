# ADR 0003: Consumed contracts and local copies

## Status

Accepted (2026-11-05).

## Context

The yard needs facts that other contexts own: where the doors and spots are, which appointments exist, which doors are
closed, what an inbound trailer holds, which receipt is open at a door. Nothing is looked up live: every fact is an
event-fed local copy (fleet rule), updated by a consumer, read by the use cases. The contracts below are read from the
producers' `origin/develop` `apis/asyncapi.yaml` (facility-layout, inbound-receiving) and from the pinned
`contracts-delta.md` (dock-scheduling, built in parallel); the consumed messages are restated in this repo's
`apis/asyncapi.yaml` as `receive` operations.

## Decision

### Consumed types

| type | topic | consumer group env | feeds |
|---|---|---|---|
| `com.warehouse.wms.facility-layout.zone.ZoneRegistered` {zoneId, siteCode} | `warehouse.facility.events` | `FACILITY_CONSUMER_GROUP` | `zones` |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered` {locationCode, zoneId, role, dockFlow} | `warehouse.facility.events` | `FACILITY_CONSUMER_GROUP` | `locations` (role Yard = spot, role Dock = door; any other role ignored) |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned` {locationCode} | `warehouse.facility.events` | `FACILITY_CONSUMER_GROUP` | `locations.active = false` |
| `com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentBooked` | `warehouse.dock-scheduling.events` | `DOCK_SCHEDULING_CONSUMER_GROUP` | `appointments` |
| `...dockappointment.DockAppointmentRescheduled` | same | same | `appointments`; NoShowFreesHolding when the door changed |
| `...dockappointment.DockAppointmentCancelled` | same | same | `appointments`; NoShowFreesHolding |
| `...dockappointment.DockAppointmentNoShow` | same | same | `appointments`; NoShowFreesHolding |
| `com.warehouse.wms.dock-scheduling.dockcalendar.DoorClosureScheduled` {site_id, closure_id, door_code, from, to} | same | same | `door_closures` |
| `...dockcalendar.DoorClosureLifted` {site_id, closure_id, door_code} | same | same | `door_closures` |
| `com.warehouse.wms.inbound-receiving.asn.ASNRegistered` {asn_number, expected_arrival, lines} | `warehouse.inbound-receiving.events` | `INBOUND_RECEIVING_CONSUMER_GROUP` | `asns` |
| `...asn.ASNCancelled` {asn_number} | same | same | `asns.cancelled` |
| `com.warehouse.wms.inbound-receiving.receipt.ReceiptOpened` {receipt_id, asn_number, appointment_id, door_code} | same | same | `receipts` |
| `...receipt.ReceiptClosed` {receipt_id, asn_number, closed_at} | same | same | `receipts.closed_at`; AutoCompleteUnload |

Facility payloads are camelCase, the others snake_case. Absent `role` means Storage. Every other type on those topics is
ignored. dock-scheduling's `DockAppointmentBooked` and `Rescheduled` carry `arrival_tolerance_minutes` (X1) and
Rescheduled carries `previous_door_code`; the copy relies on `appointment_id`, `site_id`, `direction`, `door_code`,
`carrier_scac`, `window_start`, `window_end`, `asn_numbers`, `shipment_refs`, `expected_seal_number`,
`arrival_tolerance_minutes` and `version`. yard-management does not consume DockAppointmentCheckedIn or Completed (X4).

### Local copies (Postgres tables)

- `zones(zone_id pk, site_code)`; `locations(location_code pk, zone_id, site_code null, kind SPOT|DOOR, dock_flow, active)`. A slot
  registered before its zone keeps `site_code null` and is resolved when the zone arrives (X5); a null site means unknown.
- `appointments(appointment_id pk, site_id, direction, door_code, carrier_scac, window_start, window_end, asn_numbers,
  shipment_refs, expected_seal_number, arrival_tolerance_minutes, status, version)`. Status BOOKED, CHECKED_IN, USED,
  CANCELLED, NO_SHOW. The version guard makes an old replay a no-op; Cancelled and NoShow are terminal (a later Booked of a
  lower or equal version never resurrects the row).
- `door_closures(closure_id pk, site_id, door_code, closed_from, closed_to, lifted)`.
- `asns(asn_number pk, expected_arrival, cancelled)`; `receipts(receipt_id pk, asn_number, appointment_id, door_code,
  closed_at)`.
- `processed_events(event_id pk, processed_at)` is the inbox: a consumed CloudEvent id is claimed in the same unit of work as its
  effect, so a redelivery after a rollback is processed and a duplicate is skipped. The table is not keyed by consumer: a
  CloudEvents id is unique fleet-wide and each type is handled by exactly one consumer.

### Rules

- Dispatch on the full CloudEvents `type`; unknown types are ignored; a message that is not a CloudEvent is skipped; a payload
  that can never be applied (poison) is dead-lettered to `<topic>.dlq` at once. Any other error is transient: the same
  message is retried with capped exponential backoff (200 ms doubling to 5 s) and its offset is not committed until it
  succeeds; nothing is dead-lettered because of a retry count (ADR 0009).
- Tolerant decoding: unknown fields are ignored, optional fields default.
- Consumer groups are mandatory and named by environment; boot fails naming the missing variable.
- Out-of-order tolerance: facility slots before zones (above); a Rescheduled or Cancelled for an unseen appointment creates the
  row from the event (it carries the door and window) and a later older Booked does not override it.
- NoShowFreesHolding: on Cancelled, NoShow, or Rescheduled to another door, an open TO_DOOR move for that appointment is
  cancelled (the trailer, if already on site, stays where it is; the supervisor decides). AutoCompleteUnload: on
  ReceiptClosed, join `receipt_id` to the receipt copy and its `appointment_id`; when the trailer of that appointment is
  DOCKED and every ASN on it has a closed receipt, door work completes (WAITING or IN_PROGRESS to DONE) and load_state
  becomes EMPTY.

## Consequences

- A missing local fact fails closed where it protects the yard (an unknown location is `unknown-location`) and fails open
  where the model says walk-ins are fine (an unknown appointment on a site that does not require one is a walk-in).
- dock-scheduling consumes VisitCheckedIn, TrailerDocked, TrailerUndocked and VisitRejected; inbound-receiving may consume
  TrailerDocked; exception-management consumes VisitRejected, SealExceptionRaised, TrailerDwellExceeded and
  YardCheckClosed.
