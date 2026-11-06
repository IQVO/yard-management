# ADR 0002: Aggregates, invariants and assumptions

## Status

Accepted (2026-11-05). Source: `model.yaml` context `yard-management` of the yard design instance, plus the orchestrator's
final gap decisions (X1-X7, Y-1..Y-14) which override the model wording where they differ.

## Context

The context owns everything physically on site outside the four walls. Five aggregates carry the rules; every invariant
below has a typed `Err...` in its domain package and a table-driven test. The REST adapter turns each into a problem
document whose slug is the kebab-case of the name (`ErrLocationOccupied` is `location-occupied`); 409 is a conflict with
state, 422 a value or reference rule, 400 a malformed request.

## Decision

### Packages (`internal/domain/...`)

| package | aggregate | root id | states |
|---|---|---|---|
| `yardvisit` | YardVisit | `visit-<uuid>` | CHECKED_IN, CHECKED_OUT, REJECTED |
| `trailer` | Trailer (+ domain service `Occupancy`, placement rules) | `trl-<uuid>` | PARKED, IN_MOVE, DOCKED, DEPARTED |
| `yardmove` | YardMove | `mv-<uuid>` | REQUESTED, CLAIMED, COMPLETED, CANCELLED |
| `yardcheck` | YardCheck | `yck-<uuid>` | OPEN, CLOSED |
| `yardpolicy` | YardPolicy | `siteId` (siteCode) | ACTIVE |
| `shared` | ids, `Location`, `TrailerKind`, `LoadState`, `Purpose`, `SCAC` | | |

Aggregates are pure Go: the clock and ids are passed in, `Snapshot()`/`Restore()` are the persistence boundary
(`Restore` re-validates), `version` is the optimistic-concurrency counter (starts 1, +1 per accepted change), domain events
are collected and pulled by the use case, which hands them to the transactional outbox in the same unit of work.

### YardVisit

- `CheckInVisit` creates a CHECKED_IN visit; `RejectAtGate` creates a REJECTED visit; `CheckOutVisit` is the only
  transition (CHECKED_IN to CHECKED_OUT).
- `ErrInvalidCarrier`: carrier name non-blank, at most 100 characters; scac 2..4 upper-case letters when given (422).
- `ErrVisitAlreadyOpen`: at most one CHECKED_IN visit per tractor plate per site (409; use case + partial unique index).
- `ErrAppointmentRequired`: when the site requires appointments the visit names one and it is BOOKED for this site in the
  local copy (409).
- `ErrUnknownAppointment`: a named appointment exists in the local copy and covers exactly one trailer of the visit (422).
- `ErrTrailerNotReleased`: every trailer taken out is on site and released (409).
- `ErrSealRequired`: every LOADED trailer taken out of a LIVE_LOAD or PICKUP visit carries a seal (409).

### Trailer

- `RecordArrival` creates the trailer IN_MOVE at the gate (its first move is created CLAIMED, see YardMove).
  `StartMoving` (PARKED/DOCKED to IN_MOVE), `ArriveAt` (IN_MOVE to PARKED/DOCKED), `ReturnToOrigin` (IN_MOVE to PARKED/DOCKED
  where `StartMoving` found it), `Depart` (PARKED/DOCKED to DEPARTED), plus door work, seal, hold, load state commands.
- `ErrLocationOccupied` (409): `Occupancy.CheckFree` under a per-location advisory lock, plus a partial unique index on
  `(location_kind, location_code)` for trailers PARKED or DOCKED.
- `ErrUnknownLocation` (422): the location is a known, active Yard (spot) or Dock (door) slot of the trailer's site.
- `ErrDoorClosed` (409): a door under a closure takes no trailer.
- `ErrDoorFlowMismatch` (409): inbound trailers (ASN numbers present) dock only at Inbound or Both doors, outbound trailers
  (outbound reference set, or purpose LIVE_LOAD/PICKUP) only at Outbound or Both doors.
- `ErrTrailerOnHold` (409): a held trailer is not docked and its door work does not start.
- `ErrDoorWorkOpen` (409): a DOCKED trailer departs only when its door work is DONE.
- `ErrTrailerAlreadyOnSite` (409): the natural key `(scac, unit number)` is unique among trailers on site (use case + partial
  unique index).
- Events: TrailerParked, TrailerDocked, DoorWorkStarted, DoorWorkCompleted, TrailerUndocked, TrailerDeparted,
  SealExceptionRaised, TrailerHeld, TrailerHoldReleased, TrailerDwellExceeded.

### YardMove

- `ErrTrailerHasOpenMove` (409), `ErrDestinationReserved` (409; unique index on the open-move destination), `ErrHostlerBusy`
  (409; one CLAIMED move per hostler, unique index), `ErrNotLeaseHolder` (409), `ErrUnexplainedDestination` (422).
- ErrNotLeaseHolder and ErrHostlerBusy apply to HOSTLER moves only; a VISITING_DRIVER move has no lease, no hostler_ref and
  cannot expire or be cancelled.

### YardCheck

- `ErrYardCheckOpen` (409): at most one OPEN check per site (partial unique index). `ErrLocationAlreadyObserved` (409).

### YardPolicy

- `ErrUnknownLocation` (422) for holding spots that are not known Yard slots of the site; `ErrValidation` (422) for
  non-positive thresholds or lease. Defaults (D8): `require_appointment=false`, `seal_mismatch_action=FLAG`,
  `lease_seconds=300`, no detention free time, no dwell thresholds.

### Shared errors

`shared.ErrValidation` (422) and `shared.ErrIllegalTransition` (409, `illegal-transition`) cover shape rules and state-machine
violations that the model does not name; `ErrTrailerNotOnHold`, `ErrNoFreeSpot` (409, Y-12) and `ErrUnknownDoor`-style
lookups are use-case errors listed in the OpenAPI.

