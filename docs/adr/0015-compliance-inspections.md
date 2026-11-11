# ADR 0015: Compliance inspections: versioned checklist templates, an Inspection record and a site-policy gate on dock, load and departure

## Status

Accepted (2026-11-11). Refines 0002 (a new aggregate and a new YardPolicy field), 0004 (two integration types are added to the catalogue)
and 0007 (the tool budget goes from sixteen to twenty-one, see section 7). Additive: no topic, no existing type, no existing field and no
existing request changes meaning, and a site that requires no inspection behaves exactly as before (section 4). Code:
`internal/domain/inspection`, `internal/domain/yardpolicy` (`inspections.go`), `internal/application/usecases` (`inspections.go`,
`inspection_gates.go`, `inspection_templates.go`), `migrations/0006_compliance_inspections`, `features/compliance_inspections.feature`.

## Context

Before a trailer is loaded, docked or let out of the gate, security and food-safety rules ask for a checklist: the C-TPAT tractor and
trailer inspection (17 points) and the empty container inspection (7 points, 8 for a refrigerated unit) for the security of a load, and the
sanitary transportation rules for human and animal food for the condition of the vehicle (FDA, 21 CFR 1.908). The yard knew where a trailer
was, its seal and its holds, but not whether anyone had looked at it: an inspection lived on paper, nothing refused the next step when it
was missing, and a failed check did not stop the trailer.

The goal is that a site can require a passed checklist before a yard step, that a failed check holds the trailer in the same instant, and
that the record of who checked what stays as it was, whatever the checklist becomes later.

## Decision

### 1. Templates are versioned and immutable

An **inspection template** is `(templateId, version)` with a title and 1 to 100 items. An item has a `itemCode` (lower-case letters,
digits and hyphens, unique in the template), `text` (at most 300 characters), `required`, `allowNotApplicable` and an optional `holdReason`
(`SECURITY`, `SEAL_BROKEN`, `DAMAGED`, `QUALITY`, `NOT_FOUND`, default `QUALITY`) that names the hold a failure of the item places.

- A **version is published once and never changes**. `PUT /inspection-templates/{templateId}/versions/{version}` is idempotent for the same
  content (200), `201` for a new version, and `409 template-version-immutable` for other content under a published version. A change is a
  new version. `GET /inspection-templates` lists the latest version of each template (built-ins included); `GET
  /inspection-templates/{id}` is the latest and `.../versions/{n}` is one version, both answer `latestVersion`.
- **Built-in templates ship in code** (section 2), are read-only (`409 template-is-built-in` on a publish under their ids) and have a null
  `publishedAt`. Custom templates are stored in `inspection_templates`.
- A template id that is not well formed, no items, a duplicated or malformed item code or an unknown hold reason is `422 validation`.

### 2. Built-in templates, and where they come from

The wording is our own; the areas follow public sources, which were read for this ADR (retrieved 2026-11-11). Anything that could not be
tied to a source was left out.

| Template id | Items | Source |
|---|---|---|
| `tractor-trailer-17-point` | the 17 areas of the tractor and trailer, then the agricultural pest check and the hardware check (19 items) | CBP C-TPAT Job Aid, 17-Point Inspection Checklist Sample, October 2021: `https://www.cbp.gov/sites/default/files/assets/documents/2022-Apr/CTPAT%20Job%20Aid%20-%2017-Point%20Inspection%20Checklist%20Sample%20-%20October%202021%20%28508%29%20%28002%29.pdf` |
| `empty-container-7-point` | the 7 areas, the fan housing of a refrigerated unit (8th point, not applicable on a dry unit), the pest check and the hardware check (10 items) | CBP C-TPAT Job Aid, 7-Point Container Inspection Checklist Sample, October 2021: `https://cbp.gov/sites/default/files/assets/documents/2022-Apr/CTPAT%20Job%20Aid%20-%207-Point%20Container%20Inspection%20Checklist%20Sample%20-%20October%202021%20%28508%29.pdf` |
| `reefer-pre-cool-and-sanitation` | physical condition, sanitary condition, no previous cargo, no pest infestation, pre-cooling, receiver temperature-abuse check (6 items) | 21 CFR 1.908(b)(1), (b)(2), (c)(1), (c)(2), (d) and (e)(3), eCFR: `https://www.ecfr.gov/current/title-21/section-1.908`; records in 21 CFR 1.912: `https://www.ecfr.gov/current/title-21/section-1.912` |

