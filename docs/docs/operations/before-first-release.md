---
id: before-first-release
title: Before the first release
sidebar_label: Before the first release
sidebar_position: 5
description: "The packaging checklist to work through before the first develop to main merge, with the state of each row on develop."
---

# Before the first release: yard-management

The first merge to `main` runs `docker-publish` and `release`. Two services built from
the same template (pick-face-replenishment, task-interleaving) each needed a rework because
packaging was missing or wrong when that moment came. Work through this list on a feature
branch, **before** the first `develop` → `main` PR. Delete a row only when the service
really does not have that part, and say so in the PR.

Status: **built on develop, registered in warehouse-infra develop, not applied to the
cluster**: `warehouse-infra` `terraform/apps.yaml` on `origin/develop` has the entry (PR 193,
commit `b4b827e`) and the images are pinned (commit `c42c475`), but no pod runs. The checklist
below is the template's; the section after it records what `origin/develop` of this repository
shows for each row, and the last section records the platform registration.

`new-service.sh` generated `Dockerfile` and `.dockerignore` (verbatim from
the fleet reference). The chart is NOT generated: it is shaped by what the service runs
(consumer groups, MCP, analytics, frontend), so the closest sibling's
`charts/<repo>/` is copied and adapted; `charts/yard-management/` is that adaptation. References on `origin/develop`:
`IQVO/pick-face-replenishment` (Kafka consumers, MCP, analytics, frontend) and
`IQVO/task-interleaving`.

| # | Item | Done when | Guarded by |
|---|------|-----------|------------|
| 1 | **Real runtime only.** `cmd/api` wires Postgres and Kafka; the in-memory adapters (`internal/adapters/outbound/memory`) are test doubles. No `*_MODE`/permissive fallback, no default that boots "healthy" without `DATABASE_URL` or `KAFKA_BROKERS`. | `cmd/**` has no import of the memory package; the binary exits at boot on missing `DATABASE_URL`, `KAFKA_BROKERS` or a consumer group id. | `TestRuntimeBinaryHasNoInMemoryAdapters` (`internal/architecture/runtime_no_memory_fitness_test.go`). The boot-error behaviour has no generic sensor: cover it in `cmd/api` tests and in the chart test (row 8). |
| 2 | **No Go file at the repo root.** The godog runner and steps live in `features/` (`package features_test`, `Paths: "."`), binaries in `cmd/<name>/`, everything else in `internal/`. | `ls *.go` at the root prints nothing; `make bdd` still finds `TestFeatures`. | `TestNoGoFilesAtRepoRoot` (`internal/architecture/no_go_files_at_root_fitness_test.go`). |
| 3 | **`Dockerfile` builds every `cmd/*`.** Generated; confirm `ENTRYPOINT ["./api"]` matches your main binary and `EXPOSE` matches `HTTP_ADDR`. | `docker build .` succeeds locally; `docker run --rm <image> /app/api` starts (or exits with the intended config error). | `trivy-scan` job builds the image on every PR to `main`/`develop`; `docker-publish` on push to `main`. |
| 4 | **`.dockerignore`** excludes docs, charts, harness, tests output. Generated. | Build context is small; `.git`, `.claude/`, `docs/`, `charts/` are not sent. | none: review once. |
| 5 | **Helm chart `charts/yard-management/`** with `Chart.yaml` `name: yard-management`. The chart directory name, the `Chart.yaml` name and the `helm package` file name in the `release` job must all be `yard-management`. | `helm lint charts/yard-management` and `helm template` with the required values both pass. | `helm-lint` (PRs) and the `release` job (packages `yard-management-<version>.tgz`). |
| 6 | **`DATABASE_URL` comes from a Secret** (`secretKeyRef`, chart `secret.yaml` or `database.existingSecret`), never from the ConfigMap or a literal. | `helm template` shows `secretKeyRef` for `DATABASE_URL`; the ConfigMap has no DSN. | chart test (row 8). |
| 7 | **Pod hardening and shutdown room.** `runAsNonRoot`, `readOnlyRootFilesystem: true`, `allowPrivilegeEscalation: false`, `capabilities.drop: [ALL]`; `terminationGracePeriodSeconds` greater than the sum of the binary's shutdown bounds (drain delay + HTTP + consumers + relay). | Values and Deployment carry all of it; the chart refuses a grace period below the sum. | chart test (row 8). |
| 8 | **Chart tests in `charts/yard-management/tests/`** (`render_test.sh`, `test_service_selectors.py`): envFrom ConfigMap mounted, mandatory values refuse to render, every env var `cmd/api` reads is rendered, no `*_MODE`, Secret for the DSN, hardening, grace period, every Service selects exactly one Deployment. Copy and adapt the reference tests. | `bash charts/yard-management/tests/render_test.sh` prints `PASS`. | `chart-tests` job runs every `tests/*.sh` and `test_*.py`. **It only warns when the directory is empty**, so an absent test passes CI: the checklist is the guard. |
| 9 | **Consumer group ids are chart values**, stable and service-prefixed, never derived from the pod name. | Each `*_CONSUMER_GROUP` is rendered from `config.*` and refuses to render empty. | chart test (row 8); `kafka-testing-and-consumers.md` rule. |
| 10 | **Optional CI jobs match the service.** `web` (needs `web/`), `docs-api-drift` (needs `docs/` and `apis/openapi.yaml`), `api-lint` (needs `apis/*.yaml` and the spectral rulesets): delete a job and its `docker-publish` `needs:` entry if the service has no such part, or add the part. | No job in `ci.yml` is permanently red or skipped for a missing directory. | first PR's CI run. |
| 11 | **Registry access** images push to `ghcr.io/iqvo` with the Actions token (`packages: write` on `docker-publish`/`release`, already in `ci.yml`); no Docker Hub secrets are needed. | A dry-run of `docker-publish` on a throwaway branch, or ask the repo admin. | none. |
| 12 | **`govulncheck` is clean** before the PR to `main` (CI `vuln` job installs `@latest`; `make vuln` runs the same command). | `make vuln` exits 0. | `vuln` job (a `docker-publish` prerequisite). |

