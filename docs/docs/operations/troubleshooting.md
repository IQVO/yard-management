---
id: troubleshooting
title: Troubleshooting
sidebar_label: Troubleshooting
sidebar_position: 4
description: "Symptom, cause, check, fix for the real failure modes of yard-management: boot errors, readiness, the outbox, consumers, dead letters, reports and API refusals."
---

# Troubleshooting

Every row is derived from a failure path in the code. Logs are JSON on stdout;
the messages quoted are the stable substrings
([Observability](/docs/operations/observability)).

## Boot and readiness

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| api exits 1 at once with `invalid configuration:` and a list | A mandatory variable is missing. One error line per variable: `DATABASE_URL is required`, `KAFKA_BROKERS is required`, `<…>_CONSUMER_GROUP is required` | the lines name every missing variable | set them ([Configuration](/docs/operations/configuration)); in Helm the chart refuses to render without them |
| api or mcp exits with `SHUTDOWN_DRAIN_DELAY "x" is not a non-negative duration` | unparsable drain delay | the message names the value | use a Go duration (`5s`, `0`) |
| api or mcp logs `boot step failed; retrying` `step=run migrations` or `ping postgres`, then exits | Postgres unreachable, wrong DSN or credentials, or a sidecar not yet ready (6 attempts, doubling from 500 ms) | the `error` field of the last attempt | fix the DSN / network; if `DATABASE_URL` is behind PgBouncer set a direct `MIGRATIONS_DATABASE_URL` |
| api exits `listen on :8080: …` | port in use | `HTTP_ADDR` | change it |
| `/readyz` answers 503 `{"status":"not_ready"}` | the process is draining after SIGTERM | pod is terminating | expected; wait for the replacement |
| `/readyz` answers 503 problem `dependency-unavailable` | Postgres ping or the Kafka topic-less metadata request failed (cached 3 s, bounded 2 s) | the problem names the dependency; WARN `readiness: dependency is not ready` has `error` | restore the broker/database; with one broker, a single broker restart makes every pod unready |
| api pod restarts with `a consumer died; shutting down` | a Kafka reader failed unrecoverably; a dead consumer would silently stop feeding the local copies, so the process exits | the ERROR line gives `<consumer> died` and the reader error | fix Kafka connectivity; the pod restarts and resumes from the committed offsets |
| relay exits with code 2 `DATABASE_URL is required` / `KAFKA_BROKERS is required` / `OUTBOX_POLL_INTERVAL … is not a positive duration` | relay configuration | exit code and the first log line | fix the variable |
| relay logs `outbox relay pass failed` repeatedly right after a fresh install | the relay does not migrate; the `outbox` table does not exist yet | whether the api or mcp ever started against this database | start `cmd/api` (or `cmd/mcp`) once; the relay's next poll succeeds |
| `yard-reports` never becomes ready | it reads the analytics freshness table; the projector has not created the `analytics` schema | `/readyz` 503 naming `analytics`; projector logs | start `cmd/yard-projector` with a direct `ANALYTICS_DATABASE_URL`; check its boot error |
| yard-projector exits at boot naming `ANALYTICS_DATABASE_URL` | the analytical migrations or ping failed (`run analytics migrations on …`, `open analytics pool on …`, `ping …`) | the wrapped error | use a direct DSN of a role allowed to create the `analytics` schema |
| projector exits `analytics consumer stopped unexpectedly` | the consumer loop returned (reader failure); a projector that consumes nothing must not keep answering probes | the wrapped error | fix Kafka; the pod restarts |

