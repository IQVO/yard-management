---
id: integration
title: Integration
sidebar_label: Integration
sidebar_position: 1
description: "Every upstream and downstream of yard-management: protocol, contract, event types, failure behaviour, and the edges that do not exist."
---

# Integration

yard-management **calls no other service**. Everything it needs from a sibling
arrives as a Kafka event and is kept in a local copy
([ADR 0003](/docs/adr/0003-consumed-contracts-and-local-copies),
[ADR 0009](/docs/adr/0009-kafka-consumers)); everything it tells the others
leaves through the transactional outbox. The only synchronous traffic is people,
the floor client and the console calling its REST API, and agents calling MCP.
The broker is the platform's single Kafka; the edge is Kong at `:8000` under
`/api/yard-management`.

Neighbour classifications are on [Context map](/docs/ddd/context-map); the
message shapes are on [Domain events](/docs/ddd/domain-events) and the
[Event catalogue](/docs/api-reference/events).

## Upstream (consumed)

| Upstream | Protocol and topic | Contract (`type` suffixes) | What it feeds | Failure behaviour |
| --- | --- | --- | --- | --- |
| `dock-scheduling` | Kafka `warehouse.dock-scheduling.events`, group `DOCK_SCHEDULING_CONSUMER_GROUP` | `dockappointment.DockAppointmentBooked`, `…Rescheduled`, `…Cancelled`, `…NoShow`; `dockcalendar.DoorClosureScheduled`, `…DoorClosureLifted` | `appointments`, `door_closures` copies; gates check-in (`appointment-required`, `unknown-appointment`), directs a trailer to its door, refuses a closed door (`door-closed`); `NoShowFreesHolding` cancels the open `TO_DOOR` move on no-show or cancellation | transient failure: the same message is retried forever (200 ms to 5 s backoff), blocking its partition; a payload without `version` or `window` is poison and goes to `warehouse.dock-scheduling.events.dlq`; dock-scheduling down: the copy goes stale, check-in still works on what it has |
| `facility-layout` | Kafka `warehouse.facility.events`, group `FACILITY_CONSUMER_GROUP` | `zone.ZoneRegistered`, `locationslot.LocationSlotRegistered`, `locationslot.LocationSlotDecommissioned` (camelCase payloads) | `zones`, `locations`: Yard slots are spots, Dock slots are doors with a `dockFlow`; zone to site code | same; with no location copy for a site the yard board is `site-not-found` and moves/check-ins cannot find a destination |
| `inbound-receiving` | Kafka `warehouse.inbound-receiving.events`, group `INBOUND_RECEIVING_CONSUMER_GROUP` | `asn.ASNRegistered`, `asn.ASNCancelled`, `receipt.ReceiptOpened`, `receipt.ReceiptClosed` | `asns` (which ASNs a trailer holds; decides door flow compatibility), `receipts`; `AutoCompleteUnload` completes the door work on `ReceiptClosed` | same |
| Own analytics topic | Kafka `warehouse.yard-management.analytics`, group `ANALYTICS_CONSUMER_GROUP` | the 19 published types, analytics `dataschema` | `analytics.*` tables via `cmd/yard-projector` | a dying consumer exits the projector so the pod restarts; a new group replays from the first offset |

Consumers ignore types they do not handle, skip messages that are not
CloudEvents (WARN), and are idempotent on the CloudEvents `id`
(`processed_events`).

## Downstream (published)

| Downstream | Protocol and topic | Types it reads (from its own code on `origin/develop`) | What it does | Failure behaviour on this side |
| --- | --- | --- | --- | --- |
| `dock-scheduling` | Kafka `warehouse.yard-management.events` | `VisitCheckedIn`, `VisitRejected`, `TrailerDocked`, `TrailerUndocked` | applies arrival, the actual door and door release; cancels the appointment with `TURNED_AWAY` on rejection | none: yard-management does not know or wait for consumers; unconsumed events stay in the topic |
| `outbound-shipping` | Kafka `warehouse.yard-management.events` | `TrailerDocked`, `DoorWorkCompleted`, `TrailerDeparted` | confirms a shipment when the trailer leaves | same |
| `exception-management` | Kafka `warehouse.yard-management.events` | `VisitRejected`, `SealExceptionRaised`, `TrailerDwellExceeded`, `YardCheckClosed` | opens problem cases (one per yard-check discrepancy) | same |
| floor client, console, ops agent | REST `:8080` via Kong; MCP `:8090` | [API overview](/docs/api-reference), [MCP tools](/docs/mcp/tools) | gate, hostler, dock, supervisor screens; agent proposals | idempotent writes: retry with the same key; 503 `dependency-unavailable` when Postgres or Kafka is down |
| any context | REST `GET /visits`, `/trailers`, `/moves`, `/sites/{siteId}/yard-board`, `/sites/{siteId}/yard-policy`, `/reports/*` | read-only Open Host Service | diagnostics and tools; no context needs it for a decision | n/a |
| Kubernetes CronJobs | REST `POST /dwell-sweeps`, `/move-lease-sweeps` | the chart's two CronJobs | time-driven rules | a failed Job keeps its logs; the next run repeats safely |

Delivery on the producing side is at least once: the relay may republish a row
with the same CloudEvents `id`, so every consumer must dedupe on it. Per-aggregate
order holds per key and partition; a row that hit the attempt cap is excluded and
later rows overtake it ([Troubleshooting](/docs/operations/troubleshooting#events-are-not-leaving-outbox)).

## Edges that do not exist

- **`inbound-receiving` does not consume the yard's events.** `apis/asyncapi.yaml`
  names it as a consumer of `TrailerDocked`; no yard consumer exists in that
  repository on `origin/develop`. The optional consumer was not built.
- **No call to a sibling's REST API, in either direction.** There is no HTTP client
  in the code.
- **No retail-network edge.** No `retailnetwork.*` topic is consumed and no
  retail-network service is called; the only channel to the network stays
  `network-fulfillment`.
- **No A2A endpoint** and **no authentication** anywhere.
- **No consumer of the analytics topic** other than this service's own projector.
- **Not deployed.** None of the edges above is live: the service is registered nowhere in `warehouse-infra`.
