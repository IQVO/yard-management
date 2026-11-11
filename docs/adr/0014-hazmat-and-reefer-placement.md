# ADR 0014: Hazmat and reefer placement: a trailer is parked on a spot whose kind its contents allow

## Status

Accepted (2026-11-10). Refines 0002 (the Trailer and YardPolicy invariants and the placement rules of `CanPlaceAt`), 0009 (the facility
consumer reads one more optional field) and 0012 (the optional gate attributes follow the same pattern). Additive: no topic, no type, no
existing field and no existing request changes meaning, and a trailer that declares nothing, or a spot that was registered without a kind,
is judged exactly as before (section 5). Code: `internal/domain/shared` (`hazmat.go`, `spotkind.go`), `internal/domain/trailer`
(`classification.go`, `placement.go`), `internal/domain/yardpolicy`, `internal/domain/yardvisit`, `internal/application/usecases`
(`targeting.go`, `gate_checkin.go`, `doors.go`, `consumers.go`), `internal/adapters/inbound/kafka/facility_consumer.go`,
`migrations/0005_hazmat_and_reefer_placement`.

## Context

A trailer of flammable liquid parked next to a dock door, or a refrigerated trailer parked where it cannot be plugged in, is a failure
that the yard used to record after the fact: the yard picked "the first free spot" and knew nothing of what the trailer carried or what
the spot was for. The regulations imply the rule: hazardous materials stand where the site has set aside for them (49 CFR 177.800 ff. for
carriers, the site's own fire and hazmat plan), and a refrigerated load needs power.

facility-layout (its ADR 0045) now publishes, on `LocationSlotRegistered`, an optional `spotKind` on a Yard slot (`PARKING`, `HOLDING`,
`REEFER_PLUG`, `HAZMAT`, `STAGING_LANE`) and an optional `gln`. The yard already consumes `LocationSlotRegistered` into its local
location read model (ADR 0003: no synchronous call to a sibling). This ADR persists the kind, lets a trailer say what it carries, and
makes the two places that choose a spot refuse a placement the contents forbid.

## Decision

### 1. What a trailer carries: `Classification`

`Trailer` gains an optional classification (`trailer.Classification`, a value object; the zero value declares nothing):

- **`hazmatClass`**: the US DOT hazard class or division as written in 49 CFR 172.101: `1`, `1.1` to `1.6`, `2`, `2.1` to `2.3`, `3`,
  `4`, `4.1` to `4.3`, `5`, `5.1`, `5.2`, `6`, `6.1`, `6.2`, `7`, `8`, `9`. Anything else is `422 invalid-hazmat-class` (`ErrInvalidHazmatClass`).
  The value is not normalised (no padding, no lower case, no compatibility group such as `1.4S`): the stored value is the one the caller sent.
- **`unNumber`**: `UN` and exactly four digits. Anything else is `422 invalid-un-number` (`ErrInvalidUNNumber`). A UN number needs a
  `hazmatClass` (`422 validation`): a number with no class says nothing the yard can act on.
- **`requiresTemperatureControl`**: boolean, default `false`. A load that needs a refrigeration plug. It is a declaration, not inferred from the
  trailer kind: a `REEFER` that is empty or carries a load that needs no cooling does not need a plug.

They are set at the gate (`trailers[]` of `POST /visits` gain the three optional fields) and afterwards with a new command following ADR 0012's way
for attributes that are corrected later:

- **`ClassifyTrailer`** (`POST /trailers/{trailerId}/classifications`, body `{hazmatClass?, unNumber?, requiresTemperatureControl?}`): a **full
  replacement**, like the yard policy, so a field left out is cleared and `{}` declares nothing. The same facts again change nothing and answer 200.
  A `DEPARTED` trailer is `409 illegal-transition`. Classifying does **not** move a parked trailer: the rules judge a placement, and a
  trailer already on a spot is not evicted by a declaration (a supervisor sees it and moves it; the move is then judged).

The views (`Trailer`) carry `hazmatClass`, `unNumber`, `requiresTemperatureControl` (null, null and `false` for a trailer that declares nothing, and
for every trailer stored before this ADR).

### 2. The spot kind is a consumed fact

`LocationView` gains `spotKind`, taken from `LocationSlotRegistered` on a Yard slot only (never set by this context). The decode is tolerant:

- absent, `null` or any value this service does not know (a later facility-layout enum value) is stored as **no kind** (`''`), the legacy state;
- a known value (`PARKING`, `HOLDING`, `REEFER_PLUG`, `HAZMAT`, `STAGING_LANE`) is stored;
- registering the same slot again replaces it (also back to none): the producer sets the kind at registration and there is no update event yet;
- `gln` is not used by the yard; it is documented in the consumed contract and ignored.

### 3. The placement rules

The rules are a pure function of the trailer and three facts about its site: does the site have any active `HAZMAT` spot, any active
`REEFER_PLUG` spot, and the policy `allowHazmatOnUnclassifiedSpots` (section 4). They apply to a **yard spot** only; a dock door is exempt
(a reefer at a dock door is fine, and so is a hazmat trailer being unloaded there).

| Trailer | Site | Allowed spots |
|---|---|---|
| carries a `hazmatClass` | has a `HAZMAT` spot | `HAZMAT` spots only |
| carries a `hazmatClass` | no `HAZMAT` spot, policy `allowHazmatOnUnclassifiedSpots` true | spots with **no kind** only |
| carries a `hazmatClass` | no `HAZMAT` spot, policy false (default) | none: `409 placement-rule-violated` |
| `requiresTemperatureControl`, no `hazmatClass` | has a `REEFER_PLUG` spot | `REEFER_PLUG` spots only |
| `requiresTemperatureControl`, no `hazmatClass` | no `REEFER_PLUG` spot | unrestricted, as before |
| both | any | the hazmat row only: no spot kind says "hazmat with power", and the stricter safety rule wins |
| declares nothing | any | unrestricted, as before |

The rule is enforced where a spot is chosen or confirmed:

- **`DirectOnCheckIn`** (`CheckInVisit`): the search for a free spot (the holding spots first for a walk-in or an early arrival, then the free
  spots of the parking zones) is limited to the spots the rule allows, so a hazmat trailer is sent to a `HAZMAT` spot and a refrigerated one to
  a plug; the rule therefore wins over `TO_HOLDING` for such a trailer. When the rule allows spots at the site but none is free the answer is `409
  no-free-spot` as today; when the rule allows **no** spot at the site at all (the third hazmat row) it is `409 placement-rule-violated`. A refused
  check-in persists and publishes nothing. A door chosen for an appointment is not judged.
- **`ClearDoorAfterWork`** (the `FROM_DOOR` move of a `DROP` trailer whose door work is done) searches with the same rule. Where the rule allows no
  spot at the site at all it stands down like it does for `no-free-spot`: completing door work never fails for lack of a spot, and the supervisor places the trailer.
- **`RequestMove`** to a yard spot: `CanPlaceAt` refuses with `ErrPlacementRuleViolated`, `409 placement-rule-violated`, **before** the destination
  is reserved, so no move is queued.
- **`CompleteMove`** (the hostler arrives somewhere other than the requested spot, ADR 0002 Y-8): the arrival is judged by the same `CanPlaceAt`,
  so a trailer is not parked on a forbidden spot by being "diverted" on the floor.

The error is `ErrPlacementRuleViolated`; its **detail names the rule and the spot**, for example `placement rule violated: a trailer with hazmat
class 3 may only be placed on a HAZMAT spot; spot WH1-Y02 is not classified`. The order of refusals in `CanPlaceAt` is unchanged for existing
errors (occupied, door closed, flow mismatch, hold) and the spot rule is judged last, on a spot.

**Not enforced (by decision).** A trailer with no classification may stand on a `HAZMAT` or `REEFER_PLUG` spot even when a plain spot is free: too
strict for a yard that is full, and the yard cannot know the reefer plug is wanted by another trailer. The search for a free spot for such a
trailer stays "first free by code", as before.

### 4. The policy `allowHazmatOnUnclassifiedSpots`

`YardPolicy` gains **`allowHazmatOnUnclassifiedSpots`** (`PUT /sites/{siteId}/yard-policy`, optional boolean, **default `false`**; replacing the policy
without the field turns it off, like the other optional fields). It exists for a site whose facility-layout has not classified its spots yet: it lets a
trailer with a hazard class take a spot of **unknown kind**, and **only** where the site has no `HAZMAT` spot at all. It never admits a spot of another
kind (a `PARKING` or `REEFER_PLUG` spot is classified, and is not "unknown"), and it never applies once the site has a `HAZMAT` spot.

### 5. Rollout safety: legacy behaves exactly as before

- A trailer that carries none of the three fields (every trailer today, and every client that does not send them) is judged by no rule: the use cases
  do not even read the spots of its site.
- A spot with no kind (every spot registered before facility-layout ADR 0045, and every one registered without it) is the legacy state. It is judged
  as before for any trailer that declares nothing; for a trailer that does declare, it is "not classified" in the table above.
- A site with no `HAZMAT` spot and no `REEFER_PLUG` spot gets no new refusal for a refrigerated trailer, and for a hazmat trailer only the one the
  table gives (refusal unless the policy allows it). Sending a hazard class is the opt-in: nothing in the yard declares it for a client.
- The policy defaults to the stricter reading for hazmat (`false`) and to "no change" for everything else. The default is part of the opt-in: the
  first trailer that declares a hazard class at a site whose spots are not classified is refused until the site classifies a `HAZMAT` spot or sets the
  policy. That is the intended behaviour of a safety rule, and it cannot affect a client that does not send `hazmatClass`.

### 6. Events and the contract

No new published type: **the catalogue stays at 21 types, 11 integration** (ADR 0004, ADR 0013). Three optional payload fields are added, all
additive and nullable, on two existing types:

| Event | New `data` fields |
|---|---|
| `VisitCheckedIn` (each `trailers[]` entry) | `hazmat_class` (string or null), `un_number` (string or null), `requires_temperature_control` (boolean, `false` when not declared) |
| `TrailerDocked` | the same three |

The three types outbound-shipping pins and consumes (`TrailerDocked`, `DoorWorkCompleted`, `TrailerDeparted`) stay decodable: `TrailerDocked` only gains
fields a tolerant reader ignores, and its existing fields keep their names, types and meaning. The analytics twin (ADR 0006) is byte-identical, as always.
`spot_kind` is **consumed only**; no published event carries it.

AsyncAPI: the two payload schemas and examples, and the consumed `LocationSlotRegistered` (restated, ADR 0003) gains `spotKind` and `gln`. OpenAPI:
`TrailerIn.hazmatClass|unNumber|requiresTemperatureControl`, the same three on `Trailer`, `POST /trailers/{trailerId}/classifications`,
`YardPolicy.allowHazmatOnUnclassifiedSpots` and the request field of the same name, and problem examples for the three slugs.

### 7. MCP

No tool is added and **the budget stays at sixteen** (ADR 0007, ADR 0013): classification is a gate and supervisor act, and the MCP server has no
check-in tool. The read tools return the new fields without a change of their own (the same `Trailer` and `YardPolicy` views), and `request_move`
documents the two new slugs, so `testdata/tool_registry.golden.json` is regenerated.

### 8. Persistence (migration 0005, additive)

`trailers` gains `hazmat_class` (`NOT NULL DEFAULT ''`, checked against the accepted set), `un_number` (`NOT NULL DEFAULT ''`, checked against
`^UN[0-9]{4}$`) and `requires_temperature_control` (`NOT NULL DEFAULT false`), with a check that a UN number needs a class; `locations` gains `spot_kind`
(`NOT NULL DEFAULT ''`, checked against the five kinds and empty); `yard_policies` gains `allow_hazmat_on_unclassified_spots` (`NOT NULL DEFAULT
false`). Every existing row loads as "nothing declared", "kind unknown" and "policy off", and the previous release's statements still run. The down
migration drops the columns. `FreeSpots` gains an optional kinds filter (an empty filter is today's query).

## Consequences

- A hazmat or refrigerated trailer is refused an unsuitable spot with a stable slug and a detail that names the rule and the spot, at check-in, at
  move request and at the spot where a move is completed, instead of being recorded there.
- A site must classify its spots in facility-layout to get the benefit. Until then the rule is inert for reefers (no plug known) and is the
  `allowHazmatOnUnclassifiedSpots` decision for hazmat.
- The classification is a declaration by the shipper, the gate clerk or the supervisor; the yard has no way to check what the trailer really holds.
  The rules protect against the yard's own mistakes, not against a wrong declaration.
- A trailer classified after it was parked stays where it is. A report of "classified trailers on spots that do not match" is derivable from the board
  and the location copy and is not built here.
- The analytics fold is **not** changed: the new fields travel on the analytics stream, the projector does not read them, and `trailer_facts` gets no
  hazmat column. A later fold ("hazmat dwell") needs a projector decode and an analytics migration, no contract change.

## Follow-ups (documented, not implemented)

- **Segregation (49 CFR 177.848)**: which hazard classes may not stand on adjacent spots (the segregation table), and a minimum distance, needs the
  geometry of the spots (neighbour relations) that facility-layout does not publish for yard spots, and a table that belongs in a policy of its own.
  Out of scope; a later ADR once the adjacency is published.
- **`STAGING_LANE` and `HOLDING` semantics**: stored and treated as classified spots (a hazmat trailer is not sent to them); a staging rule is for a later ADR.
- A non-hazmat, non-reefer trailer avoiding `HAZMAT` and `REEFER_PLUG` spots while a plain spot is free (decided against, section 3).
- A new `spotKind` update event in facility-layout: the yard picks the change up with the same consumer path (a re-registration replaces the kind).

## Alternatives considered and rejected

- **Infer reefer need from `kind: REEFER`**: a reefer kind says what the trailer is, not whether its load needs a plug; an explicit declaration avoids
  refusing empty reefers a plain spot.
- **Make the rules a site policy switch (`enforcePlacementRules`)**: the declaration is already the opt-in (a trailer that declares nothing is not
  judged), and a second switch would let a regulated site run with the rule off by omission.
- **Evict or hold a trailer when it is classified on a bad spot**: moving a trailer is a physical act the yard orders, not a side effect of a field
  update; the yard refuses the next placement and leaves the trailer where it is.
- **Fall back to a non-HAZMAT spot when every HAZMAT spot is taken**: that is the unsafe step the goal is to refuse; `no-free-spot` is the honest answer.
- **Reject unknown `spotKind` values at the consumer**: a later facility-layout enum value would dead-letter the whole slot; tolerant decode keeps the slot
  and reads the kind as unknown.
