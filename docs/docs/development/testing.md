---
id: testing
title: Testing
sidebar_label: Testing
sidebar_position: 1
description: "The yard-management test pyramid as it is: unit, handler, godog features, repository contract, integration, mutation and architecture fitness tests, the make targets and the CI jobs."
---

# Testing

All commands run from the repo root. Docker is needed only for
`make integration`. Nothing here was run while writing the page; counts come
from reading the tree.

## The pyramid

| Layer | Where | What it proves | Needs |
| --- | --- | --- | --- |
| Unit (domain) | `internal/domain/{shared,trailer,yardcheck,yardmove,yardpolicy,yardvisit}/*_test.go` | aggregate invariants and state machines, no I/O | Go |
| Unit (application) | `internal/application/usecases/*_test.go` (7 files) | every use case over in-memory ports | Go |
| Adapter unit tests | `internal/adapters/inbound/http` (handlers, idempotency, readiness), `inbound/mcp` (tools, idempotency, attribution, governance), `inbound/kafka` (consumers, retry, dead-letter), `outbound/kafka` (encoder golden tests, relay sink, health), `kafka/cloudevents`, `cmd/api`, `cmd/mcp`, `cmd/relay` | each adapter against fakes/httptest | Go |
| Godog features | `features/*.feature`, runner `internal/bdd/bdd_test.go` (`TestFeatures`) | the REST adapter and use cases end to end on the in-memory store | Go |
| Repository contract | `internal/testing/repocontract` | one behavioural suite every store must pass (visits, trailers, moves, checks, policies, locations, appointments, closures, ASNs, receipts, the unit of work, locks) | Go (memory), Docker (Postgres) |
| Integration | `*_integration_test.go`, build tag `integration` | real Postgres and Kafka through testcontainers: the Postgres repositories (contract suite), the outbox relay, the Kafka consumers of the three topics, the analytics store (OLTP and KPI), `cmd/api`, `cmd/mcp`, `cmd/relay` entry points | Docker |
| Mutation | `.gremlins.yaml`, `make mutation` | tests assert, not merely execute, on `internal/domain/*` | gremlins v0.6.0 |
| Architecture fitness | `internal/architecture/*_test.go` | structural rules (below) | Go |
| Spec/code parity (problem types) | `internal/adapters/inbound/http/spec_problems_test.go` | every problem type the REST adapter can answer is declared in `apis/openapi.yaml`, and every problem the spec shows is one the adapter writes, with its status and title | Go |
| Contract lint | spectral on `apis/openapi.yaml` and `apis/asyncapi.yaml` (CI `api-lint`) | the specs follow the fleet rulesets | spectral |

There are no consumer-driven contract tests against sibling services:
yard-management calls no sibling (see [Integration](/docs/ecosystem/integration)).
The consumed messages are covered by the Kafka consumer tests under
`internal/adapters/inbound/kafka`, and `TestEventCatalogueMatchesContract` ties
the produced catalogue to `apis/asyncapi.yaml`. On the REST side
`TestTheSpecDeclaresEveryProblemTypeTheAdapterAnswers` ties the problem types
the adapter can answer to `apis/openapi.yaml`.

## Godog features

Fifteen files, 231 `Scenario` and `Scenario Outline` definitions (`git grep -c -E "^[[:space:]]*Scenario" -- 'features/*.feature'`);
with the `Examples` rows expanded the runner executes 328 scenarios (`328 scenarios (328 passed)` from
`go test ./internal/bdd -run TestFeatures -count=1 -v`). They are tagged `@bdd` and run the REST handlers on the
in-memory store (`internal/adapters/outbound/memory`) behind the real HTTP handlers (`httptest`), with the events the use cases
publish captured for the `the event "X" was published` steps:

