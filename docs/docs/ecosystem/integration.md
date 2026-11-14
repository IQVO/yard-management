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
| `dock-scheduling` | Kafka `warehouse.dock-scheduling.events`, group `DOCK_SCHEDULING_CONSUMER_GROUP` | `dockappointment.DockAppointmentBooked`, `…Rescheduled`, `…Cancelled`, `…NoShow`; `dockcalendar.DoorClosureScheduled`, `…DoorClosureLifted`; `carrier.CarrierRegistered`, `…CarrierSuspended`, `…CarrierReinstated` | `appointments`, `door_closures`, `carriers` copies (the carrier copy is read by the suspended-carrier rule of the gate, ADR 0016); gates check-in (`appointment-required`, `unknown-appointment`), directs a trailer to its door, refuses a closed door (`door-closed`); `NoShowFreesHolding` cancels the open `TO_DOOR` move on no-show or cancellation | transient failure: the same message is retried forever (200 ms to 5 s backoff), blocking its partition; a payload without `version` or `window` is poison and goes to `warehouse.dock-scheduling.events.dlq`; dock-scheduling down: the copy goes stale, check-in still works on what it has |
| `facility-layout` | Kafka `warehouse.facility.events`, group `FACILITY_CONSUMER_GROUP` | `zone.ZoneRegistered`, `locationslot.LocationSlotRegistered`, `locationslot.LocationSlotDecommissioned` (camelCase payloads) | `zones`, `locations`: Yard slots are spots, Dock slots are doors with a `dockFlow`; zone to site code | same; with no location copy for a site the yard board is `site-not-found` and moves/check-ins cannot find a destination |
| `inbound-receiving` | Kafka `warehouse.inbound-receiving.events`, group `INBOUND_RECEIVING_CONSUMER_GROUP` | `asn.ASNRegistered`, `asn.ASNCancelled`, `receipt.ReceiptOpened`, `receipt.ReceiptClosed` | `asns` (which ASNs a trailer holds; decides door flow compatibility), `receipts`; `AutoCompleteUnload` completes the door work on `ReceiptClosed` | same |
| Own analytics topic | Kafka `warehouse.yard-management.analytics`, group `ANALYTICS_CONSUMER_GROUP` | the 23 published types, analytics `dataschema` | `analytics.*` tables via `cmd/yard-projector` | a dying consumer exits the projector so the pod restarts; a new group replays from the first offset |

Consumers ignore types they do not handle, skip messages that are not
CloudEvents (WARN), and are idempotent on the CloudEvents `id`
(`processed_events`).

## Downstream (published)

| Downstream | Protocol and topic | Types it reads (from its own code on `origin/develop`) | What it does | Failure behaviour on this side |
| --- | --- | --- | --- | --- |
| `dock-scheduling` | Kafka `warehouse.yard-management.events` | `VisitCheckedIn`, `VisitRejected`, `TrailerDocked`, `TrailerUndocked` | applies arrival, the actual door and door release; cancels the appointment with `TURNED_AWAY` on rejection | none: yard-management does not know or wait for consumers; unconsumed events stay in the topic |
| `inbound-receiving` | Kafka `warehouse.yard-management.events` | `VisitCheckedIn` only (its ADR 0021; every other type is committed past) | keeps the `visit_arrivals` local copy for its dock-to-stock KPI | same |
| `outbound-shipping` | Kafka `warehouse.yard-management.events` | `TrailerDocked`, `DoorWorkCompleted`, `TrailerDeparted` | confirms a shipment when the trailer leaves | same |
| `exception-management` | Kafka `warehouse.yard-management.events` | `VisitRejected`, `SealExceptionRaised`, `TrailerDwellExceeded`, `YardCheckClosed` | opens problem cases (one per yard-check discrepancy) | same |
| floor client, console, ops agent | REST `:8080` via Kong; MCP `:8090` (MCP is switched off on k3s, read 2026-11-10: no pod, no route) | [API overview](/docs/api-reference), [MCP tools](/docs/mcp/tools) | gate, hostler, dock, supervisor screens; agent proposals | idempotent writes: retry with the same key; 503 `dependency-unavailable` when Postgres or Kafka is down |
| any context | REST `GET /visits`, `/trailers`, `/moves`, `/sites/{siteId}/yard-board`, `/sites/{siteId}/yard-policy`, `/reports/*` | read-only Open Host Service | diagnostics and tools; no context needs it for a decision | n/a |
| Kubernetes CronJobs | REST `POST /dwell-sweeps`, `/move-lease-sweeps` | the chart's two CronJobs | time-driven rules | a failed Job keeps its logs; the next run repeats safely. Switched off on k3s (read 2026-11-10): no CronJob exists, call the endpoints by hand |

