# ADR 0007: MCP server

## Status

Accepted (2026-11-06). Adds a second inbound (driving) adapter over the use cases of ADR 0002 and ADR 0008; the REST adapter and the domain
are unchanged. Code: `internal/adapters/inbound/mcp` and `cmd/mcp`. Implements the fleet's Agentic Standard v2 (authority levels, stateless
transport, complete surface, attributable writes). Shape reference: dock-scheduling ADR 0007 and inbound-receiving ADR 0005.

## Context

The ops agent needs to read the yard and propose changes that a supervisor approves. The yard is physically operated through the floor client
(gate clerk, hostler, dock operator); an agent must not take those actions. What it can usefully do is read the live picture and request or
undo the supervisor-level changes: reserve a destination for a trailer, cancel an open move, hold or release a trailer, record a seal, open
an audit.

## Decisions

- **A separate binary, the same use cases.** `cmd/mcp` is its own composition root: it runs the embedded migrations (like every sibling), opens
  the pgx pool and wires the SAME `usecases.Yard` the REST adapter uses. It serves Streamable HTTP only (no stdio, no SSE endpoint of its own)
  at `/mcp` and `/`, with `GET /healthz` (liveness, never flipped by shutdown) and `GET /readyz` (drain gate, Postgres ping, Kafka broker
  metadata: the gate `cmd/api` uses). The adapter imports the application layer and the domain only (`TestMCPAdapterDependencyRule`). No
  in-memory adapter in `cmd/**` (`TestRuntimeBinaryHasNoInMemoryAdapters`); the unit tests of `cmd/mcp` use the memory store as a test double
  only.
- **Stateless.** `StreamableHTTPOptions{Stateless: true}`: no `Mcp-Session-Id`, the 2026-07-28 per-request handshake is answered, legacy
  `initialize` clients keep working. Nothing relies on session state; a write is made safe to retry by its idempotency key.
- **Fourteen tools: eight reads at L1, six writes at L3** (sixteen tools, eight writes, since ADR 0013). Arguments are camelCase like the REST bodies; times are RFC 3339 strings; a bad
  value is an `isError` result, not a protocol error.

  | Tool | Level | Use case | REST twin |
  |---|---|---|---|
  | `get_yard_board` | L1 | `GetYardBoard` | `GET /sites/{siteId}/yard-board` |
  | `list_trailers` | L1 | `ListTrailers` | `GET /trailers` |
  | `get_trailer` | L1 | `GetTrailer` | `GET /trailers/{trailerId}` |
  | `list_moves` | L1 | `ListMoves` | `GET /moves` |
  | `list_visits` | L1 | `ListVisits` | `GET /visits` |
  | `get_visit` | L1 | `GetVisit` | `GET /visits/{visitId}` |
  | `get_yard_policy` | L1 | `GetYardPolicy` | `GET /sites/{siteId}/yard-policy` |
  | `get_yard_dwell_report` | L1 | `Reports.YardDwell` | `GET /reports/yard-dwell` |
  | `request_move` | L3 | `RequestMove` | `POST /moves` |
  | `cancel_move` | L3 | `CancelMove` | `POST /moves/{moveId}/cancellations` |
  | `place_hold` | L3 | `PlaceHold` | `POST /trailers/{trailerId}/holds` |
  | `release_hold` | L3 | `ReleaseHold` | `POST /trailers/{trailerId}/hold-releases` |
  | `record_seal` | L3 | `RecordSeal` | `POST /trailers/{trailerId}/seals` |
  | `open_yard_check` | L3 | `OpenYardCheck` | `POST /yard-checks` |

  Out of the surface on purpose: gate check-in and check-out, visit rejection, claiming and completing moves, door work, observations and the
  closing of a yard check, policy changes and the sweeps. They are actions of the people on the yard (floor client) or cron jobs, and the
  ops agent never stands in for them.
- **Writes are L3, not L2.** The fleet rule and the template's `TestMCPWriteToolsAreAtLeastL3` reserve L1/L2 for tools that are provably
  read-only or only compute a recommendation: a tool that changes state is L3 unless an ADR grants L4. No tool here computes a recommendation
  (so there is no L2) and nothing grants L4. L3 is the gating the CALLER applies: a human approval sits between the proposal and the call, held
  by the client, never by this unauthenticated context. The domain invariants (occupancy, reservation, door flow and hours, holds, the
  move/trailer state machines) guard every write exactly as over REST, so a mistaken call is refused, not applied. Every tool declares its
  level in `Tool.Meta["com.warehouse/authority"]` (visible in `tools/list` `_meta`); `ReadOnlyHint` is true exactly for the L1 tools; the
  registry golden `testdata/tool_registry.golden{,.json}` pins both.
