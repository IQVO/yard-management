---
id: runbook
title: Runbook
sidebar_label: Runbook
sidebar_position: 2
description: "Deploying and operating yard-management: what runs on the k3s cluster, Helm chart, probes, migrations, Kafka topics and consumer groups, the outbox relay, dead letters, sweeps and routine procedures."
---

# Runbook

Status: **built on develop, registered in warehouse-infra develop and applied to the
k3s cluster at `v0.1.2` (api and relay only)**. Read-only on 2026-11-10 (`kubectl get`
and `kubectl logs`, context `k3s-multipass`): the Argo CD application `yard-management`
is Synced and Healthy and the `yard-management` and `yard-management-relay` pods are
Ready with 0 restarts. See [What runs on the k3s cluster](#what-runs-on-the-k3s-cluster)
for the evidence and for what is **not** verified (consumer lag, topic contents). The
sections below describe what the chart and the binaries do; on k3s the MCP server, the
analytics projector and reports, and the sweep CronJobs are switched off. The packaging
checklist for the first release is [Before the first release](/docs/operations/before-first-release).

The platform runs on one k3s cluster with **one** Kafka broker; kind and the
docker-compose Kafka are retired. All variables are in
[Configuration](/docs/operations/configuration).

## What runs on the k3s cluster

Read-only on 2026-11-10 (about 20:55 in UTC-03:00), context `k3s-multipass`, namespace
`warehouse-systems`. Nothing was applied, exec'd or port-forwarded from this page.

| Fact | Value read |
| --- | --- |
| Argo CD application `yard-management` (namespace `argocd`) | Sync `Synced`, health `Healthy`; last operation `Succeeded` at 2026-11-10T03:11:50Z. Chart from this repository, `targetRevision: develop`, synced at `226de2b`; values from `warehouse-infra` develop (`helm-values/_platform.yaml`, `helm-values/yard-management.yaml`, `helm-values/kind-resources/yard-management.yaml`). |
| Deployments and pods | `yard-management` 1/1 (pod `yard-management-7576f566cd-db8mn`) and `yard-management-relay` 1/1 (pod `yard-management-relay-55d57f45b5-drgpm`): both Running, Ready, 0 restarts, age about 20 h. The relay container runs `/app/relay`. |
| Image running | `ghcr.io/iqvo/warehouse-infra/yard-management:da1e30e-arm64` in both pods. `da1e30e` is the commit of this repository's tag `v0.1.2` (`gh release list -R IQVO/yard-management`: `v0.1.2`, Latest, 2026-11-10; the tag ref resolves to the same commit). The pin is `terraform/targets/k3s.images.yaml` in `warehouse-infra` develop, set by commit `05fd28b` "chore(images): pin dock-scheduling and yard-management at v0.1.2" (#200). |
| Service and route | Service `yard-management` ClusterIP, port 80 to 8080. HTTPRoute `yard-management` for `/api/yard-management` (prefix stripped) on the `warehouse-gateway`, accepted by the Kong controller. |
| Secrets | `yard-management-db` and `yard-management-analytics-db` exist. |
| Consumer groups in the ConfigMap | `yard-management-dock-scheduling`, `yard-management-facility`, `yard-management-inbound-receiving`. |

**Components.** The api (REST and the three Kafka consumers) and the relay run. The rest
is switched off by `warehouse-infra` commit `89c9592` ("reduced footprint on k3s",
2026-11-09, because the agent had about three free pod slots): `mcp.enabled: false`,
`analytics.enabled: false` (no `yard-projector`, no `yard-reports`) and
`sweeps.enabled: false`. So on the cluster there is no `yard-management-mcp`,
`-projector` or `-reports` pod, no `yard-management` CronJob (`sweep-dwell`,
`sweep-move-leases`), and no Kong route for MCP or reports. The relay keeps writing both
topics, but nothing consumes `warehouse.yard-management.analytics` while the projector is
off, and dwell breaches and expired move leases are only handled when someone calls the
sweeps by hand (see [Sweeps](#sweeps)). The flags go back to `true` in one
`warehouse-infra` PR when capacity is freed.

**Edges.** Both ends of every edge except `outbound-shipping` run in the cluster, and the
own-projector edge has no consumer; see [Edge status](/docs/ecosystem/integration#edge-status).

**Not verified.** Consumer group lag and committed offsets and the list of topics on the
broker (they need `kubectl exec`, which was not used); whether the dashboard and the six
alert rules are loaded in the observability stack. The page claims no health beyond pod
Ready, restart count and the log lines below.

**What the logs showed.** `kubectl logs --tail=50`, read 2026-11-10 about 20:54 in
UTC-03:00 (23:54Z):

- api: boot lines `consumer starting` for `dock-scheduling consumer`
  (`warehouse.dock-scheduling.events`), `inbound-receiving consumer`
  (`warehouse.inbound-receiving.events`) and `facility consumer`
  (`warehouse.facility.events`), then `http server listening` (03:11:55Z).
- api, 09:19Z to 09:43Z and 23:21Z: repeated `kafka reader error; retrying`, for example
  `the kafka reader got an unknown error reading partition 5 of warehouse.dock-scheduling.events at offset 0: read tcp 10.42.0.61:35432->10.42.0.232:9092: i/o timeout`
  (2026-11-10T09:19:11Z), on the dock-scheduling, inbound-receiving and facility topics.
- api: `readiness: dependency is not ready`, dependency `kafka`: `kafka metadata: ask kafka.warehouse-systems.svc.cluster.local:9092: read tcp 10.42.0.61:51418->10.43.25.132:9092: i/o timeout`
  (09:43:36Z) and `lookup kafka.warehouse-systems.svc.cluster.local: i/o timeout` (23:21:14Z).
  `kubectl get events` shows matching `Readiness probe failed ... /readyz` Warning events
  about 35 minutes before the read; the pod's Ready condition was `True` when read.
- relay: **no log line at all** (`kubectl logs` returned 0 lines). That fits the code at
  `da1e30e` (`cmd/relay/main.go` logs only a configuration error, a stop and the relay's
  own error lines, nothing at boot or on a healthy pass), so it shows no relay error since
  start, not that it publishes: the relay has no `/readyz`, and its `/healthz` is what keeps
  it Ready ([Probes](#probes-and-what-readiness-waits-for)). Delivery was not checked.

The retrying lines are the consumer loop doing its job; they are reported as found, not
diagnosed. Whether the groups caught up afterwards was not checked.

## Registration in warehouse-infra

Registered by `warehouse-infra` PR 193 (commit `b4b827e`, "register dock-scheduling and
yard-management on k3s"); the image was pinned by commit `c42c475`. Read from
`origin/develop`:

| Item | State in `warehouse-infra` develop |
| --- | --- |
| `terraform/apps.yaml` | Entry `yard-management`: `kind: service`, `repo_owner: IQVO`, `database: true`, `analytics: true` (the default key pair `ANALYTICS_DATABASE_URL` and `ANALYTICS_READER_DATABASE_URL`, the second one read by the reports and the api), no web part. |
| Image | `terraform/targets/k3s.images.yaml` pins `yard-management` at tag `da1e30e-arm64`, the commit of this repository's `v0.1.2` tag (commit `05fd28b`, #200; it replaced the `v0.1.1` pin `b106934-arm64` of commit `c42c475`). |
| Helm values | `helm-values/yard-management.yaml` sets three stable consumer groups: `yard-management-dock-scheduling`, `yard-management-facility`, `yard-management-inbound-receiving`; the projector group `yard-management-analytics`; `existingSecret` wiring for `yard-management-db` and `yard-management-analytics-db`; the relay Deployment on (`relay.enabled: true`); and, since commit `89c9592` (2026-11-09, reduced footprint), `mcp.enabled: false`, `analytics.enabled: false` and `sweeps.enabled: false` (the chart default is the two CronJobs, `/dwell-sweeps` every 5 minutes and `/move-lease-sweeps` every minute; they do not run on k3s now); Kong plugins `cors`, `opentelemetry` and `prometheus` on; no A2A block. |
| Kong routes | The values declare `/api/yard-management` (prefix stripped), `/api/mcp/yard-management` (to `/mcp`) and `/api/yard-management/reports` (to `/reports`). Only the first exists in the cluster (`kubectl get httproute -A`, 2026-11-10): the MCP and reports routes render with their components, which are off. |
| Observability | One dashboard and six alert rules: see [Observability](/docs/operations/observability#dashboards-and-alerts). |
| CI | `warehouse-infra` lints the chart and runs its selector check for this service. |
| Kafka topics | `docs/kafka-topics-yard-dock.md` in `warehouse-infra` says the yard-domain topics must be pre-created, idempotently, **before the first sync** (`scripts/tf.sh app yard-management apply`): a kafka-go group consumer started before its topic exists is never handed the partition. Whether the topics were pre-created, and what they hold now, was not checked (a listing needs `kubectl exec`); the api log of 2026-11-10 names partitions of `warehouse.dock-scheduling.events`, `warehouse.inbound-receiving.events` and `warehouse.facility.events`, so the broker answered for them. Topics: `warehouse.yard-management.events`, `warehouse.yard-management.analytics`, `warehouse.dock-scheduling.events`, `warehouse.dock-scheduling.analytics`, `warehouse.facility.events`, `warehouse.inbound-receiving.events`, `warehouse.process-path-management.events`. |
| Cluster | Applied: Argo application Synced and Healthy, api and relay pods Ready (see [What runs on the k3s cluster](#what-runs-on-the-k3s-cluster)). Every integration edge except `outbound-shipping` and the own projector has both ends running (see [Integration](/docs/ecosystem/integration#edge-status)). |

The ADR bodies in `docs/adr/` are not edited by this status update: any sentence in
them about the platform state says what it said when it was written.

## Components

One image (`Dockerfile` loops over `cmd/*`) carries five binaries. The chart
(`charts/yard-management`) picks one per Deployment with
`command: ["/app/<dirname>"]`; the image `ENTRYPOINT` is `./api`.

| Component | Binary | Default in chart | Port | Probes | Scale |
| --- | --- | --- | --- | --- | --- |
| api | `/app/api` | on | 8080 (Service port 80) | `/healthz` startup + liveness, `/readyz` readiness | HPA optional (`autoscaling.api`), safe at N>1 |
| relay | `/app/relay` | on (`relay.enabled`) | 8081 | `/healthz` | 1 is enough, N>1 is safe (`FOR UPDATE SKIP LOCKED`) |
| mcp | `/app/mcp` | **off** (`mcp.enabled`) | 8090 | `/healthz`, `/readyz` | fixed |
| projector | `/app/yard-projector` | **off** (`analytics.enabled`) | 8091 (no Service) | `/healthz`, `/readyz` | fixed, one consumer group |
| reports | `/app/yard-reports` | **off** (`analytics.enabled`) | 8092 (Service port 80) | `/healthz`, `/readyz` | fixed |
| sweeps | CronJobs using `wget` | on (`sweeps.enabled`) | n/a | n/a | n/a |

On the k3s cluster (read 2026-11-10) only the api and the relay run; `mcp`, `analytics` and
`sweeps` are switched off by `warehouse-infra`
([What runs on the k3s cluster](#what-runs-on-the-k3s-cluster)).

`cmd/api` and `cmd/mcp` only **enqueue** outbox rows. Without the relay no
event ever leaves the service, which is why the relay is on by default.

## Deploying with Helm

```text
helm lint charts/yard-management --values charts/yard-management/ci/default-values.yaml
bash charts/yard-management/tests/render_test.sh
```

The chart **refuses to render** without:

- `database.url` or `database.existingSecret` (`DATABASE_URL` is always a
  `secretKeyRef`, never in the ConfigMap);
- `kafka.brokers`;
- the three consumer groups `config.dockSchedulingConsumerGroup`,
  `config.facilityConsumerGroup`, `config.inboundReceivingConsumerGroup`;
- `analytics.url` or `analytics.existingSecret` when `analytics.enabled`;
- `terminationGracePeriodSeconds` of at least 20 for the api;
- a sweep retry budget that fits inside `sweeps.activeDeadlineSeconds`.

Consumer group ids are **stable chart values**, never derived from a pod name:
a rescheduled pod resumes from its committed offsets.

Edge exposure is chart-owned: `gatewayApi.enabled` renders the Kong HTTPRoute
for the REST API, `mcp.route` exposes MCP at `/api/mcp/yard-management`,
`analytics.reports.route` exposes the reports at `/api/yard-management/reports`.
No Kong auth plugin exists (REST, MCP and reports are unauthenticated
fleet-wide; the boundary is the cluster).

## Probes and what readiness waits for

- `/healthz` is liveness only and always 200 while the process runs.
- `/readyz` of the api and the MCP server is 200 only while the process is not
  draining, Postgres answers a ping **and** a Kafka broker answers a topic-less
  metadata request. The verdict is cached for 3 s and bounded to 2 s. A failing
  dependency answers 503 `application/problem+json` of type
  `dependency-unavailable` naming it; a draining process answers 503
  `{"status":"not_ready"}`.
- The projector's `/readyz` checks the analytical Postgres and Kafka.
- `yard-reports`' `/readyz` reads the analytics freshness table, so it is
  **not ready until the projector has created the schema**.
- The relay has no `/readyz`.

The chart gives the api a startup budget of about 60 s (`startupProbe`,
period 2 s, 30 failures). The Postgres dial and the migrations at boot are
wrapped in a bounded retry (6 attempts, doubling from 500 ms).

## Migrations

- **OLTP**: `migrations/0001_yard-management` (all tables) and
  `0002_idempotency_keys`, embedded in the binary. `cmd/api` **and** `cmd/mcp`
  run them at boot (golang-migrate with an advisory lock, so concurrent starts
  are safe) over `MIGRATIONS_DATABASE_URL` when set, else `DATABASE_URL`. Use a
  direct session-mode connection for the former when `DATABASE_URL` goes
  through PgBouncer. The relay does **not** migrate; its poll fails and is
  retried until the schema exists.
- **Analytics**: `analytics/migrations/0001_yard_analytics` and
  `0002_kpi_standard`, run by `cmd/yard-projector` at boot into the `analytics`
  schema (its migrations table lives there too). The projector is the only
  writer; the schema may be in a separate database from the OLTP one.

## Kafka topics and consumer groups

| Topic | Direction | Group / writer | Notes |
| --- | --- | --- | --- |
| `warehouse.yard-management.events` | produced | relay | eleven integration types, key = aggregate id, 8 partitions |
| `warehouse.yard-management.analytics` | produced | relay | the same eleven types (same id) plus ten analytics-only types |
| `warehouse.yard-management.events.dlq` | produced | relay | rows the encoder can never encode, verbatim, header `dlq-reason` |
| `warehouse.dock-scheduling.events` | consumed | `DOCK_SCHEDULING_CONSUMER_GROUP` (api) | appointments, door closures |
| `warehouse.facility.events` | consumed | `FACILITY_CONSUMER_GROUP` (api) | zones, location slots |
| `warehouse.inbound-receiving.events` | consumed | `INBOUND_RECEIVING_CONSUMER_GROUP` (api) | ASNs, receipts |
| `warehouse.yard-management.analytics` | consumed | `ANALYTICS_CONSUMER_GROUP` (projector) | own topic only |
| `<consumed topic>.dlq` | produced by consumers | n/a | poison messages of a handled type, with `x-dlq-source-topic` and `x-dlq-cause` headers |

Consumer behaviour (`internal/adapters/inbound/kafka`): one message at a time,
offset committed only after the handler returned; a transient failure retries
the **same** message with capped exponential backoff (200 ms doubling to 5 s)
forever, so a transiently unhandleable message blocks its partition and is
logged at ERROR on every attempt; a poison message of a handled type is moved
to the dead-letter topic and the offset moves past it; a message that is not a
valid CloudEvent is logged at WARN and skipped; a type the consumer has no
handler for is ignored. Effects are idempotent: the CloudEvents `id` is claimed
in `processed_events` in the same transaction as the effect. A new consumer
group starts from the **first** offset. A consumer that dies takes the api
process down (a dead consumer would silently stop feeding the local copies), so
the pod restarts.

Every writer of the service (the relay's and the consumers' dead-letter
writers) sets `AllowAutoTopicCreation`, matching the fleet broker that creates
business topics on first write. `outboundkafka.Topics()` lists the three
produced topics (`events`, `analytics`, `events.dlq`) for an environment that
pre-creates them; the integration tests create them with those names.

## Outbox relay

The relay polls `outbox` (index `idx_outbox_due`), claims up to 100 due rows per
pass under `FOR UPDATE SKIP LOCKED`, walks them in id order and:

- encodes and sends the row, then deletes it in the same transaction
  (at-least-once; a crash between send and commit republishes the same
  CloudEvents `id`);
- dead-letters a row the encoder refuses (malformed payload, unknown event) to
  `warehouse.yard-management.events.dlq` and deletes it (WARN log
  `outbox relay: dead-lettered a row the encoder refuses…`);
- on a send failure records `attempts`, `last_error` and a pushed-back
  `next_attempt_at` (exponential from the poll interval, capped at 5 min),
  stops the pass at the failed row and logs WARN;
- after `OUTBOX_MAX_ATTEMPTS` (20) failures excludes the row from claims and
  logs ERROR `outbox relay: row reached the attempt cap…`. The row **stays** in
  `outbox`. After fixing the cause, re-arm it:

```sql
UPDATE outbox SET attempts = 0, next_attempt_at = now() WHERE id = <id>;
```

A full batch triggers an immediate next pass. On SIGTERM the pass in flight
ends, what it already relayed is committed and the sink and pool close.

## Sweeps

Nothing in the service calls its sweeps on a timer; Kubernetes CronJobs do
(`sweeps-cronjob.yaml`):

| CronJob | Schedule | Call | Effect |
| --- | --- | --- | --- |
| `sweep-dwell` | `*/5 * * * *` | `POST /dwell-sweeps` | raises `TrailerDwellExceeded` once per trailer and threshold crossing (thresholds per trailer kind in the site's yard policy) |
| `sweep-move-leases` | `* * * * *` | `POST /move-lease-sweeps` | returns every `CLAIMED` hostler move whose lease ran out to `REQUESTED` (default lease 300 s) |

On the k3s cluster both are switched off (`warehouse-infra` commit `89c9592`; no
`yard-management` CronJob exists, read 2026-11-10): nothing raises
`TrailerDwellExceeded` or returns expired move leases unless the endpoints are called by
hand.

Both use the service image's `wget` with an empty JSON body, are naturally
idempotent (the `Idempotency-Key` is optional on them), `concurrencyPolicy:
Forbid`, `backoffLimit: 0`, and retry up to 4 times within a 120 s active
deadline. A failed Job exits non-zero and keeps its logs (3 failed Jobs are
kept). To run one by hand:

```text
curl -X POST http://<api-host>/dwell-sweeps -H 'Content-Type: application/json' -d '{}'
curl -X POST http://<api-host>/dwell-sweeps -H 'Content-Type: application/json' -d '{"siteId":"WH1"}'
```

## Routine procedures

| Task | How |
| --- | --- |
| Roll out a new version | `helm upgrade`; the api flips `/readyz` to 503, waits the drain delay (5 s), drains HTTP (10 s), stops consumers (their in-flight message stays uncommitted and is redelivered), closes the pool |
| Check outbox backlog | `SELECT count(*), min(created_at), max(attempts) FROM outbox;` rows with `attempts >= 20` are stuck |
| Re-arm a stuck outbox row | the `UPDATE outbox …` statement above, after fixing the cause (see [Troubleshooting](/docs/operations/troubleshooting)) |
| Inspect a dead letter | consume `warehouse.yard-management.events.dlq` or `<topic>.dlq`; read `dlq-reason` or `x-dlq-cause`; the value is the original message, replayable |
| Backfill analytics | start the projector with a **new** `ANALYTICS_CONSUMER_GROUP`; it replays the analytics topic from the earliest retained offset into `analytics.*` |
| Check analytics freshness | `GET /reports/freshness` (on `yard-reports`, or on the api when `ANALYTICS_READER_DATABASE_URL` is set) |
| Run a sweep now | the `curl` calls above |
| Change a site's policy | `PUT /sites/{siteId}/yard-policy` with `If-Match` of the version read from `GET` |

## What is not here

`warehouse-infra` carries a dashboard and alert rules for this service (registered in
infra; whether they are loaded in the observability stack was not checked: see [Observability](/docs/operations/observability#dashboards-and-alerts)),
but the binaries expose no metrics of their own, so the dashboard and rules read
Kong, kube-state-metrics and logs only.
