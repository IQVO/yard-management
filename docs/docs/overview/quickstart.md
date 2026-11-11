---
id: quickstart
title: Quickstart
sidebar_label: Quickstart
sidebar_position: 3
description: "Prerequisites, make targets, what runs without infrastructure, how to run the binaries against Postgres and Kafka, and the first curl calls."
---

# Quickstart

This page is written from the `Makefile`, the five `cmd/*/main.go` files,
`apis/openapi.yaml` and the BDD features. **Nothing on it was captured from a
run**: no Docker, Postgres or Kafka was started and no binary was executed
while writing it, so the responses are described, not pasted.

## Prerequisites

| Need | Why |
| --- | --- |
| Go `1.27.3` (`go.mod`) | build and test |
| `golangci-lint` v2.14.0 | `make lint` (CI pins this version) |
| Docker | **only** for `make integration` (testcontainers boots its own Postgres and Kafka) |
| A Postgres and a Kafka broker | **only** to run the service itself; there is no in-memory mode |

The platform has **one** Kafka broker and one k3s cluster. The kind cluster and
the docker-compose Kafka are retired: do not set them up for this service.
Connection details come from the platform; this repo ships no compose file. The service
itself is applied to that cluster at `v0.1.2` (api and relay; read-only check on
2026-11-10, see [What runs on the k3s cluster](/docs/operations/runbook#what-runs-on-the-k3s-cluster)).

## Make targets that need nothing but Go

```text
make check        # fmt-check vet build lint test   (the fast pre-commit bundle)
make check-all    # check + coverage + arch-test + bdd   (run before pushing)
make test         # go test ./... -race   (unit + httptest + godog features)
make bdd          # go test ./... -run TestFeatures -v   (7 feature files, 53 scenarios)
make arch-test    # go test ./internal/architecture/... -v   (fitness tests)
make coverage     # test with -coverpkg domain+application, 90 % gate
make build        # go build ./...
```

`make test` and `make bdd` need no database: the BDD world and the handler
tests run the real use cases and HTTP/MCP adapters on the in-memory test
doubles in `internal/adapters/outbound/memory`. This is the fastest way to see
the service behave: read `features/drop.feature` and run
`go test ./... -run TestFeatures -v`.

Targets that need more: `make integration` (Docker), `make mutation` /
`make mutation-full` (gremlins v0.6.0), `make vuln` (govulncheck). Details in
[Testing](/docs/development/testing).

## Running the binaries against real infrastructure

The five binaries are independent processes. Each exits at boot, naming the
variable, when a mandatory one is missing. The full list is in
[Configuration](/docs/operations/configuration); the minimum:

```text
# 1. API + the three Kafka consumers (migrates the OLTP schema at boot)
DATABASE_URL='postgres://USER:PASS@HOST:5432/yard?sslmode=disable' \
KAFKA_BROKERS='HOST:9092' \
DOCK_SCHEDULING_CONSUMER_GROUP=yard-management-dock-scheduling-dev \
FACILITY_CONSUMER_GROUP=yard-management-facility-dev \
INBOUND_RECEIVING_CONSUMER_GROUP=yard-management-inbound-receiving-dev \
go run ./cmd/api                       # listens on :8080

# 2. Outbox relay (without it no event leaves; it does not migrate)
DATABASE_URL=... KAFKA_BROKERS=... go run ./cmd/relay      # /healthz on :8081

# 3. Optional: MCP server (:8090, Streamable HTTP at / and /mcp)
DATABASE_URL=... KAFKA_BROKERS=... go run ./cmd/mcp

# 4. Optional analytics read side
ANALYTICS_DATABASE_URL=... KAFKA_BROKERS=... ANALYTICS_CONSUMER_GROUP=yard-management-analytics-dev \
go run ./cmd/yard-projector            # /healthz /readyz on :8091
ANALYTICS_READER_DATABASE_URL=... go run ./cmd/yard-reports   # :8092
```

The consumer group values above are examples for a developer machine; pick
names that cannot collide with a deployed group (a process started by hand must
not join a live one). `/readyz` on the API is 200 only when Postgres and Kafka
both answer; `yard-reports` is ready only after the projector created the
`analytics` schema.

## First calls

The API is unauthenticated. Every `POST` and `PUT` (except the two sweeps)
requires an `Idempotency-Key` header (1 to 128 printable ASCII characters); a
replay with the same key returns the first answer with
`Idempotency-Replayed: true`.

```text
curl -s localhost:8080/healthz
curl -s localhost:8080/readyz
```

`/healthz` answers `{"status":"ok"}`; `/readyz` answers `{"status":"ready"}` or
a 503 (see [Troubleshooting](/docs/operations/troubleshooting)).

**The yard is empty until the facility feed arrives.** Spots and doors are
event-fed local copies of `warehouse.facility.events`, appointments of
`warehouse.dock-scheduling.events`; there is no endpoint that creates a
location. Until a `LocationSlotRegistered` for the site has been consumed there
is no spot or door to direct a trailer to, so a check-in is refused (the
problem slugs of a refused check-in are listed on `checkInVisit`: for example
`no-free-spot` or `unknown-location`). In the BDD world
the Background step `a yard site "WH1" with spots … and doors …` seeds the
copies directly.

With a site loaded, the calls of the `drop` and `live_unload` features are:

```text
# Check a visit in (body from the openapi example of checkInVisit): 201
curl -s -X POST localhost:8080/visits \
  -H 'Idempotency-Key: demo-1' -H 'Content-Type: application/json' \
  -d '{"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23",
       "driverRef":"drv-77","purpose":"DROP",
       "trailers":[{"unitNumber":"TR-1042","kind":"DRY_VAN","loadState":"LOADED","sealNumber":"SL-778"}]}'

curl -s 'localhost:8080/visits?siteId=WH1&state=CHECKED_IN'
curl -s 'localhost:8080/trailers?siteId=WH1'
curl -s  localhost:8080/sites/WH1/yard-board

# Supervisor asks for a hostler move; a hostler pulls the next one (lease 300 s by default)
curl -s -X POST localhost:8080/moves -H 'Idempotency-Key: demo-2' -H 'Content-Type: application/json' \
  -d '{"trailerId":"<trailerId>","to":{"kind":"DOOR","code":"WH1-D02"},"reason":"TO_DOOR"}'
curl -s -X POST localhost:8080/moves/claims -H 'Idempotency-Key: demo-3' -H 'Content-Type: application/json' \
  -d '{"siteId":"WH1","hostlerRef":"h1"}'
curl -s -X POST localhost:8080/moves/<moveId>/completions -H 'Idempotency-Key: demo-4' -H 'Content-Type: application/json' \
  -d '{"hostlerRef":"h1"}'

# Dock work and gate out
curl -s -X POST localhost:8080/trailers/<trailerId>/door-work-starts -H 'Idempotency-Key: demo-5' -H 'Content-Type: application/json' -d '{}'
curl -s -X POST localhost:8080/trailers/<trailerId>/door-work-completions -H 'Idempotency-Key: demo-6' -H 'Content-Type: application/json' -d '{"loadState":"EMPTY"}'
curl -s -X POST localhost:8080/visits/<visitId>/check-outs -H 'Idempotency-Key: demo-7' -H 'Content-Type: application/json' -d '{"trailersOut":[]}'

# Reports (need yard-reports, or ANALYTICS_READER_DATABASE_URL on the API): from/to are RFC 3339, at most 92 days apart
curl -s 'localhost:8092/reports/turn-time?from=2026-11-01T00:00:00Z&to=2026-11-08T00:00:00Z'
curl -s  localhost:8092/reports/freshness
```

Field names and enums are in the generated [REST reference](/docs/api-reference);
the exact request bodies of the calls above are the schemas
`CheckInVisitRequest`, `RequestMoveRequest`, `CompleteMoveRequest` and
`DoorWorkCompletionRequest` of `apis/openapi.yaml`. Errors are
`application/problem+json` with `type` =
`https://errors.yard-management.warehouse-systems.dev/<slug>`.

Behind the platform's edge the same API is `http://<kong>:8000/api/yard-management/...`
(Kong strips the prefix) and the reports are under
`/api/yard-management/reports`; MCP is under `/api/mcp/yard-management`.
The route objects are rendered by this repo's chart when enabled.

## Where to go next

- What the service owns: [Introduction](/docs/overview/introduction) and [Architecture](/docs/overview/architecture).
- Every flow as a Gherkin scenario: `features/*.feature`, described in [Testing](/docs/development/testing).
- All interfaces: [API reference overview](/docs/api-reference).
