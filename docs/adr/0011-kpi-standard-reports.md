# ADR 0011: Yard KPI standard v1: turn time, detention exposure and exception rates

## Status

Accepted (2026-11-09). Refines 0006. Additive: the OLTP API, the integration topic `warehouse.yard-management.events` and every published
payload are unchanged. Code: `internal/analytics/report` (`kpi.go`), `internal/adapters/inbound/kafka/analytics_consumer.go`,
`internal/adapters/outbound/analyticsstore` (`projection_kpi.go`, `reader_kpi.go`), `analytics/migrations/0002_kpi_standard`,
`internal/adapters/inbound/http/reports.go`.

## Context

The yard had no written definition of its key figures. Industry has none either: the US Department of Transportation Office of
Inspector General and the Federal Motor Carrier Safety Administration both say there is no standard definition of detention, and a
dwell of more than two hours is the accepted proxy. Without a definition, two reports of "turn time" or "detention" can disagree
without anyone being wrong. We define each figure once, here and in `internal/analytics/report`, and report them from the analytics
read side of ADR 0006. This ADR does not add a source of data: it folds five more published types and adds reports over them.

## Decision

### 1. The definitions

| KPI | Definition |
|---|---|
| Gate-to-gate turn time | Minutes from `VisitCheckedIn.arrived_at` to `VisitCheckedOut.departed_at`, which is the `on_site_minutes` the event carries. Per visit **purpose** (DROP, LIVE_UNLOAD, LIVE_LOAD, PICKUP, BOBTAIL). Over the visits that checked **out** inside the window. A visit whose check-in the projection never saw has no purpose and is left out. |
| Percentiles | Every duration report gives p50 (the median) and p90 next to the average and the maximum. An average hides the long tail: three visits of 10 minutes and one of 190 average 55, which describes none of them. |
| Wait versus processing | Wait is arrival to the first `DoorWorkStarted` (already `avgDriverWaitMinutes`). Processing is `DoorWorkStarted` to `DoorWorkCompleted`, averaged over the completed door work sessions of the trailers in the row (`avgProcessingMinutes`). An unfinished session is not counted. |
| Detention exposure (proxy) | Among the trailers that **departed** inside the window, those whose `dwell_minutes` is **over** `thresholdMinutes` (strictly: a dwell equal to the threshold does not exceed it). Per trailer kind: `trailers`, `exceeding`, `share` (exceeding over trailers, 0..1, null when there are no trailers) and `excessMinutes` (the sum of dwell minus threshold over the exceeding trailers). |
| Default threshold | **120 minutes**, the two hour norm. The query parameter `thresholdMinutes` is an integer from 1 to 10080 (one week); anything else is `400 invalid-threshold-minutes`. The response echoes the threshold used. |
| Exception rates | Per site, over the window: `visitsCheckedIn` (by arrival), `visitsRejected` (by rejection time), `rejectionRate` = rejected / (checkedIn + rejected); `sealExceptions` and `sealExceptionRate` = seal exceptions per check-in; `holdsByReason` for DAMAGED, SEAL_BROKEN, NOT_FOUND, QUALITY and SECURITY (all five always listed); `yardChecksClosed` and the total `discrepancies`. A rate whose denominator is zero is null. |

Detention exposure is **exposure, not billing**. It says how much of the yard's traffic is at risk of a detention claim under a
two hour norm. It makes no statement about free time agreed with a carrier, and charging detention is out of scope.

The seal exception rate is not capped at 1: a seal exception raised at the gate of a visit that was then rejected (the payload has no
trailer, the CloudEvents subject is the visit) counts in the numerator although that visit never checked in.

### 2. Facts from the contract

Dispatch is on the full type string, the payloads are the snake_case `data` of `apis/asyncapi.yaml`, decoded tolerantly. The
contract decides where the instructions that led to this ADR were silent:

- `VisitCheckedIn.purpose` is recorded (blank tolerated for a payload that predates it, an unknown value is dead-lettered).
- `VisitCheckedOut` carries `departed_at` and `on_site_minutes` but **no purpose**: the purpose and the arrival come from the
  check-in of the same visit, joined by `visit_id` in `analytics.visit_facts`. Either event may arrive first.
