---
id: ddd-artifacts
title: DDD artifacts
sidebar_label: DDD artifacts
sidebar_position: 3
description: "The ddd-crew artifacts of yard-management: core domain chart, bounded context canvas, context map, aggregate design canvas, domain message flow, EventStorming, class diagram, entity relationship, sequence diagrams and domain events."
---

# DDD artifacts

The strategic and tactical design of the context in the shape of the
[ddd-crew](https://github.com/ddd-crew) tools, plus the UML and ER views of the
built service. Every diagram names the code file it reflects in a `Source:` line.
Where code and an earlier design decision disagree, the page documents the code.

The yard domain's design model lives in
[warehouse-docs](https://iqvo.github.io/warehouse-docs/yard/contexts/yard-management/);
these pages are the own-site, as-built version for this repository.

| Page | What it holds | Tool |
| --- | --- | --- |
| [Ubiquitous language](/docs/ddd/ubiquitous-language) | Yard, Yard Visit, Trailer, Spot, Dock Door, Hostler, Yard Move, Door Work, Dwell and the neighbour terms | glossary |
| [Use cases](/docs/ddd/use-cases) | one section per command: trigger, adapter, flow, errors, events, idempotency | application layer |
| [Subdomain classification](/docs/ddd/subdomain-classification) | why Supporting, neighbours' classes | Core Domain Chart companion |
| [Core domain chart](/docs/ddd/core-domain-chart) | position on the chart and the evolution of each part | [core-domain-charts](https://github.com/ddd-crew/core-domain-charts) |
| [Bounded context canvas](/docs/ddd/bounded-context-canvas) | purpose, classification, roles, every inbound and outbound message | [bounded-context-canvas](https://github.com/ddd-crew/bounded-context-canvas) v5 |
| [Context map](/docs/ddd/context-map) | every neighbour with its pattern and channel | [context-mapping](https://github.com/ddd-crew/context-mapping) |
| [Aggregate design canvas](/docs/ddd/aggregate-design-canvas) | `YardVisit`, `Trailer`, `YardMove`, `YardCheck`, `YardPolicy`: states, invariants, commands, events | [aggregate-design-canvas](https://github.com/ddd-crew/aggregate-design-canvas) v1.1 |
| [Domain message flow](/docs/ddd/domain-message-flow) | live unload, drop and later door call, gate rejection, hostler move with lease expiry | [domain-message-flow-modelling](https://github.com/ddd-crew/domain-message-flow-modelling) |
| [EventStorming](/docs/ddd/eventstorming) | process-level boards with hotspots | [eventstorming-glossary-cheat-sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet) |
| [Class diagram](/docs/ddd/class-diagram) | domain types and the hexagonal ports and adapters | UML |
| [Entity relationship](/docs/ddd/entity-relationship) | the Postgres schema (OLTP and analytics) | ER |
| [Sequence diagrams](/docs/ddd/sequence-diagrams) | runtime sequences per significant command | UML |
| [Domain events](/docs/ddd/domain-events) | the 23 published and 16 consumed events with CloudEvents types, topics and payloads | event catalogue |

Related: [API overview](/docs/api-reference), [Event catalogue](/docs/api-reference/events),
[Architecture](/docs/overview/architecture).
