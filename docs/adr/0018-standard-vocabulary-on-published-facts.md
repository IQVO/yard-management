# ADR 0018: Standard vocabulary on published facts: X12 214 status, CBV business step and CBV disposition

## Status

Accepted (2026-11-17). Refines 0004 (up to three optional fields on six payloads). Additive: no type, topic, route, tool, table or existing
field is added, removed or renamed; every new field is optional and omitted when it has no value, so a payload that carries none is
byte-for-byte what it was. Code: `internal/domain/standards` (the table), `internal/application/outbox` (`payload.go`, `standards.go`).
Plan item Y0.3, the yard side of dock-scheduling's ADR 0013 (standard vocabulary on published facts).

## Context

Partners do not want to learn this context's private event names and enums (`VisitCheckedIn`, `TrailerDeparted`, a `TrailerHeld` reason of
`SEAL_BROKEN`). They read standards: the ANSI X12 214 shipment status message on the carrier side and GS1 EPCIS with its Core Business
Vocabulary (CBV) on the supply-chain-visibility side. dock-scheduling already states, on its own facts, what they mean in those terms
(its ADR 0013: `x12_status`, `cbv_biz_step`, `read_point_gln`). This ADR does the same for the yard's facts: the gate, the visit, the
trailer's departure, inspections and holds. The yard is where most of what a 214 or an EPCIS event reports physically happens, so a wrong
mapping here would do the most damage; the rule below is therefore "when in doubt, publish nothing".

## Decision

### 1. Three optional fields on the `data` of the facts

| Field | Value | Present when |
|---|---|---|
| `x12_status` | the ANSI X12 214 shipment status code (element 1650, carried in AT7-01), two characters | the table below maps the event to a code |
| `cbv_biz_step` | the GS1 CBV 2.0 business step as the URN `urn:epcglobal:cbv:bizstep:<step>` | the table maps the event to a step |
| `cbv_disposition` | the GS1 CBV 2.0 disposition as the URN `urn:epcglobal:cbv:disp:<disposition>` | the table maps the event (for one outcome or reason) to a disposition |

The CBV values are published in ONE form only, the URN form (CBV 2.0 sections 7.1.1 and 7.2.1), never the short word and never the
`https://ref.gs1.org/cbv/...` web form. A field with no value is OMITTED, never `null` and never `""`: ADR 0004's rule "nullable fields are
`null`" is for the existing fields and is unchanged, and the new fields are the one deliberate exception, because omission is what keeps
every existing payload byte-identical. The integration fact and its analytics twin are built once and carry identical `data` (ADR 0006).
No field carries personal data, and the opaque driver reference of the yard is not touched.

### 2. Only honest mappings

An event (for one deciding value, where its meaning depends on one) maps to a code only when the standard's own definition says what the
fact says. Otherwise the decision is NOT MAPPED, with the reason written in the table, and the field is omitted. A code is never approximated,
widened or invented; "close enough" is not a reason. A later, better-founded mapping is an additive change (a new value on an existing
optional field), whereas a wrong one is a published contract error that history keeps.

### 3. The mapping

`variant` is the one data field a meaning depends on: the `outcome` of `InspectionCompleted`, the `reason` of `TrailerHeld`. Names are as
the code lists publish them. Every one of the 23 published types has a row.