| Feature | Definitions / executed | Covers |
| --- | ---: | --- |
| `audit_rules.feature` | 12 / 17 | the documented rules of the yard policy (defaults, validation, replace not merge), the yard check (one open per site, one observation per location, MISSING / UNEXPECTED / WRONG_LOCATION) and the yard board |
| `checkout.feature` | 7 / 7 | a visit leaves only with released trailers (no open move; parked, or docked with door work done); seal on a loaded pickup; a visit checks out once |
| `compliance_inspections.feature` | 37 / 55 | built-in and custom checklist templates (versions never change), an inspection from open to PASSED or FAILED, the hold a failure places, and the site policy gates before a door move, loading and check-out (ADR 0015) |
| `dock_safety_interlock.feature` | 16 / 18 | securing before door work and release before leaving the door, `trailer-still-secured` (ADR 0013) |
| `drop.feature` | 3 / 3 | drop trailer: hold, door, work, clear door, tractor leaves |
| `floor_client.feature` | 12 / 17 | what the gate kiosk, dock tablet and hostler phone rely on: optional trailers, deviation note, filters, no lease renewal, sweeps without key, idempotent replay, ETag of the policy, report shape before analytics |
| `gate_rules.feature` | 24 / 35 | the documented gate rules: carrier name and SCAC, one open visit per plate, the appointment copy (`appointment-required`, `unknown-appointment`, a cancelled booking), `DirectOnCheckIn` and the tolerance boundary, the all-or-nothing check-out and the seal rule |
| `hazmat_reefer_placement.feature` | 19 / 40 | hazmat and reefer placement by spot kind (ADR 0014) |
| `lease.feature` | 7 / 7 | claim leases, the expiry sweep, lease ownership, one move per hostler, claim order (lowest band, earliest due) |
| `live_unload.feature` | 4 / 4 | live unload from gate in to gate out, receipt-close auto-completes the unload, ASNs carried by the trailer |
| `move_rules.feature` | 35 / 53 | the documented rules of `RequestMove`, `ClaimNextMove`, `CompleteMove` and `CancelMove`, the lease and `ReturnToOrigin`, `ArriveAt`, `CallToDoor` and `NoShowFreesHolding` |
| `occupancy.feature` | 11 / 14 | one trailer per spot or door, reservations, one open move per trailer, races end in 409, closed doors, flow mismatch, holds |
| `rejection.feature` | 9 / 9 | gate turn-away, appointment required, seal mismatch under FLAG and REJECT, one open visit per plate, unknown appointment |
| `standard_identifiers.feature` | 19 / 29 | ISO 6346 unit numbers, seal class and seal state vocabulary (ADR 0012) |
| `trailer_rules.feature` | 16 / 20 | door work order, holds and their release, the trailer `version` (ETag), `AutoCompleteUnload`, the dwell sweep |

Steps are in `internal/bdd/steps_*_test.go`. Run one feature with
`go test ./internal/bdd -run TestFeatures -v`.

## Architecture fitness tests

`make arch-test` (`go test ./internal/architecture/... -v`). Each sensor also has
a "bites" test proving it fails on a bad fixture.

| Test | Rule |
| --- | --- |
| `TestHexagonalArchitecture` | domain imports nothing outward; application imports domain and ports only; adapters never import each other |
| `TestEventCatalogueMatchesContract` | the events the code publishes equal the catalogue of `apis/asyncapi.yaml` |
| `TestCloudEventsOnly`, `TestNoEventEnvelopeToggleOrFlatEnvelope` | every message is a CloudEvents 1.0 envelope, no flat or toggled envelope |
| `TestReplayConsumersSetCommitInterval` | consumer commit settings |
| `TestKafkaConsumerGroupNeverHardcodedInline` | consumer groups come from the environment |
| `TestKafkaIntegrationTestsUseTestcontainers`, `TestPostgresIntegrationTestsUseTestcontainers` | no `DATABASE_URL`-gated or skipped integration tests |
| `TestDeadLetterCallsGuardCancellation` | the dead-letter path checks `ctx.Err()` so shutdown never dead-letters |
| `TestKafkaWritersSetRequiredAcks`, `TestKafkaWriterTopicNotSetTwice` | `acks=all`; a writer has its topic on the writer **or** the message, never both |
| `TestOutboxRelaysPassAttemptCap` | the composition root passes `WithMaxAttempts` |
| `TestRuntimeBinaryHasNoInMemoryAdapters` | `cmd/**` never imports the memory package |
| `TestNoGoFilesAtRepoRoot` | no `.go` at the repo root |
| `TestMCPAdapterDependencyRule`, `TestEveryMCPToolDeclaresAuthority`, `TestMCPWriteToolsAreAtLeastL3`, `TestMCPServerOffersResourceAndPrompt`, `TestMCPOptionalToolInputsCarryOmitempty` | MCP adapter layering, authority annotations, optional inputs |
| `TestNoAuthMiddlewareReintroduced` | no auth middleware reappears (fleet decision) |

