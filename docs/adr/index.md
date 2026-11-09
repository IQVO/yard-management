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
| [0004](./0004-cloudevents-envelope-and-type-catalogue.md) | CloudEvents envelope and type catalogue | Accepted (2026-11-05) |
| [0005](./0005-postgres-persistence-and-outbox.md) | Postgres persistence and the transactional outbox | Accepted (2026-11-06) |
| [0006](./0006-analytics-read-side.md) | Analytics read side: analytics stream, projector and yard reports over a separate analytical schema | Accepted (2026-11-06); refined by 0011 |
| [0007](./0007-mcp-server.md) | MCP server | Accepted (2026-11-06) |
| [0008](./0008-rest-adapter-and-api-process.md) | The REST adapter and the API process | Accepted (2026-11-06) |
| [0009](./0009-kafka-consumers.md) | Kafka consumers | Accepted (2026-11-06); refines 0003 |
| [0010](./0010-door-work-and-departure-on-the-integration-topic.md) | DoorWorkCompleted and TrailerDeparted on the integration topic | Accepted (2026-11-09); refines 0004 |
| [0011](./0011-kpi-standard-reports.md) | Yard KPI standard v1: turn time, detention exposure and exception rates | Accepted (2026-11-09); refines 0006 |