| Event | Variant | `x12_status` | `cbv_biz_step` | `cbv_disposition` | Decision |
|---|---|---|---|---|---|
| `VisitCheckedIn` | any | `I1` In-Gate | `urn:epcglobal:cbv:bizstep:arriving` | not mapped | mapped: the tractor and driver passed the gate in, and the CBV's own example of `arriving` is "truckload of a shipment arrives into a yard". No disposition: a gate pass says nothing about the state of the object |
| `VisitRejected` | any | not mapped | not mapped | not mapped | the visit never passed the gate, so `I1` would say it entered; X12 `A7` (Refused by Consignee) refuses a shipment, not an entry; CBV `entering_exiting` is the retail entrance door |
| `VisitCheckedOut` | any | `OA` Out-Gate | `urn:epcglobal:cbv:bizstep:departing` | not mapped | mapped: the visit left the site through the gate; the CBV's own example of `departing` is "truckload departs a yard, typically through a gate" |
| `TrailerParked` | any | not mapped | not mapped | not mapped | `S1` (Trailer Spotted at Consignee's Location) is a drop at the consignee and a trailer rests on a spot for many other reasons; CBV `storing` is goods moved into and out of storage, not a trailer's position |
| `TrailerDocked` | any | not mapped | not mapped | not mapped | `X5` (Arrived at Delivery Location Loading Dock) and `X8` (Arrived at Pick-up Location Loading Dock) differ by direction, and the fact carries none (see open decision 2); the CBV has no step for docking |
| `TrailerUndocked` | any | not mapped | not mapped | not mapped | no status or step for leaving a door |
| `DoorWorkStarted` | any | not mapped | not mapped | not mapped | `L1` (Loading) and CBV `loading` say a load, CBV `unloading` an unload, and the fact carries no direction |
| `DoorWorkCompleted` | any | not mapped | not mapped | not mapped | `D1` (Completed Unloading at Delivery Location), `CP` (Completed Loading at Pick-up Location) and CBV `unloading` / `loading` each say which one was done; the fact carries only the load state afterwards, recorded by the operator and not cross-checked against the work, and PARTIAL fits both (open decision 2) |
| `TrailerDeparted` | any | `OA` Out-Gate | `urn:epcglobal:cbv:bizstep:departing` | not mapped | mapped: the trailer left the site, through the gate. `AF` (Carrier Departed Pick-up Location with Shipment) and `CD` (Carrier Departed Delivery Location) differ by direction, which the fact does not carry |
| `SealExceptionRaised` | any | not mapped | not mapped | not mapped | a seal number that differs from the expected one is not a broken seal (CBV `container_open`), X12 214 has no seal status, and a finding is not an activity |
| `TrailerDwellExceeded` | any | not mapped | not mapped | not mapped | a threshold alarm this context derives, not a status either standard defines |
| `TrailerHeld` | `DAMAGED` | not mapped | not mapped | `urn:epcglobal:cbv:disp:damaged` | mapped: the trailer is held because it is damaged, CBV `damaged` is "impaired in its usefulness and/or reduced in value due to a defect" |
| `TrailerHeld` | `SEAL_BROKEN` | not mapped | not mapped | `urn:epcglobal:cbv:disp:container_open` | mapped: CBV `container_open` is "a container's doors have been opened; or a seal of a shipment has been broken" and "makes no indication as to whether the act of opening was authorised", which is what a broken-seal hold also does not say |
| `TrailerHeld` | `NOT_FOUND` | not mapped | not mapped | not mapped | no CBV disposition says an object was not found where it was expected (`unknown` means its condition is not known) |
| `TrailerHeld` | `QUALITY` | not mapped | not mapped | not mapped | a quality hold may follow a failed inspection (`non_conformant`) or any other judgement, and the fact does not say which |
| `TrailerHeld` | `SECURITY` | not mapped | not mapped | not mapped | a security hold does not say the object is stolen or tampered with |
| `TrailerHoldReleased` | any | not mapped | not mapped | not mapped | CBV `available` is "returned to service" and a released hold does not say the cause was cleared |
| `TrailerSecured` | any | not mapped | not mapped | not mapped | a vehicle restraint or wheel chocks are neither a shipment status in X12 214 nor an activity in the CBV (`container_closed` is a loaded container with doors closed and a seal applied) |
| `TrailerSecuringReleased` | any | not mapped | not mapped | not mapped | the same, in reverse |
| `YardMoveRequested` | any | not mapped | not mapped | not mapped | an internal yard movement; CBV `transporting` is a move between locations by a vehicle, not a hostler's move within one yard |
| `YardMoveClaimed` | any | not mapped | not mapped | not mapped | an internal yard bookkeeping fact; X12 214 has no shipment status for it |
| `YardMoveCompleted` | any | not mapped | not mapped | not mapped | the same |
| `YardMoveCancelled` | any | not mapped | not mapped | not mapped | the same |
| `YardMoveLeaseExpired` | any | not mapped | not mapped | not mapped | the same |
| `InspectionCompleted` | `PASSED` | not mapped | `urn:epcglobal:cbv:bizstep:inspecting` | `urn:epcglobal:cbv:disp:conformant` | mapped: `inspecting` is "reviewing objects to address potential physical or documentation defects" and `conformant` is "outcome of a successful/passed inspection in an inspecting or repairing step". X12 214 has no inspection status |
| `InspectionCompleted` | `FAILED` | not mapped | `urn:epcglobal:cbv:bizstep:inspecting` | `urn:epcglobal:cbv:disp:non_conformant` | mapped: `non_conformant` is "outcome of an unsuccessful/failed inspection in an inspecting or repairing step" |
| `InspectionFailed` | any | not mapped | `urn:epcglobal:cbv:bizstep:inspecting` | `urn:epcglobal:cbv:disp:non_conformant` | mapped, the same facts as `InspectionCompleted` FAILED; X12 reason code `P2` (Waiting Inspection) is a reason (element 1651), not a status, and does not say the inspection failed |
| `YardCheckClosed` | any | not mapped | not mapped | not mapped | a yard check reconciles trailers against spots; CBV `stock_taking` and `cycle_counting` count objects within a location for inventory |

Notes a reader of the code lists needs:

- `I1` and `OA` are Shipment Status Codes of X12 element 1650 (AT7-01). Their names are the generic "In-Gate" and "Out-Gate"; carriers
  best known for using them are railroads and intermodal terminals, whose published 214 guides say `I1` is the in-gate and `OA` the out-gate.
  A yard gate is a gate, and the code does not say whose. The X12 214 is a carrier-to-shipper message: this context does not produce a 214, it
  states in the 214 vocabulary what its own fact means.
- The same real arrival is also stated by dock-scheduling's `DockAppointmentCheckedIn` as `X1` or `X3` (arrived at the delivery or pick-up
  location, which needs the appointment's direction). The two facts are complementary, not contradictory: this one says the gate was passed
  (`I1`), that one says where the carrier arrived. See open decision 1.
- The CBV says `receiving` is mutually exclusive with `arriving` and `accepting`; the yard never claims `receiving`, which is inbound-receiving's
  fact (the goods are added to inventory there).
- Direction (`TrailerDocked`, door work, the `AF`/`CD`/`CP`/`D1` family) is deliberately not inferred. The trailer aggregate can derive one
  from its load state, an appointment or the visit purpose, but that is the yard's own inference (Y-9) and it is not on the fact, so a
  partner could not verify the code from the payload.

Sources (read on 2026-11-17):

- X12 element 1650, Shipment Status Code: https://www.stedi.com/edi/x12-004010/element/1650 (`I1` In-Gate, `OA` Out-Gate; also `X5`, `X8`,
  `L1`, `AF`, `CD`, `CP`, `D1`, `S1`, `A7`). Element 1651, status reason codes: https://www.stedi.com/edi/x12-004010/element/1651 (`P2`).
  The AT7 segment carries 1650 in AT7-01: https://www.stedi.com/edi/x12-004010/segment/AT7.