Delivery on the producing side is at least once: the relay may republish a row
with the same CloudEvents `id`, so every consumer must dedupe on it. Per-aggregate
order holds per key and partition; a row that hit the attempt cap is excluded and
later rows overtake it ([Troubleshooting](/docs/operations/troubleshooting#events-are-not-leaving-outbox)).

## Edge status

Read-only on 2026-11-10 (`kubectl get pods,deploy -n warehouse-systems`, context
`k3s-multipass`). An edge is **live** only when both ends run in the cluster **and** the
consumer or producer code and the spec declare it. `yard-management` is applied (Argo
application Synced and Healthy, api and relay pods Ready at `v0.1.2`; see the
[Runbook](/docs/operations/runbook#what-runs-on-the-k3s-cluster)), so an edge is live when
the neighbour also runs. The groups are those of the `warehouse-infra` helm values
(`helm-values/yard-management.yaml` and the neighbour's own file) and of the running
Deployments' environment. "Live" here means both ends run and declare the edge; it does
**not** say events flow without lag: consumer group offsets and lag need `kubectl exec` and
were not read.

| Edge | Direction | Consumer group | Neighbour in the cluster (image running) | Status |
| --- | --- | --- | --- | --- |
| `dock-scheduling` | in: `warehouse.dock-scheduling.events` | `yard-management-dock-scheduling` | `dock-scheduling` api Ready (`d5db895`, `v0.1.2`) | live |
| `dock-scheduling` | out: `warehouse.yard-management.events` | `dock-scheduling-yard` | same pod; its `yard_consumer.go` | live |
| `facility-layout` | in: `warehouse.facility.events` | `yard-management-facility` | `facility-layout` Ready (`1305245`) | live |
| `inbound-receiving` | in: `warehouse.inbound-receiving.events` | `yard-management-inbound-receiving` | `inbound-receiving` Ready (`484490f`, v2.0.6) | live |
| `inbound-receiving` | out: `warehouse.yard-management.events` (`VisitCheckedIn` only) | `inbound-receiving-visit-arrivals` | same pod; `visit_arrival_consumer.go` at `484490f`; the Deployment carries `VISIT_ARRIVAL_CONSUMER_GROUP` | live (see the spec note below) |
| `exception-management` | out: `warehouse.yard-management.events` | `exception-management-yard-management` | `exception-management` Ready (`35bb7d9`); `yard_exceptions_consumer.go`; the Deployment carries `YARD_MANAGEMENT_EXCEPTIONS_CONSUMER_GROUP` | live |
| `outbound-shipping` | out: `warehouse.yard-management.events` | none: no `warehouse-infra` entry or helm values found for it | no pod (`kubectl get deploy outbound-shipping`: NotFound) | not live, not registered in infra |
| own projector | in: `warehouse.yard-management.analytics` | `yard-management-analytics` | none: `analytics.enabled: false`, no `yard-management-projector` pod | not live: the relay writes the topic, nothing consumes it |

The event types a neighbour reads come from the neighbours' own code and specs at the
commit each pod runs (the image tag is that commit), as the tables above say; the group
and neighbour columns come from `warehouse-infra` `origin/develop` and the cluster
listing. The `exception-management` row was "not wired in infra" until its group ids were
pinned (`warehouse-infra` commit `a703971`, 2026-11-09); the running Deployment carries it.
Spec note: this service's `apis/asyncapi.yaml` used to name `inbound-receiving` as a consumer
of `TrailerDocked`, not of `VisitCheckedIn`; it now names it as a consumer of `VisitCheckedIn`,
as the `inbound-receiving` spec and code declare (its ADR 0021).

Not verified: the offsets and lag of every group, whether the topics hold the events a
consumer expects, and delivery from the relay. The api log of the yard pod shows
`kafka reader error; retrying` lines with `i/o timeout` on the dock-scheduling,
inbound-receiving and facility topics at 09:19Z to 09:43Z and 23:21Z on 2026-11-10 (quoted
in the [Runbook](/docs/operations/runbook#what-runs-on-the-k3s-cluster)); they were not
diagnosed.

## Edges that do not exist

- **`inbound-receiving` does not consume `TrailerDocked`.** Its consumer reads only
  `VisitCheckedIn` (its ADR 0021) and commits every other type on the topic past. The
  optional door-confirm consumer was not built.
- **No call to a sibling's REST API, in either direction.** There is no HTTP client
  in the code.
- **No retail-network edge.** No `retailnetwork.*` topic is consumed and no
  retail-network service is called; the only channel to the network stays
  `network-fulfillment`.
- **No A2A endpoint** and **no authentication** anywhere.
- **No consumer of the analytics topic** other than this service's own projector.
- **No live edge to `outbound-shipping`.** It is merged on develop but has no
  `warehouse-infra` entry and no pod; see [Edge status](#edge-status).
