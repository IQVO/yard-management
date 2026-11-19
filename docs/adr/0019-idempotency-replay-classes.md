# ADR 0019: Every problem type has a replay class; a retryable refusal is not stored under the Idempotency-Key

## Status

Accepted (2026-11-18). Refines 0008 (the Idempotency-Key middleware: a stored `4xx` is no longer one rule for every refusal), 0007 (the MCP write
wrapper applies the same classes) and 0016 (the addendum of 2026-11-18 made `carrier-suspended` the first OUTCOME; this ADR makes it one of three classes).
Decided by the owner on 2026-11-18 as a fleet rule; this repository is the second pilot (the first is `shipping-and-tracking`, which uses a list; here the
class is a field of the catalogue entry and the list is gone). Code: `internal/adapters/inbound/http/errors.go` (the class of each entry),
`replay_class.go`, `capture.go`, `idempotency.go`; `internal/adapters/inbound/mcp/errors.go`, `replay_class.go`, `idempotency.go`.

## Context

A request retried under the same `Idempotency-Key` was answered from the store whatever the first answer was, a `4xx` included (ADR 0008). That is wrong for a
refusal the same request could pass later: a `409 concurrent-modification` is gone on the next attempt, a `404 no-claimable-move` stops being true when a
supervisor requests a move, a `422 unknown-location` stops being true when the facility event arrives. A client that reuses its key (the floor client reuses the
key of an identical pending action; a deterministic key is normal) was handed the stale refusal for 24 hours. It also cannot simply be "never store a 4xx":
`409 carrier-suspended` COMMITS a REJECTED visit before it answers, and a second execution would record a second turn-away (ADR 0016, addendum).

## Decision

Every problem type belongs to exactly one replay class, declared on its entry of the problem catalogue (`problemCatalogue` in `errors.go`). The middleware reads
the class; it holds no list of slugs and no status range except 2xx and 5xx.

| Class | Meaning | Middleware |
| --- | --- | --- |
| FINAL | the answer cannot change for this request | the handler's writes roll back; the answer is stored on its own and replayed with `Idempotency-Replayed: true` |
| OUTCOME | the command COMMITTED state and then answered an error that must be remembered | the writes commit together with the stored answer, in one transaction; a replay records nothing |
| RETRYABLE | nothing was committed and the answer depends on state that can change | everything rolls back, nothing is stored, the key is free: the same request under the same key runs again |

A `2xx` is stored and a `5xx` or a panic is never stored, as before. The classes are chosen by what the use case does and by whether the same request can pass once
state changes, not by the status code (`409` is FINAL, OUTCOME and RETRYABLE in the table below). There is no runtime default: an entry without a class fails
`TestEveryCatalogueEntryDeclaresAReplayClass`, and a problem the adapter writes that is neither a catalogue entry nor listed as outside the store fails
`TestEveryProblemTheAdapterWritesHasAClassOrIsOutsideTheStore`. This table and the catalogue are held equal by `TestTheADRDeclaresTheClassOfEveryProblem` (it reads the
rows below that start with a backticked slug, a class and `|`).

**How the class was chosen.** FINAL only when the answer cannot change: a field of the request, or a fact that never changes after it is set. OUTCOME only for a
refusal that commits state, proven by the use case. Everything else that depends on state that can change is RETRYABLE, and when unsure RETRYABLE: a refusal that
rolled everything back costs nothing to run again, while a wrongly FINAL one pins a refusal that is no longer true for 24 hours. The one real risk of RETRYABLE is a
refusal that did commit; the use cases were read for it (section "OUTCOME", below).

### Classification

