# ADR 0012: Standard identifiers at the gate: ISO 6346 unit numbers, ISO 17712 seal class and a seal state vocabulary

## Status

Accepted (2026-11-10). Refines 0002 (the Trailer and YardPolicy invariants) and 0004 (three payloads gain optional fields). Additive:
no topic, no type, no existing field and no existing request changes meaning, with one visible exception that the Decision names
(section 3: a matching seal now reads `VERIFIED`, a different one `MISMATCH`, where both read `INTACT`). Code:
`internal/domain/shared` (`unitnumber.go`, `sealclass.go`), `internal/domain/trailer` (`seal.go`), `internal/domain/yardvisit`,
`internal/domain/yardpolicy`, `internal/application/usecases` (`gate_checkin.go`, `doors.go`), `migrations/0003_standard_identifiers`.

## Context

The gate accepted any unit number and any seal number. A mistyped container number was discovered at the dock, a customs
declaration or a carrier invoice, never at the gate. Partners and customs speak ISO 6346 (container identification) and ISO 17712
(mechanical seals); the seal state had three values (`NONE`, `INTACT`, `BROKEN`) and said nothing about whether the seal had been
compared with what the appointment expected. Stopping bad data at the gate is the cheapest place to stop it.

The context already has what is needed to do this without a new concept: the trailer kind `CONTAINER` (ADR 0002) is the intermodal
container, so no kind is added. There is no chassis kind; a container on a chassis is one unit, the container.

## Decision

### 1. UnitNumber: ISO 6346 when it applies, free-form otherwise

`shared.UnitNumber` (pure, `NewUnitNumber(raw, kind, declaredScheme)`) validates a unit number at check-in:

- **ISO 6346 applies** when the trailer kind is `CONTAINER`, or when the caller declares `unitNumberScheme: ISO6346` on the trailer.
  A `CONTAINER` cannot opt out. `ISO6346` is the only scheme a caller may declare; any other value is `422 validation`.
- **Everything else stays free-form**: a domestic road trailer (`DRY_VAN`, `REEFER`, `FLATBED`) keeps any number up to 64 characters,
  exactly as before. The natural key `(scac, unit number)` is unchanged.
- **The number is not normalised.** It must be written in its canonical form: upper-case letters, no spaces or hyphens. A lower-case
  or spaced number is refused, not repaired, so the stored number is the one the caller sent and the natural key never changes
  under the caller's feet.

The form is 11 characters: a 3-letter owner code, the category letter `U` (freight container), `J` (detachable freight-container
equipment) or `Z` (trailer or chassis), six serial digits and the check digit. The check digit is computed over the first ten
characters:

1. Letters take the values `A`=10, `B`=12, `C`=13 ... `K`=21, `L`=23 ... `U`=32, `V`=34 ... `Z`=38: the sequence 10..38 with every
   multiple of 11 (11, 22, 33) skipped. Digits take their own value.
2. Multiply the value at position `i` (0 to 9, left to right) by 2 to the power `i`.
3. Sum the products, take the sum modulo 11. A remainder of 10 is written 0. That is the check digit.

Worked example, `CSQU3054383` (the example of the standard): C=13, S=30, Q=28, U=32 and the digits 3 0 5 4 3 8 give
13 + 60 + 112 + 256 + 48 + 0 + 320 + 512 + 768 + 4096 = 6185; 6185 = 562 x 11 + 3, so the check digit is 3. A remainder of 10 occurs
(for example `ABCU000007`: 3926 = 356 x 11 + 10), so `ABCU0000070` is valid and `ABCU0000071` is not.

A failure is `ErrInvalidUnitNumber`, **`422 invalid-unit-number`**, and the detail says why: a wrong check digit names the expected one
(`... has check digit 0; the expected check digit is 3`), a number of ten characters says it has no check digit and which one it
needs, anything else restates the form. The check proves the number is internally consistent. It does not prove the owner code is
registered with the BIC.

