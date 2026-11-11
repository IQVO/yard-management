---
id: class-diagram
title: Class Diagram
sidebar_label: Class Diagram
description: "UML class diagrams of the Yard Management domain (YardVisit, Trailer, YardMove, YardCheck, YardPolicy, Occupancy, events, policies) and its hexagonal ports and adapters."
---

# Class Diagram


UML class diagrams of the `internal/domain/**` packages of the built service (`yardvisit`,
`trailer`, `yardmove`, `yardcheck`, `yardpolicy`, and `shared`), then the hexagonal ports and
adapters in the Go layout the fleet uses. Type and field names come from
[`model.yaml`](https://github.com/IQVO/warehouse-docs/blob/main/yard/model/model.yaml), whose Go identifiers were reconciled with the code (the aggregate roots are `yardvisit.Visit`,
`trailer.Trailer`, `yardmove.Move`, `yardcheck.Check`, `yardpolicy.Policy`); method names are the model's commands.
The drawings are not generated from the code, so a method or field may differ in detail. Fields are shown `-`
(unexported), methods `+`.

## 1. Trailer aggregate and the Occupancy service

```mermaid
classDiagram
    direction LR
    class Trailer {
        <<AggregateRoot>>
        -trailerId string
        -siteId string
        -kind TrailerKind
        -unit UnitNumber
        -state TrailerState
        -loadState LoadState
        -contents Contents
        -seal Seal
        -hold Hold
        -location Location
        -doorWork DoorWork
        -arrivedAt time
        -version int
        +RecordArrival(unit, kind, contents)$ Trailer
        +StartMoving() error
        +ArriveAt(location, facts, occupancy) error
        +StartDoorWork(at) error
        +CompleteDoorWork(at, loadState) error
        +RecordSeal(seal) error
        +BreakSeal() error
        +PlaceHold(reason) error
        +ReleaseHold() error
        +RecordLoadState(state) error
        +Depart(at) error
    }
    class TrailerState {
        <<Enumeration>>
        PARKED
        IN_MOVE
        DOCKED
        DEPARTED
    }
    class TrailerKind {
        <<Enumeration>>
        DRY_VAN
        REEFER
        FLATBED
        CONTAINER
    }
    class LoadState {
        <<Enumeration>>
        EMPTY
        LOADED
        PARTIAL
    }
    class DoorWork {
        <<Enumeration>>
        WAITING
        IN_PROGRESS
        DONE
    }
    class UnitNumber {
        <<ValueObject>>
        carrierScac string
        number string
    }
    class Contents {
        <<ValueObject>>
        asnNumbers string[]
        outboundRef string
    }
    class Seal {
        <<ValueObject>>
        number string
        state SealState
    }
    class Hold {
        <<ValueObject>>
        reason HoldReason
    }
    class Securing {
        <<ValueObject>>
        method RESTRAINT or WHEEL_CHOCKS
        securedAt timestamp
        securerRef string
    }
    class Location {
        <<ValueObject>>
        kind SPOT or DOOR
        code string
    }
    class Occupancy {
        <<DomainService>>
        +CheckFree(siteId, location) error
    }
    Trailer --> TrailerState
    Trailer --> TrailerKind
    Trailer --> LoadState
    Trailer --> DoorWork
    Trailer *-- UnitNumber
    Trailer *-- Contents
    Trailer *-- Seal
    Trailer *-- Hold
    Trailer *-- Securing
    Trailer *-- Location
    Trailer ..> Occupancy : uses at ArriveAt
```

Source: `internal/domain/trailer/trailer.go`, `internal/domain/trailer/work.go`, `internal/domain/trailer/events.go`, `internal/adapters/outbound/kafka/encoder.go`.

