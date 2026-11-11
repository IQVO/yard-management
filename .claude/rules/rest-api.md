---
paths:
  - "internal/adapters/inbound/http/**"
  - "apis/openapi*.yaml"
  - "apis/openapi/**"
---

# REST API (inbound adapter)

`internal/adapters/inbound/http` serves every route of `apis/openapi.yaml` on a chi router (`NewRouter` in
`server.go`). Each handler decodes, calls ONE method of `usecases.Yard` and maps the result to a DTO; it never
touches a domain aggregate. Keep this list in sync with the spec by hand (it is not generated).

- `POST /visits` -> `CheckInVisit`; `POST /visit-rejections` -> `RejectAtGate`;
  `POST /visits/{visitId}/check-outs` -> `CheckOutVisit`; `GET /visits`, `GET /visits/{visitId}` -> `ListVisits`, `GetVisit`
- `GET /trailers`, `GET /trailers/{trailerId}` -> `ListTrailers`, `GetTrailer`
- `POST /trailers/{trailerId}/door-work-starts` -> `StartDoorWork`; `.../door-work-completions` -> `CompleteDoorWork`
- `POST /trailers/{trailerId}/holds` -> `PlaceHold`; `.../hold-releases` -> `ReleaseHold`; `.../seals` -> `RecordSeal`
- `POST /moves` -> `RequestMove`; `GET /moves` -> `ListMoves`; `POST /moves/claims` -> `ClaimNextMove`;
  `POST /moves/{moveId}/completions` -> `CompleteMove`; `POST /moves/{moveId}/cancellations` -> `CancelMove`
- `POST /yard-checks` -> `OpenYardCheck`; `.../observations` -> `RecordObservation`; `.../closures` -> `CloseYardCheck`
- `POST /trailers/{trailerId}/inspections` -> `OpenInspection`; `GET /trailers/{trailerId}/inspections`; `GET /inspections/{inspectionId}`;
  `PUT /inspections/{inspectionId}/items/{itemCode}` -> `RecordInspectionItem`; `POST .../completions` -> `CompleteInspection`;
  `GET /inspection-templates[/{id}[/versions/{n}]]`, `PUT /inspection-templates/{id}/versions/{n}` (ADR 0015)
- `GET /sites/{siteId}/yard-board` -> `GetYardBoard`; `GET`/`PUT /sites/{siteId}/yard-policy` -> `GetYardPolicy`, `DeclareYardPolicy`
- `POST /dwell-sweeps` -> `SweepDwell`; `POST /move-lease-sweeps` -> `SweepMoveLeases`
- `GET /reports/*` (yard-dwell, door-utilisation, hostler-productivity, turn-time, detention-exposure, exceptions,
  freshness): read the analytical database (ADR 0006, ADR 0011). Served by `cmd/yard-reports`
  (`NewReportsRouter`) and by `cmd/api` when `ANALYTICS_READER_DATABASE_URL` is set.

## Conventions

- Errors are RFC 7807 `application/problem+json`; the slug is the kebab-case domain error name (ADR 0002).
- Every POST and PUT except the sweeps requires `Idempotency-Key`; the sweeps take it optionally (they are
  naturally idempotent). A single-aggregate `GET` returns a weak `ETag` of its `version`;
  `PUT /sites/{siteId}/yard-policy` honours `If-Match`.
- A NUL character in the path is a 404 `resource-not-found`; in the query or body a 400 `malformed-request`,
  answered before the idempotency middleware records the key (`internal/adapters/inbound/http/nul.go`).
- No authentication (fleet rule); `TestNoAuthMiddlewareReintroduced` in `internal/architecture/fitness_test.go`
  fails CI if auth middleware comes back.
