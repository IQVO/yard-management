# ADR 0013: Dock safety interlock: a trailer is secured before door work starts and released before it leaves its door

## Status

Accepted (2026-11-10). Refines 0002 (the Trailer and YardPolicy invariants), 0004 (two integration types are added to the catalogue) and 0007
(the tool budget goes from fourteen to sixteen, see section 6). Additive: no topic, no existing type, no existing field and no existing request
changes meaning, and a site that does not opt in behaves exactly as before (section 2). Code: `internal/domain/trailer` (`securing.go`,
`work.go`, `movement.go`), `internal/domain/yardpolicy`, `internal/application/usecases` (`doors.go`, `moves.go`),
`migrations/0004_dock_safety_interlock`.

## Context

A loading dock is the most dangerous place on a warehouse site: a forklift drives into a trailer that is not held to the dock. If the
trailer creeps or the tractor pulls away, the truck leaves under the forklift ("trailer creep" and "premature departure"). The rule that
prevents it is old and written down: a trailer must be kept from moving while it is loaded or unloaded, with a vehicle restraint or wheel
chocks (OSHA 29 CFR 1910.178(k)(1) for powered industrial trucks; ANSI MH30.3 for dock safety practice; EN 1398 for dock levellers).

The yard knew where a trailer was and whether its door work had started, and recorded both after the fact. It had no notion of "secured",
so it could not refuse the unsafe step: door work started on any DOCKED trailer, and a docked trailer could be driven away. The goal of this
ADR is that the system refuses the step instead of recording it afterwards.

