# 1. Yard Management is a bounded context of the yard domain

Status: Accepted (bootstrap). Design source: IQVO/warehouse-docs `yard/model/model.yaml`, context `yard-management`, and its yard ADRs YD-0001 to YD-0005.

## Context

The yard is modelled as two bounded contexts: dock-scheduling (door time, the future) and yard-management (the physical yard, the present). Owns everything physically on site outside the four walls: who passed the gate (visits), which trailer is where (spot, door or an open move), its load, seal and hold state, the moves hostlers and visiting drivers make, door work at the door, and yard checks. The source of dwell, door occupancy and yard truck productivity facts.

## Decision

- This repo owns the aggregates YardVisit, Trailer, YardMove, YardCheck, YardPolicy; the invariants and error names are those of the model.
- Business decisions of the context:
- A trailer on site is at exactly one place - one yard spot, one door, or one open move.
- A spot or a door holds at most one trailer.
- A trailer has at most one open move, and a move's destination is reserved when the move is requested.
- Moves are pulled by hostlers (claim-next with a lease), never pushed, as in fulfillment-execution.
- A visit checks out only when every trailer it takes is released (not docked, no open move) and every loaded outbound trailer carries a seal.
- Walk-ins are allowed into a holding spot unless the site's policy requires an appointment.
- A seal mismatch at the gate raises SealExceptionRaised; whether it also rejects the visit is site policy (FLAG or REJECT).
- CloudEvents subdomain `wms`: types `com.warehouse.wms.yard-management.<entity>.<Event>` on `warehouse.yard-management.events`
  (analytics-only events on `.analytics`). Published: VisitCheckedIn, VisitRejected, VisitCheckedOut, TrailerDocked, TrailerUndocked, TrailerParked, DoorWorkStarted, DoorWorkCompleted, TrailerDeparted, SealExceptionRaised, TrailerDwellExceeded, TrailerHeld, TrailerHoldReleased, YardMoveRequested, YardMoveClaimed, YardMoveCompleted, YardMoveCancelled, YardCheckClosed.
- No live REST or MCP lookup into sibling contexts; sibling data is an event-fed local copy.
- No authentication on REST, consistent with the fleet.

## Consequences

Relationships and the full consumed-event list are in the model and in the AsyncAPI contract added with the first feature PR.