| Problem type | Class | Status | Reason (from the code) |
| --- | --- | --- | --- |
| `malformed-request-body` | FINAL | 400 | the body is not valid JSON for the operation: a function of the request (`decodeJSON`) |
| `unknown-query-parameter` | FINAL | 400 | a query parameter the operation does not take |
| `invalid-if-match` | FINAL | 400 | the `If-Match` header is not an entity tag of a version |
| `invalid-report-range` | FINAL | 400 | `from`/`to` are not RFC 3339 instants, or `to` is not after `from` within 92 days: both are in the request |
| `invalid-threshold-minutes` | FINAL | 400 | `thresholdMinutes` is not an integer from 1 to 10080 |
| `invalid-state` | FINAL | 400 | `state` is not a value of the operation |
| `invalid-limit` | FINAL | 400 | the paging `limit` is invalid |
| `invalid-cursor` | FINAL | 400 | the paging cursor is invalid |
| `visit-not-found` | FINAL | 404 | the visit id is minted by this service (`IDs.New("visit")`) and handed out after its commit; a visit is never deleted, so an id not found now is never found later. Judgement call J1 |
| `trailer-not-found` | FINAL | 404 | same as `visit-not-found` (`IDs.New("trl")`, `loadTrailer`). J1 |
| `move-not-found` | FINAL | 404 | same (`IDs.New("mv")`, `moves.go` 173 and 231). J1 |
| `yard-check-not-found` | FINAL | 404 | same (`checks_policy.go` 35). J1 |
| `inspection-not-found` | FINAL | 404 | same (`inspections.go` 135 and 153). J1 |
| `site-not-found` | RETRYABLE | 404 | `GET /sites/{siteId}/yard-board` (`queries.go` 122): a site is named by the caller and known from facility events (ADR 0003), which may arrive later |
| `template-not-found` | RETRYABLE | 404 | a template or version is published by a later `PUT` (`inspection_templates.go` 46) |
| `no-claimable-move` | RETRYABLE | 404 | `ClaimNextMove` finds no REQUESTED hostler move now (`moves.go` 115-117); a supervisor requests one later, and a polling hostler must reach it |
| `visit-already-open` | RETRYABLE | 409 | the tractor plate has an open visit (`gate_checkin.go` 75-76, unique index `uq_yard_visits_one_open_per_plate`); passes when that visit checks out |
| `appointment-required` | RETRYABLE | 409 | the site needs a BOOKED appointment in the local copy (`visit.go` 164); the copy is event-fed and the policy is changeable. J4 |
| `trailer-not-released` | RETRYABLE | 409 | a trailer taken out is not on site or not released (`visit.go` 352, `ReleasedForDeparture`): door work ends, a move ends |
| `seal-required` | RETRYABLE | 409 | a loaded outbound trailer has no seal (`visit.go` 355); the seal is recorded later by `POST .../seals` |
| `carrier-suspended` | OUTCOME | 409 | `checkInVisit` turns the refusal into a nil error inside its unit of work (`gate_checkin.go` 56-66), so `rejectSuspendedCarrier` saves the REJECTED visit and publishes `VisitRejected` before the refusal is returned (`carriers.go` 77-92, called at `gate_checkin.go` 101-103). A second execution would record a second turn-away (ADR 0016, addendum) |
| `location-occupied` | RETRYABLE | 409 | another trailer holds the spot or door (`placement.go` 75); passes when it leaves |
| `door-closed` | RETRYABLE | 409 | the door is closed in the closure copy fed by facility events (`placement.go` 130) |
| `door-flow-mismatch` | RETRYABLE | 409 | the door's flow, from the facility copy, does not suit the trailer's direction (`placement.go` 133) |
| `trailer-on-hold` | RETRYABLE | 409 | the trailer is held (`work.go` 31 and 90, `placement.go` 136); passes after the release |
| `trailer-not-on-hold` | RETRYABLE | 409 | `ReleaseHold` on a trailer that is not held (`work.go` 101); passes once a hold exists |
| `door-work-open` | RETRYABLE | 409 | door work is not done (`movement.go` 99, `securing.go` 93); passes when it is |
| `trailer-not-secured` | RETRYABLE | 409 | the trailer is not secured (`work.go` 34, `securing.go` 90); passes after `POST .../securings` |
| `trailer-still-secured` | RETRYABLE | 409 | the trailer is still secured (`securing.go` 107); passes after the release |
| `trailer-already-on-site` | RETRYABLE | 409 | the unit is on site (`gate_checkin.go` 141); passes after it departs |
| `placement-rule-violated` | RETRYABLE | 409 | the spot's kind, from the facility copy, does not allow what the trailer carries (`classification.go` 113 and 160); the copy and the trailer's classification can change. J4 |
| `seal-class-too-low` | RETRYABLE | 409 | the seal is below the site's minimum class (`sealclass.go` 69-71); the policy is changeable. J4 |
| `inspection-required` | RETRYABLE | 409 | no PASSED inspection yet (`gate.go` 34); passes after one |
| `inspection-failed` | RETRYABLE | 409 | the latest inspection FAILED (`gate.go` 36); passes after a passing re-inspection |
| `inspection-closed` | FINAL | 409 | the inspection is PASSED or FAILED (`inspection.go` 158 and 214) and a completed inspection never reopens |
| `required-item-pending` | RETRYABLE | 409 | a required item has no result (`inspection.go` 223); passes after it is recorded |
| `inspection-already-open` | RETRYABLE | 409 | an inspection is open for the trailer, template and gate (`inspections.go` 56); passes after it completes |
| `template-version-immutable` | FINAL | 409 | the version was published with other content (`inspection_templates.go` 109) and a published version never changes (ADR 0015) |
| `template-is-built-in` | FINAL | 409 | the template id is a built-in (`inspection_templates.go` 95), a constant of the code |
| `trailer-has-open-move` | RETRYABLE | 409 | the trailer has an open move (`moves.go` 76, `doors.go` 149); passes when it ends |
| `destination-reserved` | RETRYABLE | 409 | another open move reserves the destination (`targeting.go` 134, `moves.go` 208) |
| `hostler-busy` | RETRYABLE | 409 | the hostler holds a claimed move (`moves.go` 111); passes after its completion |
| `not-lease-holder` | RETRYABLE | 409 | the completing hostler is not the holder; passes if the move is claimed again by that hostler. J6 |
| `lease-not-expired` | RETRYABLE | 409 | mapped but never answered (`mappedButNeverAnswered`, only the lease sweep raises it); by its meaning it passes when the lease expires |
| `yard-check-open` | RETRYABLE | 409 | the site has an open yard check (unique index); passes after its closure |
| `location-already-observed` | FINAL | 409 | the observations of an OPEN check only grow (`yardcheck.go` 104-110); a closed check answers `illegal-transition` |
| `no-free-spot` | RETRYABLE | 409 | every spot is taken (`targeting.go` 160); a spot frees |
| `illegal-transition` | RETRYABLE | 409 | the state machine has no such transition from the aggregate's state now. Mixed: most callers can pass after the state moves on (`StartDoorWork` on a PARKED trailer), a few cannot (`CheckOut` of a CHECKED_OUT visit). J5 |
| `concurrent-modification` | RETRYABLE | 409 | the optimistic version guard lost a race (`UPDATE ... WHERE version = $n`, ADR 0005); the retry reads the new version and passes |
| `precondition-failed` | FINAL | 412 | the `If-Match` names a policy version and versions only grow (`checks_policy.go` 116-118); a stale one never matches again. J2 |
| `invalid-carrier` | FINAL | 422 | the carrier name or SCAC is malformed (`shared.go` 64-68, `visit.go` 210) |
| `unknown-location` | RETRYABLE | 422 | the slot is not in the local copy fed by facility events (`placement.go` 121, `yardpolicy.go` 141); it can arrive later |
| `invalid-unit-number` | FINAL | 422 | the unit number fails its scheme |
| `invalid-seal-class` | FINAL | 422 | the seal class is not I, S or H |
| `invalid-hazmat-class` | FINAL | 422 | the hazmat class is not a US DOT class or division |
| `invalid-un-number` | FINAL | 422 | the UN number is malformed |
| `unknown-item` | FINAL | 422 | the item code is not in the inspection, whose items are frozen at opening (`inspection.go` 162, ADR 0015) |
| `not-applicable-not-allowed` | FINAL | 422 | the template does not allow NOT_APPLICABLE for the item (`inspection.go` 168) |
| `unknown-template` | RETRYABLE | 422 | the template or version is not published yet (`inspections.go` 45, `inspection_gates.go` 56); a later `PUT` publishes it |
| `unknown-appointment` | RETRYABLE | 422 | the appointment is not in the local copy fed by dock-scheduling (`visit.go` 161); it can arrive later. The same slug also covers "not exactly one trailer", the request's own part. J3 |
| `unexplained-destination` | FINAL | 422 | a completion elsewhere than the destination without a note: both are in the request |
| `validation` | FINAL | 422 | a field of the request is invalid (`shared.Invalid`, about 100 sites). One site is state-dependent (`moves.go` 79). J3 |
| `idempotency-key-required` | OUTSIDE | 400 | written by the middleware before a key exists; never stored |
| `idempotency-key-invalid` | OUTSIDE | 400 | written by the middleware before a key is read; never stored |
| `idempotency-key-reused` | OUTSIDE | 409 | written by the middleware on a replay with another fingerprint; it is never the stored answer |
| `method-not-allowed` | OUTSIDE | 405 | written by the router, outside the keyed routes |
| `resource-not-found` | OUTSIDE | 404 | written by the router and the NUL guard, before the middleware |
| `dependency-unavailable` | OUTSIDE | 503 | the 503 of `/readyz`, not a keyed route (and a 5xx is never stored) |
| `internal-error` | OUTSIDE | 500 | a 5xx is never stored |