Alternatives weighed: make it a hard rule for every site from day one (rejected: it changes the behaviour of every existing site and every
existing client, which do not record a securing, so door work would start failing on release); infer "secured" from the door (rejected: the
yard has no dock hardware feed, and an inferred fact is not the operator's assertion); model the restraint as part of the door in the
facility-layout context (rejected: a securing is a fact about a trailer at a moment, not a property of a place).

## Decision

### 1. The Trailer records how it is secured

`Trailer` gains `securedAt`, `securedBy` and an optional `securerRef`, all empty while the trailer is not secured:

- `securedBy` is the method, `RESTRAINT` (a vehicle restraint engaged) or `WHEEL_CHOCKS` (the wheels chocked). Any other value is
  `422 validation`.
- `securedAt` is the instant the securing was recorded; `securerRef` is the optional reference of the person who did it (like the other
  performer references, `hostlerRef` and `driverRef`; at most 64 characters).
- Only a trailer that is `DOCKED` is secured. A secured trailer has therefore a door and, while secured, no other state; `Restore`
  refuses a row that says otherwise, and a row from before this ADR (nothing secured) still loads.

Two commands:

- **`SecureTrailer`** (`POST /trailers/{trailerId}/securings`, body `{method, securerRef?}`). It needs a DOCKED trailer, else `409
  illegal-transition`. It is **idempotent for the same method**: securing again with the method the trailer already has changes nothing,
  raises nothing, keeps the first instant and performer, and answers 200. A **different** method while secured is `409
  illegal-transition` (release first: a trailer is not "re-secured" silently). A trailer with an **open move** cannot be secured (`409
  trailer-has-open-move`): the move would take it off the door.
- **`ReleaseTrailerSecuring`** (`POST /trailers/{trailerId}/securing-releases`). It clears the three fields. A trailer that is not secured is
  `409 trailer-not-secured` (there is nothing to release). While door work is `IN_PROGRESS` the securing is kept: `409 door-work-open`,
  because loading or unloading must never go on after the restraint is released; complete the door work first.

### 2. The interlock is a site policy, off by default

`YardPolicy` gains **`requireSecuringBeforeDoorWork`** (`PUT /sites/{siteId}/yard-policy`, optional boolean, **default `false`**; replacing the
policy without the field turns it off again, like `minimumSealClass`).

- **`false` (the default) is today's behaviour exactly.** `StartDoorWork` does not look at the securing. A site opts in when it is ready to
  record securings: a regulated site (OSHA 29 CFR 1910.178(k)(1)) sets it `true`. Making `true` the default is a decision for a later ADR once
  the sites and clients record securings; this ADR does not take it, because that change would be breaking for every client that never calls
  `SecureTrailer`.
- **`true`**: `StartDoorWork` on a trailer that is not secured is `ErrTrailerNotSecured`, `409 trailer-not-secured`. The order of refusals
  is: not DOCKED or door work not WAITING (`illegal-transition`), held (`trailer-on-hold`), not secured (`trailer-not-secured`). Door work that
  is already `IN_PROGRESS` when the policy is switched on is not interrupted: the rule judges a start.

**The KPI "door-work starts without the interlock must be 0 where the policy is on" holds by construction**, not by a counter: such a start
is refused before `DoorWorkStarted` exists, so it cannot be counted. Auditing it needs no fold (see Consequences).

### 3. A secured trailer does not leave its door

Whatever takes a trailer off its door is refused while it is secured, with **`ErrTrailerStillSecured`, `409 trailer-still-secured`**. This
rule is **not conditional on the policy**: a securing that was recorded is a fact that the restraint is engaged, whether or not the site
requires it. A trailer nobody secured is unaffected, so a site that never uses the commands never meets the error.

The directed ways off a door in this context are three, and each one is covered:

| Way off the door | Where it is refused |
|---|---|
| `RequestMove` of a docked trailer to a spot or another door (supervisor) | the use case at request time (`Trailer.CanLeaveDoor`), so the supervisor hears it when asking and no move is queued |
| the claim of a move (`StartMoving`, `ClaimNextMove`) | the domain, as the invariant behind the request check |
| the gate check-out of a visit that takes a docked trailer (`Depart`, `CheckOutVisit`) | the domain; the check-out is one unit of work, so a refused trailer rolls the whole check-out back and nothing is published |

`ReleaseTrailerSecuring` is the way through. Because `ClearDoorAfterWork` (a DROP trailer whose door work is done gets its FROM_DOOR move)
runs when door work completes, it now **stands down while the trailer is secured** (a move that could never be claimed would block the
site's move queue) and `ReleaseTrailerSecuring` runs it again, so a DROP trailer is cleared as soon as the securing is released. The
order of refusals at check-out is door work open (`door-work-open`), then still secured.

### 4. AutoCompleteUnload is not blocked

`ReceiptClosed` completes an unload by itself (`AutoCompleteUnload`, Y-7: WAITING or IN_PROGRESS to DONE). It completes work that was done;
it does not start any, so it uses the policy-free `StartDoorWork` and never `ErrTrailerNotSecured`. The unload completes whether or not the
trailer was secured. If it is secured the trailer stays so (and `ClearDoorAfterWork` stands down) until the securing is released.

### 5. Events and the contract

Two integration types, on `warehouse.yard-management.events` and mirrored on `warehouse.yard-management.analytics` with the same `id`
(ADR 0004, ADR 0006): `com.warehouse.wms.yard-management.trailer.TrailerSecured` and `...trailer.TrailerSecuringReleased`, key and subject
`trailer_id`, `dataschema` `urn:warehouse:yard-management:<events|analytics>:<EventName>:v1`.

| Event | `data` |
|---|---|
| `TrailerSecured` | `trailer_id`, `site_id`, `door_code`, `method` (`RESTRAINT` or `WHEEL_CHOCKS`), `secured_at`, `version` |
| `TrailerSecuringReleased` | `trailer_id`, `site_id`, `door_code`, `released_at`, `version` |

`version` is the aggregate version after the change, which ADR 0004 gives to every event that is not advisory. The performer reference is
on the read model, not in the events. The catalogue goes from 19 to **21 types, 11 integration** (`TestGoldenCasesCoverTheWholeCatalogue`,
`TestEventCatalogueMatchesContract`). The three types outbound-shipping pins and consumes (`TrailerDocked`, `DoorWorkCompleted`,
`TrailerDeparted`) are not touched; no type is removed and no payload field is added or changed by this ADR. A consumer of the integration
topic that does not know the two new types ignores them (CloudEvents rule: an unlisted `type` is ignored), and nobody consumes them yet.

OpenAPI: `POST /trailers/{trailerId}/securings`, `POST /trailers/{trailerId}/securing-releases`, `Trailer.securedAt|securedBy|securerRef`
(nullable), `YardPolicy.requireSecuringBeforeDoorWork` and the request field of the same name, and problem examples for the two slugs.

### 6. MCP

Two write tools at L3 (approval and `initiated_by` like the other writes, ADR 0007): **`secure_trailer`** (`trailerId`, `method`,
`securerRef` optional) and **`release_securing`** (`trailerId`), each with the required `idempotencyKey`. The read tools carry the new
fields without a change of their own: they return the same `Trailer` and `YardPolicy` views, whose output schema now lists them (the
registry golden `testdata/tool_registry.golden.json` is regenerated). The tool budget of ADR 0007 (M4: fourteen) is **raised to sixteen**:
this ADR is the amendment M4 asks for. A securing is a dock operator's act, so the tools exist for a human-approved proposal (an operator
who cannot reach the floor client), not to let an agent assert a securing on its own: L3 means a human approved it.

### 7. Persistence (migration 0004, additive)

`trailers` gains `secured_by` (`NOT NULL DEFAULT ''`, checked against `''`, `RESTRAINT`, `WHEEL_CHOCKS`), `secured_at` (nullable) and
`secured_by_ref` (`NOT NULL DEFAULT ''`) with a check that the instant is present exactly when the method is and a performer needs a method;
`yard_policies` gains `require_securing_before_door_work` (`NOT NULL DEFAULT false`). Every existing row loads as not secured with the
policy off, and the previous release's statements still run. The down migration drops the four columns.

## Consequences

- Where a site opts in, the unsafe step (starting to load a trailer that is not held to the dock) is refused with a stable slug and nothing
  is published; and a secured trailer cannot be driven away from its door or out of the gate. Where it does not, nothing changes.
- A site that turns the policy on must have its clients call `SecureTrailer`. Door work started by clients that do not will fail with
  `trailer-not-secured`. That is the point of an opt-in, and it is why the default stays `false`.
- Three behaviours are visible beyond the new endpoints, all only for a trailer that is secured: a move off its door is refused,
  its check-out is refused, and `ClearDoorAfterWork` waits for the release. A trailer that was never secured is not affected.
- The analytical side is **not** changed: the two events are on the analytics stream, the projector ignores types it does not fold, and
  `trailer_facts` / `door_work` get no securing columns. A later fold ("securing latency" between docking and securing, or a counter of
  refused starts) needs only a projector decode and an analytics migration, no contract change. A refused start leaves no event, so a counter of
  refusals would need a new advisory event; the KPI "starts without the interlock" does not.
- Securing is a recorded assertion by a person or a client; the yard has no sensor and cannot tell that the restraint is really engaged.
  A hardware feed (restraint status from the dock equipment) would be a different, later ADR and a new input, not a change of this model.
- `SecureTrailer` is a floor action (the dock operator) on REST; its MCP twin exists only behind approval.

## Alternatives considered and rejected

- **Default `true`**: safest in principle, breaking in practice (see Context); the opt-in policy is the rollout path, and the default may
  flip in a later ADR.
- **Auto-release the securing when door work completes**: the restraint is released by a person at the dock, and the yard must not record a
  release that did not happen; the explicit release is also the signal that makes `ClearDoorAfterWork` safe to run.
- **Make `trailer-still-secured` conditional on the policy**: it would let a policy flip make an engaged restraint invisible; the fact is
  the fact.
- **Refuse the claim only (not the request)**: the supervisor would queue a move that blocks the site's move queue at the head until someone
  noticed; failing at request time keeps the queue clean, and the claim keeps the invariant.
- **A counter event for refused starts**: an event for something that did not happen; the refusal is already a 409 in the access log and the
  KPI is zero by construction.
