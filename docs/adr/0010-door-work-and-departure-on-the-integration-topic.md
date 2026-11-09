# ADR 0010: DoorWorkCompleted and TrailerDeparted on the integration topic

## Status

Accepted (2026-11-09). Refines ADR 0004 (stream split) and ADR 0005 (one outbox row, up to two messages). Additive: no payload
changes, no new type, no consumer is broken.

## Context

ADR 0004 published seven types on `warehouse.yard-management.events` and kept the other twelve analytics-only. DoorWorkCompleted
and TrailerDeparted were in the analytics-only group because the first consumer of the yard facts, the analytics projector,
reads the analytics stream.

outbound-shipping (its ADR 0002, ship-confirm follows the physical departure) consumes exactly `TrailerDocked`,
`DoorWorkCompleted` and `TrailerDeparted` from the **integration** topic: a closed load is confirmed shipped when the trailer it
is on physically leaves the site. Because the two messages never reached that topic, the confirmation could not fire. The
consumer's tests used fixture events, so no test crossed the two services.

## Decision

- `DoorWorkCompleted` and `TrailerDeparted` become integration events: they go to `warehouse.yard-management.events` and, as the
  usual twin (same id, type, subject and byte-identical data, analytics dataschema), to `warehouse.yard-management.analytics`.
- The catalogue is now 19 types: nine integration, ten analytics-only. `VisitCheckedOut`, `TrailerParked`, `DoorWorkStarted`,
  `TrailerHeld`, `TrailerHoldReleased` and the five yard-move types stay analytics-only until a consumer needs them.
- Payload, key (`trailer_id`), `type` and the analytics dataschema are unchanged. The integration dataschema is
  `urn:warehouse:yard-management:events:<EventName>:v1`.
- `apis/asyncapi.yaml` declares both on the `yardManagementEvents` channel (`publishYardManagementEvents` lists nine messages).

## Consequences

- outbound-shipping can attach the trailer at the door and confirm the load on departure; it replaces its fixture with a
  contract gate pinned to this AsyncAPI (separate change in that repo).
- Messages already on the analytics topic are not copied back: only departures raised after this release reach the integration
  topic. Nothing was deployed before this change (no yard release exists), so no departure is lost.
- Rolling deploy: the relay and the consumers are independent processes; consumers ignore unknown types, so either can ship
  first. Ship yard-management first so the topic carries the types when outbound-shipping starts asking for them.
- A future breaking change to either payload is a `.v2` type, as for every integration type (ADR 0004).

## Alternatives considered and rejected

- **outbound-shipping reads the analytics topic**: that stream is this service's own projector input and its schema may change
  without a contract (ADR 0006 rejected the opposite direction for the same reason).
- **A new type for the departure fact**: duplicates `TrailerDeparted` with no new information.
