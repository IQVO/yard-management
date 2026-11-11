---
id: use-cases
title: Use Cases
sidebar_label: Use Cases
description: "One section per Yard Management command use case, with its trigger, inbound adapter, preconditions, main flow, errors mapped to HTTP problems, events and idempotency."
---

# Use Cases


Every use case is a method of `usecases.Yard` (`internal/application/usecases`), one per REST route,
MCP write tool or Kafka event it serves. This page has one section per **command** of the five
aggregates (`YardVisit`, `Trailer`, `YardMove`, `YardCheck`, `YardPolicy`): where a command is
executed inside a larger use case (`RecordArrival`, `StartMoving`, `ArriveAt`, `Depart` run inside
`CheckInVisit`, `ClaimNextMove`, `CompleteMove` and `CheckOutVisit`) the section says so. The runtime
sequence of the significant ones is on [Sequence Diagrams](/docs/ddd/sequence-diagrams). Reads
(`ListVisits`, `GetVisit`, `ListTrailers`, `GetTrailer`, `ListMoves`, `GetYardBoard`,
`GetYardPolicy`) are query methods in `queries.go` and `checks_policy.go`: they read the
repositories and views and raise nothing.

| Section | `Yard` method (file) | Trigger |
| --- | --- | --- |
| CheckInVisit | `CheckInVisit` (`gate_checkin.go`) | `POST /visits` |
| RejectAtGate | `RejectAtGate` (`gate_checkin.go`) | `POST /visit-rejections` |
| CheckOutVisit | `CheckOutVisit` (`gate_checkout.go`) | `POST /visits/{visitId}/check-outs` |
| StartDoorWork, CompleteDoorWork | `StartDoorWork`, `CompleteDoorWork` (`doors.go`) | `POST /trailers/{trailerId}/door-work-starts`, `…/door-work-completions`; `CompleteDoorWork` also from `ReceiptClosed` |
| RecordSeal, PlaceHold, ReleaseHold | `RecordSeal`, `PlaceHold`, `ReleaseHold` (`doors.go`) | `POST /trailers/{trailerId}/seals`, `…/holds`, `…/hold-releases` |
| DwellSweep | `SweepDwell` (`checks_policy.go`) | `POST /dwell-sweeps` (CronJob) |
| RequestMove, ClaimNextMove, CompleteMove, CancelMove | `RequestMove`, `ClaimNextMove`, `CompleteMove`, `CancelMove` (`moves.go`) | `POST /moves`, `/moves/claims`, `/moves/{moveId}/completions`, `/moves/{moveId}/cancellations` |
| ExpireLease | `SweepMoveLeases` (`moves.go`) | `POST /move-lease-sweeps` (CronJob) |
| OpenYardCheck, RecordObservation, CloseYardCheck | `OpenYardCheck`, `RecordObservation`, `CloseYardCheck` (`checks_policy.go`) | `POST /yard-checks`, `…/observations`, `…/closures` |
| DeclareYardPolicy | `DeclareYardPolicy` (`checks_policy.go`) | `PUT /sites/{siteId}/yard-policy` |
| Event-driven reactions | `ApplyZone`, `ApplySlot`, `DecommissionSlot`, `ApplyAppointment`, `ScheduleClosure`, `LiftClosure`, `RegisterAsn`, `CancelAsn`, `OpenReceipt`, `CloseReceipt` (`consumers.go`) | the three Kafka consumers |

Source: `internal/application/usecases/*.go`, `internal/adapters/inbound/http/server.go`, `internal/adapters/inbound/mcp/tools_write.go`.

## Conventions for every use case

- **One transaction.** The aggregate changes, their outbox rows and (for a
  Kafka-driven use case) the inbox row commit together. A use case that touches two
  aggregates (`ClaimNextMove`: move and trailer; `CompleteMove`: move and trailer;
  `CheckInVisit`: visit, trailers and first move) does so in one unit of work, as the
  model's `Trailer.notes` states for `StartMoving` and `ArriveAt`.
- **Errors** are `application/problem+json`. The model assigns an HTTP status only to
  four generic errors:

  | Error | HTTP | Problem `type` slug |
  | --- | --- | --- |
  | `ErrNotFound` | 404 | `not-found` |
  | `ErrIllegalTransition` | 409 | `illegal-transition` |
  | `ErrConcurrentModification` | 409 | `concurrent-modification` |
  | `ErrValidation` | 422 | `validation` |