Omitted: the error variables (see
[Aggregate Design Canvas](/docs/ddd/aggregate-design-canvas)),
`HoldReason` (`DAMAGED`, `SEAL_BROKEN`, `NOT_FOUND`, `QUALITY`, `SECURITY`) and
`SealState` (`NONE`, `INTACT`, `BROKEN`, `APPLIED`, `VERIFIED`, `MISMATCH`; ADR 0012) enumerations, read accessors. `ArriveAt`
receives the facts it cannot know alone (closure, door flow, active slot) from the use
case; `Occupancy.CheckFree` runs under a per-location advisory lock, with a partial
unique index as backstop. `RecordArrival` is drawn as a constructor; it creates the trailer `IN_MOVE`
together with its first move (decision Y-1).

## 2. YardVisit and YardMove aggregates

```mermaid
classDiagram
    direction LR
    class YardVisit {
        <<AggregateRoot>>
        -visitId string
        -siteId string
        -carrier Carrier
        -plate TractorPlate
        -driverRef DriverRef
        -purpose Purpose
        -appointment AppointmentRef
        -trailersIn TrailerRef[]
        -trailersOut TrailerRef[]
        -state VisitState
        -version int
        +CheckInVisit(carrier, plate, purpose, policy)$ YardVisit
        +RejectAtGate(carrier, reason)$ YardVisit
        +CheckOutVisit(trailersOut, released) error
    }
    class VisitState {
        <<Enumeration>>
        CHECKED_IN
        CHECKED_OUT
        REJECTED
    }
    class Purpose {
        <<Enumeration>>
        DROP
        LIVE_UNLOAD
        LIVE_LOAD
        PICKUP
        BOBTAIL
    }
    class YardMove {
        <<AggregateRoot>>
        -moveId string
        -siteId string
        -trailerId string
        -performer Performer
        -from Location
        -to Location
        -priority Priority
        -reason Reason
        -lease Lease
        -state MoveState
        -version int
        +RequestMove(trailer, to, reason, priority)$ YardMove
        +VisitingDriverMove(trailer, to, reason)$ YardMove
        +ClaimNextMove(hostlerRef, leaseSeconds, now) error
        +CompleteMove(by, actual, reason, now) error
        +CancelMove(reason) error
        +ExpireLease(now) error
    }
    class MoveState {
        <<Enumeration>>
        REQUESTED
        CLAIMED
        COMPLETED
        CANCELLED
    }
    class Performer {
        <<Enumeration>>
        HOSTLER
        VISITING_DRIVER
    }
    class Reason {
        <<Enumeration>>
        TO_DOOR
        FROM_DOOR
        RELOCATE
        TO_HOLDING
    }
    class Priority {
        <<ValueObject>>
        band int
        dueBy time
    }
    class Lease {
        <<ValueObject>>
        hostlerRef string
        expiresAt time
    }
    YardVisit --> VisitState
    YardVisit --> Purpose
    YardMove --> MoveState
    YardMove --> Performer
    YardMove --> Reason
    YardMove *-- Priority
    YardMove *-- Lease
```

Source: `internal/domain/yardvisit/visit.go`, `internal/domain/yardmove/yardmove.go`, `internal/domain/trailer/events.go`, `internal/adapters/outbound/kafka/encoder.go`.

Omitted: `Carrier{name, scac?}`, `TractorPlate`, `DriverRef`, `AppointmentRef`,
`RejectionReason` and `TrailerRef` (the shape of `TrailersIn[]` and `TrailersOut[]` is
given in the OpenAPI of the service: `checkInVisit` and `checkOutVisit`); the error variables.
`VisitingDriverMove` is drawn as a constructor named after the policy; the move is created `CLAIMED` with no lease and
the dock operator or gate clerk completes it (decisions Y-1, Y-3). `ClaimNextMove` is a method of the aggregate and its
selection is the repository's claim query: lowest band, then earliest `due_by`, then request time (decision Y-11). `released` is the fact
the use case computes from the trailers and moves.

## 3. YardCheck and YardPolicy aggregates

