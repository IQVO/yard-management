# ADR 0008: The REST adapter and the API process

## Status

Accepted (2026-11-06). 0005 is persistence; 0006 (analytics) and 0007 (MCP) are reserved for the children that build them.

## Context

`apis/openapi.yaml` is the contract of every route the model lists (`yard-management-build.md` `rest:`). The floor client
(the PWA the gate clerk, the dock operator and the hostler use) fixed seven details before the first line of the adapter
(F1-F7), and the fleet REST guideline fixes the rest: `problem+json`, `Idempotency-Key`, weak ETags, keyset paging.

## Decision

### Shape

- `internal/adapters/inbound/http` is a chi router over `*usecases.Yard`. It imports the application and domain layers
  and the `ports` interfaces; it never imports an outbound adapter (the architecture test). `cmd/api` is the only
  place where Postgres, Kafka and the router meet.
- The middlewares, in order: `rejectNUL` (a NUL in the path is `404 resource-not-found`, in the query or body `400`),
  then the route-scoped Idempotency-Key middleware. No auth middleware (fleet decision 2026-09-11).
- Collections answer `{items, nextCursor}`. The cursor is the opaque keyset `(instant, id)` of the last row; a
  parameter an operation does not declare is `400 unknown-query-parameter`, so a retired paging parameter fails loudly.

### Errors

Slug = kebab-case of the domain error name without `Err`. `409` for conflicts and illegal states (`visit-already-open`,
`appointment-required`, `trailer-not-released`, `seal-required`, `location-occupied`, `door-closed`,
`door-flow-mismatch`, `trailer-on-hold`, `trailer-not-on-hold`, `door-work-open`, `trailer-already-on-site`,
`trailer-has-open-move`, `destination-reserved`, `hostler-busy`, `not-lease-holder`, `lease-not-expired`,
`yard-check-open`, `location-already-observed`, `no-free-spot`, `illegal-transition`, `concurrent-modification`);
`422` for value and reference rules (`validation`, `invalid-carrier`, `unknown-location`, `unknown-appointment`,
`unexplained-destination`); `404` `visit-not-found`, `trailer-not-found`, `move-not-found`, `yard-check-not-found`
(F7), `site-not-found`, `no-claimable-move`; `412 precondition-failed` for a stale `If-Match`. An unmapped error is a
`500 internal-error` whose detail never leaks the cause.

### Idempotency-Key

The key is required on every POST and PUT and optional on the sweeps (F6). The middleware talks to ports only
(`UnitOfWork`, `IdempotencyStore`, `LocationLock`, `Clock`), so the Postgres adapters are wired in `cmd/api`.

- A keyed request opens ONE unit of work, takes the advisory lock `idempotency-key/<key>`, re-reads the key and, when
  it is new, runs the handler inside that unit (the use case's own `Do` joins it). A `2xx` commits the handler's writes,
  its outbox rows and the stored answer together; two concurrent requests with one key therefore run the handler once and
  the second replays the first answer.
- A `4xx` is a refusal: the unit is rolled back (a refusal persists nothing) and the answer is stored on its own unit,
  so the same key and request replay the same refusal. A `5xx` rolls back and is never stored: the key can retry.
- The key is global, looked up alone; another method, path or body is `409 idempotency-key-reused`. A replay carries
  `Idempotency-Replayed: true`; `Location` of a created visit and the `ETag` of a replaced policy are rebuilt from the
  stored body.
- The sweeps keep one unit of work per item, so with a key they run outside an outer unit and only the answer is stored.

### Concurrency and versions

`GET` of a visit, a trailer or a policy carries a weak `ETag` `W/"<version>"`. `PUT /sites/{siteId}/yard-policy`
takes an optional `If-Match` (`W/"3"`, `"3"` or `3`; `*` or absent is unconditional); a stale version is `412`, a value
that is not a version is `400 invalid-if-match`. Occupancy races (two moves to one door, two completions to one spot) are
decided by the use cases under the per-location advisory lock and the partial unique indexes (ADR 0005): exactly one
request wins, the other is `409` (`destination-reserved` or `location-occupied`).

### Floor-client findings applied

F1 `trailers` is optional on check-in (BOBTAIL and PICKUP without an appointment bring none); naming an appointment means
exactly one trailer (`422 unknown-appointment` otherwise). F2 `deviationNote` on move completion maps to the domain note.
F3 `RejectVisitRequest.driverRef`. F4 `GET /moves?hostlerRef=` and `GET /trailers?visitId=`. F5 no lease renewal
endpoint. F6 see above. F7 see Errors.

### Readiness

`/healthz` is liveness only. `/readyz` is `200 ready` while the process is not draining and Postgres answers a ping and a
Kafka broker answers a topic-less metadata request (an EMPTY, non-nil topic list, so asking never creates a topic and
never means "all topics"); the verdict is cached 3 s and bounded 2 s. Draining answers `503 not_ready`; a failing
dependency answers `503 dependency-unavailable` naming it.

### Reports

`GET /reports/yard-dwell`, `/door-utilisation`, `/hostler-productivity` and `/freshness` read `ports.ReportReader`. Until
the analytics read side (ADR 0006) is wired the API answers the contract shape with no rows; `from` and `to` are required
RFC 3339 instants, `to` after `from`, at most 92 days (`400 invalid-report-range`).

## Consequences

- The router is testable with the in-memory store (unit and BDD) and with real Postgres and Kafka (the `cmd/api`
  integration test) without a second set of handlers.
- A handler never decides a business rule; a new domain error needs one line in the catalogue
  (`internal/adapters/inbound/http/errors.go`) and a `TestEveryProblemHasAKebabSlugAndAnAllowedStatus` pass.
- The memory store serialises its outermost unit of work so concurrent requests behave as they do under the Postgres
  advisory locks; it stays a test double (the runtime-no-memory fitness test).