- **Domain errors** (`ErrLocationOccupied`, `ErrAppointmentRequired`, ...) have a name
  in the model and, at first, no status. As built (decision D-10, service ADR 0002): a conflict with current
  state (occupancy, ownership, release, an open check) is **409**; a value-object or
  reference rule (`ErrInvalidCarrier`, `ErrUnknownLocation`,
  `ErrUnexplainedDestination`) is **422**; a malformed request is **400**. The problem `type` slug is the
  kebab case of the name without `Err` (`ErrLocationOccupied` becomes
  `location-occupied`). Errors added as built: `ErrNoFreeSpot` (409), `ErrTrailerNotOnHold` (409),
  `ErrLeaseNotExpired`.
- **Idempotency.** Every state-changing `POST` and `PUT` takes an `Idempotency-Key`
  header; a replay with the same key returns the first response (`idempotency_keys`); sweeps are
  naturally idempotent. Mutable aggregates carry a `version`; a `GET` of one aggregate returns a weak ETag of
  it, `PUT /sites/{siteId}/yard-policy` honours `If-Match`, and a stale write is `ErrConcurrentModification`. Every
  Kafka consumer is idempotent on the CloudEvents `id` (inbox).
- **Kafka-driven errors** have no HTTP status. A policy refusal is logged and counted,
  the event is marked processed, and nothing is retried; an infrastructure failure
  rolls back and the message is redelivered.