Seventeen areas of the first template, in the order of the source: bumpers/tires/rims, engine, tires, tractor floor, fuel tanks, interior
cab and sleeper, air tanks, drive shafts, fifth wheel, outside and undercarriage, outside and inside doors, trailer floor, side walls,
front wall, ceiling and roof, refrigeration unit, exhaust. The 7 areas of the second: outside/undercarriage, inside/outside doors, right
side, left side, front wall, ceiling/roof, floor inside.

- Every built-in item is `required`. The refrigeration unit (17-point) and the fan housing (7-point) allow `NOT_APPLICABLE`, as do the two
  temperature items of the reefer template (pre-cooling does not apply to an ambient load, the receiver check does not apply to a load that
  is going out). The security areas hold for `SECURITY`, the pest check for `QUALITY`; in the reefer template physical condition holds for
  `DAMAGED` and the sanitary items for `QUALITY`.
- The reefer template is **a checklist of what the regulation asks the loader, shipper, carrier and receiver to determine; it is not a
  compliance certificate**. Rule 1.908 leaves "any appropriate means" to the party; a site that needs more publishes its own template.
- Not verified and therefore not in the templates: any numeric temperature or cleaning standard (the regulation defers to the shipper's
  written specification), and any country's own checklist beyond the US CBP ones.

### 3. The Inspection aggregate

`Inspection` (`insp-…`) is opened for a trailer that is on site (`409 illegal-transition` for a DEPARTED one) from a template version: the
latest unless the request names `templateVersion`.

- **The template is snapshotted** into the inspection at open: every item with its text, required flag and hold reason, result pending. The
  inspection never reads the template again, so publishing a later version, or any change of a stored row, cannot rewrite it (`BDD: An
  inspection keeps the template version it was opened from`).
- Fields: `status` `OPEN` / `PASSED` / `FAILED`, `gate` (`PRE_DOCK`, `PRE_LOAD`, `PRE_DEPARTURE` or `ON_DEMAND`), `visitId` (the visit the
  trailer came with unless the body names another visit of the same site), `performerRef` (optional reference, at most 64 characters, like the
  other performer references), `conditionGrade` (`CLEAN`, `DIRTY`, `ODOUR`, `DAMAGED`, set at completion, advisory), `openedAt`,
  `completedAt`, `version` (the weak ETag).
- **Items** are recorded with `PUT /inspections/{id}/items/{itemCode}`: `result` `PASS`, `FAIL` or `NOT_APPLICABLE` (only where the item
  allows it, else `422 not-applicable-not-allowed`), an optional `note` (500 characters) and `evidenceRefs` (up to 20 opaque references, such
  as `photo:abc`; no file is stored here). Recording again replaces. An unknown item is `422 unknown-item`.
- **Completion** (`POST /inspections/{id}/completions`, optional `conditionGrade`): every required item must have a result, else `409
  required-item-pending`. The inspection is `FAILED` when a required item failed, else `PASSED`; a failed optional item does not fail the
  inspection. The record is **final**: a result or a second completion afterwards is `409 inspection-closed`.
- **One OPEN inspection per trailer, template and gate** (`409 inspection-already-open`, backed by a partial unique index). A new one may be
  opened once the earlier one is completed, which is how a re-inspection follows a failure.

### 4. Gating: a site policy, off by default

`YardPolicy` gains **`requiredInspections`**: a list (at most 20) of `{gate, templateId, appliesTo?}` where `gate` is `PRE_DOCK`,
`PRE_LOAD` or `PRE_DEPARTURE` (`ON_DEMAND` is not a gate a policy can require, `422 validation`) and `appliesTo` optionally narrows the rule to
`purposes` and `trailerKinds` (empty means all). The template must be published, else `422 unknown-template`. **Default empty: today's
behaviour exactly.** Replacing the policy without the field turns the rules off again, like `minimumSealClass`.

The step behind each gate asks the trailer's completed inspections for the template at that gate, **the latest decides**:

| Gate | Step refused | Not refused |
|---|---|---|
| `PRE_DOCK` | `RequestMove` of a trailer to a door, and the move the gate makes at check-in, which parks the trailer instead when the inspection is missing | a move to a spot |
| `PRE_LOAD` | `StartDoorWork` on a trailer that is being loaded: an outbound direction (an outbound reference, or a LIVE_LOAD or PICKUP visit) or an empty trailer (`Trailer.Loading`) | the start of an unload |
| `PRE_DEPARTURE` | `CheckOutVisit` taking a trailer, whole check-out rolled back, nothing published | a check-out taking no trailer |

- **`409 inspection-required`**: no completed inspection of that template at that gate. A pass at another gate, or `ON_DEMAND`, does not count.
- **`409 inspection-failed`**: the latest one FAILED; a passing re-inspection opens the gate, a failure after a pass closes it again.
- The inspections judged are those of the trailer id, which is minted per arrival: an earlier stay can never satisfy a later one.
- Order of refusals: the existing refusals first (state, hold, securing, placement); a trailer on hold is `trailer-on-hold` until a human
  releases it, then `inspection-failed` until it is re-inspected.
- **AutoCompleteUnload is not blocked.** `ReceiptClosed` completes an unload that was already done; it starts nothing, so it takes no gate
  (BDD: "Closing the receipt completes an unload even where inspections are required").

### 5. A failed required item holds the trailer, in the same unit of work

When `CompleteInspection` ends `FAILED`, and the trailer is on site and carries **no hold**, the trailer is held in the **same unit of work** as
the inspection save and the outbox rows. The reason is that of the **most severe failed required item**, in the order `SECURITY`,
`SEAL_BROKEN`, `DAMAGED`, `QUALITY`, `NOT_FOUND`. A trailer already held is **left as it is** (it has one hold; `hold_placed` is false on the
event); a departed trailer cannot be held. The hold is released by the existing human action (`POST /trailers/{id}/hold-releases`); the
inspection does not release it, and re-inspection does not either: a trailer is released by a person, then passes the gate by passing.
`ON_DEMAND` inspections hold a failing trailer too.

### 6. Events and the contract

Two **integration** types on `warehouse.yard-management.events`, mirrored on `.analytics` with the same `id` (ADR 0004, ADR 0006): `com.warehouse.wms.yard-management.inspection.InspectionCompleted` and `...inspection.InspectionFailed`;
key and subject `inspection_id`, `dataschema` `urn:warehouse:yard-management:<events|analytics>:<EventName>:v1`. Both are advisory (an
inspection is final once completed) and carry no `version`. A completed inspection raises `InspectionCompleted`, and a failed one
`InspectionFailed` after it, in that order, before the `TrailerHeld` that the hold raises.

| Event | `data` |
|---|---|
| `InspectionCompleted` | `inspection_id`, `site_id`, `trailer_id`, `visit_id` (nullable), `template_id`, `template_version`, `gate`, `outcome` (`PASSED`/`FAILED`), `condition_grade` (nullable), `completed_at`, `failed_item_codes` |
| `InspectionFailed` | `inspection_id`, `site_id`, `trailer_id`, `template_id`, `gate`, `failed_items` (`item_code`, `note` nullable, `required`), `failed_at`, `hold_placed` |

- **Consumers.** exception-management is the planned consumer of `InspectionFailed` (a failed inspection is an exception to work). It is not
  consumed yet, and the contract says so. `InspectionCompleted` is the audit record of every inspection; it has no consumer and carries an
  explicit `x-no-consumer-reason`. A consumer that does not know the types ignores them (CloudEvents rule).
- The catalogue goes from 21 to **23 types, 13 integration** (ten analytics-only, unchanged). The three types outbound-shipping pins and
  consumes (`TrailerDocked`, `DoorWorkCompleted`, `TrailerDeparted`) are not touched; no type is removed and no existing payload field
  changes. **outbound-shipping's pinned copy of this contract does not need a refresh for correctness**, only if it wants to document the new types.
- The `<entity>` segment of the type gains **`inspection`** (the Inspection aggregate), next to `yardvisit`, `trailer`, `yardmove` and
  `yardcheck`.
- OpenAPI: the routes of sections 1 and 3, `YardPolicy.requiredInspections` (read and request), and problem examples for every slug below.

