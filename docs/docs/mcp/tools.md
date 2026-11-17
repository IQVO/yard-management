---
id: tools
title: MCP tools
sidebar_label: MCP tools
sidebar_position: 2
description: "The yard-management MCP server: eight read tools, eight write tools, resources and the review_yard prompt, with inputs and annotations."
---

# MCP tools

`cmd/mcp` serves the Model Context Protocol over **Streamable HTTP only**
(stateless; no stdio, no SSE-only transport) at `/` and `/mcp` on `MCP_ADDR`
(default `:8090`), plus `/healthz` and `/readyz`
([ADR 0007](/docs/adr/0007-mcp-server)). Server name `yard-management-mcp`,
version `1.0.0`. **There is no authentication**: access control is the
in-cluster boundary. Behind the platform edge the chart exposes it at
`/api/mcp/yard-management` (when `mcp.route.enabled`).

Tool names, descriptions, inputs and annotations below are read from
`internal/adapters/inbound/mcp/tools_read.go`, `tools_write.go` and
`inputs.go`; they were **not captured from a live `tools/list`** because the
server was not run while writing this page. `TestEveryMCPToolDeclaresAuthority`
and `TestMCPWriteToolsAreAtLeastL3` keep the annotations and authority levels
in the code consistent.

The surface (the budget is twenty-one tools since ADR 0015): **ten read tools (L1)** and **eleven write tools (L3)**. Gate
check-in and check-out, hostler claims and completions and door work are
floor-client actions on REST and are deliberately **not** tools.

Every tool declares `com.warehouse/authority` in its `_meta` (`L1` or `L3`) and
`openWorldHint: false`. Argument names are camelCase like the REST contract.

## Read tools (L1)

All ten carry `readOnlyHint: true`, `idempotentHint: true`.

| Tool | Inputs | Answers |
| --- | --- | --- |
| `get_yard_board` | `siteId` (required) | the live map of a site: every active spot and door (code, kind, dockFlow, closed, trailer at it), trailers in motion, open moves. `site-not-found` for an unknown site |
| `list_trailers` | `limit`, `cursor`, `siteId`, `state` (`PARKED`, `IN_MOVE`, `DOCKED`, `DEPARTED`; omitted = not departed), `kind` (`DRY_VAN`, `REEFER`, `FLATBED`, `CONTAINER`), `locationCode`, `unitNumber`, `visitId` (all optional) | trailers by arrival, a page at a time |
| `get_trailer` | `trailerId` (required) | one trailer: location, load state, door work, seal, hold, ASNs, visit, appointment, arrival time, open move, version. `trailer-not-found` |
| `list_moves` | `limit`, `cursor`, `siteId`, `state` (`REQUESTED`, `CLAIMED`, `COMPLETED`, `CANCELLED`), `trailerId`, `hostlerRef` (optional) | moves, oldest request first |
| `list_visits` | `limit`, `cursor`, `siteId`, `state` (`CHECKED_IN`, `CHECKED_OUT`, `REJECTED`), `tractorPlate` (optional) | gate visits, newest arrival first |
| `get_visit` | `visitId` (required) | one visit with carrier, plate, purpose, appointment, state, rejection reason, trailers in and out. `visit-not-found` |
| `get_yard_policy` | `siteId` (required) | the site's policy (appointment required, holding spots, parking zones, dwell thresholds per kind, lease seconds, seal mismatch action, free time); the defaults with `declared: false` when none was declared |
| `get_yard_dwell_report` | `from`, `to` (required, RFC 3339, `to` after `from`, at most 92 days), `siteId` (optional) | per trailer kind: trailers, average and maximum dwell minutes, average driver wait; `lastEventAt`. **Rows are empty**: `cmd/mcp` does not wire the analytics reader (`mcpDeps` in `cmd/mcp/wire.go`). `invalid-report-range` for a bad window |
| `get_inspection` | `inspectionId` (required) | one compliance inspection: gate, status, the template snapshot with each item's result, note and evidence references, condition grade, version. `inspection-not-found` |
| `list_trailer_inspections` | `trailerId` (required), `limit`, `cursor` | the inspections of a trailer, newest first. `trailer-not-found` |

List tools page with `limit` (1 to 500, default 100) and the opaque
`nextCursor` returned by the previous page.

## Write tools (L3)

Each carries `readOnlyHint: false`, `idempotentHint: true`; `destructiveHint`
is `true` only for `cancel_move`. Every write takes the shared arguments:

