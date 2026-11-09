---
id: overview
title: API overview
slug: /api-reference
sidebar_label: API overview
sidebar_position: 1
description: "Every interface of yard-management: REST, reports, events, MCP; endpoint table and contract conventions."
---

# API overview

All interfaces are **unauthenticated** (fleet decision 2026-09-11; the
fitness test `TestNoAuthMiddlewareReintroduced` fails CI if auth middleware
comes back under `internal/adapters/inbound`). Access control is the cluster
boundary.

## Interfaces

| Interface | Served by | Source of truth | Notes |
| --- | --- | --- | --- |
| REST (commands and queries) | `cmd/api`, `:8080` | `apis/openapi.yaml` (OpenAPI 3.0.3, version 1.0.0) | The pages under *REST* are generated from the spec (`npm run gen-api-docs`); do not edit them by hand. Through Kong: `/api/yard-management/...`. The spec's server list still names a "local kind cluster through Kong" (`http://localhost:8000/api/yard-management`); kind is retired. |
| REST (reports) | `cmd/yard-reports`, `:8092` (and optionally `cmd/api`) | the `Reports` tag of `apis/openapi.yaml`, [ADR 0006](/docs/adr/0006-analytics-read-side), [ADR 0011](/docs/adr/0011-kpi-standard-reports) | read-only, from the `analytics` schema; chart route `/api/yard-management/reports`. On `cmd/api` the same routes answer from the analytics database only when `ANALYTICS_READER_DATABASE_URL` is set, else with no rows |
| Events | Kafka, produced by `cmd/relay` | `apis/asyncapi.yaml` (AsyncAPI 3.0, version 1.0.0) | [Event catalogue](/docs/api-reference/events); every field on [Domain events](/docs/ddd/domain-events) |
| MCP | `cmd/mcp`, `:8090` | [ADR 0007](/docs/adr/0007-mcp-server), `internal/adapters/inbound/mcp` | Streamable HTTP at `/` and `/mcp` (through Kong `/api/mcp/yard-management`); eight read tools (L1) and six write tools (L3), a resource pair and the `review_yard` prompt. See [MCP tools](/docs/mcp/tools) |
| A2A | none | n/a | There is **no `cmd/a2a`** in this repository |
| Probes | every binary with HTTP | `/healthz`, `/readyz` | see [Runbook](/docs/operations/runbook#probes-and-what-readiness-waits-for) |

## Endpoints

`cmd/api` registers exactly these routes (`internal/adapters/inbound/http/server.go`,
`NewRouter`); there is no route in the code that the spec does not list. "Key" is
whether `Idempotency-Key` is required.

| Tag | Endpoint | Operation id | Key | Success |
| --- | --- | --- | :-: | --- |
| Visits | `POST /visits` | `checkInVisit` | yes | `201`, the check-in (visit, trailers, first moves), or a `REJECTED` visit under the `REJECT` seal policy |
| Visits | `GET /visits` | `listVisits` | | `200 {items, nextCursor?}`; filters `siteId`, `state`, `tractorPlate` |
| Visits | `GET /visits/{visitId}` | `getVisit` | | `200` |
| Visits | `POST /visits/{visitId}/check-outs` | `checkOutVisit` | yes | `200`, state `CHECKED_OUT`; body `trailersOut` |
| Visits | `POST /visit-rejections` | `rejectAtGate` | yes | `201`, a `REJECTED` visit |
| Trailers | `GET /trailers` | `listTrailers` | | `200`; filters `siteId`, `state`, `kind`, `locationCode`, `unitNumber`, `visitId` |
| Trailers | `GET /trailers/{trailerId}` | `getTrailer` | | `200` |
| Trailers | `POST /trailers/{trailerId}/door-work-starts` | `startDoorWork` | yes | `200` |
| Trailers | `POST /trailers/{trailerId}/door-work-completions` | `completeDoorWork` | yes | `200`; body `loadState` |
| Trailers | `POST /trailers/{trailerId}/holds` | `placeHold` | yes | `200`; body `reason` |
| Trailers | `POST /trailers/{trailerId}/hold-releases` | `releaseHold` | yes | `200` |
| Trailers | `POST /trailers/{trailerId}/seals` | `recordSeal` | yes | `200` |
| Moves | `POST /moves` | `requestMove` | yes | `201`, the move |
| Moves | `GET /moves` | `listMoves` | | `200`; filters `siteId`, `state`, `trailerId`, `hostlerRef` |
| Moves | `POST /moves/claims` | `claimNextMove` | yes | `201`, the claimed move; `404 no-claimable-move` when none waits |
| Moves | `POST /moves/{moveId}/completions` | `completeMove` | yes | `200` |
| Moves | `POST /moves/{moveId}/cancellations` | `cancelMove` | yes | `200` |
| Yard checks | `POST /yard-checks` | `openYardCheck` | yes | `201` |
| Yard checks | `POST /yard-checks/{yardCheckId}/observations` | `recordObservation` | yes | `200` |
| Yard checks | `POST /yard-checks/{yardCheckId}/closures` | `closeYardCheck` | yes | `200`, with the discrepancies |
| Yard board | `GET /sites/{siteId}/yard-board` | `getYardBoard` | | `200`, the live spot and door map |
| Yard policy | `GET /sites/{siteId}/yard-policy` | `getYardPolicy` | | `200` with `ETag`; the defaults (`declared: false`) when none was declared |
| Yard policy | `PUT /sites/{siteId}/yard-policy` | `declareYardPolicy` | yes | `200`, honours `If-Match` |
| Sweeps | `POST /dwell-sweeps` | `sweepDwell` | optional | `200`, count of `TrailerDwellExceeded` raised; body `{siteId?}` |
| Sweeps | `POST /move-lease-sweeps` | `sweepMoveLeases` | optional | `200`, count of leases returned to `REQUESTED` |
| Reports | `GET /reports/yard-dwell` | `getYardDwellReport` | | `200`; `from`, `to`, `siteId?` |
| Reports | `GET /reports/door-utilisation` | `getDoorUtilisationReport` | | `200` |
| Reports | `GET /reports/hostler-productivity` | `getHostlerProductivityReport` | | `200` |
| Reports | `GET /reports/turn-time` | `getTurnTimeReport` | | `200` (KPI standard v1) |
| Reports | `GET /reports/detention-exposure` | `getDetentionExposureReport` | | `200`; `thresholdMinutes` (1 to 10080, default the two-hour norm) |
| Reports | `GET /reports/exceptions` | `getExceptionsReport` | | `200` |
| Reports | `GET /reports/freshness` | `getReportsFreshness` | | `200`, time of the last event folded in |
| Health | `GET /healthz`, `GET /readyz` | `getHealthz`, `getReadyz` | | `200`; `/readyz` is `503` while draining or a dependency is down |

The write routes of this table are the REST side of the use cases on
[Use cases](/docs/ddd/use-cases).

## Conventions in the contract

- **Idempotency.** Every state-changing `POST` and `PUT` except the two sweeps
  requires `Idempotency-Key` (1 to 128 printable ASCII characters): `400
  idempotency-key-required` / `idempotency-key-invalid`; the same key with a
  different method, path or body is `409 idempotency-key-reused`; an equal
  request replays the stored answer with `Idempotency-Replayed: true`. A 2xx
  commits with its key; a 4xx refusal is stored and replays as the same refusal;
  a 5xx rolls back and is never stored. The sweeps accept the key but do not
  need it (`internal/adapters/inbound/http/idempotency.go`).
- **Versioning and conflicts.** Aggregates carry a `version`. A write that loses
  a race is `409 concurrent-modification`. The yard policy has a weak `ETag`
  of its version: `PUT` honours `If-Match`; a stale value is `412
  precondition-failed`, an unparsable one `400 invalid-if-match`.
- **Paging.** List endpoints take `limit` (1 to 500, default 100) and the opaque
  `cursor` (the previous page's `nextCursor`); no `nextCursor` marks the last
  page. A parameter the operation does not declare is `400
  unknown-query-parameter`; a bad `state` is `400 invalid-state`.
- **Reports** take `from` and `to` as RFC 3339 instants (`to` after `from`, at
  most 92 days: `400 invalid-report-range`). The body is `{from, to, siteId,
  rows}`; `siteId` is `null` for every site.
- **Errors** are RFC 7807 `application/problem+json` with `type =
  https://errors.yard-management.warehouse-systems.dev/<slug>`, the slug being
  the kebab case of the domain error without `Err`. Status classes: 400 the
  request itself, 404 a missing resource, 409 a conflict with state or a lost
  race, 412 a stale `If-Match`, 422 a value or reference rule, 503
  `dependency-unavailable`, 500 `internal-error` (the cause only in the log).
  The catalogue is `problemCatalogue` in `internal/adapters/inbound/http/errors.go`;
  causes and fixes are on [Troubleshooting](/docs/operations/troubleshooting#api-refusals).
- **NUL guard.** A U+0000 in the path, query or body is a uniform client error.
- **Events** are CloudEvents 1.0 (structured JSON, `application/cloudevents+json`)
  on `warehouse.yard-management.events` and `.analytics`, key = aggregate root id;
  a breaking payload change is a new `.v2` type, never a mutation.
- **Sites and locations** are addressed by the facility-layout `siteCode` and
  location code; the API never calls a sibling context (the copies are event-fed,
  [ADR 0003](/docs/adr/0003-consumed-contracts-and-local-copies)).
