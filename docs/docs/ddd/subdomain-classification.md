---
id: subdomain-classification
title: Subdomain classification
sidebar_label: Subdomain classification
sidebar_position: 2
description: "yard-management is a Supporting subdomain of the WMS area: the reasoning, its parts, and its neighbours' classes."
---

# Subdomain classification

**yard-management is Supporting.** Differentiation 0.50, complexity 0.60,
evolution Custom-built (the values of the yard design model); business model: cost
reducer (detention, demurrage, lost trailers, idle dock labour) and throughput
enabler. Area: WMS (CloudEvents subdomain segment `wms`). The Core Domain Chart is
on [Core domain chart](/docs/ddd/core-domain-chart); the decision record is
[ADR 0001](/docs/adr/0001-yard-management-bounded-context).

## Why Supporting

- **Every site with trucks needs a yard record**, and the rules that vary (whether
  an appointment is required, the seal mismatch action `FLAG` or `REJECT`, dwell
  thresholds per trailer kind, lease length) differ per operator rather than per
  product. They live in a replaceable per-site `YardPolicy`; the platform does not
  win on them.
- **Not Generic.** The model is not a commodity record: one place per trailer,
  one trailer per place, pull-with-lease moves, door work coupled to the dock
  calendar, and seal and hold rules are this fleet's own vocabulary. It also
  produces the facts (dwell, door occupancy, yard-truck productivity, turn time,
  detention exposure) that no other context has.
- **Not Core.** Nobody chooses a warehouse for how it records a yard. The
  competitive core of the fleet stays in the WES conductor
  (`wes-work-planning`) and the task lifecycle (`fulfillment-execution`). The
  pull-with-lease shape is copied from `fulfillment-execution`, not invented here.
- **Where it could move.** If the platform competed on yard throughput itself
  (trailer prioritisation by shortage, nearest-to-door spot selection, engineered
  move standards, autonomous yard trucks) the occupancy-and-moves slice would move
  toward Core. All of those are out of scope today; `DirectOnCheckIn` picks the
  first free spot by location code.

## Parts

| Part | Evolution | Why |
| --- | --- | --- |
| Occupancy and moves (one place per trailer, claim-next with a lease) | Custom-built | the model's own invariant across `Trailer` and `YardMove`, enforced by a domain service under per-location advisory locks and partial unique indexes |
| Gate, visits and seal check | Custom-built | site-policy driven; a kiosk or ANPR front-end is an adapter around `CheckInVisit` |
| Yard checks and yard policy | Custom-built, towards Product | audit and per-site settings are common yard-system functions |
| Analytics read side (projector, reports) | Custom-built | the yard KPI standard v1 ([ADR 0011](/docs/adr/0011-kpi-standard-reports)) |
| Plumbing (outbox, inbox, CloudEvents) | Commodity | the same building blocks as every fleet context |

## Neighbours

Copied from `warehouse-docs` on `origin/main`: the yard section,
[Subdomain classification](https://iqvo.github.io/warehouse-docs/yard/strategic-design/subdomain-classification), and
the fleet table, [Subdomain classification](https://iqvo.github.io/warehouse-docs/strategic-design/subdomain-classification).

| Neighbour | Class | Area | Relation to this context |
| --- | --- | --- | --- |
| `dock-scheduling` | Supporting (differentiation 0.4, complexity 0.45, Product) | WMS (yard) | the other yard context; owns the future of a door, consumes this context's arrival, docking and release facts, feeds it appointments and closures |
| `facility-layout` | Generic | WMS | upstream registry of Yard and Dock locations and zones |
| `inbound-receiving` | Supporting | WMS | upstream: ASNs and receipts |
| `exception-management` | Supporting | WES | downstream: opens cases from yard events |
| `outbound-shipping` | Supporting (a deferred context in the yard section) | WMS | downstream: confirms shipment when the trailer leaves; built on develop in its own repository |

Deferred or deliberately not contexts, per the same page: `transport-visibility`
(Generic, ACL for ETAs), `yard-billing` (Generic, detention charges: here the free
time is only a site setting), gate camera / ANPR / kiosk (an inbound adapter),
reefer telemetry (a measurement value object on `Trailer`, phase 3), a network
view across sites (a read model), autonomous yard trucks (a reserved
device-control seam).

Classification is per context; the slices above can differ, and the analytics
read side behaves like a Generic reporting add-on inside a Supporting context.
