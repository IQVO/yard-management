# CLAUDE.md — yard-management

`yard-management` is a Supporting bounded context of the yard domain (`wms`
tier, CloudEvents subdomain `wms`, ADR 0001 and ADR 0004) in the
`warehouse-systems` fleet (GitHub org `IQVO`, Go module
`github.com/claudioed/yard-management`). It owns everything physically on site
outside the four walls: who passed the gate (visits), which trailer is where
(spot, door or an open move), its load, seal and hold state, the moves hostlers
and visiting drivers make, door work at the door, and yard checks. It is the
source of dwell, door occupancy and yard-truck productivity facts.
Dock-scheduling owns door TIME (appointments); this context owns the physical
present. Aggregates `YardVisit`, `Trailer`, `YardMove`, `YardCheck`,
`YardPolicy`. Five binaries in one image: `cmd/api` (REST `:8080` and the three
Kafka consumers), `cmd/mcp` (MCP `:8090`), `cmd/relay` (transactional outbox to
Kafka, health on `:8081`), `cmd/yard-projector` (`:8091`, writer of the
analytical database) and `cmd/yard-reports` (`:8092`, read-only `/reports/*`).
Design source: warehouse-docs `yard/model/model.yaml`.

## Hard rules (each one is in an ADR or fitness-tested)

1. **Hexagonal dependency rule.** `internal/domain` depends on nothing internal
   but domain; `internal/application` only on domain and application; inbound
   and outbound adapters never depend on each other; only `cmd/` wires layers.
   `make arch-test` (`TestHexagonalArchitecture`) enforces it.
2. **CloudEvents 1.0 is MANDATORY** on every Kafka message produced or
   consumed (structured mode, no flat envelope, no envelope toggle). Build and
   decode ONLY through `internal/adapters/kafka/cloudevents/`; `type` is
   `com.warehouse.wms.yard-management.<entity>.<EventName>` (`<entity>` one of
   `yardvisit`, `trailer`, `yardmove`, `yardcheck`). A breaking payload change
   is a new `.v2` type. Eleven integration types on
   `warehouse.yard-management.events`, mirrored with the same `id` on
   `warehouse.yard-management.analytics`; ten more are analytics-only (ADR
   0004, ADR 0010, ADR 0013). Payloads: `apis/asyncapi.yaml`
   (`TestCloudEventsOnly`, `TestEventCatalogueMatchesContract`).
3. **Events leave through the transactional outbox only** (ADR 0005): the
   aggregate save and its outbox row commit in one unit of work; `cmd/relay`
   publishes, with an attempt cap (`TestOutboxRelaysPassAttemptCap`). Never
   write to Kafka from a handler or use case.
4. **No live lookup into a sibling context** (ADR 0003). Appointments,
   locations, closures, ASNs and receipts are event-fed local copies from
   `warehouse.dock-scheduling.events`, `warehouse.facility.events` and
   `warehouse.inbound-receiving.events`. There is no HTTP client to another
   service.
5. **A trailer is at exactly one place**, a spot or door holds at most one
   trailer, a trailer has at most one open move. Occupancy is checked under a
   per-location advisory lock and backed by partial unique indexes
   (`migrations/`); keep both.
6. **Moves are pulled, never pushed**: hostlers claim the next move with a
   lease (`POST /moves/claims`); expired leases are swept.
7. **Consumers** (ADR 0009): one per producer topic, group ids only from
   `DOCK_SCHEDULING_CONSUMER_GROUP`, `FACILITY_CONSUMER_GROUP`,
   `INBOUND_RECEIVING_CONSUMER_GROUP` (blank = boot error;
   `TestKafkaConsumerGroupNeverHardcodedInline`); replays are version-guarded;
   poison goes to the dead-letter topic with a cancellation guard
   (`TestDeadLetterCallsGuardCancellation`).
8. **Writes are keyed**: every POST and PUT requires `Idempotency-Key` (the
   sweeps take it optionally). `version` is the weak ETag;
   `PUT /sites/{siteId}/yard-policy` honours `If-Match`.