| Argument | Required | Meaning |
| --- | --- | --- |
| `idempotencyKey` | **yes** | 1 to 128 printable ASCII characters; same key + same arguments returns the first answer (result `_meta` `com.warehouse/idempotency-replayed: true`); same key with other arguments is refused with `idempotency-key-reused`. Shared store with the REST `Idempotency-Key` |
| `proposalId`, `approvedBy`, `agentId` | no (max 128 characters each) | attribution: stamped into `initiated_by` on the data of every event the call raises |

A write runs in one unit of work with the idempotency key and the outbox rows
it raises; the events leave through `cmd/relay`. Call a write only for a change
a human approved.

| Tool | Inputs (besides the shared ones) | Effect |
| --- | --- | --- |
| `request_move` | `trailerId`, `to` (`{kind: SPOT\|DOOR, code}`) required; `reason` (`TO_DOOR`, `FROM_DOOR`, `RELOCATE`, `TO_HOLDING`), `priorityBand` (1 most urgent to 9), `dueBy` (RFC 3339) optional | queues a hostler move; the destination is validated and **reserved** now. Omitted `reason` derives from the destination |
| `cancel_move` | `moveId` required; `reason` optional | cancels a `REQUESTED` or `CLAIMED` move; cancelling a claimed hostler move returns the trailer where it was |
| `place_hold` | `trailerId`, `reason` (`DAMAGED`, `SEAL_BROKEN`, `NOT_FOUND`, `QUALITY`, `SECURITY`) required | a held trailer is not docked or moved to a door |
| `release_hold` | `trailerId` required | releases the hold |
| `record_seal` | `trailerId`, `state` (`INTACT`, `APPLIED`, `BROKEN`) required; `sealNumber` (required when `INTACT` or `APPLIED`); `sealClass` (`I`, `S`, `H`, optional) | `BROKEN` also places a `SEAL_BROKEN` hold; a class below the site's `minimumSealClass` is `seal-class-too-low`, an unknown one `invalid-seal-class` (ADR 0012) |
| `secure_trailer` | `trailerId`, `method` (`RESTRAINT`, `WHEEL_CHOCKS`) required; `securerRef` optional | records that a `DOCKED` trailer is secured (the dock safety interlock, ADR 0013); the same method again changes nothing; `trailer-has-open-move`, `illegal-transition` or `validation` otherwise |
| `release_securing` | `trailerId` required | records that the restraint was released or the chocks removed; required before the trailer leaves its door; `trailer-not-secured` when it is not secured, `door-work-open` while door work is in progress |
| `open_inspection` | `trailerId`, `templateId`, `gate` (`PRE_DOCK`, `PRE_LOAD`, `PRE_DEPARTURE`, `ON_DEMAND`) required; `templateVersion`, `visitId`, `performerRef` optional | opens an inspection from the latest (or named) template version, every item pending (ADR 0015); `unknown-template`, `inspection-already-open` |
| `record_inspection_item` | `inspectionId`, `itemCode`, `result` (`PASS`, `FAIL`, `NOT_APPLICABLE`) required; `note`, `evidenceRefs` optional | records one item while the inspection is open; `unknown-item`, `not-applicable-not-allowed`, `inspection-closed` |
| `complete_inspection` | `inspectionId` required; `conditionGrade` (`CLEAN`, `DIRTY`, `ODOUR`, `DAMAGED`) optional | ends the inspection `PASSED` or `FAILED`; a failure holds the trailer in the same unit of work; `required-item-pending`, `inspection-closed` |
| `open_yard_check` | `siteId` required | opens an audit; at most one open check per site. The walk and the closing are floor actions |

Errors come back as tool results with `isError: true` and a slug such as
`trailer-not-found`, `destination-reserved`, `trailer-on-hold`,
`trailer-not-secured`, `trailer-still-secured`, `invalid-state`; an unexpected error is `internal-error` and is logged.

## Resources and prompt

| Kind | Name | URI / id | Content |
| --- | --- | --- | --- |
| Resource template | `yard-board` | `warehouse://yard/sites/{siteId}/board` | the live yard board of one site (JSON) |
| Resource template | `trailer` | `warehouse://yard/trailers/{trailerId}` | one trailer (JSON) |
| Prompt | `review_yard` | no arguments | the SOP: read the board, find trailers and moves that need attention, propose changes, act only on approval with an `idempotencyKey` and attribution, read back; escalate what only the floor can do |

The server's `initialize` instructions summarise the same split between the
read tools and the approval-gated writes.

## Related

- [API reference overview](/docs/api-reference) lists every interface.
- [ADR 0007](/docs/adr/0007-mcp-server) records the decision and the tool selection.