A check-in refused here persists nothing (the refusal is raised before the unit of work writes). A trailer already stored is never
re-validated: `Restore` does not apply the ISO rule, so an existing `CONTAINER` row with a free-form number still loads. The read
model carries `unitNumberScheme` (`ISO6346`, or null for a free-form number and for every trailer stored before this ADR).

### 2. SealClass: ISO 17712

`shared.SealClass` is `I` (indicative), `S` (security) or `H` (high security), ordered I < S < H. It is optional everywhere:

- **On the trailer at check-in** (`trailers[].sealClass`) and **on the seal record** (`POST /trailers/{id}/seals`, MCP `record_seal`),
  for the seal states `INTACT` and `APPLIED`. A class needs a seal number (`422 validation` otherwise); a class on a `BROKEN` record
  is `422 validation`. An unknown class (including lower case) is `ErrInvalidSealClass`, **`422 invalid-seal-class`**.
- The appointment copy (ADR 0003) carries the expected seal number only, no class, and that consumed contract is not changed here. So
  the only "expectation" of a class is the site policy below; nothing is compared per appointment.
- **`minimumSealClass`** is an optional field of the site yard policy (`PUT /sites/{siteId}/yard-policy`, default none = today's
  behaviour). When it is set, a seal of a lower class **or without a class** at check-in or record-seal is `ErrSealClassTooLow`,
  **`409 seal-class-too-low`**: an unclassified seal does not satisfy a class requirement. A trailer that arrives with no seal at
  all is not judged by this rule (a missing seal on an outbound load is still `ErrSealRequired` at check-out, unchanged). An
  unknown value in the policy is `422 invalid-seal-class`. Replacing the policy without the field clears it.
- Every existing seal rule (`ErrSealRequired`, the seal mismatch FLAG or REJECT, the `SEAL_BROKEN` hold) is unchanged.

### 3. The seal state vocabulary

| State | Meaning | Produced by |
|---|---|---|
| `NONE` | no seal | a trailer with no seal number |
| `INTACT` | present and unbroken, not yet verified | check-in with a seal number when nothing is expected; `record-seal` with `INTACT` (what every existing flow produces) |
| `APPLIED` | recorded as applied at the origin or dock | `record-seal` with `APPLIED` (new) |
| `VERIFIED` | checked at the gate against the expected number, equal | SealCheckAtGate, match (new) |
| `MISMATCH` | checked at the gate against the expected number, different | SealCheckAtGate, difference (new) |
| `BROKEN` | found broken; the trailer is held `SEAL_BROKEN` | `record-seal` with `BROKEN`, unchanged |

- `INTACT` and `BROKEN` keep their meaning and stay valid. No value is removed or renamed.
- **SealCheckAtGate** compares an `INTACT` or `APPLIED` seal with the appointment's expected seal. Equal: the seal ends `VERIFIED`.
  Different: it ends `MISMATCH` and `SealExceptionRaised` is raised exactly as before (same payload, same FLAG or REJECT
  behaviour; under REJECT no trailer is stored). No seal found, or none expected: nothing changes (`INTACT` stays `INTACT`).
- This is the one visible change to an existing flow: a check-in whose seal matches the appointment now answers `seal.state`
  `VERIFIED` where it answered `INTACT`, and a mismatch under FLAG `MISMATCH`. A consumer that switches on the three old values must
  accept the new ones; everything it could read before is still there.
- `BreakSeal` is allowed from `INTACT`, `APPLIED`, `VERIFIED` and `MISMATCH` (a verified seal can still be found broken). A new
  `record-seal` replaces the number, the class and the state. `VERIFIED` and `MISMATCH` are never accepted from a caller: they are
  facts the gate derives (`record-seal` with them is `422 validation`).
- **The invariant of `snapshot.go` is kept**: a seal number is present exactly when the state is not `NONE`. All five states other
  than `NONE` carry a number; a class is only present with a number. `Restore` validates the wider enumeration, so a corrupt row is
  still refused and a row from before this ADR still loads.

### 4. Surfaces