- `YardCheckClosed.discrepancies` is a **list** of findings (kind, location, trailer), not a count. The projection stores its length.
- `TrailerHeld` carries no instant: a hold happened when its event was published, so `held_at` is the CloudEvents `time`. A trailer
  can be held many times; the key is the trailer and the aggregate `version` after the hold.
- `SealExceptionRaised.trailer_id` is null when the visit was rejected at the gate; the subject is then the visit id.

### 3. The model (`analytics/migrations/0002_kpi_standard`, additive)

Facts, never aggregates, upserted by natural key with the same `LEAST`/`COALESCE` rules as 0001, so arrival order across keys never
matters and a replay converges:

- `analytics.visit_facts(visit_id pk, site_id, purpose, arrived_at, departed_at, on_site_minutes)`: the earliest arrival and the
  earliest departure win; a departure carries its own `on_site_minutes`.
- `analytics.gate_exceptions(kind, subject_id, occurred_at, site_id, reason)`: VISIT_REJECTED and SEAL_EXCEPTION rows.
- `analytics.trailer_holds(trailer_id, version, site_id, reason, held_at)`.
- `analytics.yard_check_results(yard_check_id pk, site_id, closed_at, discrepancies)`.

The migration repeats the reader-role grant block of 0001. The projector folds **twelve** kinds now (the seven of ADR 0006 plus
`VisitCheckedOut`, `VisitRejected`, `SealExceptionRaised`, `TrailerHeld`, `YardCheckClosed`), each inside the same dedupe
transaction. The rest of the published types are still acknowledged and ignored.

### 4. The reports

All four take `from`, `to` (RFC 3339, `to` after `from`, at most 92 days) and an optional `siteId`, are read-only, answer RFC 7807
errors and, with no read side wired, the contract shape with no rows.

| route | rows |
|---|---|
| `GET /reports/yard-dwell` | existing columns plus `p50DwellMinutes`, `p90DwellMinutes`, `avgProcessingMinutes` (additive; null when there is no sample) |
| `GET /reports/turn-time` | per purpose: `visits`, `avgMinutes`, `p50Minutes`, `p90Minutes`, `maxMinutes` |
| `GET /reports/detention-exposure` | per trailer kind: `trailers`, `exceeding`, `share`, `excessMinutes`; body also `thresholdMinutes` |
| `GET /reports/exceptions` | per site: the exception rates above |

**The one deviation from "SQL only sums and averages" (ADR 0006 section 4):** the median and the 90th percentile are computed by
Postgres with `percentile_cont` (linear interpolation between neighbouring samples). Moving every sample to the application to rank
them would transfer up to 92 days of rows for no gain in correctness. Everything else stays in `internal/analytics/report`, which is
pure: the shares and rates (`Share`, `Ratio`, `RejectionRate`, `SealExceptionRate`) and the threshold bounds.

`cmd/yard-reports` and `cmd/api` (when `ANALYTICS_READER_DATABASE_URL` is set) serve the new routes. The MCP report tool is not
changed: its code is untouched, but it shares the dwell row type, so the three additive dwell fields appear in its output schema.

## Consequences

- One written definition per KPI; a figure that disagrees with this ADR is a bug in the figure.
- Turn time and the purpose split are only as complete as the retained history: visits whose check-in was before the stream
  existed are left out until the topic is replayed from the earliest offset (the same backfill as ADR 0006).
- Percentiles over small windows are noisy: a row with a few visits shows its `visits` count so the reader can judge.
- A new `TrailerHeld` reason or visit purpose in the contract is dead-lettered until the enum here is extended, the same rule as the
  trailer kind.

## Alternatives considered and rejected

- **Report averages only**: cheap and misleading; the long tail is the cost.
- **Compute percentiles in Go**: correct but needs every sample over the wire; `percentile_cont` is the standard aggregate.
- **Bill detention in the service**: needs carrier agreements and free-time rules this context does not own.
- **Derive turn time from `TrailerDeparted`**: a visit can bring several trailers and leave without some of them; the visit is the
  unit the gate measures.
