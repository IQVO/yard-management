# ADR 0016: Carrier registry copy and the suspended-carrier rule at the gate

## Status

Accepted (2026-11-12). Refines 0002 (the YardPolicy and YardVisit aggregates gain one lever, one flag and one rejection reason), 0003 (three more
consumed types and one more local copy), 0004 (one additive field on `VisitCheckedIn`, one more `VisitRejected` reason) and 0009 (the dock-scheduling
consumer handles the three types). Additive: a site that does not set the lever is judged exactly as before (section 3). Code:
`internal/application/usecases/carriers.go`, `internal/application/usecases/gate_checkin.go`, `internal/adapters/inbound/kafka/dock_scheduling_consumer.go`,
`internal/adapters/outbound/postgres/carriers.go`, `internal/domain/yardpolicy`, `internal/domain/yardvisit`, `migrations/0007_carriers`.

## Context

This ADR was written retroactively from the code on 2026-11-13. The code and `apis/asyncapi.yaml` already cited "ADR 0016" for the carrier registry copy and the
suspended-carrier rule, and no such file existed (ADR 0017 left the number open for it). Everything below states what `origin/develop` does; the change that
introduced it is PR #21, `feat(carriers): the CarrierView local copy of dock-scheduling's carrier registry`, merged on 2026-11-12.

dock-scheduling publishes the standing of its carriers (registered, suspended, reinstated) on `warehouse.dock-scheduling.events`, the topic this service already
consumes for appointments and door closures (ADR 0003). Before that change the gate had no carrier rule.

## Decision

### 1. The carrier copy is fed by three dock-scheduling types

The existing dock-scheduling consumer (`NewDockSchedulingConsumer`, group `DOCK_SCHEDULING_CONSUMER_GROUP`, topic `warehouse.dock-scheduling.events`) handles three
more full `type` strings, byte-identical to the producer's AsyncAPI (`dock_scheduling_consumer.go`, the handler map in `NewDockSchedulingConsumerForTopic`):

| type | effect on the `carriers` copy (`ports.CarrierFeed`) |
|---|---|
| `com.warehouse.wms.dock-scheduling.carrier.CarrierRegistered` {scac, name, usdot, mc, status, version} | `Register`: the whole snapshot replaces the stored carrier |
| `com.warehouse.wms.dock-scheduling.carrier.CarrierSuspended` {scac, reason, version} | `Suspend`: status becomes `SUSPENDED` with the reason; the stored name and identifiers are kept |
| `com.warehouse.wms.dock-scheduling.carrier.CarrierReinstated` {scac, version} | `Reinstate`: status becomes `ACTIVE` and the reason is cleared |

- `updated_at` is the CloudEvents `time` of the event, or the handling time when the event carries none (`eventTime` in `carriers.go`).
- A payload without a `scac` or with a `version` below 1 is poison: it is dead-lettered to `<topic>.dlq` and never retried (`requireCarrier`, ADR 0009). A null
  `reason`, `usdot` or `mc` decodes as `""`.
- Each fact runs in one unit of work that first claims the CloudEvents id in `processed_events` (`consume` in `consumers.go`), like every other consumed type.
- **Version guard.** Every feed method is a version-guarded upsert (`INSERT ... ON CONFLICT (scac) DO UPDATE ... WHERE EXCLUDED.version > carriers.version`): a
  fact whose version is not higher than the stored one changes nothing, so a replay or a reordering never undoes a newer fact. The in-memory adapter applies the same
  guard. A carrier first seen through `CarrierSuspended` or `CarrierReinstated` is inserted with no name.
- The copy is the table `carriers(scac pk, name, usdot, mc, status ACTIVE|SUSPENDED, reason, version >= 1, updated_at)` (`migrations/0007_carriers.up.sql`),
  read through `ports.Carriers.Get(scac)`.
- The three messages are restated in this repo's `apis/asyncapi.yaml` as `receive` messages on the `dockSchedulingEvents` channel, never imported (ADR 0003). With
  them the service consumes sixteen types: six dock-scheduling appointment and closure types, three carrier types, three facility-layout types and four
  inbound-receiving types.

### 2. The site policy gets a suspended-carrier lever, off by default

`YardPolicy` gains `suspendedCarrierAction`, one of `NONE`, `FLAG` or `REJECT` (`yardpolicy.CarrierAction`), set through `PUT /sites/{siteId}/yard-policy`
(`suspendedCarrierAction`, optional). Left out, it is `NONE`, so replacing the policy without the field turns the rule off again; any other value is refused as a
validation error (`Declare` in `yardpolicy.go`). It is stored in `yard_policies.suspended_carrier_action`, default `'NONE'`.

### 3. The gate rule

`CheckInVisit` judges one SCAC (`gateSCAC`): the `carrierScac` the check-in declares, else the carrier SCAC of the visit's BOOKED appointment. When neither exists
the rule does not apply. The carrier is suspended (`carrierSuspended`) only when the action is `FLAG` or `REJECT`, a SCAC is known and the copy holds that SCAC
with status `SUSPENDED`. With `NONE` or no SCAC the copy is not read; a SCAC the copy does not know is not suspended.

The order inside `checkIn` (`gate_checkin.go`): the open-plate, appointment and unit checks, then the copy is read, then `yardvisit.CheckIn` applies its own
rules (an appointment that is required or unknown refuses first), then the carrier action, then the arrivals and the seal check.

- **`NONE`** (the default): today's behaviour; the visit is admitted as before the rule existed.
- **`FLAG`**: the visit is admitted. It carries `carrierFlag` `SUSPENDED` (`yard_visits.carrier_flag`, `''` for no flag; the visit view's `carrierFlag`; the
  nullable `carrier_flag` of the `VisitCheckedIn` payload, `null` otherwise).
- **`REJECT`**: `rejectSuspendedCarrier` records a REJECTED visit with reason `CARRIER_SUSPENDED` (`yardvisit.RejectAtGate`), saves it and publishes its events
  (`VisitRejected`). No trailer is persisted and the seal check does not run. The use case then answers `yardvisit.ErrCarrierSuspended`, but `checkInVisit`
  commits the unit of work first and returns the refusal afterwards, so the turn-away is on record although the call fails. The REST adapter answers it as a `409`
  problem with slug `carrier-suspended` (`errors.go`), and the check-in counter records `outcome=rejected`, `reason=carrier-suspended` (ADR 0017).

## Consequences

- A site that never sets the lever pays nothing: the copy is not read and every existing row loads with `NONE` and no flag (`0007_carriers.up.sql`).
- The copy is event-fed, so a carrier dock-scheduling has not yet published is treated as not suspended and its visit is admitted.
- Only the check-in's `carrierScac` and the booked appointment's carrier are judged; the optional per-trailer `carrierScac` is not.
- The MCP tool registry carries `suspendedCarrierAction` and `carrierFlag` (`internal/adapters/inbound/mcp/testdata/tool_registry.golden.json`).