9. **Real Postgres and Kafka only.** `DATABASE_URL` and `KAFKA_BROKERS` are
   mandatory; `internal/adapters/outbound/memory` is a test double no binary
   imports (`TestRuntimeBinaryHasNoInMemoryAdapters`).
10. **No auth layer on REST or MCP** (fleet rule): never add bearer/JWT/API-key
    middleware (`TestNoAuthMiddlewareReintroduced`).
11. **MCP authority** (ADR 0007): eight read tools at L1, eight write tools at L3;
    MCP writes stamp `initiated_by` into the event data, REST writes carry
    none (`TestEveryMCPToolDeclaresAuthority`, `TestMCPWriteToolsAreAtLeastL3`).
12. **An ADR comes first** for a new cross-context contract, a new consumed or
    published type, a type moving between streams, or any auth. ADRs live in
    `docs/adr/`.

## Commands

```bash
make check-fast   # fmt-check + vet + arch-test + tests of changed packages: run before saying "done"
make check        # fmt-check vet build lint test (lefthook pre-push)
make check-all    # check + coverage (90% gate on domain + application) + arch-test + bdd
make integration  # real Postgres/Kafka via testcontainers (needs Docker)
make mutation     # gremlins on every domain package (CI job mutation-fast, blocking)
make guide-lint   # these guides: skills load, references resolve, context budget
make harness-test # unit tests of the agent hooks and red-issue loop
```

Run locally: `DATABASE_URL=... KAFKA_BROKERS=... <the three *_CONSUMER_GROUP
vars> go run ./cmd/api`; `cmd/api` also serves `/reports/*` when
`ANALYTICS_READER_DATABASE_URL` is set. The projector needs
`ANALYTICS_DATABASE_URL` (ADR 0006). Every variable per binary:
`docs/docs/operations/configuration.md`.

## Where things are written down (read before editing the matching area)

- `.claude/rules/domain-model.md`: language, aggregates, invariants, use
  cases. `.claude/rules/rest-api.md`: routes and conventions.
  `.claude/rules/integration-events.md`: published and consumed events.
- `.claude/rules/fleet/*.md`: fleet-wide rules copied from the harness
  template; never edit them here.
- `docs/adr/`: decisions (index `docs/adr/index.md`; KPI reports in ADR 0011).
  Contracts: `apis/openapi.yaml`, `apis/asyncapi.yaml`. Operating docs:
  `docs/docs/operations/` (configuration, runbook, observability,
  troubleshooting), `docs/docs/development/testing.md`,
  `docs/docs/overview/quickstart.md`. Helm chart: `charts/yard-management`.
- `HARNESS.md`: what each sensor runs. `INCIDENTS.md`: fleet incident log.
  `.claude/memory/MEMORY.md`: repo heuristics.

harness-template: v4
(hooks, guide-lint).

<!-- harness:scoped-rules:start (generated by tools/migrate_v3.py in warehouse-harness-template; do not hand-edit) -->
## Scoped rules and harness

Claude Code loads each rule below automatically when you touch the matching paths. OpenCode and Codex do NOT: read the rule BEFORE editing matching files.

| When touching | Read |
|---|---|
| `internal/domain/**`, `internal/application/**`, `features/**` | `.claude/rules/domain-model.md` |
| `internal/adapters/**/kafka/**`, `internal/adapters/outbound/events/**`, `apis/asyncapi*` | `.claude/rules/integration-events.md` |
| `internal/adapters/inbound/http/**`, `apis/openapi*.yaml`, `apis/openapi/**` | `.claude/rules/rest-api.md` |

Hooks (`scripts/harness/hook.py`, wired for Claude Code, Codex and OpenCode) block pushes to develop/main, `--no-verify`, bare `rm -rf`, and edits to generated files, and feed gofmt/vet findings back after each edit. Before saying "done" run `make check-fast`; the full gate is `make check-all`. `HARNESS_OFF=1` disables the hooks when debugging the harness itself.
<!-- harness:scoped-rules:end -->