## Mutation testing

`.gremlins.yaml` (gremlins v0.6.0, one worker, timeout coefficient 30) sets
efficacy `90` and mutant coverage `65` as a blocking floor, measured per domain
package. `make mutation` runs gremlins once per `internal/domain/*` package (a
new package is picked up by the wildcard); `make mutation-full` runs the
exhaustive pass on `./internal/domain`. The file records the last measurement
per package (shared, trailer, yardmove, yardvisit at 100 % efficacy; yardcheck
94.74 %, yardpolicy 100 %).

## Make targets

| Target | Runs |
| --- | --- |
| `make check` | `fmt-check vet build lint test` (fast pre-commit; lefthook runs it) |
| `make check-all` | `check` + `coverage` + `arch-test` + `bdd` (before pushing) |
| `make test` | `go test ./... -race` (unit, handler tests, godog) |
| `make coverage` | same with `-coverpkg=./internal/domain/...,./internal/application/...`, fails below **90 %** |
| `make integration` | `go test -tags=integration ./... -race -count=1` (Docker; not in `check`) |
| `make bdd` | `go test ./... -run TestFeatures -v` |
| `make arch-test` | the fitness tests |
| `make mutation` / `make mutation-full` | gremlins fast subset / exhaustive |
| `make vuln` | `govulncheck ./...` |
| `make lint`, `make vet`, `make fmt`, `make fmt-check`, `make build` | as named |
| `make check-fast`, `make guide-lint`, `make harness-test` | agent-harness helpers (`scripts/harness`) |

## CI jobs (`.github/workflows/ci.yml`)

Triggers: push to `main`/`develop`, pull requests to those, a weekly schedule
(Monday 06:00 UTC) and manual dispatch.

| Job | Runs | When |
| --- | --- | --- |
| `lint` | golangci-lint | always |
| `guide-lint` | `scripts/harness/guide_lint.py`, `test_hook.py` | always |
| `complexity` | gocyclo report (gate 15) | always |
| `file-length` | file-length gate on touched Go files | always |
| `test` | build, vet, `make coverage` | always |
| `bdd` | `go test ./... -run TestFeatures -v` | always |
| `integration` | `go test -tags=integration ./... -race -count=1` | always |
| `mutation-fast` | gremlins per domain package | always |
| `mutation` | exhaustive gremlins; opens/closes a `harness:red` issue | schedule, dispatch |
| `drift` | dead code, `go mod tidy -diff`, coverage-quality | schedule, dispatch |
| `api-lint` | spectral on the OpenAPI and AsyncAPI specs | always |
| `vuln` | `govulncheck` | always |
| `arch-test` | `go test ./internal/architecture/... -v` | always |
| `helm-lint` | `ct lint` and chart wiring tests | pull requests |
| `chart-tests` | `charts/yard-management/tests/*.sh` and `test_*.py` | always |
| `trivy-scan` | builds the image, Trivy SARIF + blocking gate (CRITICAL/HIGH with a fix) | pull requests |
| `docker-publish` | build, push to `ghcr.io/iqvo`, cosign, SBOM; needs lint, complexity, test, bdd, integration, mutation-fast, vuln, api-lint, arch-test | push to `main` |
| `release` | version tag, chart package, GitHub release; needs `docker-publish` | push to `main` |

The docs site has its own workflow, `.github/workflows/docs.yml`: it builds on
pull requests touching `docs/**` or `apis/**` and deploys to GitHub Pages on
push to `main`. There is no `docs-api-drift` job in `ci.yml`.