- **OpenAPI**: `TrailerIn.unitNumberScheme` and `sealClass`, `SealRequest.sealClass` and states `INTACT|APPLIED|BROKEN`,
  `DeclareYardPolicyRequest.minimumSealClass` (all optional request fields); `Trailer.unitNumberScheme`, `Seal.class` and the
  extended `Seal.state` enum, `YardPolicy.minimumSealClass` (nullable response fields); problem examples for the three slugs.
- **AsyncAPI** (additive, never `required`, nullable): `VisitCheckedIn.trailers[].unit_number_scheme` and `seal_class`;
  `TrailerDocked.seal_state` and `seal_class`; `TrailerDeparted.seal_state` and `seal_class`. The other fields of the three
  consumed integration types (`TrailerDocked`, `DoorWorkCompleted`, `TrailerDeparted`), `seal_number` included, are unchanged and
  `DoorWorkCompleted` and `SealExceptionRaised` are untouched. `VisitCheckedIn` carries no seal state because it is emitted before
  the gate verifies the seal; the trailer events carry it. CloudEvents types, topics and dataschema versions do not change, so no
  `.v2` type is needed.
- **MCP**: the write tool `record_seal` gains `sealClass` and the state `APPLIED`; its output schema gains the new nullable view
  fields. The registry golden is regenerated. There is no tool that registers a trailer.
- **REST and MCP errors** share the slugs: `invalid-unit-number` (422), `invalid-seal-class` (422), `seal-class-too-low` (409).

### 5. Persistence (migration 0003, additive)

`trailers` gains `seal_class` and `unit_number_scheme` (`NOT NULL DEFAULT ''`, checked against the enumerations) and the
`seal_state` check widens to the six values; `yard_policies` gains `minimum_seal_class` (`NOT NULL DEFAULT ''`). Every existing row
satisfies the new constraints and loads, and the previous release's statements still run. The down migration is lossy on purpose:
`APPLIED`, `VERIFIED` and `MISMATCH` collapse to `INTACT`, which is what the previous release called every such seal, and the three
new columns are dropped.

## Consequences

- A mistyped container number, an unknown seal class and a seal below the site's minimum are stopped with a stable slug at the gate;
  nothing is stored and nothing is published.
- Domestic road trailers are untouched. A shipper that gives a container kind to a non-ISO unit (a "container" with an internal
  number) is now refused until the kind is corrected or the unit is declared a dry van; this is intended, and it is the only way a
  request that worked before can fail.
- The trailer read model and three event payloads carry the new facts. Consumers that decode only the fields they act on are
  unaffected; outbound-shipping's pinned copy of `apis/asyncapi.yaml` needs its routine refresh to stay equal to this contract.
- The analytical read side is **not** changed: the exceptions report (ADR 0011) does not fold seal class or state counts. The fields
  are on the stream, so a later fold needs no contract change, only an analytics migration, a projector decode and a reader column.
- The check digit is not an owner-code registry lookup, and a seal class is the caller's declaration, not a verified fact.

## Alternatives considered and rejected

- **Normalise before validating** (strip spaces and hyphens, upper-case): friendlier, but it silently rewrites the natural key
  `(scac, unit number)` and every later comparison with a customs or carrier document.
- **A new trailer kind or an ISO-only mode for every trailer**: breaks domestic road trailers, which have no standard number.
- **Let a container opt out with `unitNumberScheme: FREE_FORM`**: it would make the rule optional exactly where it matters.
- **Replace `INTACT` by `APPLIED` or `VERIFIED`**, or split verification into a second field next to the state: a breaking rename
  for every reader of `seal.state`, or two fields that can disagree. One enumeration that only grows keeps a single state machine.
- **Accept `VERIFIED` and `MISMATCH` from callers**: lets a human or an agent assert a comparison the gate did not make.
- **An expected seal class on the appointment**: needs a change to the consumed dock-scheduling contract; the site policy covers
  the need without a new cross-context field.
- **Treat an unclassified seal as satisfying a minimum**: a policy that is silent for the seals that say nothing defeats its purpose.
- **Fold seal class and state into the exceptions report now**: needs an analytics migration, projector and reader changes for a
  number nobody asked for yet; left out, contract-ready.
