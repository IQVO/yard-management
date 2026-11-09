---
id: domain-message-flow
title: Domain Message Flow
sidebar_label: Domain Message Flow
description: "Yard Management scenarios as ddd-crew domain message flows: a live unload, a drop and its later door call, gate rejection and seal mismatch, a hostler move with lease expiry, and a cancelled appointment."
---

# Domain Message Flow


Following ddd-crew [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling).
Participants are contexts and actors. Every arrow is a message from [`model.yaml`](https://github.com/IQVO/warehouse-docs/blob/main/yard/model/model.yaml),
prefixed `cmd:`, `qry:` or `evt:`. Events are written `entity.EventName`; the full
type is `com.warehouse.wms.<producer>.<entity>.<EventName>` (see
[Domain Events](/docs/ddd/domain-events)). Route parameters are
written without braces. Events on the analytics topic only
(`warehouse.yard-management.analytics`) are not drawn as arrows. The yard-wide flows
are on [Strategic Design](https://iqvo.github.io/warehouse-docs/yard/strategic-design/).

## 1. Live unload: from booking to door release

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 104, "actorMargin": 12, "messageMargin": 26, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant DS as dock-<br/>scheduling
    actor GC as Gate clerk<br/>or kiosk
    participant YM as yard-<br/>management
    actor DO as Dock<br/>operator
    participant IR as inbound-<br/>receiving
    participant OS as outbound-<br/>shipping
    DS->>YM: evt: dockappointment.<br/>DockAppointmentBooked<br/>door, window, expected seal
    Note over YM: AppointmentView
    GC->>YM: cmd: CheckInVisit<br/>POST /visits<br/>purpose LIVE_UNLOAD
    Note over YM: DirectOnCheckIn<br/>door free, open, flow ok<br/>VISITING_DRIVER move<br/>TO_DOOR, created CLAIMED
    YM->>DS: evt: yardvisit.<br/>VisitCheckedIn<br/>appointment_id
    Note over DS: ArrivalFromGate<br/>CHECKED_IN
    DO->>YM: cmd: CompleteMove<br/>POST /moves/moveId/<br/>completions
    Note over YM: Trailer ArriveAt door<br/>Occupancy.CheckFree
    YM->>DS: evt: trailer.TrailerDocked
    YM->>OS: evt: trailer.TrailerDocked
    Note over DS: ActualDoorWins<br/>reassign only if the door differs
    DO->>YM: cmd: StartDoorWork<br/>POST /trailers/trailerId/<br/>door-work-starts
    IR->>YM: evt: receipt.ReceiptOpened<br/>appointment_id, door_code
    IR->>YM: evt: receipt.ReceiptClosed
    Note over YM: AutoCompleteUnload<br/>CompleteDoorWork<br/>load_state EMPTY
    YM->>OS: evt: trailer.DoorWorkCompleted
    GC->>YM: cmd: CheckOutVisit<br/>POST /visits/visitId/<br/>check-outs
    YM->>DS: evt: trailer.TrailerUndocked<br/>appointment_id
    YM->>OS: evt: trailer.TrailerDeparted
    Note over DS: CompleteOnDoorRelease<br/>COMPLETED
```

Source: `internal/application/usecases/targeting.go`, `internal/application/usecases/gate_checkin.go`, `internal/application/usecases/consumers.go`, `internal/domain/trailer/events.go`, `internal/adapters/outbound/kafka/encoder.go`, `internal/adapters/inbound/kafka/consumer.go`.

Omitted: analytics events (`VisitCheckedOut`, `DoorWorkStarted`, `YardMove*`); the `receipt` work itself
(`inbound-receiving`'s). `TrailerDocked`, `DoorWorkCompleted` and `TrailerDeparted` are read by `outbound-shipping`
(merged on `develop`); `inbound-receiving` is not a consumer of `TrailerDocked` as built. `ArriveAt` raises
`TrailerDocked`, and `CheckOutVisit` calls `Depart` for the trailer, which raises `TrailerUndocked` from the
door and then `TrailerDeparted` (decisions Y-6 and Y-2: a released trailer is parked, or docked with door work
`DONE`). The gate clerk or dock operator completes the visiting driver's move (decision Y-3). The order of
`StartDoorWork` and `ReceiptOpened` is not fixed; `AutoCompleteUnload` completes door work from `WAITING` or
`IN_PROGRESS` once every ASN on the trailer has a closed receipt (decision Y-7).

## 2. Drop, then the door call and the return to a spot

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 104, "actorMargin": 12, "messageMargin": 26, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant DS as dock-<br/>scheduling
    actor GC as Gate clerk
    participant YM as yard-<br/>management
    actor HO as Hostler
    actor DO as Dock<br/>operator
    DS->>YM: evt: dockappointment.<br/>DockAppointmentBooked
    GC->>YM: cmd: CheckInVisit<br/>purpose DROP, appointment
    Note over YM: DirectOnCheckIn<br/>the door is not free<br/>move to the first free spot<br/>of the parking zones
    YM->>DS: evt: yardvisit.<br/>VisitCheckedIn
    GC->>YM: cmd: CompleteMove<br/>at the spot
    Note over YM: Trailer PARKED<br/>TrailerParked on analytics
    GC->>YM: cmd: CheckOutVisit<br/>no trailer taken out
    Note over YM: the tractor leaves<br/>the dropped trailer stays
    YM->>DS: evt: trailer.TrailerUndocked<br/>the trailer ahead leaves the door
    Note over YM: CallToDoor<br/>HOSTLER move TO_DOOR<br/>due_by window_start
    HO->>YM: cmd: ClaimNextMove<br/>POST /moves/claims
    HO->>YM: cmd: CompleteMove
    YM->>DS: evt: trailer.TrailerDocked
    DO->>YM: cmd: StartDoorWork
    DO->>YM: cmd: CompleteDoorWork<br/>POST /trailers/trailerId/<br/>door-work-completions
    Note over YM: ClearDoorAfterWork<br/>DROP trailer: HOSTLER move<br/>FROM_DOOR to a free spot
    HO->>YM: cmd: ClaimNextMove
    YM->>DS: evt: trailer.TrailerUndocked<br/>appointment_id
    Note over DS: CompleteOnDoorRelease<br/>COMPLETED
    HO->>YM: cmd: CompleteMove<br/>to the spot
```

Source: `internal/domain/yardvisit/visit.go`, `internal/domain/trailer/trailer.go`, `internal/domain/trailer/work.go`, `internal/domain/yardmove/yardmove.go`, `internal/application/usecases/targeting.go`, `internal/application/usecases/gate_checkin.go`, `internal/application/usecases/doors.go`, `internal/domain/trailer/events.go`, `internal/adapters/outbound/kafka/encoder.go`.

Omitted: `YardMoveRequested`, `YardMoveClaimed` and `YardMoveCompleted` (analytics only);
how `CallToDoor` chooses among several `PARKED` trailers (the next appointment of that
door by `window_start`). The appointment completes when `dock-scheduling` reads `TrailerUndocked`, and the trailer leaves the
door when the `FROM_DOOR` move is claimed (`StartMoving` from `DOCKED`; decision Y-6). A move to a plain
parking spot has the reason `TO_SPOT` (decision Y-4). A drop's trailer carries its `appointment_id`, set at
arrival (decision X3).

## 3. Gate rejection: an appointment is required

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 104, "actorMargin": 12, "messageMargin": 26, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    actor GC as Gate clerk<br/>or kiosk
    participant YM as yard-<br/>management
    participant EM as exception-<br/>management
    participant DS as dock-<br/>scheduling
    GC->>YM: cmd: CheckInVisit<br/>POST /visits<br/>no BOOKED appointment
    Note over YM: YardPolicy<br/>require_appointment is true
    YM-->>GC: reply: problem<br/>ErrAppointmentRequired
    GC->>YM: cmd: RejectAtGate<br/>POST /visit-rejections<br/>reason APPOINTMENT_REQUIRED
    Note over YM: YardVisit REJECTED
    YM->>EM: evt: yardvisit.<br/>VisitRejected
    Note over EM: opens a problem case
    Note over DS: not told<br/>the model names no consumer
```

Source: `internal/domain/yardvisit/visit.go`, `internal/domain/trailer/events.go`, `internal/adapters/outbound/kafka/encoder.go`, `internal/domain/shared/shared.go`.

Omitted: the other reasons (`UNKNOWN_APPOINTMENT`, `TRAILER_ALREADY_ON_SITE`,
`SITE_REQUEST`). A refused `CheckInVisit` (`ErrAppointmentRequired`) creates nothing, and `RejectAtGate` records a
`REJECTED` visit; the two-step flow shown is decision Y-5. `dock-scheduling` consumes `VisitRejected` and
cancels a `BOOKED` appointment with the reason `TURNED_AWAY` (decision X2), so it no longer waits for the no-show
sweep.

### 3b. Seal mismatch at the gate

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 104, "actorMargin": 12, "messageMargin": 26, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    actor GC as Gate clerk<br/>or kiosk
    participant YM as yard-<br/>management
    participant EM as exception-<br/>management
    participant DS as dock-<br/>scheduling
    GC->>YM: cmd: CheckInVisit<br/>seal differs from the<br/>expected_seal_number
    Note over YM: SealCheckAtGate
    YM->>EM: evt: trailer.<br/>SealExceptionRaised<br/>expected, found
    alt seal_mismatch_action FLAG
        YM->>DS: evt: yardvisit.<br/>VisitCheckedIn
        Note over YM: the visit goes on<br/>exception raised only
    else seal_mismatch_action REJECT
        YM->>EM: evt: yardvisit.<br/>VisitRejected<br/>reason SEAL_MISMATCH
    end
```

Source: `internal/domain/yardpolicy/yardpolicy.go`, `internal/application/usecases/gate_checkin.go`, `internal/domain/trailer/events.go`, `internal/adapters/outbound/kafka/encoder.go`, `internal/domain/shared/shared.go`.

Omitted: the other `Trailer` and `YardVisit` events of a normal check-in. The
seal number is recorded at the gate on the trailer entry of the check-in (a missing seal raises nothing) and
`RecordSeal` is the supervisor route; when the visit is then rejected (`REJECT` policy) the
`SealExceptionRaised` has a null `trailer_id` (decisions Y-5, Y-13; assumptions A9 and A10).

## 4. A hostler claims a move, the lease expires, another hostler completes it

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 104, "actorMargin": 12, "messageMargin": 26, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    actor SU as Yard<br/>supervisor
    actor H1 as Hostler A
    actor H2 as Hostler B
    participant YM as yard-<br/>management
    participant CR as Scheduler<br/>cron
    SU->>YM: cmd: RequestMove<br/>POST /moves<br/>destination reserved
    H1->>YM: cmd: ClaimNextMove<br/>POST /moves/claims
    Note over YM: REQUESTED to CLAIMED<br/>lease of lease_seconds<br/>Trailer StartMoving
    CR->>YM: cmd: ExpireLease<br/>POST /move-lease-sweeps
    Note over YM: CLAIMED to REQUESTED<br/>no event raised<br/>trailer stays IN_MOVE
    H2->>YM: cmd: ClaimNextMove<br/>POST /moves/claims
    H1->>YM: cmd: CompleteMove<br/>late, lease lost
    YM-->>H1: reply: problem<br/>ErrNotLeaseHolder
    H2->>YM: cmd: CompleteMove<br/>POST /moves/moveId/<br/>completions
    Note over YM: Trailer ArriveAt<br/>Occupancy.CheckFree
    YM-->>H2: ok, move COMPLETED
```

Source: `internal/domain/yardmove/yardmove.go`, `internal/application/usecases/moves.go`, `internal/adapters/inbound/kafka/consumer.go`, `apis/openapi.yaml`, `internal/adapters/inbound/http/server.go`.

Omitted: the analytics events `YardMoveRequested`, `YardMoveClaimed`,
`YardMoveLeaseExpired` and `YardMoveCompleted`; the result when nobody re-claims the move (a late `CompleteMove`
from `REQUESTED` is `ErrIllegalTransition`); `ErrHostlerBusy` when a hostler already
holds a `CLAIMED` move. The expiry returns the move to `REQUESTED` and `ReturnToOrigin` puts the trailer back where
`StartMoving` found it, in the same unit of work (decision Y-1); the expiry raises `YardMoveLeaseExpired`
(decision Y-10). There is no lease renewal.

## 5. An appointment is cancelled while a door call is open

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 104, "actorMargin": 12, "messageMargin": 26, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant DS as dock-<br/>scheduling
    participant YM as yard-<br/>management
    DS->>YM: evt: dockappointment.<br/>DockAppointmentCancelled<br/>or DockAppointmentNoShow
    Note over YM: AppointmentView updated
    alt an open TO_DOOR move exists for it
        Note over YM: NoShowFreesHolding<br/>CancelMove
        alt the move is REQUESTED
            Note over YM: CANCELLED<br/>nothing else changes
        else the move is CLAIMED
            Note over YM: CANCELLED<br/>trailer left IN_MOVE
        end
    else no open move
        Note over YM: view update only
    end
```

Source: `internal/domain/yardmove/yardmove.go`, `internal/application/usecases/consumers.go`, `internal/adapters/inbound/kafka/consumer.go`.

Omitted: what the policy does for a `DockAppointmentRescheduled` to another door. The move is found
through the `appointment_id` that `YardMove` carries (decision X3, `YardMoveRequested` has the field). Cancelling
a `CLAIMED` hostler move runs `ReturnToOrigin` in the same unit of work, so the trailer goes back where it was
(decision Y-1); when that origin has been taken meanwhile the trailer stays `IN_MOVE` and the move
`REQUESTED` for the supervisor to re-plan (assumption A16). A `CHECKED_IN` appointment cancelled with the
trailer on site is settled by decision Y-14.
