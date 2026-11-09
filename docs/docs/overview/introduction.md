---
id: introduction
title: Introduction
sidebar_label: Introduction
sidebar_position: 1
description: "What yard-management owns and does not own: the present of the yard (gate, trailers, moves, door work, seals, holds, dwell), its subdomain class, area and capabilities."
---

# Introduction

**yard-management** is the WMS-tier bounded context of the yard domain in the
`warehouse-systems` fleet (`IQVO` organisation), code `YM`. It owns everything
physically on site **outside the four walls**: the gate, the parking spots and
the dock doors. It is a **Supporting** subdomain (see
[Subdomain classification](/docs/ddd/subdomain-classification)) in the **WMS**
area; CloudEvents type segment `wms`.

Status: **built on develop, registered in warehouse-infra develop, not applied to
the cluster**. The repository has the code, the Helm chart and the CI, and
`warehouse-infra` `origin/develop` registers it (a `terraform/apps.yaml` entry, helm
values, dashboard and alert rules, the image pinned at v0.1.1), but no pod of it
runs yet; see [Registration in warehouse-infra](/docs/operations/runbook#registration-in-warehouse-infra).

## What it owns

| Capability | Where |
| --- | --- |
| Gate: check a visit in, reject it, check it out (`YardVisit`) | `POST /visits`, `/visit-rejections`, `/visits/{visitId}/check-outs` |
| Where each trailer is: exactly one spot, one door or one open move; its load state, seal and hold (`Trailer`) | `GET /trailers`, `GET /sites/{siteId}/yard-board` |
| Moves: hostlers pull the next move under a lease; the visiting driver's first move is created already claimed (`YardMove`) | `POST /moves`, `/moves/claims`, completions, cancellations |
| Door work while a trailer is docked: start, complete, auto-complete on receipt close; holds and seals | `/trailers/{trailerId}/door-work-*`, `/holds`, `/seals` |
| Dwell: a sweep raises `TrailerDwellExceeded` once per trailer and threshold crossing | `POST /dwell-sweeps` |
| Yard checks (audits) with discrepancies (`YardCheck`) | `/yard-checks` |
| Per-site rules: appointment required, holding spots, parking zones, dwell thresholds, lease length, seal mismatch action `FLAG` or `REJECT` (`YardPolicy`) | `PUT /sites/{siteId}/yard-policy` |
| Yard facts for everyone else: nine integration events and ten analytics-only events | [Event catalogue](/docs/api-reference/events) |
| Yard KPIs: dwell, door utilisation, hostler productivity, turn time, detention exposure, exception rates | `/reports/*` ([ADR 0011](/docs/adr/0011-kpi-standard-reports)) |
| Agent access: eight read tools and six approval-gated write tools | [MCP tools](/docs/mcp/tools) |

## What it does not own

- **The door calendar**: bookings, opening hours, no-shows, overlap rules and
  ETA risk belong to `dock-scheduling`. yard-management keeps event-fed local
  copies of appointments and door closures and never calls it.
- **Locations**: `facility-layout` registers the Yard and Dock slots; here they are
  a local copy fed by `warehouse.facility.events`.
- **Receiving**: ASNs and receipts belong to `inbound-receiving`; the yard keeps
  the ASN content and the receipt-at-door copies it needs.
- **Outbound loading and ship-confirm**: `outbound-shipping`, a downstream reader of
  the yard's facts.
- **Cases for problems**: `exception-management` opens them from the yard's events.
- **Authentication and personal data**: REST, MCP and reports are unauthenticated
  fleet-wide, and a driver is an opaque `driver_ref`.

## Where to go next

| I want to | Page |
| --- | --- |
| understand the layout and the five binaries | [Architecture](/docs/overview/architecture) |
| run it and make a first call | [Quickstart](/docs/overview/quickstart) |
| set it up and operate it | [Configuration](/docs/operations/configuration), [Runbook](/docs/operations/runbook), [Observability](/docs/operations/observability), [Troubleshooting](/docs/operations/troubleshooting) |
| know what to do before the first release | [Before the first release](/docs/operations/before-first-release) |
| test it | [Testing](/docs/development/testing) |
| see who it talks to | [Integration](/docs/ecosystem/integration) |
| read the model | [Use cases](/docs/ddd/use-cases), [Subdomain classification](/docs/ddd/subdomain-classification), [DDD artifacts](/docs/ddd/ddd-artifacts), [Ubiquitous language](/docs/ddd/ubiquitous-language) |
| call it | [API overview](/docs/api-reference), [Event catalogue](/docs/api-reference/events), [MCP tools](/docs/mcp/tools) |
| read the decisions | [ADR index](/docs/adr) |

## The problem in plain language

A site has walls, and a lot of its day happens outside them. A tractor and
driver arrive at the **gate**. Some of them have a booked time at a door; some
are walk-ins. The trailer they bring has to go somewhere: straight to a
**dock door**, or to a **parking spot** until a door is free. Someone has to
move it (a yard driver, the **hostler**, or the visiting driver for the first
move). At the door it is unloaded or loaded (**door work**). Afterwards it
leaves the door, waits in a spot, or goes out through the gate with the same
tractor.

The other contexts of the fleet keep everything inside the four walls. `facility-layout` registers the `Yard` and `Dock` locations of a
site, but no context records what is physically on them. So nobody can answer,
at a given moment: which trailer is on which spot, which door is free, how long
has that trailer been here, did the seal match what the carrier declared, which
hostler is doing what.

This context answers those questions. It is the **present** of the yard.

## Two contexts, one boundary

| Question | Owner | Why |
| --- | --- | --- |
| Is door 7 booked for carrier X from 10:00 to 11:00 tomorrow? | `dock-scheduling` | Door time in the future; the no-double-booking rule lives there. |
| Is trailer 4471 on spot Y12 right now, and has it been there too long? | `yard-management` | A physical fact in the present; the one-trailer-per-place rule lives here. |

The scope rule: `dock-scheduling` owns the future of a door,
`yard-management` owns the present of the yard, and each keeps an event-fed
local copy of what it needs from the other. Neither calls the other
synchronously. `yard-management` keeps `AppointmentView` and `DoorClosureView`
from `dock-scheduling` events, and `dock-scheduling` applies arrival, docking
and door release from `yard-management` events
([YD-0004](https://iqvo.github.io/warehouse-docs/yard/adr/yd-0004-physical-facts-win): the physical fact wins).

## Where the yard's rules come from

- **One place per trailer, one trailer per place.** A trailer on site is at
  exactly one yard spot, one door, or one open move. A spot or door holds at
  most one trailer (`ErrLocationOccupied`). A move's destination is reserved
  when the move is requested (`ErrDestinationReserved`).
- **Moves are pulled.** Hostlers claim the next move with a lease; nothing is
  pushed to them. A lease that expires returns the move to the queue. A
  visiting driver's first move from the gate is the one exception: it is created
  already claimed, with no lease.
- **The gate decides who comes in.** Walk-ins are allowed into a holding spot
  unless the site's policy requires an appointment. A seal mismatch at the gate
  always raises an exception; whether it also rejects the visit is site policy.
- **A visit leaves only when its trailers are released.** Every trailer taken
  out must be off the door and without an open move, and every loaded outbound
  trailer must carry a seal.

## Actors

| Actor | Kind | What they do here |
| --- | --- | --- |
| Gate clerk or kiosk | Person / device | Checks a visit in, rejects it, checks it out (`CheckInVisit`, `RejectAtGate`, `CheckOutVisit`). A camera, ANPR or kiosk is an inbound adapter issuing `CheckInVisit`, not a context. |
| Hostler | Person (floor client) | Claims the next move and completes it (`ClaimNextMove`, `CompleteMove`). Also called jockey, spotter or shunter. |
| Dock operator | Person (floor client) | Starts and completes door work (`StartDoorWork`, `CompleteDoorWork`). Outbound door work is completed here; `outbound-shipping` (built on develop, outside this section) only reads the yard's facts. |
| Yard supervisor | Person (console) | Requests and cancels moves, places and releases holds, records seals, declares the yard policy, runs yard checks. |
| Dock planner | Person (console) | Not an actor here: declares door hours and closures in `dock-scheduling`. This context only reads the closures. |
| Scheduler (cron) | Kubernetes CronJob | Calls `POST /dwell-sweeps` and `POST /move-lease-sweeps`. |
| `dock-scheduling` | Sibling context (upstream and downstream) | Publishes appointments and door closures; consumes visit check-in and trailer docking and undocking. |
| `facility-layout` | Fleet context (upstream) | Publishes the Yard and Dock slots and the zone-to-site mapping. |
| `inbound-receiving` | Fleet context (upstream and downstream) | Publishes ASNs and receipts. It does not consume `TrailerDocked` as built; the optional consumer was not built. |
| `exception-management` | Fleet context (downstream) | Opens problem cases from rejected visits, seal exceptions, dwell breaches and yard-check discrepancies (merged on develop). |
| `outbound-shipping` | Fleet context (downstream) | Confirms a shipment when the trailer leaves: reads `TrailerDocked`, `DoorWorkCompleted` and `TrailerDeparted` (merged on develop, not deployed). |

## Decisions this context makes

The business decisions the context enforces:

1. A trailer on site is at exactly one place: one yard spot, one door, or one
   open move.
2. A spot or a door holds at most one trailer.
3. A trailer has at most one open move, and a move's destination is reserved
   when the move is requested.
4. Moves are pulled by hostlers (claim-next with a lease), never pushed, as in
   `fulfillment-execution`.
5. A visit checks out only when every trailer it takes is released (not docked,
   no open move) and every loaded trailer of a live-load or pickup visit carries a seal.
6. Walk-ins are allowed into a holding spot unless the site's policy requires an
   appointment.
7. A seal mismatch at the gate raises `SealExceptionRaised`; whether it also
   rejects the visit is site policy (`FLAG` or `REJECT`).

It does **not** decide when a door is booked, whether two appointments overlap,
or whether a window is inside a door's opening hours (`dock-scheduling`), nor
how a trailer is received once it is at a door (`inbound-receiving`).

## Deliberately out of scope

Each item below is a boundary of the context.

- **Door calendar, bookings, opening hours, no-shows, ETA risk.** These belong
  to `dock-scheduling`.
- **Trailer load planning, loading, bill of lading and ship-confirm.** The context
  `outbound-shipping` (Supporting), built on develop in its own repository; it reads the yard's facts.
- **Real-time ETA feeds.** A future context, `transport-visibility` (Generic);
  an ETA enters `dock-scheduling` first.
- **Detention and yard service charges.** A future context, `yard-billing`
  (Generic). Here the detention free time is only a site setting.
- **Gate camera, ANPR and kiosk.** An inbound adapter, not a context.
- **Reefer temperature telemetry.** Phase 3: a measurement value object on
  `Trailer`, fed by an IoT adapter.
- **A network view across sites.** A cross-site read model of `YardBoardView`
  (ops agent, console), not a context.
- **Autonomous yard trucks.** A device-control seam, reserved and unbuilt.
- **Anything of the retail network.** No `retailnetwork.*` topic is consumed and
  no retail-network service is called; the only channel to the network stays
  `network-fulfillment` and the Vendor API.
- **Authentication and personal data.** The fleet has no auth on REST or MCP,
  and no driver PII is stored: a driver is an opaque `driver_ref`.