Counts: 27 FINAL, 1 OUTCOME, 34 RETRYABLE, 7 outside the store: 69 problem types, the 69 slugs of PR #48's parity test.

### OUTCOME: why only `carrier-suspended`

The use cases that run behind a keyed route were read for a refusal returned after a commit. `checkInVisit` is the only one that turns an error into a nil result
inside `atomically` and returns it afterwards (`gate_checkin.go` 56-66). `rejectOnSeal` and `RejectAtGate` record a REJECTED visit and answer success (`201`), not a
refusal. Every other use case returns its refusal from inside `atomically`, which rolls back. The two sweeps keep one unit of work per item and may commit items before
a later item fails, but what they return then is an unmapped error (a 5xx, not stored) or, for the mapped ones (`concurrent-modification`, `illegal-transition`,
`lease-not-expired`, not-found), they skip the item inside the loop. A new OUTCOME needs an ADR and a use case that commits before it refuses
(`TestTheSuspendedCarrierIsTheOnlyOutcome`).

### Mechanism

- The catalogue entry is `{err, problem, replayClass}`. `writeError` tells the middleware's capturing writer the class of the problem it writes (`MarkReplay`);
  the two lists of PR #51 (`outcomeRefusals`, `KeepWrites`) are replaced by it.
- `serveInUnitOfWork` asks `verdict` for the answer: a success or an OUTCOME refusal saves the key row inside the handler's unit of work and commits both; a FINAL
  refusal rolls the writes back and `storeRefusal` stores it on its own (as before); a RETRYABLE refusal, a 5xx and a 4xx no entry classified end the unit of work
  with `errNotStored`: it rolls back, the key row was never written, and the answer is sent. 2xx, 5xx and FINAL behave as before.
