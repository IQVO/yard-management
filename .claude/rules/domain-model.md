---
paths:
  - "internal/domain/**"
  - "internal/application/**"
  - "features/**"
---

# Domain model: ubiquitous language, aggregates, events, use cases

Source of truth: `docs/adr/0002-aggregates-invariants-and-assumptions.md` (packages, states, error names,
numbered assumptions). This file is the skimmable summary; when they differ, the ADR wins.

## Ubiquitous Language (use these exact names)

- **YardVisit** (`visit-<uuid>`): one pass through the gate. `CHECKED_IN`, `CHECKED_OUT`, `REJECTED`. At most one
  open visit per tractor plate per site; checks out only when every trailer it takes is released and every
  loaded outbound trailer carries a seal.
- **Trailer** (`trl-<uuid>`): `PARKED`, `IN_MOVE`, `DOCKED`, `DEPARTED`. On site it is at exactly ONE place: one
  spot, one door, or one open move. Natural key `(scac, unit number)` is unique among trailers on site.
- **YardMove** (`mv-<uuid>`): `REQUESTED`, `CLAIMED`, `COMPLETED`, `CANCELLED`. Hostler moves are PULLED
  (claim-next with a lease), never pushed; the destination is reserved when the move is requested.
  A VISITING_DRIVER move (created at the gate) has no lease and cannot expire or be cancelled.
- **YardCheck** (`yck-<uuid>`): `OPEN`, `CLOSED`; one open check per site; closing only reports
  (MISSING, WRONG_LOCATION, UNEXPECTED).
- **YardPolicy** (`siteId`): appointment requirement, seal-mismatch action (FLAG or REJECT), lease seconds,
  holding spots, dwell thresholds, detention free time.
- **Location**: a facility-layout location slot (`locationCode`), a Yard spot or a Dock door; event-fed local
  copy, never looked up live.

## Aggregates

Pure Go: clock and ids are passed in, `Snapshot()`/`Restore()` are the persistence boundary, `version` starts at
1 and grows by one per accepted change; the use case pulls the events and hands them to the outbox in the
same unit of work. Packages: `internal/domain/yardvisit`, `trailer` (with the domain service `Occupancy`),
`yardmove`, `yardcheck`, `yardpolicy`, `shared`.

Key errors: `ErrLocationOccupied` (409, `Occupancy.CheckFree` under a per-location advisory lock plus a
partial unique index), `ErrDoorClosed`, `ErrDoorFlowMismatch`, `ErrTrailerOnHold`, `ErrDoorWorkOpen`,
`ErrTrailerHasOpenMove`, `ErrDestinationReserved`, `ErrHostlerBusy`, `ErrNotLeaseHolder`, `ErrVisitAlreadyOpen`,
`ErrTrailerNotReleased`, `ErrSealRequired`, `shared.ErrIllegalTransition`. Problem slug = kebab-case of the name
without `Err`.

## Domain events

Eleven integration events on `warehouse.yard-management.events` (VisitCheckedIn, VisitRejected, TrailerDocked,
TrailerUndocked, DoorWorkCompleted, TrailerDeparted, SealExceptionRaised, TrailerDwellExceeded,
YardCheckClosed, TrailerSecured, TrailerSecuringReleased), mirrored on `.analytics`; ten more are analytics-only. Catalogue:
`docs/adr/0004-cloudevents-envelope-and-type-catalogue.md`, refined by ADR 0010 and ADR 0013.

## Key use cases (`internal/application/usecases`, type `Yard`)

- Gate: `CheckInVisit` (seal check, direct-on-check-in placement), `RejectAtGate`, `CheckOutVisit`.
- Door: `StartDoorWork`, `CompleteDoorWork`; holds and seals: `PlaceHold`, `ReleaseHold`, `RecordSeal`.
- Moves: `RequestMove`, `ClaimNextMove`, `CompleteMove`, `CancelMove`, `SweepMoveLeases`.
- Audit and policy: `OpenYardCheck`, `RecordObservation`, `CloseYardCheck`, `DeclareYardPolicy`, `SweepDwell`.
- Local copies from consumers: `ApplyAppointment`, `ApplySlot`, `ApplyZone`, `DecommissionSlot`, `ScheduleClosure`,
  `LiftClosure`, `RegisterAsn`, `CancelAsn`, `OpenReceipt`, `CloseReceipt`.