- Independent vectors for `I1` and `OA`: Canadian National's 214 implementation guide, https://cn.ca/-/media/files/customer-centre/customer-centre-documents/214-implementation-guide-4010-en.pdf
  (its example segment `AT7*I1*NS***20250112*1430` is annotated "I1=ingate"; its event table lists "In gate I1" and "Out gate OA"), and
  https://cn.ca/-/media/Files/Customer-Centre/Customer-Centre-Documents/EDI-214-implementation-guide-en.pdf ("In-Gate I1", "Out-Gate OA").
  Gnosis Freight's 214 guide on Stedi lists `I1` In-Gate and `OA` Out-Gate next to `X1` and `X3` "(Gate In)": https://portal.stedi.com/app/guides/view/gnosis-freight/gnosis-freight-carrier-shipment-status-message/01J68AT318NPAT030ERAD2Q5AV.
- GS1 Core Business Vocabulary 2.0 (Ratified, Jun 2022), https://ref.gs1.org/standards/cbv/, section 7.1 (business steps) and 7.2 (dispositions), with the
  value pages https://ref.gs1.org/cbv/BizStep-arriving, https://ref.gs1.org/cbv/BizStep-departing, https://ref.gs1.org/cbv/BizStep-inspecting,
  https://ref.gs1.org/cbv/Disp-conformant (the same page lists `non_conformant`, `damaged`, `container_open` and `container_closed`). The URN form is
  specified in 7.1.1 and 7.2.1; the CBV's own example writes `urn:epcglobal:cbv:bizstep:departing`.