- **Idempotency matches REST.** Every write takes `idempotencyKey` (REST's `Idempotency-Key`: 1..128 printable ASCII, required). The lookup,
  the work and the stored answer run in ONE unit of work, so the aggregate, its outbox rows and the key commit or roll back together. A
  success is stored; a refusal from the error catalogue is stored on its own (replayed verbatim for that key even after the world changed,
  like REST's stored 4xx); an untyped error (infrastructure) is rolled back and never stored, so the same key can retry the real work. The
  same key with another tool or other business arguments is `idempotency-key-reused`; the fingerprint is the tool name plus the canonical JSON
  of the command (not the key, not the attribution labels). Racing calls with one key are serialised by an advisory lock: the loser replays
  the winner's answer. A replay carries `_meta["com.warehouse/idempotency-replayed"] = true`. Keys live in the shared `idempotency_keys` table.
- **Attribution (`initiated_by`).** The optional arguments `proposalId`, `approvedBy` and `agentId` (each at most 128 characters, trimmed, no
  NUL) build an `Initiator` whose `via` is ALWAYS set by the adapter (`mcp`, never caller-supplied). It rides the call's context and
  `AttributingPublisher`, a decorator of the transactional outbox in `cmd/mcp`, stamps `initiated_by` `{via, proposal_id, approved_by,
  agent_id}` (the last three only when given) into the DATA of every event the write raises, inside the same unit of work. REST writes and
  the sweeps carry none. It is a data field, never a CloudEvents extension attribute, and it grants nothing. The domain events are untouched.
- **Errors.** A tool error is `"<slug>: <detail>"` with `isError: true`. The slugs are the REST problem slugs (`trailer-not-found`,
  `move-not-found`, `trailer-has-open-move`, `destination-reserved`, `location-occupied`, `door-closed`, `door-flow-mismatch`,
  `trailer-on-hold`, `illegal-transition`, `yard-check-open`, ...); the adapter adds `invalid-limit`, `invalid-cursor`, `invalid-state`,
  `invalid-report-range` and the `idempotency-key-*` slugs. An untyped error is logged and answered with no detail (no DSN, no SQL).
- **Resources and prompt.** Two read-model resource templates (`warehouse://yard/sites/{siteId}/board`, `warehouse://yard/trailers/{trailerId}`;
  model-supplied segments are validated, never defaulted) and one operator SOP prompt (`review_yard`); a test checks every tool the prompt
  names exists.
- **No auth** (fleet-wide revert 2026-09-11; `TestNoAuthMiddlewareReintroduced`). The Service is ClusterIP only.

## Configuration of `cmd/mcp`

| Variable | Default | |
|---|---|---|
| `DATABASE_URL` | | required; boot error naming it (there is no in-memory mode) |
| `KAFKA_BROKERS` | | required; boot error naming it. The binary starts no relay and no consumer: its writes enqueue outbox rows that `cmd/relay` drains, and `/readyz` checks the broker like the API's, so a deployment without Kafka refuses to boot instead of accepting writes whose events never leave |
| `MCP_ADDR` | `:8090` | listen address |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | direct DSN for the boot migrations |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | wait after `/readyz` flips to 503 before the server drains |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |

Every missing required variable is named at once. Shutdown on SIGINT/SIGTERM: `/readyz` flips, the drain delay passes, the HTTP server drains
in-flight requests (10 s bound), then the pool closes.

## Tests

- Per tool (`tools_read_test.go`, `tools_write_test.go`) over the in-memory test doubles, through a real SDK client and server pair; the
  registry golden and the surface test; idempotency, attribution and stateless tests; the template's `mcp_authority_fitness_test.go` and
  `mcp_optional_inputs_test.go` pass unchanged.
- `cmd/mcp/main_integration_test.go` (tag `integration`, testcontainers Postgres + Kafka) boots the real `run()` and drives it only through an
  MCP client over HTTP: the surface and its authority tags, a stateless handshake, reads over rows written by the REST use cases, a write with
  replay, key reuse, refusals with REST slugs, attribution, a two-moves-one-door race, eight concurrent calls with one key, the attributed
  events read back from the real analytics topic through the outbox relay, `/readyz` turning 503 when Kafka stops, and a graceful stop.

## Follow-up

- `apis/asyncapi.yaml` does not yet declare the optional `initiated_by` object on the event schemas the six writes can raise (`YardMoveRequested`,
  `YardMoveCancelled`, `TrailerHeld`, `TrailerHoldReleased`, `SealExceptionRaised` and the yard-check opening event); the data is on the wire and consumers must ignore unknown fields. Declaring it
  belongs to the contract owner's change (a docs/spec file outside this adapter).
- `get_yard_dwell_report` answers no rows until the analytics read side (ADR 0006) is wired into `cmd/mcp`, exactly like the REST report.
- The chart needs `MCP_ADDR`, `DATABASE_URL`, `KAFKA_BROKERS` and, for the ops agent, a `*_MCP_ENDPOINT` value pointing at `http://<svc>:8090/mcp` (the variable name is the ops-agent owner's to pick; boot aborts without it once the upstream is declared).

## Assumptions

- **M1** Tool arguments are camelCase (the REST bodies' casing); event data stays snake_case, including the keys inside `initiated_by`.
- **M2** The list tools accept `limit` 1..500 (0 or absent = 100); a larger or negative value is `invalid-limit`, not silently clamped.
- **M3** A stored client-error answer is replayed verbatim for its key (REST does the same); the agent uses a new key for a deliberate retry
  after fixing the cause.
- **M4** The tool budget is fourteen; raising it is an amendment of this ADR. ADR 0013 amends it to sixteen (the writes `secure_trailer` and `release_securing`).
