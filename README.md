# yard-management

Yard Management (WMS-tier Supporting bounded context of the yard domain): Owns everything physically on site outside the four walls: who passed the gate (visits), which trailer is where (spot, door or an open move), its load, seal and hold state, the moves hostlers and visiting drivers make, door work at the door, and yard checks. The source of dwell, door occupancy and yard truck productivity facts.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

Status: built on develop, not deployed. The design lives in the yard instance of
[warehouse-docs](https://iqvo.github.io/warehouse-docs/yard/contexts/yard-management/); `yard/model/model.yaml` there is the single source of truth.

Runtime: Go, hexagonal, real PostgreSQL and Kafka only (`DATABASE_URL`, `KAFKA_BROKERS` are mandatory), CloudEvents 1.0 on every message
(`com.warehouse.wms.yard-management.<entity>.<Event>`), transactional outbox.

## Documentation

The documentation site of this service is published at <https://iqvo.github.io/yard-management/>
(built from `docs/` by `.github/workflows/docs.yml`; the decisions are in `docs/adr/`). The yard
domain's design model stays in
[warehouse-docs](https://iqvo.github.io/warehouse-docs/yard/contexts/yard-management/). Status: built on
develop, not deployed.

| Section | Pages |
| --- | --- |
| Overview | [Introduction](https://iqvo.github.io/yard-management/docs/overview/introduction), [Architecture](https://iqvo.github.io/yard-management/docs/overview/architecture), [Quickstart](https://iqvo.github.io/yard-management/docs/overview/quickstart) |
| Operations | [Configuration](https://iqvo.github.io/yard-management/docs/operations/configuration), [Runbook](https://iqvo.github.io/yard-management/docs/operations/runbook), [Observability](https://iqvo.github.io/yard-management/docs/operations/observability), [Troubleshooting](https://iqvo.github.io/yard-management/docs/operations/troubleshooting), [Before the first release](https://iqvo.github.io/yard-management/docs/operations/before-first-release) |
| Development | [Testing](https://iqvo.github.io/yard-management/docs/development/testing) |
| Domain-driven design | [Ubiquitous language](https://iqvo.github.io/yard-management/docs/ddd/ubiquitous-language), [Use cases](https://iqvo.github.io/yard-management/docs/ddd/use-cases), [Subdomain classification](https://iqvo.github.io/yard-management/docs/ddd/subdomain-classification), [DDD artifacts](https://iqvo.github.io/yard-management/docs/ddd/ddd-artifacts): [core domain chart](https://iqvo.github.io/yard-management/docs/ddd/core-domain-chart), [bounded context canvas](https://iqvo.github.io/yard-management/docs/ddd/bounded-context-canvas), [context map](https://iqvo.github.io/yard-management/docs/ddd/context-map), [aggregate design canvas](https://iqvo.github.io/yard-management/docs/ddd/aggregate-design-canvas), [domain message flow](https://iqvo.github.io/yard-management/docs/ddd/domain-message-flow), [EventStorming](https://iqvo.github.io/yard-management/docs/ddd/eventstorming), [class diagram](https://iqvo.github.io/yard-management/docs/ddd/class-diagram), [entity relationship](https://iqvo.github.io/yard-management/docs/ddd/entity-relationship), [sequence diagrams](https://iqvo.github.io/yard-management/docs/ddd/sequence-diagrams), [domain events](https://iqvo.github.io/yard-management/docs/ddd/domain-events) |
| API reference | [API overview](https://iqvo.github.io/yard-management/docs/api-reference), [REST reference](https://iqvo.github.io/yard-management/docs/api-reference/rest/yard-management-api), [Event catalogue](https://iqvo.github.io/yard-management/docs/api-reference/events), [MCP tools](https://iqvo.github.io/yard-management/docs/mcp/tools) |
| Ecosystem | [Integration](https://iqvo.github.io/yard-management/docs/ecosystem/integration) |
| ADRs | [ADR index](https://iqvo.github.io/yard-management/docs/adr) |

See `docs/adr/` for decisions.
