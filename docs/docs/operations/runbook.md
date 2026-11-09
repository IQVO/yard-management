---
id: runbook
title: Runbook
sidebar_label: Runbook
sidebar_position: 2
description: "Deploying and operating yard-management: Helm chart, probes, migrations, Kafka topics and consumer groups, the outbox relay, dead letters, sweeps and routine procedures."
---

# Runbook

Status: **built on develop, not deployed**. `yard-management` has no entry in
`warehouse-infra` `terraform/apps.yaml` on `origin/develop`, so no cluster runs
it yet. Everything below describes what the chart and the binaries do; the
packaging checklist for the first release is
[Before the first release](/docs/operations/before-first-release).

The platform runs on one k3s cluster with **one** Kafka broker; kind and the
docker-compose Kafka are retired. All variables are in
[Configuration](/docs/operations/configuration).

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
| `warehouse.yard-management.events` | produced | relay | nine integration types, key = aggregate id, 8 partitions |
| `warehouse.yard-management.analytics` | produced | relay | the same nine types (same id) plus ten analytics-only types |
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

No dashboards, alerts or scrape config for this service exist in
`warehouse-infra` (it is not registered there) and the binaries expose no
metrics; see [Observability](/docs/operations/observability).