- The sweeps (`NewOptionalIdempotency`) have no unit of work around the handler and store the answer when it is a success, FINAL or OUTCOME; a RETRYABLE refusal is not stored.
- **A concurrent duplicate while the first ends RETRYABLE.** The duplicate waits on the advisory lock `idempotency-key/<key>` (`pg_advisory_xact_lock`, released at commit or
  rollback). The first request rolls back and releases it; the duplicate takes the lock, finds no row for the key, and runs the handler itself: it does not replay the
  released refusal and it does not run alongside the first. If it succeeds, its answer is stored and later copies replay that success.
  `TestACopyWaitingOnARetryableFirstRunsItself` shows 12 copies: one refusal, one fresh success, ten replays of the success, the handler run twice and never concurrently.
- Consequence for `idempotency-key-reused`: after a RETRYABLE refusal the key is free, so the same key with a different body is accepted as a new request.
- MCP: the write wrapper (`keyed`) has its own copy of the store shape (the key row inside the tool's unit of work, a refusal stored on its own after the rollback). A RETRYABLE
  release is trivial there: the tool's error is returned from inside the unit of work like an infrastructure failure, so it rolls back and nothing is stored. The MCP catalogue carries the same
  class per slug (`TestEveryMCPProblemDeclaresTheClassOfItsRESTTwin` reads the REST catalogue's source). It has no OUTCOME: no write tool answers `carrier-suspended`.

### Judgement calls for the owner

- **J1. 404 on the command's own target (`visit`, `trailer`, `move`, `yard-check`, `inspection`): FINAL.** The plan's default was RETRYABLE ("state-dependent by the definition").
  Here every id is minted by this service and returned after its commit, and nothing is deleted, so the same request cannot pass later. If the owner wants the plan's default,
  five entries change to RETRYABLE; nothing else moves.
- **J2. `precondition-failed` (stale `If-Match`): FINAL.** It is an optimistic-lock answer, which the plan lists as RETRYABLE, but the request itself carries the stale version and versions only
  grow, so a retry of the same request always fails. Flip to RETRYABLE costs one more execution per retry and changes no outcome.
- **J3. `validation` and `unknown-appointment` are mixed slugs.** `validation` is FINAL although `moves.go` 79 ("the trailer is already at X") depends on where the trailer is; a
  stored 422 there can outlive the move of the trailer. The clean fix is its own problem type (not done here). `unknown-appointment` is RETRYABLE although the
  "not exactly one trailer" part is the request's own.
- **J4. Policy- and copy-dependent refusals are RETRYABLE** (`appointment-required`, `seal-class-too-low`, `placement-rule-violated`, `door-flow-mismatch`): the site policy or the facility copy
  can change. The cost of the doubt is one more execution of a refusal that rolled back.
- **J5. `illegal-transition` is RETRYABLE although some of its callers can never pass** (checking out a CHECKED_OUT visit). One class per slug; RETRYABLE is the side that cannot pin a stale refusal.
- **J6. `not-lease-holder` is RETRYABLE** (the same hostler can become the holder again); a different caller never passes, which a re-execution tells again.

## Consequences

- A client that reuses its key after a RETRYABLE refusal now runs the command again; the OpenAPI text that said a stored 4xx is replayed was made true (descriptions only).
  A client that wants to replay-protect a refusal gets that for a FINAL or OUTCOME one only.
- The migration comment of `0002_idempotency_keys` ("only 2xx and 4xx answers are stored") is history and is not edited; the `status < 500` check still holds.
- A new problem type needs a class on its catalogue entry (and in this table), and the same slug in the MCP catalogue the same class.
- `INCIDENTS.md` and the fleet standard are updated by the wave, not by this repository.