Tick this list in the PR that adds `Dockerfile`/chart wiring, and keep the file: it is the
service's packaging record. Reference for the three failures this list prevents: a runtime that
can run without Postgres/Kafka (row 1), Go files at the root (row 2), a release that fails on
the first push to `main` because packaging was never built (rows 3-8).

## State of each row on `origin/develop`

Read from the repository; nothing here was run (no Docker, `helm` or `make vuln` while writing).

| # | State | Evidence |
|---|-------|----------|
| 1 | Done | `TestRuntimeBinaryHasNoInMemoryAdapters`; `cmd/api`, `cmd/mcp`, `cmd/relay`, `cmd/yard-projector` and `cmd/yard-reports` exit at boot naming each missing variable ([Configuration](/docs/operations/configuration)) |
| 2 | Done, with a different layout | `TestNoGoFilesAtRepoRoot` exists; the godog runner is `internal/bdd/bdd_test.go` (`TestFeatures`, `Paths: ../../features`), not a `package features_test` inside `features/`; `features/` holds only `.feature` files |
| 3 | Done | `Dockerfile` loops over `cmd/*` (five binaries); `ENTRYPOINT ["./api"]`, `EXPOSE 8080`; CI `trivy-scan` builds it on pull requests |
| 4 | Present | `.dockerignore` |
| 5 | Done | `charts/yard-management/Chart.yaml`; CI `helm-lint` |
| 6 | Done | `DATABASE_URL` is a `secretKeyRef` in the Deployments; the chart refuses to render without `database.url` or `database.existingSecret` |
| 7 | **Partly** | `runAsNonRoot`, `allowPrivilegeEscalation: false`, `capabilities.drop: [ALL]` and the grace-period floor (refused below 20 s; 40 s default for the api) are in `values.yaml` and `_helpers.tpl`; **`securityContext.readOnlyRootFilesystem` is `false`** in `charts/yard-management/values.yaml`, while the row asks for `true` (the Dockerfile comment says the service writes nothing to disk) |
| 8 | Present | `charts/yard-management/tests/render_test.sh`, `test_service_selectors.py`, `test_kong_config.py`; CI `chart-tests` |
| 9 | Done | the three consumer groups and `analytics.consumerGroup` are chart values; the render fails when a group is empty |
| 10 | Matches | `ci.yml` has `api-lint` (the `apis/*.yaml` and spectral rulesets exist); it has no `web` job and no `docs-api-drift` job; `docker-publish` `needs:` lists neither. The documentation site builds in its own workflow, `.github/workflows/docs.yml` |
| 11 | Configured, not exercised | `packages: write` on `docker-publish` and `release`; no dry run is recorded |
| 12 | Not recorded | `vuln` job and `make vuln` exist; a clean run on this commit is not recorded here |

Sweeps are part of the packaging too: the chart's two CronJobs (`sweeps-cronjob.yaml`) are the
only scheduler of `POST /dwell-sweeps` and `POST /move-lease-sweeps` ([Runbook](/docs/operations/runbook#sweeps)).

## Platform registration in warehouse-infra

The packaging rows above say nothing of the platform side, which lives in `warehouse-infra`.
State on its `origin/develop`:

| Item | State | Evidence |
|---|---|---|
| `terraform/apps.yaml` entry (`kind: service`, `database: true`, `analytics: true`, no web part) | Done | `warehouse-infra` PR 193, commit `b4b827e`, entry `yard-management`. |
| `helm-values/yard-management.yaml` (stable `yard-management-*` consumer groups, `existingSecret` wiring, reports and MCP routes, relay, sweeps, Kong plugins) | Done | PR 193, commit `b4b827e`. |
| Dashboard and alert rules | Done | PR 193, commit `b4b827e`: `terraform/dashboards/contexts/yard-management.json` and six rules in `terraform/alerting/warehouse-alert-rules.json` (see [Observability](/docs/operations/observability#dashboards-and-alerts)). |
| Chart lint and selector check in `warehouse-infra` CI | Done | PR 193, commit `b4b827e` (`.github/workflows/ci.yml`, `scripts/check-chart-selectors.py`). |
| Image pinned | Done | Commit `c42c475` pins `yard-management` at `b106934-arm64`, this repository's `v0.1.1` commit on `main`. |
| Yard-domain Kafka topics pre-created before the first sync | Documented, not done | `docs/kafka-topics-yard-dock.md` in `warehouse-infra` (PR 193) gives the command; no pod has been synced, and whether the topics exist on the broker was not checked. |
| Applied to the k3s cluster | Not done | `kubectl get pods -n warehouse-systems` lists no `yard-management` pod (checked 2026-11-09). |

The service is **built on develop, registered in warehouse-infra develop, not applied to the cluster.** The ADR bodies in `docs/adr/` are not edited by this update.