### 4. One place, applied where the outbox row is built

`internal/domain/standards` holds the table (`Of(event, variant)`, `Decisions()`), pure Go with no I/O. `outbox.Payload`
(`internal/application/outbox`), the one function that turns a domain event into an outbox row and that the encoder's golden tests drive,
applies it: it appends the fields AFTER every existing field of the already-encoded data, so the bytes of every existing field are exactly what
`json.Marshal` of the event wrote, and returns the data untouched when the table maps nothing. The one field a meaning depends on (the `outcome` of
`InspectionCompleted`, the `reason` of `TrailerHeld`) is read from the wire data by its json name, not from the Go type, because an event raised
through MCP is wrapped by the attribution (ADR 0007), which re-marshals it through a map and adds `initiated_by`: the same fact means the same
whichever adapter raised it (`attribution_standards_internal_test.go`). The integration and analytics payloads
therefore cannot differ. Three tests make the table impossible to forget:

- `standards_test.go` reads the domain: every struct that embeds `shared.Header` is an event and must have a decision, every value of
  `trailer.HoldReason` and every completed `inspection.Status` must be decided, and the table decides nothing that no longer exists;
- `internal/architecture/standards_fitness_test.go` does the same for the types `apis/asyncapi.yaml` publishes, checks that the contract declares
  each standard field (optional, never `required`) on exactly the data schemas whose event the table maps it for, and that this ADR lists every
  event, every variant, every code and its sources;
- the exact-JSON goldens (`encoder_test.go`) pin the payloads: `VisitCheckedIn`, `VisitCheckedOut`, `TrailerDeparted`, `InspectionCompleted`
  (PASSED and FAILED) and `InspectionFailed` changed, `TrailerHeld` has a golden per mapped reason (DAMAGED, SEAL_BROKEN) and one that is
  byte-identical to before (QUALITY), and the other seventeen types are byte-identical to before.

### 5. Contract and surface

`apis/asyncapi.yaml` declares the fields (optional, never sent as null, a `pattern` on the URN fields) on `VisitCheckedInData`,
`VisitCheckedOutData`, `TrailerDepartedData` (`x12_status`, `cbv_biz_step`), `InspectionCompletedData`, `InspectionFailedData`
(`cbv_biz_step`, `cbv_disposition`) and `TrailerHeldData` (`cbv_disposition`), with the values in the examples of both topics. A schema declares a
field only where the table maps it for at least one variant, so the contract never promises a code the table cannot produce. `apis/openapi.yaml`
is NOT changed (no REST request or response carries these fields, so the OpenAPI document and the generated `docs/docs/api-reference/rest` stay as they are): no REST route or MCP tool exposes the table (the MCP tool budget of ADR 0007 is a hard cap, and there is no read
model of published facts to hang a route on); the AsyncAPI document and this ADR are the published surface. The `read_point_gln` field of
dock-scheduling's ADR 0013 is NOT carried here (open decision 3).

## Out of scope

- An X12 / EDI translation adapter (producing a 214) and an EPCIS export. This ADR only makes the semantics available on the facts so that such
  a context, if it exists, is a pure translation.
- A GS1 GLN read point. The yard consumes `LocationSlotRegistered` into its own location copy, but storing the optional `gln` there needs a
  column and migration; it is a separate change.