## Events are not leaving (outbox)

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Writes succeed but no event appears on `warehouse.yard-management.events`, `outbox` grows | no relay running (`relay.enabled: false`), or the relay cannot reach Kafka | `SELECT count(*), min(created_at) FROM outbox;`; relay logs; `kubectl get deploy` for the relay | enable and start the relay; fix broker reachability |
| WARN `outbox relay: send failed, stopping this pass at the failed row` | broker down or rejected the write; the row backs off 1 s, 2 s, 4 s … up to 5 min and later rows wait behind it | `outbox.attempts`, `outbox.last_error` | restore Kafka; the relay recovers by itself |
| ERROR `outbox relay: row reached the attempt cap and is excluded from future claims` | the row failed `OUTBOX_MAX_ATTEMPTS` (20) times; later rows now overtake it (per-key order is not guaranteed past a stuck row, ADR 0004) | log fields `outbox_id`, `topic`, `event_type`, `key`, `error` | fix the cause (topic ACL, size limit, broker), then `UPDATE outbox SET attempts = 0, next_attempt_at = now() WHERE id = <outbox_id>;` |
| WARN `outbox relay: dead-lettered a row the encoder refuses` | the row's payload is not valid JSON, has no data, or names an event outside the published catalogue (`encoder.go`, `published`) | the message on `warehouse.yard-management.events.dlq`, header `dlq-reason` | a code bug: the row is already deleted from `outbox`; replay from the DLQ value after fixing the producer |
| Analytics topic has the event, the integration topic does not | by design: ten event types are analytics-only (`VisitCheckedOut`, `TrailerParked`, `DoorWorkStarted`, `TrailerHeld`, `TrailerHoldReleased`, five `YardMove*`) | [Events](/docs/api-reference/events) | none |
| the same event arrives twice | at-least-once: a crash between send and commit republishes the row with the same CloudEvents `id` | duplicate `id` | consumers dedupe on `id` (this service does so in `processed_events`) |

## Consumers

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| ERROR `<consumer> handling failed; retrying the same message` with growing `attempt` | a **transient** failure (database down, lock timeout). The same message is retried forever with backoff 200 ms to 5 s and **blocks its partition** | `error`, `partition`, `offset` | fix the database; no skip exists by design, so a message is never silently lost |
| Local copies (locations, appointments) stay empty | the consumer group is new and the topic is empty, the group id changed (it starts from the **first** offset, so it replays), or the upstream never published | consumer-group lag on the broker; `event consumed` INFO lines | check the group env value is the stable one; check upstream |
| WARN `skipping a message that is not a valid CloudEvent` | a producer wrote a non-CloudEvents message on the topic | the `topic` field | fix the producer; the message is committed past (it cannot be dead-lettered: its type is unknowable) |
| Messages appear on `<topic>.dlq` | poison message of a handled type: payload does not decode, blank natural id, no `version`, no `window` (ERROR `message can never be applied`) | headers `x-dlq-source-topic`, `x-dlq-cause`; the value is the original message | fix the producer; replay the value |
| A consumed event has no effect | its CloudEvents `type` has no handler here (ignored) or its `id` was already claimed in `processed_events` | the message `type` against the consumed types in [Integration](/docs/ecosystem/integration) | none; or delete the `processed_events` row only if a deliberate replay is needed |
| A check-in refuses an appointment that exists | the appointment copy is stale or missing (`unknown-appointment`) or its door is closed | `appointments`, `door_closures` tables | wait for or replay the dock-scheduling events |

## API refusals

Errors are `application/problem+json`; the slug is the last segment of `type`
(`https://errors.yard-management.warehouse-systems.dev/<slug>`).