## Numbered assumptions

Where the model is silent the least surprising reading was taken. Items marked (gap) are also returned as `model_gaps`.

- A1. Sites are addressed by siteCode, doors and spots by their facility-layout `locationCode`, globally unique (G-A, X5). A
  `LocationSlotRegistered` that arrives before its `ZoneRegistered` is held pending (site unknown) and resolved when the
  zone arrives; an unresolved location is `unknown-location`.
- A2. Ids are `<prefix>-<uuid>` minted through the `IDGen` port: `visit-`, `trl-`, `mv-`, `yck-`.
- A3. A trailer IN_MOVE has no location; the origin is released at `StartMoving` and the destination was reserved when the move
  was requested (Y-8). The destination is validated again at `ArriveAt`; an occupied location is refused and the move stays
  CLAIMED.
- A4. `RecordArrival` creates the trailer IN_MOVE together with its first move, created CLAIMED (performer VISITING_DRIVER,
  from none, no lease). That move can only be completed by the gate clerk or dock operator (Y-1, Y-3).
- A5. "Released" for check-out: no open move and either PARKED, or DOCKED with door work DONE (Y-2); a live visit departs from
  the door, so `Depart` from DOCKED raises TrailerUndocked then TrailerDeparted.
- A6. `RequestMove` derives a missing `reason`: a door destination is TO_DOOR; a spot destination is FROM_DOOR when the trailer
  is DOCKED, RELOCATE otherwise. A HOSTLER performer is implied by the REST route; VISITING_DRIVER moves exist only from the gate.
- A7. Contents: `asn_numbers` come from the appointment (INBOUND) and are checked against the AsnContentView for known ASNs;
  `outbound_ref` is the first `shipment_refs` entry of an OUTBOUND appointment. A trailer without either is unconstrained
  by door flow unless the purpose is LIVE_LOAD or PICKUP (Y-9).
- A8. One appointment covers exactly one trailer (X3): a check-in naming an appointment carries exactly one trailer.
- A9. SealCheckAtGate: when a trailer arrives with `sealNumber` and its appointment has `expected_seal_number` and they differ,
  SealExceptionRaised is raised (FLAG) or the visit is recorded REJECTED with VisitRejected(SEAL_MISMATCH) plus the
  SealExceptionRaised (trailer_id null, REJECT). A missing seal raises nothing (Y-5, Y-13).
- A10. `POST /trailers/{id}/seals` with state BROKEN marks the seal BROKEN and places a SEAL_BROKEN hold (Y-6). PlaceHold on a
  DOCKED trailer is allowed; door work then waits for the release (Y-13).
- A11. `CompleteDoorWork` requires IN_PROGRESS; `AutoCompleteUnload` may complete WAITING or IN_PROGRESS door work once every ASN
  on the trailer has a closed receipt, joined through `receipt_id` from the ReceiptOpened copy (Y-7).
- A12. Dwell is `now - arrived_at`; the dwell sweep raises TrailerDwellExceeded once per trailer for its kind's threshold (the
  trailer keeps `dwell_alerted_at`). A kind without a threshold is never alerted.
- A13. YardCheck closing only reports (Y-11): every observed location is compared with the model, and a location NOT observed
  is MISSING when the model records a trailer there. Observed with a different unit: the expected trailer is MISSING and the
  observed unit is WRONG_LOCATION (known elsewhere on site) or UNEXPECTED (unknown).
- A14. Claim-next order: lowest priority band, then earliest `due_by` (none last), then request time (Y-11). CallToDoor band is
  the minutes to `window_start` bucketed 1 (due now) .. 9. The claim body is `{siteId, hostlerRef}`; 404 `no-claimable-move`
  when nothing waits.
- A15. The AppointmentView status is BOOKED, CHECKED_IN (own VisitCheckedIn), USED (own TrailerUndocked), CANCELLED or NO_SHOW (X4).
  Version-guarded: a replay of an older version never overwrites a newer row.
- A16. ReturnToOrigin raises TrailerParked or TrailerDocked again (the origin is re-occupied); an expiring or cancelled HOSTLER
  claim whose origin has since been taken leaves the trailer IN_MOVE and the move REQUESTED (the supervisor re-plans).
- A17. DirectOnCheckIn picks, in order: the appointment door when free, open and flow-compatible and the arrival is not earlier
  than `window_start - arrival_tolerance_minutes`; otherwise a walk-in or early arrival goes to the first free holding spot of
  the policy (or the first free spot when none is declared, TO_HOLDING); a trailer with an on-time appointment whose door is
  unavailable goes to the first free Yard slot of the parking zones, ordered by locationCode (TO_SPOT). None free is
  `no-free-spot` (409, Y-12).
- A18. Writes carry `Idempotency-Key`; sweeps are naturally idempotent. `GET` of a single aggregate returns a weak `ETag` of its
  version; `PUT /sites/{siteId}/yard-policy` honours `If-Match`.
- A19. Reports read the analytics database fed by the projector; driver wait is arrival (VisitCheckedIn) to DoorWorkStarted.
- A20. Trailer `kind`/`load_state` of a PICKUP visit's outgoing trailer are those already recorded; the check-out body names only
  `trailerId`.

## Consequences

- The model names are the code names, so a reviewer can grep an `Err` from the model to its test.
- Cross-aggregate rules (one open visit per plate, one open move per trailer, hostler busy, location occupancy, one open yard
  check per site) are enforced twice: by the use case under an advisory lock for a readable error, and by a unique index as the
  backstop (ADR 0003 of the Postgres adapter in `migrations/`).
