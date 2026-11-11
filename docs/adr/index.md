---
title: Architecture decision records
sidebar_label: Index
sidebar_position: 0
slug: /
---

# Architecture decision records

Each status below is copied from the ADR body's own `## Status` line, which is
the authority.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](./0001-yard-management-bounded-context.md) | Yard Management is a bounded context of the yard domain | Accepted (bootstrap) |
| [0002](./0002-aggregates-invariants-and-assumptions.md) | Aggregates, invariants and assumptions | Accepted (2026-11-05) |
| [0003](./0003-consumed-contracts-and-local-copies.md) | Consumed contracts and local copies | Accepted (2026-11-05); failure handling refined by 0009 |
| [0004](./0004-cloudevents-envelope-and-type-catalogue.md) | CloudEvents envelope and type catalogue | Accepted (2026-11-05); stream split refined by 0010, 0013 and 0015 |
| [0005](./0005-postgres-persistence-and-outbox.md) | Postgres persistence and the transactional outbox | Accepted (2026-11-06) |
| [0006](./0006-analytics-read-side.md) | Analytics read side: analytics stream, projector and yard reports over a separate analytical schema | Accepted (2026-11-06); refined by 0011 |
| [0007](./0007-mcp-server.md) | MCP server | Accepted (2026-11-06) |
| [0008](./0008-rest-adapter-and-api-process.md) | The REST adapter and the API process | Accepted (2026-11-06) |
| [0009](./0009-kafka-consumers.md) | Kafka consumers | Accepted (2026-11-06); refines 0003 |
| [0010](./0010-door-work-and-departure-on-the-integration-topic.md) | DoorWorkCompleted and TrailerDeparted on the integration topic | Accepted (2026-11-09); refines 0004 and 0005 |
| [0011](./0011-kpi-standard-reports.md) | Yard KPI standard v1: turn time, detention exposure and exception rates | Accepted (2026-11-09); refines 0006 |
| [0012](./0012-standard-identifiers-at-the-gate.md) | Standard identifiers at the gate: ISO 6346 unit numbers, ISO 17712 seal class and a seal state vocabulary | Accepted (2026-11-10); refines 0002 and 0004 |
| [0013](./0013-dock-safety-interlock.md) | Dock safety interlock: a trailer is secured before door work starts and released before it leaves its door | Accepted (2026-11-10); refines 0002, 0004 and 0007 |
| [0014](./0014-hazmat-and-reefer-placement.md) | Hazmat and reefer placement: a trailer is parked on a spot whose kind its contents allow | Accepted (2026-11-10); refines 0002, 0009 and 0012 |
| [0015](./0015-compliance-inspections.md) | Compliance inspections: versioned checklist templates, an Inspection record and a site-policy gate on dock, load and departure | Accepted (2026-11-11); refines 0002, 0004 and 0007 |