New problem slugs: `inspection-required`, `inspection-failed`, `inspection-closed`, `required-item-pending`, `inspection-already-open`,
`inspection-not-found`, `template-not-found`, `unknown-item`, `not-applicable-not-allowed`, `unknown-template`,
`template-version-immutable`, `template-is-built-in`. They exist in the OpenAPI problem examples, in `http/errors.go` and in the MCP error
catalogue (one-for-one test).

### 7. MCP

Five tools, no more: **reads at L1** `get_inspection` and `list_trailer_inspections`, **writes at L3** `open_inspection`,
`record_inspection_item` and `complete_inspection` (approval and `initiated_by` like the other writes, ADR 0007; `idempotencyKey`
required). Publishing a template and the policy stay on REST: they are supervisor configuration, and the policy gate is not a thing an
agent should be able to switch off. The tool budget (ADR 0007 M4) is **raised deliberately from sixteen to twenty-one**: this ADR is the
amendment M4 asks for. `complete_inspection` carries the floor-client word "complete"; it is the inspector's record of a checklist and not
the completion of a move or of door work, so the governance test names it in an exception that points at this ADR. The MCP actor lands in
`initiated_by` of the events of the tools, and the hold a failing completion places is attributed to the MCP actor too.

### 8. Persistence (migration 0006, additive)

`inspection_templates(template_id, version, title, items JSONB, published_at)` keyed by `(template_id, version)`; `inspections(...)` with
the item snapshot as JSONB, a partial unique index `(trailer_id, template_id, gate) WHERE status = 'OPEN'` and an index for the completed
inspections of a trailer; `yard_policies.required_inspections JSONB NOT NULL DEFAULT '[]'`. Every existing row loads as "no inspection
required" and the previous release's statements still run. The down migration drops the two tables and the column.

## Consequences

- A site that opts in has its dock, load and gate steps refused with a stable slug until the checklist has passed, and a failed
  checklist holds the trailer in the same commit as its record. A site that does not opt in notices nothing, apart from the new
  routes.
- A site that turns a gate on must have clients that open, fill in and complete inspections. A start of door work, a move to a door or a
  check-out by a client that does not will fail with `inspection-required`. That is why the default is empty.
- The record is evidence: the template snapshot and the final status do not change. Evidence is held as opaque references, so the
  retention and the files themselves stay outside this service.
- Each gated step reads the policy and the trailer's completed inspections; a site with no rule for a gate pays only the policy read.
- The built-in templates are a starting point built from public checklists, not legal advice; the regulation text is in the ADR links.

## What is not done

- **The analytical fold of inspection completion and failure rates onto the exceptions report (ADR 0011) is not done.** It needs a
  projector decode, an analytics migration (a table next to `gate_exceptions`), the reader, the report view and its OpenAPI schema, and
  would double the surface of this change. Both events are already on the analytics stream, so it needs no contract change.
- A move to a door requested **before** the policy was switched on and claimed afterwards is not re-judged at the claim: the gate is at
  request time (and at check-in). The domain does not hold a second copy of the rule.
- Photo or document storage, signatures, and a dedicated inspector role (there is no auth in this service).
- A template editor with draft state, a deprecation flag for a version, and a hold-by-item-severity table other than the fixed ranking.
- Linking an inspection to the seal record (`SealRecorded`): both exist on the trailer and are independent.
- exception-management does not consume `InspectionFailed` yet; that is its change, not this repository's.
- Other jurisdictions' checklists (CBP C-TPAT and FDA only), and any numeric standard for temperatures or cleaning.

## Alternatives considered and rejected

- **Inspection as a field of the Trailer**: it would not carry a history, several templates or a re-inspection, and a trailer
  aggregate with a growing JSON of checklists would be rewritten on every item.
- **Mutable templates with a "current" pointer**: a template change would rewrite what an inspector answered; the immutable version and the
  snapshot make the record stand.
- **A hard rule for every site**: it would break every client that does not inspect; the policy is the rollout path, as for ADR 0013.
- **A hold on any failed item, optional ones included**: an optional item is the template author saying it does not decide the outcome.
- **The inspection releasing the hold when a re-inspection passes**: a hold may have other causes and is a person's decision; the gate is
  reopened by the passing inspection, the hold by the human.
- **Gating the claim and the completion of moves as well**: the request already refuses and keeps the move queue clean, as in ADR 0013.
- **Putting the tractor checklist and the container checklist into one template**: they are two checklists with two sets of holds that a
  site requires for different kinds and purposes.