```mermaid
classDiagram
    direction LR
    class YardCheck {
        <<AggregateRoot>>
        -yardCheckId string
        -siteId string
        -observations Observation[]
        -discrepancies Discrepancy[]
        -state CheckState
        -version int
        +OpenYardCheck(siteId)$ YardCheck
        +RecordObservation(locationCode, unitNumber) error
        +CloseYardCheck(expected) error
    }
    class CheckState {
        <<Enumeration>>
        OPEN
        CLOSED
    }
    class Observation {
        <<ValueObject>>
        locationCode string
        unitNumber string
    }
    class Discrepancy {
        <<ValueObject>>
        kind MISSING, UNEXPECTED, WRONG_LOCATION
        locationCode string
        trailerId string
        unitNumber string
    }
    class YardPolicy {
        <<AggregateRoot>>
        -siteId string
        -requireAppointment bool
        -holdingSpotCodes string[]
        -parkingZoneIds string[]
        -dwellThresholds map
        -leaseSeconds int
        -sealAction SealMismatchAction
        -freeTimeMinutes int
        -version int
        +DeclareYardPolicy(settings) error
    }
    class SealMismatchAction {
        <<Enumeration>>
        FLAG
        REJECT
    }
    YardCheck --> CheckState
    YardCheck *-- Observation
    YardCheck *-- Discrepancy
    YardPolicy --> SealMismatchAction
```

Source: `internal/domain/yardcheck/yardcheck.go`, `internal/domain/yardpolicy/yardpolicy.go`.

Omitted: `ErrYardCheckOpen`, `ErrLocationAlreadyObserved`, `ErrUnknownLocation`,
`ErrValidation`. `expected` in `CloseYardCheck` is the places the model holds for the
observed locations, passed in by the use case (the discrepancy rules are decision Y-11). A site with
no declared `YardPolicy` answers the defaults (`require_appointment` false, `seal_mismatch_action` `FLAG`,
`lease_seconds` 300).

### Repositories

```mermaid
classDiagram
    direction LR
    class VisitRepo {
        <<port>>
        +Save(visit) error
        +FindByID(id) Visit
        +FindOpenByPlate(siteId, plate) Visit
        +List(filter, after, limit) Visit[]
    }
    class TrailerRepo {
        <<port>>
        +Save(trailer) error
        +FindByID(id) Trailer
        +FindAt(location) Trailer
        +FindOnSiteByUnit(siteId, scac, number) Trailer
        +FindByAppointment(appointmentId) Trailer
        +ListOnSite(siteId) Trailer[]
        +List(filter, after, limit) Trailer[]
    }
    class MoveRepo {
        <<port>>
        +Save(move) error
        +FindByID(id) Move
        +FindOpenByTrailer(trailerId) Move
        +FindOpenByDestination(location) Move
        +FindClaimedByHostler(siteId, hostlerRef) Move
        +ClaimNext(siteId) Move
        +ListExpiredClaims(now, limit) Move[]
        +ListOpenByAppointment(appointmentId) Move[]
        +ListOpen(siteId) Move[]
        +List(filter, after, limit) Move[]
    }
    class CheckRepo {
        <<port>>
        +Save(check) error
        +FindByID(id) Check
    }
    class PolicyRepo {
        <<port>>
        +Get / Save of the site policy
    }
```

Source: `internal/application/ports/ports.go`, `internal/adapters/outbound/postgres/*.go`, `internal/adapters/outbound/memory/*.go`.

One repository per aggregate root. `ClaimNext` backs `ClaimNextMove` (it row-locks the
`REQUESTED` hostler move with the lowest priority band, then the earliest due time, then the
earliest request time); `FindOpenByTrailer`, `FindOpenByDestination` and `FindOpenByPlate`
back `ErrTrailerHasOpenMove`, `ErrDestinationReserved` and `ErrVisitAlreadyOpen`. Each `Save`
maps the unique indexes of the schema to those domain errors. The same contract suite
(`internal/testing/repocontract`) runs against the memory and the Postgres adapter.

## 4. Events and policies

Visit and trailer events:

```mermaid
classDiagram
    direction LR
    class VisitCheckedIn {
        <<DomainEvent>>
        visit_id, site_id, carrier
        carrier_scac, purpose
        appointment_id, trailers
        arrived_at, version
    }
    class VisitRejected {
        <<DomainEvent>>
        visit_id, site_id
        carrier_scac, appointment_id
        reason, rejected_at
    }
    class TrailerDocked {
        <<DomainEvent>>
        trailer_id, site_id, door_code
        appointment_id, asn_numbers
        visit_id, docked_at, version
    }
    class TrailerUndocked {
        <<DomainEvent>>
        trailer_id, site_id, door_code
        appointment_id, undocked_at
        version
    }
    class SealExceptionRaised {
        <<DomainEvent>>
        trailer_id, site_id
        appointment_id
        expected_seal_number
        found_seal_number, raised_at
    }
    class TrailerDwellExceeded {
        <<DomainEvent>>
        trailer_id, site_id, kind
        dwell_minutes
        threshold_minutes
        location_code
    }
    class DirectOnCheckIn {
        <<Policy>>
        +OnCheckIn()
    }
    class SealCheckAtGate {
        <<Policy>>
        +OnSealRecorded()
    }
    class DwellSweep {
        <<Policy>>
        +OnSweep()
    }
    DirectOnCheckIn ..> VisitCheckedIn : via CheckInVisit
    SealCheckAtGate ..> SealExceptionRaised : via Trailer
    SealCheckAtGate ..> VisitRejected : REJECT
    DwellSweep ..> TrailerDwellExceeded : via Trailer
```

Source: `internal/domain/yardvisit/events.go`, `internal/domain/trailer/events.go`.

Move, door-work and check events:

```mermaid
classDiagram
    direction LR
    class YardMoveRequested {
        <<DomainEvent>>
        move_id, site_id, trailer_id
        performer, from_location
        to_location, reason
        due_by, version
    }
    class YardMoveClaimed {
        <<DomainEvent>>
        move_id, site_id
        hostler_ref, claimed_at
        version
    }
    class YardMoveCompleted {
        <<DomainEvent>>
        move_id, site_id, trailer_id
        performer, hostler_ref
        from_location, actual_location
        completed_at, duration_seconds
        version
    }
    class DoorWorkCompleted {
        <<DomainEvent>>
        trailer_id, site_id, door_code
        appointment_id, completed_at
        load_state, version
    }
    class YardCheckClosed {
        <<DomainEvent>>
        yard_check_id, site_id
        closed_at, discrepancies
    }
    class CallToDoor {
        <<Policy>>
        +OnTrailerUndocked()
    }
    class ClearDoorAfterWork {
        <<Policy>>
        +OnDoorWorkCompleted()
    }
    class AutoCompleteUnload {
        <<Policy>>
        +OnReceiptClosed()
    }
    class YardMoveCancelled {
        <<DomainEvent>>
        move_id, site_id
        reason, version
    }
    class NoShowFreesHolding {
        <<Policy>>
        +OnNoShowOrCancelled()
    }
    CallToDoor ..> YardMoveRequested : via RequestMove
    ClearDoorAfterWork ..> YardMoveRequested : via RequestMove
    AutoCompleteUnload ..> DoorWorkCompleted : via CompleteDoorWork
    NoShowFreesHolding ..> YardMoveCancelled : via CancelMove
```

Source: `internal/domain/trailer/events.go`, `internal/adapters/outbound/kafka/encoder.go`.

Omitted: the CloudEvents envelope (see
[Domain Events](/docs/ddd/domain-events)); the events not drawn
(`VisitCheckedOut`, `TrailerParked`, `DoorWorkStarted`, `TrailerDeparted`, `TrailerHeld`,
`TrailerHoldReleased`, `YardMoveLeaseExpired`) are raised directly by their commands, not by a policy, and carry
the fields listed there; the policies `VisitingDriverMove` and `LeaseExpirySweep` (the
sweep returns expired moves to `REQUESTED` and raises `YardMoveLeaseExpired`, decision Y-10). The seven
read models (`LocationView`, `AppointmentView`, `DoorClosureView`, `AsnContentView`,
`ReceiptAtDoorView`, `YardBoard`, the analytics reports) are projections, shown in the
ports view below.

## 5. Hexagonal ports and adapters

Inbound side: adapters, use cases and domain packages.

