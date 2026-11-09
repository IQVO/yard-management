---
id: sequence-diagrams
title: Sequence Diagrams
sidebar_label: Sequence Diagrams
description: "UML sequence diagrams for Yard Management: the live unload, the drop and later door call, gate rejection and the hostler move with lease expiry, then one diagram per significant command use case."
---

# Sequence Diagrams


Two parts. **Part A** has the four scenarios the pack must show, each as one story
across several use cases: inbound adapter → use cases → aggregates → outbox → relay →
Kafka → neighbour. **Part B** has one diagram per significant command use case of
[Use Cases](/docs/ddd/use-cases), in the hexagonal layout of the built service:
inbound adapter → use case → aggregate → repository → outbox (same transaction) → relay →
Kafka. The relay and Kafka share one lifeline ("relay to Kafka") to keep each diagram at
six participants or fewer. `alt` blocks show error branches, optimistic-concurrency
`version` checks and idempotency (REST `Idempotency-Key`, Kafka inbox on the CloudEvents
`id`). Error names are the model's; HTTP statuses of domain errors follow decision D-10: 409 conflicts, 422 value rules (see
[Use Cases](/docs/ddd/use-cases#conventions-for-every-use-case)).
Route parameters are written without braces. The relay publishes integration events to
`warehouse.yard-management.events` and, as a twin, to `warehouse.yard-management.analytics`; analytics-only events
go to the analytics topic alone. Both are keyed by the aggregate root id.

## Part A: the four scenarios

### A1. Live unload

An inbound carrier with a booked door: check-in, the visiting driver's move to the door,
docking, door work closed by the receipt, check-out.

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 104, "actorMargin": 12, "messageMargin": 26, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    actor P as Gate clerk,<br/>dock operator
    participant U as http and<br/>use cases
    participant A as aggregates<br/>Visit, Trailer,<br/>Move
    participant O as outbox,<br/>relay to Kafka
    participant DS as dock-<br/>scheduling
    P->>U: POST visits<br/>LIVE_UNLOAD, appointment
    U->>A: YardVisit CHECKED_IN<br/>Trailer arrives<br/>VISITING_DRIVER move<br/>TO_DOOR, CLAIMED
    U->>O: VisitCheckedIn, one transaction
    O->>DS: VisitCheckedIn after commit
    Note over DS: ArrivalFromGate
    P->>U: POST moves/moveId/completions
    U->>A: Move COMPLETED<br/>Trailer ArriveAt door<br/>Occupancy.CheckFree<br/>DOCKED, DoorWork WAITING
    U->>O: TrailerDocked, one transaction
    O->>DS: TrailerDocked
    Note over DS: ActualDoorWins
    P->>U: POST trailers/trailerId/<br/>door-work-starts
    U->>A: DoorWork IN_PROGRESS
    Note over U,A: ReceiptClosed from inbound-receiving<br/>AutoCompleteUnload<br/>CompleteDoorWork, load_state EMPTY
    P->>U: POST visits/visitId/check-outs
    U->>A: Depart from the door<br/>Trailer DEPARTED<br/>Visit CHECKED_OUT
    U->>O: TrailerUndocked, one transaction
    O->>DS: TrailerUndocked
    Note over DS: CompleteOnDoorRelease<br/>appointment COMPLETED
```

Source: `internal/domain/yardvisit/visit.go`, `internal/domain/trailer/trailer.go`, `internal/domain/trailer/work.go`, `internal/domain/yardmove/yardmove.go`, `internal/application/usecases/targeting.go`, `internal/application/usecases/gate_checkin.go`, `internal/application/usecases/consumers.go`, `internal/domain/trailer/events.go`, `internal/adapters/outbound/kafka/encoder.go`.

Omitted: the failure branches (Part B), the analytics events (`VisitCheckedOut`,
`DoorWorkStarted`, `YardMove*`; `DoorWorkCompleted` and `TrailerDeparted` also go to the integration topic since
2026-11-09), and `inbound-receiving`'s own receipt flow. `Depart` from the door under `CheckOutVisit` raises
`TrailerUndocked` and `TrailerDeparted` (decisions Y-2, Y-6). The dock operator or gate clerk completes the
first move through the completion route (decision Y-3).

### A2. Drop, then a later door call

A carrier drops a trailer whose door is busy; the trailer waits in a spot; when the door
frees, a hostler moves it in; after unloading it is returned to a spot.

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 104, "actorMargin": 12, "messageMargin": 26, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    actor P as Gate clerk,<br/>hostler
    participant U as http and<br/>use cases
    participant A as aggregates<br/>Visit, Trailer,<br/>Move
    participant O as outbox,<br/>relay to Kafka
    participant DS as dock-<br/>scheduling
    P->>U: POST visits<br/>DROP, appointment
    U->>A: door not free<br/>move to the first free spot<br/>of the parking zones
    U->>O: VisitCheckedIn
    O->>DS: VisitCheckedIn
    P->>U: POST moves/moveId/completions
    U->>A: Trailer PARKED
    P->>U: POST visits/visitId/check-outs<br/>no trailer taken out
    U->>A: Visit CHECKED_OUT<br/>the trailer stays PARKED
    Note over A,O: the trailer ahead leaves the door
    U->>O: TrailerUndocked
    O->>DS: TrailerUndocked
    O->>U: TrailerUndocked, policy CallToDoor
    U->>A: RequestMove HOSTLER TO_DOOR<br/>next appointment of the door<br/>whose trailer is PARKED<br/>due_by window_start<br/>destination reserved
    P->>U: POST moves/claims
    U->>A: Move CLAIMED, lease<br/>Trailer StartMoving
    P->>U: POST moves/moveId/completions
    U->>A: Trailer DOCKED
    U->>O: TrailerDocked
    O->>DS: TrailerDocked
    Note over U,A: door work done<br/>ClearDoorAfterWork<br/>RequestMove HOSTLER FROM_DOOR
    P->>U: POST moves/claims
    U->>A: Trailer StartMoving from DOCKED
    U->>O: TrailerUndocked
    O->>DS: TrailerUndocked
    Note over DS: CompleteOnDoorRelease<br/>appointment COMPLETED
    P->>U: POST moves/moveId/completions
    U->>A: Trailer PARKED at a free spot
```

Source: `internal/domain/trailer/trailer.go`, `internal/domain/trailer/work.go`, `internal/domain/yardmove/yardmove.go`, `internal/application/usecases/targeting.go`, `internal/application/usecases/doors.go`, `internal/domain/trailer/events.go`, `internal/adapters/outbound/kafka/encoder.go`.

Omitted: the door work commands (A1), `YardMoveRequested`, `YardMoveClaimed`,
`YardMoveCompleted` and `TrailerParked` (analytics only), the idempotency and inbox rows.
`CallToDoor` reacts to the own `TrailerUndocked`; this page reads that as a consumer of
the own topic, as `dock-scheduling` consumes it. `StartMoving` from `DOCKED` (the claim of a `FROM_DOOR` move)
raises `TrailerUndocked` (decision Y-6); the trailer carries its appointment through `appointment_id` (decision X3);
the first move to a plain spot has the reason `TO_SPOT` (decision Y-4).

### A3. Gate rejection

A walk-in at a site that requires appointments; then a seal mismatch under both policies.

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 104, "actorMargin": 12, "messageMargin": 26, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    actor GC as Gate clerk<br/>or kiosk
    participant U as http and<br/>use cases
    participant A as aggregates<br/>Visit, Trailer
    participant O as outbox,<br/>relay to Kafka
    participant EM as exception-<br/>management
    GC->>U: POST visits<br/>walk-in
    U->>A: YardPolicy and<br/>AppointmentView
    alt appointment required, none BOOKED
        A-->>U: ErrAppointmentRequired
        U-->>GC: problem, appointment-required
        GC->>U: POST visit-rejections<br/>reason APPOINTMENT_REQUIRED
        H->>U: RejectAtGate
        U->>A: YardVisit REJECTED
        U->>O: VisitRejected, one transaction
        O->>EM: VisitRejected
        Note over EM: opens a problem case
    else seal differs from expected
        U->>A: SealCheckAtGate
        U->>O: SealExceptionRaised
        O->>EM: SealExceptionRaised
        alt seal_mismatch_action FLAG
            U->>O: VisitCheckedIn, the visit goes on
        else seal_mismatch_action REJECT
            U->>A: YardVisit REJECTED
            U->>O: VisitRejected<br/>reason SEAL_MISMATCH
            O->>EM: VisitRejected
        end
    end
```

Source: `internal/domain/yardvisit/visit.go`, `internal/domain/yardpolicy/yardpolicy.go`, `internal/application/usecases/gate_checkin.go`, `internal/domain/trailer/events.go`, `internal/adapters/outbound/kafka/encoder.go`, `internal/domain/shared/shared.go`.

Omitted: `ErrTrailerAlreadyOnSite` (reason `TRAILER_ALREADY_ON_SITE`), `UNKNOWN_APPOINTMENT`
and `SITE_REQUEST` rejections; the detail that `dock-scheduling` now receives `VisitRejected` and cancels the
appointment with `TURNED_AWAY` (decision X2). A refused `CheckInVisit` creates nothing and `RejectAtGate` records the
rejection (decision Y-5). The seal number arrives with the trailer at check-in, and a rejected visit's
`SealExceptionRaised` has a null `trailer_id` (decisions Y-5, Y-13).

### A4. A hostler claims a move, the lease expires, another hostler completes it

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 104, "actorMargin": 12, "messageMargin": 26, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    actor H1 as Hostler A
    actor H2 as Hostler B
    participant CR as Scheduler<br/>cron
    participant U as http and<br/>use cases
    participant A as YardMove,<br/>Trailer
    participant O as outbox,<br/>relay to Kafka
    H1->>U: POST moves/claims
    U->>A: Move REQUESTED to CLAIMED<br/>Lease hostler A, expires_at<br/>Trailer StartMoving
    U->>O: YardMoveClaimed on analytics
    Note over H1: Hostler A stalls
    CR->>U: POST move-lease-sweeps
    U->>A: lease expired<br/>Move CLAIMED to REQUESTED
    Note over U,O: no event raised<br/>trailer state not defined
    H2->>U: POST moves/claims
    U->>A: Move CLAIMED, lease hostler B
    U->>O: YardMoveClaimed
    H1->>U: POST moves/moveId/completions<br/>late
    U-->>H1: 409 ErrNotLeaseHolder
    H2->>U: POST moves/moveId/completions<br/>actual_location
    U->>A: CompleteMove<br/>Occupancy.CheckFree on<br/>the actual location
    alt location occupied
        A-->>U: ErrLocationOccupied
        U-->>H2: 409, move stays CLAIMED
    else free
        A-->>U: Move COMPLETED<br/>Trailer PARKED or DOCKED
        U->>O: YardMoveCompleted<br/>TrailerParked or TrailerDocked
        U-->>H2: 200 move
    end
```

Source: `internal/domain/trailer/trailer.go`, `internal/domain/trailer/work.go`, `internal/domain/yardmove/yardmove.go`, `internal/application/usecases/moves.go`, `internal/adapters/inbound/kafka/consumer.go`, `apis/openapi.yaml`, `internal/adapters/inbound/http/server.go`.

Omitted: the `Idempotency-Key` rows; `ErrHostlerBusy` when a hostler already holds a
`CLAIMED` move; the late completion after expiry when nobody has re-claimed the move,
which is `ErrIllegalTransition` (the move is `REQUESTED`). The expiry returns the move to
`REQUESTED` and `ReturnToOrigin` brings the trailer back (decision Y-1), raises `YardMoveLeaseExpired` (decision
Y-10) and has no lease renewal. A refusal at `ArriveAt` still happens when the hostler is already at the
location; the destination was also validated when the move was requested (decision Y-8).

## Part B: one diagram per significant command

Commands not drawn follow a pattern shown here: `RejectAtGate` (A3); `RecordArrival`,
`DirectOnCheckIn` inside B1; `StartMoving` inside B4; `ArriveAt` inside B5; `Depart`
inside B2; `RecordSeal`, `BreakSeal`, `RecordLoadState`, `ReleaseHold` as B9 (load, check
state, mutate, version check, optional outbox row); `OpenYardCheck`, `RecordObservation`
and `DeclareYardPolicy` as B11 without the outbox.

### B1. CheckInVisit

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 104, "actorMargin": 12, "messageMargin": 26, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http<br/>handler
    participant U as use case
    participant A as Visit, Trailer,<br/>Move
    participant R as repositories,<br/>read models
    participant O as outbox
    participant RL as relay to Kafka
    H->>U: POST visits<br/>Idempotency-Key
    U->>O: idempotency_keys has key?
    alt key seen
        U-->>H: stored response
    else new key
        U->>R: YardPolicy, AppointmentView,<br/>LocationView, DoorClosureView
        U->>A: YardVisit.CheckInVisit
        alt carrier invalid
            A-->>U: ErrInvalidCarrier
        else plate already CHECKED_IN
            A-->>U: ErrVisitAlreadyOpen
        else appointment required, none BOOKED
            A-->>U: ErrAppointmentRequired
        else accepted
            U->>A: Trailer.RecordArrival per trailer
            alt natural key already on site
                A-->>U: ErrTrailerAlreadyOnSite
            else new
                U->>A: seal check, DirectOnCheckIn<br/>destination, Occupancy.CheckFree
                alt destination not free
                    A-->>U: ErrDestinationReserved
                else reserved
                    A-->>U: first move CLAIMED<br/>VISITING_DRIVER
                    Note over U,O: one transaction
                    U->>R: insert visit, trailers, move
                    U->>O: insert VisitCheckedIn<br/>and SealExceptionRaised if any
                    U->>O: store idempotency key
                    U-->>H: 201 visit
                end
            end
        end
    end
    Note over O,RL: after commit
    RL->>O: claim unpublished rows
    Note over RL: publish to warehouse.yard-management.events<br/>analytics events to the analytics topic
    RL->>O: mark published
```

Source: `internal/domain/yardvisit/visit.go`, `internal/domain/trailer/trailer.go`, `internal/domain/trailer/work.go`, `internal/application/usecases/targeting.go`, `internal/application/usecases/gate_checkin.go`, `migrations/0001_yard-management.up.sql`, `migrations/0002_idempotency_keys.up.sql`.

Omitted: the `REJECT` seal outcome (A3); the choice of door, holding spot or parking spot
(A2); each domain error is answered `409` or `422` (decision D-10). The first move's events
(`YardMoveRequested`, `YardMoveClaimed`) go to the analytics topic; the move is created `CLAIMED`, so the
service raises both (decision Y-1).

### B2. CheckOutVisit

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 104, "actorMargin": 12, "messageMargin": 26, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http<br/>handler
    participant U as use case
    participant A as YardVisit,<br/>Trailer
    participant R as repositories
    participant O as outbox
    participant RL as relay to Kafka
    H->>U: POST visits/visitId/check-outs<br/>Idempotency-Key
    U->>R: Get visit and trailers out<br/>with versions
    alt not found
        U-->>H: 404 not-found
    else found
        U->>A: YardVisit.CheckOutVisit
        alt visit not CHECKED_IN
            A-->>U: ErrIllegalTransition
        else a trailer not released
            A-->>U: ErrTrailerNotReleased
        else LOADED outbound trailer without seal
            A-->>U: ErrSealRequired
        else accepted
            U->>A: Trailer.Depart per trailer out
            alt DOCKED and door work not DONE
                A-->>U: ErrDoorWorkOpen
            else departed
                Note over U,O: one transaction
                U->>R: update visit and trailers<br/>where version matches
                alt version changed
                    U-->>H: 409 concurrent-modification
                else saved
                    U->>O: insert TrailerDeparted,<br/>TrailerUndocked if from a door,<br/>VisitCheckedOut
                    U-->>H: 200 visit
                end
            end
        end
    end
    Note over O,RL: after commit
    RL->>O: claim unpublished rows
    Note over RL: TrailerUndocked to the events topic<br/>the rest to the analytics topic
    RL->>O: mark published
```

Source: `internal/domain/yardvisit/visit.go`, `internal/domain/trailer/trailer.go`, `internal/domain/trailer/work.go`, `internal/domain/trailer/events.go`, `internal/adapters/outbound/kafka/encoder.go`.

Omitted: the idempotency-key check (as in B1). `Depart` is called from here for each trailer taken out
(decision Y-6). For a live visit the trailer leaves from the door: released means no open move and either
`PARKED` or `DOCKED` with door work `DONE` (decision Y-2).

### B3. RequestMove

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 104, "actorMargin": 12, "messageMargin": 26, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http handler<br/>or policy
    participant U as use case
    participant A as YardMove
    participant R as repositories
    participant O as outbox
    participant RL as relay to Kafka
    H->>U: RequestMove trailer, to_location,<br/>reason, priority
    U->>R: Get trailer
    alt not found
        U-->>H: 404 not-found
    else found
        U->>R: lock the destination<br/>advisory lock per location
        U->>A: RequestMove
        alt trailer has another open move
            A-->>U: ErrTrailerHasOpenMove
        else destination not free or reserved
            A-->>U: ErrDestinationReserved
        else accepted
            A-->>U: Move REQUESTED, HOSTLER
            Note over U,O: one transaction
            U->>R: insert move<br/>unique indexes back the rules
            U->>O: insert YardMoveRequested
            U-->>H: 201 move
        end
    end
    Note over O,RL: after commit
    RL->>O: claim unpublished rows
    Note over RL: publish to warehouse.yard-management.analytics
    RL->>O: mark published
```

Source: `internal/domain/yardmove/yardmove.go`, `internal/application/usecases/doors.go`.

Omitted: the idempotency-key check; the Kafka inbox row when a policy is the caller. The
destination is validated at the request (known slot, closure, flow, hold, `ErrDestinationReserved`,
`ErrTrailerHasOpenMove`) and again at arrival (decision Y-8).

### B4. ClaimNextMove

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 104, "actorMargin": 12, "messageMargin": 26, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http<br/>handler
    participant U as use case
    participant A as YardMove,<br/>Trailer
    participant R as repositories
    participant O as outbox
    participant RL as relay to Kafka
    H->>U: POST moves/claims<br/>hostler_ref, Idempotency-Key
    U->>R: moves CLAIMED by this hostler?
    alt hostler holds a CLAIMED move
        U-->>H: 409 ErrHostlerBusy
    else free
        U->>R: next REQUESTED HOSTLER move<br/>band, due_by, skip locked
        alt none available
            U-->>H: 404 no-claimable-move
        else found
            U->>A: Move.ClaimNextMove lease_seconds
            U->>A: Trailer.StartMoving
            Note over U,O: one transaction
            U->>R: update move and trailer<br/>where versions match
            alt version changed
                U-->>H: 409 concurrent-modification
            else saved
                U->>O: insert YardMoveClaimed<br/>TrailerUndocked if from a door
                U-->>H: 200 move
            end
        end
    end
    Note over O,RL: after commit
    RL->>O: claim unpublished rows
    Note over RL: YardMoveClaimed to analytics<br/>TrailerUndocked to the events topic
    RL->>O: mark published
```

Source: `internal/domain/trailer/trailer.go`, `internal/domain/trailer/work.go`, `internal/domain/yardmove/yardmove.go`.

Omitted: the idempotency-key check. The claim order is the lowest band, then `due_by`, then request time
(decision Y-11); with nothing to claim the answer is `404 no-claimable-move`; a claim from a door raises
`TrailerUndocked` (decision Y-6).

### B5. CompleteMove

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 104, "actorMargin": 12, "messageMargin": 26, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http<br/>handler
    participant U as use case
    participant A as YardMove,<br/>Trailer
    participant R as repositories,<br/>read models
    participant O as outbox
    participant RL as relay to Kafka
    H->>U: POST moves/moveId/completions<br/>actual_location, reason?
    U->>R: Get move and trailer<br/>with versions
    alt not found
        U-->>H: 404 not-found
    else found
        U->>A: Move.CompleteMove
        alt move not CLAIMED
            A-->>U: ErrIllegalTransition
        else caller not the lease holder
            A-->>U: ErrNotLeaseHolder
        else other location without reason
            A-->>U: ErrUnexplainedDestination
        else accepted
            U->>R: LocationView, DoorClosureView
            U->>A: Trailer.ArriveAt actual_location
            alt slot unknown
                A-->>U: ErrUnknownLocation
            else door closed
                A-->>U: ErrDoorClosed
            else door flow mismatch
                A-->>U: ErrDoorFlowMismatch
            else trailer on hold
                A-->>U: ErrTrailerOnHold
            else location occupied
                A-->>U: ErrLocationOccupied
            else free
                Note over U,O: one transaction under the<br/>location advisory lock
                U->>R: update move and trailer<br/>where versions match
                alt version or unique index hit
                    U-->>H: 409 concurrent-modification<br/>or location-occupied
                else saved
                    U->>O: insert YardMoveCompleted,<br/>TrailerParked or TrailerDocked
                    U-->>H: 200 move
                end
            end
        end
    end
    Note over O,RL: after commit
    RL->>O: claim unpublished rows
    Note over RL: TrailerDocked to the events topic<br/>the rest to the analytics topic
    RL->>O: mark published
```

Source: `internal/domain/trailer/trailer.go`, `internal/domain/trailer/work.go`, `internal/domain/yardmove/yardmove.go`, `internal/application/views/views.go`.

Omitted: the idempotency-key check. For a `VISITING_DRIVER` move there is no lease; the dock
operator or gate clerk completes it through the same route (decision Y-3). The partial unique index on active
occupancy is the race-safe backstop for `ErrLocationOccupied`.

### B6. ExpireLease

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 104, "actorMargin": 12, "messageMargin": 26, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant CR as scheduler<br/>cron
    participant H as http<br/>handler
    participant U as use case
    participant A as YardMove
    participant R as repositories
    CR->>H: POST move-lease-sweeps
    H->>U: ExpireLease now
    U->>R: CLAIMED moves with<br/>lease expires_at before now
    loop each expired move
        U->>A: ExpireLease
        alt no longer CLAIMED
            A-->>U: skipped
        else CLAIMED
            A-->>U: Move REQUESTED, lease cleared<br/>destination stays reserved
            U->>R: update where version matches
            alt version changed
                U-->>U: skipped, next sweep
            else saved
                U-->>U: count plus one
            end
        end
    end
    U-->>H: 200 expired count
    H-->>CR: 200
```

Source: `internal/domain/yardmove/yardmove.go`, `internal/application/usecases/moves.go`.

Omitted: the outbox row for `YardMoveLeaseExpired` (analytics only, decision Y-10) and the
`ReturnToOrigin` step on the trailer (decision Y-1), both in the same unit of work as the move. Each
move is its own transaction, so one conflict does not stop the sweep.

### B7. CancelMove

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 104, "actorMargin": 12, "messageMargin": 26, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http handler<br/>or kafka consumer
    participant U as use case
    participant A as YardMove
    participant R as repositories
    participant O as outbox
    participant RL as relay to Kafka
    H->>U: CancelMove reason<br/>Idempotency-Key or event id
    U->>R: Get move with version
    alt not found
        U-->>H: 404 not-found
    else found
        U->>A: CancelMove
        alt already CANCELLED
            A-->>U: unchanged
            U-->>H: 200 move
        else COMPLETED
            A-->>U: ErrIllegalTransition
        else REQUESTED or CLAIMED
            A-->>U: CANCELLED, reservation released
            Note over U,O: one transaction
            U->>R: update where version matches
            alt version changed
                U-->>H: 409 concurrent-modification
            else saved
                U->>O: insert YardMoveCancelled
                U-->>H: 200 move
            end
        end
    end
    Note over O,RL: after commit
    RL->>O: claim unpublished rows
    Note over RL: publish to warehouse.yard-management.analytics
    RL->>O: mark published
```

Source: `internal/domain/yardmove/yardmove.go`, `internal/application/usecases/consumers.go`.

Omitted: the idempotency check and the inbox row. A cancelled `CLAIMED` hostler move runs `ReturnToOrigin` in the same
unit of work, so the trailer goes back where it was (decision Y-1); the policy finds the move through the
`appointment_id` that the move carries (decisions X3, Y-14).

### B8. StartDoorWork and CompleteDoorWork

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 104, "actorMargin": 12, "messageMargin": 26, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http handler<br/>or kafka consumer
    participant U as use case
    participant A as Trailer
    participant R as repositories
    participant O as outbox
    participant RL as relay to Kafka
    H->>U: StartDoorWork or CompleteDoorWork<br/>load_state, Idempotency-Key
    U->>R: Get trailer with version
    alt not found
        U-->>H: 404 not-found
    else found
        U->>A: StartDoorWork or CompleteDoorWork
        alt trailer not DOCKED
            A-->>U: ErrIllegalTransition
        else already in the target door-work state
            A-->>U: unchanged, idempotent
        else accepted
            A-->>U: DoorWork IN_PROGRESS or DONE
            Note over U,O: one transaction
            U->>R: update where version matches
            alt version changed
                U-->>H: 409 concurrent-modification
            else saved
                U->>O: insert DoorWorkStarted<br/>or DoorWorkCompleted
                U-->>H: 200 trailer
            end
        end
    end
    Note over O,RL: after commit
    RL->>O: claim unpublished rows
    Note over RL: publish to warehouse.yard-management.analytics
    RL->>O: mark published
```

Source: `internal/domain/trailer/trailer.go`, `internal/domain/trailer/work.go`, `internal/application/usecases/doors.go`, `internal/application/usecases/consumers.go`, `internal/domain/trailer/events.go`, `internal/adapters/outbound/kafka/encoder.go`.

Omitted: the `AutoCompleteUnload` caller (a `ReceiptClosed` consumer: inbox row instead of
the idempotency key, joining `ReceiptAtDoorView` to the trailer through `receipt_id`, decision Y-7) and the
follow-up `RequestMove` of `ClearDoorAfterWork` for a `DROP` trailer (B3). `AutoCompleteUnload` completes from
`WAITING` or `IN_PROGRESS`; an operator's completion needs `IN_PROGRESS` (assumption A11).

### B9. PlaceHold

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 104, "actorMargin": 12, "messageMargin": 26, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http<br/>handler
    participant U as use case
    participant A as Trailer
    participant R as repositories
    participant O as outbox
    participant RL as relay to Kafka
    H->>U: POST trailers/trailerId/holds<br/>reason, Idempotency-Key
    U->>R: Get trailer with version
    alt not found
        U-->>H: 404 not-found
    else found
        U->>A: PlaceHold reason
        alt reason not a HoldReason
            A-->>U: ErrValidation
        else DEPARTED
            A-->>U: ErrIllegalTransition
        else accepted
            A-->>U: Hold set
            Note over U,O: one transaction
            U->>R: update where version matches
            alt version changed
                U-->>H: 409 concurrent-modification
            else saved
                U->>O: insert TrailerHeld
                U-->>H: 200 trailer
            end
        end
    end
    Note over O,RL: after commit
    RL->>O: claim unpublished rows
    Note over RL: publish to warehouse.yard-management.analytics
    RL->>O: mark published
```

Source: `internal/domain/trailer/trailer.go`, `internal/domain/trailer/work.go`, `internal/domain/shared/shared.go`.

Omitted: the idempotency-key check. A hold on an already `DOCKED` trailer is allowed and door work waits for
the release (decision Y-13); a held trailer is refused docking at `ArriveAt` (B5).

### B10. DwellSweep

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 104, "actorMargin": 12, "messageMargin": 26, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant CR as scheduler<br/>cron
    participant U as http and<br/>use case
    participant A as Trailer
    participant R as repositories
    participant O as outbox
    participant RL as relay to Kafka
    CR->>U: POST dwell-sweeps
    U->>R: YardPolicy thresholds per kind<br/>trailers on site
    loop each trailer on site
        U->>A: dwell = now minus ArrivedAt
        alt below threshold or already reported
            A-->>U: nothing to raise
        else threshold crossed
            Note over U,O: one transaction per trailer
            U->>R: mark crossing reported
            U->>O: insert TrailerDwellExceeded
        end
    end
    U-->>CR: 200 raised count
    Note over O,RL: after commit
    RL->>O: claim unpublished rows
    Note over RL: publish to warehouse.yard-management.events
    RL->>O: mark published
```

Source: `internal/domain/yardpolicy/yardpolicy.go`, `internal/application/usecases/checks_policy.go`, `internal/domain/trailer/events.go`, `internal/adapters/outbound/kafka/encoder.go`.

Omitted: a kind with no threshold (skipped). The marker that makes the sweep "once per
trailer and threshold crossing" is `dwell_alerted_at` on the trailer (decision Y-13); the sweep has no aggregate
command of its own.

### B11. CloseYardCheck

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 104, "actorMargin": 12, "messageMargin": 26, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http<br/>handler
    participant U as use case
    participant A as YardCheck
    participant R as repositories
    participant O as outbox
    participant RL as relay to Kafka
    H->>U: POST yard-checks/id/closures
    U->>R: Get check, version,<br/>trailers on site
    alt not found
        U-->>H: 404 not-found
    else found
        U->>A: CloseYardCheck expected places
        alt state CLOSED already
            A-->>U: unchanged, idempotent
            U-->>H: 200 check
        else OPEN
            A-->>U: CLOSED with<br/>discrepancies
            Note over U,O: one transaction
            U->>R: update where version matches
            alt version changed
                U-->>H: 409 concurrent-modification
            else saved
                U->>O: insert YardCheckClosed
                U-->>H: 200 check
            end
        end
    end
    Note over O,RL: after commit
    RL->>O: claim unpublished rows
    Note over RL: publish to warehouse.yard-management.events
    RL->>O: mark published
```

Source: `internal/domain/yardcheck/yardcheck.go`, `internal/domain/trailer/events.go`, `internal/adapters/outbound/kafka/encoder.go`.

Omitted: the idempotency-key check. `OpenYardCheck` and `RecordObservation` are the same
shape without the outbox row (`ErrYardCheckOpen` and `ErrLocationAlreadyObserved` as the
guards). The discrepancy rules are decision Y-11 (a location not observed is `MISSING` when the model records a trailer
there) and nothing applies a discrepancy to a trailer: closing only reports; `exception-management` opens one case
per discrepancy.