- Backfilling the facts already published: the fields appear on facts recorded after the deployment.
- Any code for a fact this table decides not to map.

## Open decisions

1. **I1/OA or X1/X3 for the gate arrival.** `VisitCheckedIn` is stated as `I1` because that is purpose-independent and exactly what the fact is (a
   gate pass). `X1` (Arrived at Delivery Location) and `X3` (Arrived at Pick-up Location) would match what dock-scheduling publishes for the
   same arrival, but they need a direction, and of the visit purposes only `LIVE_UNLOAD` (delivery) and `LIVE_LOAD`/`PICKUP` (pick-up) have one:
   `DROP` and `BOBTAIL` do not. If the owner prefers `X1`/`X3` where the purpose has a direction, that is an additive change to this table
   (a variant on `purpose`), not a schema change.
2. **Direction on the door facts.** `TrailerDocked`, `DoorWorkStarted` and `DoorWorkCompleted` could carry `X5`/`X8`, `L1` and `D1`/`CP`, and
   CBV `loading`/`unloading`, if the fact said which direction the work has. Publishing a direction is a new payload field and a decision
   about which of the yard's inferences (Y-9) may be stated to partners; it is left for a decision rather than guessed here.
3. **`read_point_gln`.** Not implemented (out of scope above). Whether the yard publishes the GLN of the door a fact happened at, reusing its
   door copy as dock-scheduling does, is open.

## Consequences

- Partners can read `x12_status`, `cbv_biz_step` and `cbv_disposition` without our enums. A consumer that does not know the fields keeps
  working: the fleet's consumers decode by field name and ignore the rest, and this service's own analytics projector decodes with the
  tolerant reader (`contracts_tolerance_test.go`). A consumer that rejects unknown fields would break, which the fleet event standard forbids.
- The consumers that pin this repository's `apis/asyncapi.yaml` (dock-scheduling, inbound-receiving, exception-management and
  outbound-shipping, and this repo's own `testdata/contracts/yard-management.asyncapi.yaml`) need a re-pin after this merges. It is additive and
  the pinned copies still decode. This change does NOT touch the self-pinned copy: the `pinned-contracts` gate compares every pin with the producer's
  `develop`, so refreshing it in the same PR would turn that gate red until the merge; it is refreshed in the follow-up, as 0016's was.
- Adding an event type forces a decision in `internal/domain/standards` and a row here, or the build fails.
- A wrong mapping is a published contract error: it is fixed by a new value on the optional field (the old value stays in history). That is why
  the rule is "only honest mappings" and why the unmapped decisions are written down with their reasons.
- The visible behaviour change is exactly that: facts recorded after deployment carry the extra fields on the six event types above. Nothing
  else observable changes (no route, no state, no stored column).
- Rolling back the code removes the fields from new facts; nothing is stored, so there is nothing to migrate back.

## Alternatives considered and rejected

- **Map every event to the nearest X12 code** (`S1` for a parked trailer, `D1`/`CP` for door work, `A7` for a rejected visit, `AF`/`CD` for a
  departure): each overstates or guesses what the fact says and a partner would act on it. Rejected by the honest-mapping rule.
- **`entering_exiting` for the gate.** Rejected: the CBV defines it at the entrance/exit door of a facility "where customers are either
  leaving with purchased product or entering with product to be returned"; the yard gate is a carrier gate, for which `arriving` and
  `departing` give a yard example.
- **Translate in a consumer or an adapter only.** Rejected for now (out of scope): the mapping is knowledge of this context's facts, and a
  translation adapter would have to rediscover it.
- **Publish the CBV values as the short word.** Rejected: the URN is the CBV's own identifier and cannot be confused with free text.
- **Publish the fields as `null` when absent.** Rejected: it would change the bytes of every existing fact and break pinned goldens; omission is
  the fleet's additive pattern.
- **Put the fields on the domain event structs.** Rejected: the mapping is a published-contract concern, so it lives in one table applied where
  the row is built, and the domain events keep exactly the fields of their aggregate's facts.