```mermaid
flowchart LR
    subgraph IN["adapters/inbound"]
        HTTP["http<br/>and cron sweeps"]
        KAF["kafka"]
    end
    subgraph APP["application/usecases"]
        UC1["Visit<br/>CheckIn, Reject,<br/>CheckOut"]
        UC2["Trailer<br/>door work, seal,<br/>hold, dwell sweep"]
        UC3["Move<br/>Request, ClaimNext,<br/>Complete, Cancel,<br/>ExpireLease"]
        UC4["YardCheck,<br/>YardPolicy"]
    end
    subgraph DOM["domain"]
        D1["yardvisit"]
        D2["trailer<br/>with Occupancy"]
        D3["yardmove"]
        D4["yardcheck<br/>yardpolicy"]
    end
    HTTP --> UC1
    HTTP --> UC2
    HTTP --> UC3
    HTTP --> UC4
    KAF --> UC2
    KAF --> UC3
    UC1 --> D1
    UC2 --> D2
    UC3 --> D3
    UC4 --> D4
```

Source: `cmd/api/wire.go`, `internal/adapters/inbound/http/server.go`, `internal/adapters/inbound/kafka/*_consumer.go`, `internal/adapters/inbound/mcp/server.go`.

Outbound side: the ports every use case calls and their adapters.

```mermaid
flowchart LR
    APP["use cases<br/>and projectors"]
    subgraph PORTS["application/ports"]
        P1["Visit, Trailer, Move,<br/>Check, Policy<br/>repositories"]
        P2["Locations, Appointments,<br/>Closures, Asns, Receipts<br/>and their feeds"]
        P3["EventPublisher<br/>outbox"]
        P4["ProcessedEvents, UnitOfWork,<br/>Clock, IDGen, LocationLock,<br/>IdempotencyStore"]
    end
    subgraph OUT["adapters/outbound"]
        PG["postgres"]
        KP["kafka relay"]
    end
    APP -.-> P1
    APP -.-> P2
    APP -.-> P3
    APP -.-> P4
    P1 --- PG
    P2 --- PG
    P3 --- PG
    P4 --- PG
    PG --> KP
```

Source: `internal/adapters/inbound/kafka/consumer.go`, `internal/application/views/views.go`, `migrations/0001_yard-management.up.sql`, `migrations/0002_idempotency_keys.up.sql`.

All paths are under `internal/` (`internal/adapters/inbound/{http,kafka,mcp}`,
`internal/application/usecases`, `internal/domain`, `internal/application/ports`,
`internal/adapters/outbound/{postgres,kafka,analyticsstore,memory,system}`). The service has five binaries under
`cmd/` (`api`, `mcp`, `relay`, `yard-projector`, `yard-reports`); `http` is the `api` binary and `mcp` serves the
16 tools of the service's MCP server (8 reads, 8 L3 writes). The ports are named as in `ports.go`: the five
repositories, the read ports `Locations`, `Appointments`, `Closures`, `Asns` and `Receipts` with a feed each,
`LocationLock`, `EventPublisher`, `ProcessedEvents`, `IDGen`, `Clock`, `IdempotencyStore` and `UnitOfWork`. The
KPI reports read an analytics store (`analyticsstore`) fed from the analytics topic. The dashed arrows mean every use case
calls ports only: the repository of its aggregate, the event publisher (outbox), the
inbox, the unit of work and, for occupancy, the per-location lock. `http` drives the gate,
hostler, dock operator and supervisor commands and the two sweeps (a CronJob calls
`POST /dwell-sweeps` and `POST /move-lease-sweeps`, so there is no `scheduler` adapter),
`kafka` drives the event policies (`CallToDoor`, `AutoCompleteUnload`,
`NoShowFreesHolding`), which call the trailer and move use cases, and the read-model
projectors (updates of `LocationView`, `AppointmentView` and the other views, not drawn
as a node).

Omitted: the HTTP read path (the `GET` routes read the repositories and read models
directly); `Clock` has no adapter package. There are no outbound HTTP clients:
`sync_calls` is empty. A gate camera, ANPR or kiosk is a further inbound adapter that
issues `CheckInVisit`, not a new context.