- **Where the model was silent** the flow first said *proposed* or *this page's reading* and
  named the gap. The decisions that closed the gaps (Y-1 to Y-14, X1 to X7; see the
  [Model Gap Register](https://iqvo.github.io/warehouse-docs/yard/strategic-design/model-gap-register)) are now stated in the flows; what remains
  marked *this page's reading* is a detail the service does not state.

## YardVisit

### CheckInVisit

- **Actor / trigger:** gate clerk or kiosk (a camera or ANPR adapter issues the same
  command).
- **Inbound adapter:** REST `POST /visits`. Body (OpenAPI `checkInVisit`): `siteId`, `carrier`,
  `carrierScac?`, `tractorPlate`, `driverRef?`, `purpose`, `appointmentId?`, `trailers` (the trailers in).
- **Preconditions:** none. A site with no declared `YardPolicy` answers the defaults (`require_appointment` false,
  `seal_mismatch_action` `FLAG`, `lease_seconds` 300).
- **Main flow:**
  1. Read the site's `YardPolicy` (`require_appointment`, `holding_spot_codes`,
     `parking_zone_ids`, `seal_mismatch_action`).
  2. Validate the carrier; refuse a second `CHECKED_IN` visit for the plate.
  3. If the site requires appointments, the named appointment must be `BOOKED` for this
     site in `AppointmentView`.
  4. Create `YardVisit` in `CHECKED_IN`.
  5. For each trailer brought in, `RecordArrival` (below).
  6. `SealCheckAtGate`: compare a recorded seal with the appointment's
     `expected_seal_number`; a match ends the seal `VERIFIED`, a mismatch ends it `MISMATCH` and raises `SealExceptionRaised`, and under
     `REJECT` the visit is recorded `REJECTED` instead, with `VisitRejected(SEAL_MISMATCH)` and a
     `SealExceptionRaised` that has no trailer (decision Y-5).
  7. `DirectOnCheckIn` and `VisitingDriverMove`: for each trailer in, create the first
     move, `CLAIMED`, performer `VISITING_DRIVER`, no lease. Destination: a door when the
     appointment's door is free, not closed and flow-compatible (reason `TO_DOOR`); the
     holding spot when it is a walk-in or the arrival is earlier than the tolerance
     (`TO_HOLDING`); otherwise the first free spot of the parking zones by location code.
  8. Save and write `VisitCheckedIn` to the outbox, one transaction.

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| `ErrInvalidCarrier` | 422 | `invalid-carrier` |
| `ErrVisitAlreadyOpen` | 409 | `visit-already-open` |
| `ErrAppointmentRequired` | 409 | `appointment-required` |
| `ErrTrailerAlreadyOnSite` (from `RecordArrival`) | 409 | `trailer-already-on-site` |
| `ErrSealClassTooLow` (site `minimumSealClass`; ADR 0012) | 409 | `seal-class-too-low` |
| `ErrDestinationReserved` (first move) | 409 | `destination-reserved` |
| `ErrInvalidUnitNumber` (ISO 6346 check; ADR 0012) | 422 | `invalid-unit-number` |
| `ErrInvalidSealClass` (ADR 0012) | 422 | `invalid-seal-class` |
| `ErrValidation` | 422 | `validation` |

- **Events emitted:** `VisitCheckedIn`; `SealExceptionRaised` when the seal mismatches;
  `VisitRejected` under `REJECT`; analytics move events for the first move.
- **Idempotency:** `Idempotency-Key`; `ErrVisitAlreadyOpen` is the natural second guard.
- **Settled gaps:** the arrival tolerance comes from the appointment copy (X1); a move to a plain spot has the
  reason `TO_SPOT` (Y-4); with no free spot the check-in fails with `ErrNoFreeSpot` (Y-12); the first move has no
  origin, it comes from the gate (Y-1). A walk-in or an arrival earlier than the tolerance goes to the first
  free holding spot of the policy, or to the first free spot when none is declared (assumption A17).

### RejectAtGate

- **Actor / trigger:** gate clerk or kiosk, when a visit must not enter.
- **Inbound adapter:** REST `POST /visit-rejections`. Body (OpenAPI `rejectAtGate`): `siteId`, `carrier`,
  `carrierScac?`, `tractorPlate`, `appointmentId?`, `reason` (a `RejectionReason`).
- **Preconditions:** none (the rejected visit never became `CHECKED_IN`).
- **Main flow:**
  1. Validate the carrier and the reason.
  2. Create `YardVisit` in `REJECTED`; no trailer is recorded on site.
  3. Save and write `VisitRejected` to the outbox, one transaction.

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| `ErrInvalidCarrier` | 422 | `invalid-carrier` |
| `ErrValidation` (unknown reason) | 422 | `validation` |

- **Events emitted:** `VisitRejected` (consumed by `dock-scheduling`, which cancels the appointment with
  `TURNED_AWAY`, and by `exception-management`).
- **Idempotency:** `Idempotency-Key`.
- **Settled gap:** a refused `CheckInVisit` creates nothing, `RejectAtGate` is the one command that records a
  rejection, and the `REJECT` seal outcome uses it (decision Y-5).

### CheckOutVisit

- **Actor / trigger:** gate clerk or kiosk.
- **Inbound adapter:** REST `POST /visits/{visitId}/check-outs`. Body (OpenAPI `checkOutVisit`): `trailersOut`,
  naming only the trailer ids (decision Y-9).
- **Preconditions:** the visit is `CHECKED_IN`.
- **Main flow:**
  1. Load the visit and the trailers it takes out.
  2. Every trailer is on site and released: no open move, and either `PARKED` or `DOCKED` with door work
     `DONE` (a live visit leaves from its door; decision Y-2).
  3. Every `LOADED` trailer taken out of a `LIVE_LOAD` or `PICKUP` visit carries a seal (`ErrSealRequired`).
  4. `Depart` for each trailer taken out (decision Y-6).
  5. Transition `CHECKED_IN → CHECKED_OUT`, compute `on_site_minutes`.
  6. Save and write `VisitCheckedOut` (analytics) and, per trailer, `TrailerDeparted`
     (integration and analytics since ADR 0010) and `TrailerUndocked` (when it left a door), one transaction.

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| `ErrTrailerNotReleased` | 409 | `trailer-not-released` |
| `ErrSealRequired` | 409 | `seal-required` |
| `ErrDoorWorkOpen` (from `Depart`) | 409 | `door-work-open` |
| `ErrIllegalTransition` | 409 | `illegal-transition` |
| `ErrNotFound` | 404 | `not-found` |
| `ErrConcurrentModification` | 409 | `concurrent-modification` |

- **Events emitted:** `VisitCheckedOut` (analytics only), `TrailerDeparted` (integration, read by
  `outbound-shipping`), `TrailerUndocked`.
- **Idempotency:** `Idempotency-Key`; a visit already `CHECKED_OUT` is returned
  unchanged (this page's reading).

## Trailer

### RecordArrival

- **Actor / trigger:** `CheckInVisit`, once per trailer in. No route of its own.
- **Inbound adapter:** internal (called by the visit use case).
- **Preconditions:** the natural key `(scac, number)` is not already on site.
- **Main flow:**
  1. Create `Trailer` with `UnitNumber`, `TrailerKind`, `LoadState`, `Contents`
     (`asn_numbers` from the appointment and `AsnContentView`, or `outbound_ref`),
     `Seal` and `ArrivedAt` (the dwell clock start).
  2. The trailer is created `IN_MOVE` together with its first move, created `CLAIMED` (decision Y-1);
     `appointment_id` is set from the visit.
  3. Save.

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| `ErrTrailerAlreadyOnSite` | 409 | `trailer-already-on-site` |
| `ErrValidation` | 422 | `validation` |

- **Events emitted:** none of its own (`VisitCheckedIn` carries the trailers).
- **Idempotency:** the natural key; the visit's `Idempotency-Key`.

### StartMoving

- **Actor / trigger:** the `ClaimNextMove` use case (and, for a visiting driver's move,
  its creation), in the same unit of work. Not an inbound message.
- **Preconditions:** the trailer is `PARKED` or `DOCKED`.
- **Main flow:**
  1. Transition to `IN_MOVE`; the place (spot or door) is released (decision Y-8).
  2. When it left a door: raise `TrailerUndocked` (decision Y-6).

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| `ErrIllegalTransition` | 409 | `illegal-transition` |

- **Events emitted:** `TrailerUndocked` when leaving a door (reading).
- **Idempotency:** none of its own; the claim's.

### ArriveAt

- **Actor / trigger:** the `CompleteMove` use case, in the same unit of work.
- **Preconditions:** the trailer is `IN_MOVE`.
- **Main flow:**
  1. The location is a known, active slot of the trailer's site (`LocationView`).
  2. For a door: not under a closure (`DoorClosureView`), flow allows the trailer's
     direction (`LocationView.dockFlow`), the trailer is not on hold.
  3. `Occupancy.CheckFree` on the location under a per-location advisory lock (a partial
     unique index is the backstop).
  4. Transition to `PARKED` (spot) or `DOCKED` (door); at a door, `DoorWork` starts
     `WAITING`.
  5. Raise `TrailerParked` or `TrailerDocked`.

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| `ErrUnknownLocation` | 422 | `unknown-location` |
| `ErrDoorClosed` | 409 | `door-closed` |
| `ErrDoorFlowMismatch` | 409 | `door-flow-mismatch` |
| `ErrTrailerOnHold` | 409 | `trailer-on-hold` |
| `ErrLocationOccupied` | 409 | `location-occupied` |
| `ErrIllegalTransition` | 409 | `illegal-transition` |

- **Events emitted:** `TrailerParked` (analytics) or `TrailerDocked` (integration).
- **Idempotency:** the completion's.
- **Settled gaps:** the destination is also validated when the move is requested, and an occupied location
  at arrival is refused with the move staying `CLAIMED` (decision Y-8); an `EMPTY` trailer is unconstrained
  by door flow unless the visit purpose is `LIVE_LOAD` or `PICKUP` (decision Y-9).

### StartDoorWork

- **Actor / trigger:** dock operator (floor client).
- **Inbound adapter:** REST `POST /trailers/{trailerId}/door-work-starts`.
- **Preconditions:** the trailer is `DOCKED` and its door work is `WAITING`.
- **Main flow:**
  1. Load the trailer.
  2. Set `DoorWork` to `IN_PROGRESS`.
  3. Save and write `DoorWorkStarted` (`started_at`) to the outbox. The driver wait KPI
     is arrival to this instant.

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| `ErrNotFound` | 404 | `not-found` |
| `ErrIllegalTransition` (not `DOCKED`) | 409 | `illegal-transition` |
| `ErrConcurrentModification` | 409 | `concurrent-modification` |

- **Events emitted:** `DoorWorkStarted` (analytics only).
- **Idempotency:** `Idempotency-Key`; door work already `IN_PROGRESS` is returned
  unchanged (this page's reading). A held trailer's door work does not start (`ErrTrailerOnHold`).

### CompleteDoorWork

- **Actor / trigger:** dock operator; policy `AutoCompleteUnload` on `ReceiptClosed`.
- **Inbound adapter:** REST `POST /trailers/{trailerId}/door-work-completions` (body: the
  resulting `loadState`, optional); Kafka consumer of
  `warehouse.inbound-receiving.events` for the policy.
- **Preconditions:** the trailer is `DOCKED`.
- **Main flow:**
  1. Load the trailer with its `version`.
  2. Set `DoorWork` to `DONE` and record the `load_state` (`EMPTY` for the policy).
  3. Save and write `DoorWorkCompleted` to the outbox.
  4. `ClearDoorAfterWork`: a `DROP` trailer gets a `HOSTLER` move `FROM_DOOR` to a free
     spot (`RequestMove`); a `LIVE` trailer stays until its visit checks out. How the
     trailer knows it is a drop is the visit's purpose.

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| `ErrNotFound` | 404 | `not-found` |
| `ErrIllegalTransition` (not `DOCKED`) | 409 | `illegal-transition` |
| `ErrConcurrentModification` | 409 | `concurrent-modification` |

- **Events emitted:** `DoorWorkCompleted` (integration since 2026-11-09, read by `outbound-shipping`).
- **Idempotency:** `Idempotency-Key`, inbox for the policy; completing `DONE` work is
  a no-op.
- **Settled gap:** `AutoCompleteUnload` joins through `receipt_id` from `ReceiptOpened` and completes door work
  (from `WAITING` or `IN_PROGRESS`) once every ASN on the trailer has a closed receipt (decision Y-7).

### RecordSeal

- **Actor / trigger:** yard supervisor.
- **Inbound adapter:** REST `POST /trailers/{trailerId}/seals` (body: seal number).
- **Preconditions:** the trailer is on site.
- **Main flow:**
  1. Set `Seal{number, INTACT}` (or `APPLIED`), with the optional ISO 17712 class; a class below the site's
     `minimum_seal_class` is `ErrSealClassTooLow` (409 `seal-class-too-low`), an unknown one
     `ErrInvalidSealClass` (422 `invalid-seal-class`).
  2. At check-in, if the trailer's appointment has an `expected_seal_number`: equal ends the seal `VERIFIED`, different
     ends it `MISMATCH` and raises `SealExceptionRaised` (`SealCheckAtGate`; the comparison happens when the seal arrives,
     decision Y-13; ADR 0012).
  3. Save with the version check.

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| `ErrNotFound` | 404 | `not-found` |
| `ErrValidation` | 422 | `validation` |
| `ErrIllegalTransition` (`DEPARTED`) | 409 | `illegal-transition` |
| `ErrConcurrentModification` | 409 | `concurrent-modification` |

- **Events emitted:** `SealExceptionRaised` (consumed by `exception-management`) on
  mismatch; none otherwise.
- **Idempotency:** `Idempotency-Key`.

### BreakSeal

- **Actor / trigger:** `RecordSeal` with state `BROKEN` (`POST /trailers/{trailerId}/seals`); no route of its own.
- **Inbound adapter:** the seal route (body: `state`, `sealNumber?`).
- **Preconditions:** the trailer is on site.
- **Main flow:** set `Seal.state` to `BROKEN`, place a `SEAL_BROKEN` hold and save (decision Y-6, assumption A10).
- **Errors:** `ErrNotFound`, `ErrIllegalTransition`.
- **Events emitted:** `TrailerHeld` (analytics only).
- **Idempotency:** a seal already `BROKEN` is unchanged.

### PlaceHold

- **Actor / trigger:** yard supervisor.
- **Inbound adapter:** REST `POST /trailers/{trailerId}/holds` (body: `reason`, a
  `HoldReason`).
- **Preconditions:** the trailer is on site and not already held.
- **Main flow:**
  1. Set `Hold{reason}`.
  2. Save and write `TrailerHeld` to the outbox. A held trailer cannot be docked
     (`ErrTrailerOnHold`); a hold on an already `DOCKED` trailer is allowed and door work then
     waits for the release (decision Y-13).

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| `ErrNotFound` | 404 | `not-found` |
| `ErrValidation` (unknown reason) | 422 | `validation` |
| `ErrIllegalTransition` (`DEPARTED`) | 409 | `illegal-transition` |
| `ErrConcurrentModification` | 409 | `concurrent-modification` |

- **Events emitted:** `TrailerHeld` (analytics only).
- **Idempotency:** `Idempotency-Key`.

### ReleaseHold

- **Actor / trigger:** yard supervisor.
- **Inbound adapter:** REST `POST /trailers/{trailerId}/hold-releases`.
- **Preconditions:** the trailer is on hold.
- **Main flow:** clear the hold; save; write `TrailerHoldReleased`.

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| `ErrNotFound` | 404 | `not-found` |
| `ErrIllegalTransition` (not held) | 409 | `illegal-transition` |
| `ErrConcurrentModification` | 409 | `concurrent-modification` |

- **Events emitted:** `TrailerHoldReleased` (analytics only).
- **Idempotency:** `Idempotency-Key`; a trailer not held is returned unchanged
  (this page's reading).

### RecordLoadState

- **Actor / trigger:** `DoorWorkCompleted` carries a `load_state`, and `AutoCompleteUnload` sets `EMPTY`.
- **Inbound adapter:** none of its own; the door-work completion body takes an optional `loadState`.
- **Main flow:** set `LoadState` (`EMPTY`, `LOADED` or `PARTIAL`); save. It is a step of `CompleteDoorWork`.
- **Errors:** `ErrNotFound`, `ErrValidation`.
- **Events emitted:** none of its own.
- **Idempotency:** setting the same value is a no-op.

### Depart

- **Actor / trigger:** `CheckOutVisit` calls it for each trailer taken out.
- **Inbound adapter:** none of its own (no route).
- **Preconditions:** the trailer is `PARKED` or `DOCKED`; if `DOCKED`, its door work is
  `DONE`.
- **Main flow:**
  1. Transition to `DEPARTED`; compute `dwell_minutes` from `ArrivedAt`.
  2. Save; write `TrailerDeparted` and, when it left a door, `TrailerUndocked`.

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| `ErrDoorWorkOpen` | 409 | `door-work-open` |
| `ErrIllegalTransition` (`IN_MOVE` or already `DEPARTED`) | 409 | `illegal-transition` |

- **Events emitted:** `TrailerDeparted` (analytics), `TrailerUndocked` (integration, for
  a trailer leaving a door; reading).
- **Idempotency:** the visit's.

### DwellSweep

- **Actor / trigger:** scheduler (cron), policy `DwellSweep`.
- **Inbound adapter:** REST `POST /dwell-sweeps`.
- **Preconditions:** none.
- **Main flow:**
  1. For each trailer on site, compare its dwell (now minus `ArrivedAt`) with the
     `dwell_threshold_minutes` of its `TrailerKind` in the site's `YardPolicy`.
  2. For each trailer that crossed its threshold and has not been reported for it, write
     `TrailerDwellExceeded` (`dwell_minutes`, `threshold_minutes`, `location_code`).
  3. Return the count raised.

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| a missing threshold for a kind (no `Err...`) | — | skipped, logged |

- **Events emitted:** `TrailerDwellExceeded` (consumed by `exception-management`).
- **Idempotency:** once per trailer and threshold crossing, so a rerun raises nothing new;
  the marker is `dwell_alerted_at` on the trailer (decision Y-13). A kind without a threshold
  is never alerted.

## YardMove

### RequestMove

- **Actor / trigger:** yard supervisor; policies `DirectOnCheckIn`, `CallToDoor`,
  `ClearDoorAfterWork`.
- **Inbound adapter:** REST `POST /moves` (body, OpenAPI `requestMove`: `trailerId`, `to`,
  `reason?`, `priorityBand?`, `dueBy?`); the policies call the use case in-process.
- **Preconditions:** the trailer is on site and not `IN_MOVE` with another move.
- **Main flow:**
  1. Load the trailer; `from_location` is its current place.
  2. Refuse if the trailer has another `REQUESTED` or `CLAIMED` move.
  3. Under the destination's advisory lock: the destination is free
     (`Occupancy.CheckFree`) and no other open move names it; reserve it.
  4. Create `YardMove` in `REQUESTED`, performer `HOSTLER`.
  5. Save and write `YardMoveRequested` to the outbox.

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| `ErrTrailerHasOpenMove` | 409 | `trailer-has-open-move` |
| `ErrDestinationReserved` | 409 | `destination-reserved` |
| `ErrNotFound` (trailer) | 404 | `not-found` |
| `ErrValidation` | 422 | `validation` |

- **Events emitted:** `YardMoveRequested` (analytics only).
- **Idempotency:** `Idempotency-Key`; the open-move invariant is the guard.
- **Settled gap:** the destination (known slot, closure, flow, hold) is validated at request and again at arrival
  (decision Y-8). A missing `reason` is derived: `TO_DOOR` for a door, `FROM_DOOR` for a spot when the trailer is
  `DOCKED`, `RELOCATE` otherwise (assumption A6).

### ClaimNextMove

- **Actor / trigger:** hostler (floor client).
- **Inbound adapter:** REST `POST /moves/claims` (body, OpenAPI `claimNextMove`: `siteId`, `hostlerRef`).
- **Preconditions:** the hostler holds no `CLAIMED` move.
- **Main flow:**
  1. Refuse if the hostler already holds a `CLAIMED` move.
  2. Pick the next `REQUESTED` `HOSTLER` move of the site: lowest priority band, then earliest `due_by`
     (none last), then request time (decision Y-11), skipping rows locked by another claim.
  3. Transition `REQUESTED → CLAIMED` with `Lease{hostler_ref, expires_at}`, where
     `expires_at` = now plus the site's `lease_seconds`.
  4. `Trailer.StartMoving` in the same unit of work.
  5. Save and write `YardMoveClaimed`.

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| `ErrHostlerBusy` | 409 | `hostler-busy` |
| `ErrConcurrentModification` (another hostler won) | 409 | `concurrent-modification` |
| no move available | 404 | `no-claimable-move` |
| `ErrValidation` | 422 | `validation` |

- **Events emitted:** `YardMoveClaimed` (analytics only).
- **Idempotency:** `Idempotency-Key`; a replay returns the same claim.

### CompleteMove

- **Actor / trigger:** the lease-holding hostler; for a `VISITING_DRIVER` move the dock
  operator or gate clerk (same route; decision Y-3).
- **Inbound adapter:** REST `POST /moves/{moveId}/completions` (body, OpenAPI `completeMove`:
  `hostlerRef?`, `actualLocation?`, `deviationNote?`).
- **Preconditions:** the move is `CLAIMED`.
- **Main flow:**
  1. Load the move with its `version`.
  2. Only the lease holder completes it. A `VISITING_DRIVER` move has no lease.
  3. A location other than the destination needs a reason.
  4. `Trailer.ArriveAt(actual_location)`, with its checks and `Occupancy.CheckFree`, in
     the same unit of work.
  5. Transition `CLAIMED → COMPLETED`; compute `duration_seconds`.
  6. Save and write `YardMoveCompleted` and the trailer's `TrailerParked` or
     `TrailerDocked`.

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| `ErrNotLeaseHolder` | 409 | `not-lease-holder` |
| `ErrUnexplainedDestination` | 422 | `unexplained-destination` |
| `ErrLocationOccupied`, `ErrDoorClosed`, `ErrDoorFlowMismatch`, `ErrTrailerOnHold`, `ErrUnknownLocation` (from `ArriveAt`) | 409 / 422 | see `ArriveAt` |
| `ErrIllegalTransition` (not `CLAIMED`, for example after lease expiry) | 409 | `illegal-transition` |
| `ErrNotFound` | 404 | `not-found` |
| `ErrConcurrentModification` | 409 | `concurrent-modification` |

- **Events emitted:** `YardMoveCompleted` (analytics), `TrailerParked` or
  `TrailerDocked`.
- **Idempotency:** `Idempotency-Key`; a `COMPLETED` move is returned unchanged.

### CancelMove

- **Actor / trigger:** yard supervisor; policy `NoShowFreesHolding` on
  `DockAppointmentNoShow` or `DockAppointmentCancelled`.
- **Inbound adapter:** REST `POST /moves/{moveId}/cancellations` (body: `reason`);
  Kafka consumer of `warehouse.dock-scheduling.events` for the policy.
- **Preconditions:** the move is `REQUESTED` or `CLAIMED`.
- **Main flow:**
  1. Load the move.
  2. Transition to `CANCELLED`; release the destination reservation.
  3. Save and write `YardMoveCancelled`.

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| `ErrIllegalTransition` (`COMPLETED` or already `CANCELLED`) | 409 | `illegal-transition` |
| `ErrNotFound` | 404 | `not-found` |
| `ErrConcurrentModification` | 409 | `concurrent-modification` |

- **Events emitted:** `YardMoveCancelled` (analytics only).
- **Idempotency:** `Idempotency-Key`, inbox for the policy; a `CANCELLED` move is
  returned unchanged.
- **Settled gaps:** cancelling a `CLAIMED` hostler move runs `ReturnToOrigin` in the same unit of work, so the
  trailer goes back where it was (decision Y-1); the policy finds the move through the `appointment_id` the move
  carries (decision X3).

### ExpireLease

- **Actor / trigger:** scheduler (cron), policy `LeaseExpirySweep`.
- **Inbound adapter:** REST `POST /move-lease-sweeps`.
- **Preconditions:** none.
- **Main flow:**
  1. Select `CLAIMED` moves whose `Lease.expires_at` is in the past.
  2. For each, transition `CLAIMED → REQUESTED` and clear the lease; its destination
     stays reserved.
  3. Return the count.

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| a move that changed since selection (no `Err...`) | — | skipped, next sweep |

- **Events emitted:** `YardMoveLeaseExpired` (analytics only, decision Y-10).
- **Idempotency:** rerunning returns the same moves to `REQUESTED` only once.
- **Settled gap:** the trailer goes back where `StartMoving` found it (`ReturnToOrigin`, decision Y-1); when that
  origin has since been taken it stays `IN_MOVE` and the move `REQUESTED` (assumption A16).

## YardCheck

### OpenYardCheck

- **Actor / trigger:** yard supervisor.
- **Inbound adapter:** REST `POST /yard-checks`.
- **Main flow:** refuse if the site has an `OPEN` check; create `YardCheck` in `OPEN`;
  save.

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| `ErrYardCheckOpen` | 409 | `yard-check-open` |
| `ErrValidation` | 422 | `validation` |

- **Events emitted:** none (the model lists only `YardCheckClosed`).
- **Idempotency:** `Idempotency-Key`; `ErrYardCheckOpen` is the natural guard.

### RecordObservation

- **Actor / trigger:** yard supervisor, walking the yard.
- **Inbound adapter:** REST `POST /yard-checks/{yardCheckId}/observations` (body:
  `location_code`, `unit_number?`).
- **Preconditions:** the check is `OPEN`.
- **Main flow:** refuse a location already observed in this check; append the
  `Observation`; save.

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| `ErrLocationAlreadyObserved` | 409 | `location-already-observed` |
| `ErrIllegalTransition` (`CLOSED`) | 409 | `illegal-transition` |
| `ErrNotFound` | 404 | `not-found` |
| `ErrValidation` | 422 | `validation` |

- **Events emitted:** none.
- **Idempotency:** `Idempotency-Key`; the same observation replayed is refused by the
  invariant.

### CloseYardCheck

- **Actor / trigger:** yard supervisor.
- **Inbound adapter:** REST `POST /yard-checks/{yardCheckId}/closures`.
- **Preconditions:** the check is `OPEN`.
- **Main flow:**
  1. Compare the observations with the trailers the model places at those locations.
  2. Build the `Discrepancy` list: `MISSING`, `UNEXPECTED` or `WRONG_LOCATION`. The rules
     are: every observed location is compared with the trailer the model records there; a location not observed is
     `MISSING` when the model records a trailer there; an observed unit that differs makes the expected trailer
     `MISSING` and the observed unit `WRONG_LOCATION` (known elsewhere on site) or `UNEXPECTED` (unknown). Closing only
     reports (decision Y-11, assumption A13).
  3. Transition `OPEN → CLOSED`.
  4. Save and write `YardCheckClosed` with the discrepancies.

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| `ErrIllegalTransition` (`CLOSED`) | 409 | `illegal-transition` |
| `ErrNotFound` | 404 | `not-found` |
| `ErrConcurrentModification` | 409 | `concurrent-modification` |

- **Events emitted:** `YardCheckClosed` (consumed by `exception-management`, one case
  per discrepancy).
- **Idempotency:** `Idempotency-Key`; a `CLOSED` check is returned unchanged.

## YardPolicy

### DeclareYardPolicy

- **Actor / trigger:** yard supervisor.
- **Inbound adapter:** REST `PUT /sites/{siteId}/yard-policy` (If-Match for a change).
- **Main flow:**
  1. Validate: holding spots are known Yard slots of the site (`LocationView`);
     thresholds and lease are positive.
  2. Replace the policy of the site: `require_appointment`, `holding_spot_codes`,
     `parking_zone_ids`, `dwell_threshold_minutes` per kind, `lease_seconds`,
     `seal_mismatch_action`, optional `free_time_minutes`.
  3. Save.

| Error | HTTP | Problem `type` slug |
| --- | --- | --- |
| `ErrUnknownLocation` | 422 | `unknown-location` |
| `ErrValidation` | 422 | `validation` |
| `ErrConcurrentModification` | 409 | `concurrent-modification` |

- **Events emitted:** none (`YardPolicy.events: []`).
- **Idempotency:** `PUT` replaces; the same body is a no-op; `Idempotency-Key`.

## Event-driven reactions that are not commands

These policies react to a Kafka event and call the use cases above; the projectors
update the read models in their own transaction.

| Trigger event | Reaction | Use case called |
| --- | --- | --- |
| `TrailerUndocked` (own) | `CallToDoor` | `RequestMove` (`HOSTLER`, `TO_DOOR`) |
| `DoorWorkCompleted` (own) | `ClearDoorAfterWork` | `RequestMove` (`FROM_DOOR`) for a `DROP` trailer |
| `ReceiptClosed` (`inbound-receiving`) | `AutoCompleteUnload` | `CompleteDoorWork` |
| `DockAppointmentNoShow`, `DockAppointmentCancelled` | `NoShowFreesHolding` | `CancelMove` |
| `DockAppointmentBooked`, `Rescheduled`, `Cancelled`, `NoShow` | projector | `AppointmentView` |
| `DoorClosureScheduled`, `DoorClosureLifted` | projector | `DoorClosureView` |
| `LocationSlotRegistered`, `LocationSlotDecommissioned`, `ZoneRegistered` | projector | `LocationView` |
| `ASNRegistered`, `ASNCancelled` | projector | `AsnContentView` |
| `ReceiptOpened`, `ReceiptClosed` | projector | `ReceiptAtDoorView` |