| Symptom | Cause | Fix |
| --- | --- | --- |
| 400 `idempotency-key-required` / `idempotency-key-invalid` | a `POST`/`PUT` without the header, or a key that is not 1-128 printable ASCII characters | send a unique key per logical request (not needed on the two sweeps) |
| 409 `idempotency-key-reused` | the same key with another method, path or body | use a new key; to retry, send the identical request |
| 409 `concurrent-modification` | the aggregate was changed by another request (version guard) | re-read and retry |
| 412 `precondition-failed` on `PUT /sites/{siteId}/yard-policy` | `If-Match` is not the policy's current version | `GET` the policy, send its ETag |
| 400 `invalid-if-match` | `If-Match` is not an entity tag of a policy version | send the ETag returned by `GET` |
| 400 `unknown-query-parameter`, `invalid-limit`, `invalid-cursor`, `invalid-state` | list endpoints reject unknown parameters, bad `limit`, a cursor not produced by the service, a state outside the enum | fix the query |
| 400 `invalid-report-range`, `invalid-threshold-minutes` | `from`/`to` must be RFC 3339, `to` after `from`, at most 92 days; `thresholdMinutes` an integer from 1 to 10080 | fix the query |
| 409 `visit-already-open` | an open visit exists for that plate at the site (unique index `uq_yard_visits_one_open_per_plate`) | check the visit out first |
| 409 `appointment-required` | the site's yard policy has `requireAppointment` | send a booked `appointmentId` or change the policy |
| 409 `no-free-spot` | no free Yard slot for the trailer (also when the facility feed has not loaded the site) | free a spot, or load the layout |
| 409 `location-occupied`, `destination-reserved`, `trailer-has-open-move` | a spot or door holds a trailer or is reserved by an open move; one open move per trailer (partial unique indexes) | wait or choose another destination |
| 409 `door-closed`, `door-flow-mismatch` | a door closure copy covers it; the door's flow (Inbound/Outbound/Both) does not match the trailer | pick another door or wait for `DoorClosureLifted` |
| 409 `trailer-on-hold` / `trailer-not-on-hold` | move or release of a held trailer / release of one not held | release the hold first |
| 409 `door-work-open`, `trailer-not-released`, `seal-required` | check-out of a trailer whose door work is not `DONE`, that still has an open move, or a `LOADED` outbound trailer without a seal | finish the work, close the move, record the seal (`POST /trailers/{trailerId}/seals`) |
| 409 `hostler-busy`, `not-lease-holder`, `lease-not-expired` | a hostler can hold one claimed move; only the lease holder completes it | complete or cancel the current move |
| 404 `no-claimable-move` | `POST /moves/claims` found no `REQUESTED` hostler move at the site | expected when idle |
| a `CLAIMED` move is not released after the lease | the lease sweep did not run (the CronJob is the only scheduler) | `kubectl get cronjob`; run `POST /move-lease-sweeps` by hand; the default lease is 300 s |
| no `TrailerDwellExceeded` events | the dwell sweep did not run, or the site's policy has no `dwellThresholdMinutes` for that trailer kind | the CronJob, `GET /sites/{siteId}/yard-policy` |
| 422 `unknown-location`, `unknown-appointment`, `invalid-carrier`, `validation`, `unexplained-destination` | a value or reference the model refuses; locations and appointments are event-fed copies | fix the body; check the copies are loaded |
| 400 on a body containing a NUL character | U+0000 in the path, query or body is refused uniformly (it would be a Postgres error otherwise) | remove it |
| 500 `internal-error` | an unexpected error; the cause is only in the log (`request failed`, `method`, `path`, `error`) | find the ERROR line |
| `GET /reports/*` on the **api** returns rows empty | `ANALYTICS_READER_DATABASE_URL` is unset on the api, so it answers the contract shape with no rows | set it, or call `yard-reports`; the MCP `get_yard_dwell_report` tool always answers no rows (the reader is not wired in `cmd/mcp`) |
| `/reports/*` rows are stale | the projector is behind or down | `GET /reports/freshness`; projector logs and lag of its group |

## MCP

| Symptom | Cause | Fix |
| --- | --- | --- |
| mcp exits `DATABASE_URL is required` / `KAFKA_BROKERS is required` | both are mandatory although the binary starts no consumer: writes enqueue outbox rows and `/readyz` reports the broker | set them |
| MCP writes succeed but no event is published | no relay is running | start `cmd/relay` |
| a tool returns an `internal-error` result | unexpected error, logged as `mcp tool failed with an unexpected error` | read the log |
