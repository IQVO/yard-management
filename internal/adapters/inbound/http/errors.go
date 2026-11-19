package http

import (
	"errors"
	"net/http"

	"github.com/claudioed/yard-management/internal/adapters/inbound/http/page"
	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/domain/inspection"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardcheck"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

// problemBaseURI is the namespace of this service's RFC 7807 `type` URIs. The
// slug is the kebab-case of the domain error name without its Err prefix
// (ErrLocationOccupied is location-occupied).
const problemBaseURI = "https://errors.yard-management.warehouse-systems.dev/"

// errMalformedRequest marks a body or parameter the adapter itself rejects
// (bad JSON, unknown field, wrong type).
var errMalformedRequest = errors.New("request body is not valid JSON for this operation")

// problem is the fixed (status, slug, title) of one error category; the
// detail comes from the error at write time.
type problem struct {
	status int
	slug   string
	title  string
}

var internalProblem = problem{http.StatusInternalServerError, "internal-error", "Internal server error"}

// methodNotAllowed is the 405 of a known path with an unsupported method.
var methodNotAllowed = problem{http.StatusMethodNotAllowed, "method-not-allowed", "Method not allowed"}

// Problems the middleware writes itself, before any use case runs.
var (
	idempotencyKeyRequired = problem{http.StatusBadRequest, "idempotency-key-required", "Idempotency-Key header is required"}
	idempotencyKeyInvalid  = problem{http.StatusBadRequest, "idempotency-key-invalid", "Idempotency-Key header is invalid"}
	idempotencyKeyReused   = problem{http.StatusConflict, "idempotency-key-reused", "Idempotency-Key was already used with a different request"}
)

// Errors the adapter raises about the request itself.
var (
	errUnknownQueryParameter = errors.New("unknown query parameter")
	errInvalidIfMatch        = errors.New("If-Match is not an entity tag of a policy version")
	errInvalidReportRange    = errors.New("from and to must be RFC 3339 instants, to after from and at most 92 days later")
	errInvalidThreshold      = errors.New("thresholdMinutes must be an integer from 1 to 10080")
	errInvalidState          = errors.New("state is not one of the values of this operation")
)

func conflict(slug, title string) problem { return problem{http.StatusConflict, slug, title} }
func unprocessable(slug, title string) problem {
	return problem{http.StatusUnprocessableEntity, slug, title}
}
func notFound(slug, title string) problem { return problem{http.StatusNotFound, slug, title} }
func badRequest(slug, title string) problem {
	return problem{http.StatusBadRequest, slug, title}
}

// catalogueEntry is one typed error, its problem and the replay class of that
// problem (what the Idempotency-Key middleware does with the answer, ADR 0019).
type catalogueEntry struct {
	err    error
	p      problem
	replay replayClass
}

// problemCatalogue maps every typed error to its problem, in match order: 400
// for the request itself, 404 for a missing resource, 409 for conflicts and
// illegal states, 412 for a stale If-Match, 422 for value and reference rules.
//
// Every entry declares its replay class (replayClass; ADR 0019 holds the
// reason of each): FINAL when the answer cannot change for the same request,
// OUTCOME when the command committed state and then refused, RETRYABLE when
// nothing was committed and the answer depends on state that can change (an
// optimistic-lock conflict, a fact an event may still deliver, a state the
// aggregate is not in yet, a policy or a door that can change). When unsure the
// class is RETRYABLE: the refusal rolled everything back, so running it again
// costs nothing. A new entry without a class fails
// TestEveryCatalogueEntryDeclaresAReplayClass.
var problemCatalogue = []catalogueEntry{
	{errMalformedRequest, badRequest("malformed-request-body", "Malformed request body"), replayFinal},
	{errUnknownQueryParameter, badRequest("unknown-query-parameter", "Unknown query parameter"), replayFinal},
	{errInvalidIfMatch, badRequest("invalid-if-match", "If-Match is invalid"), replayFinal},
	{errInvalidReportRange, badRequest("invalid-report-range", "Report range is invalid"), replayFinal},
	{errInvalidThreshold, badRequest("invalid-threshold-minutes", "Threshold is invalid"), replayFinal},
	{errInvalidState, badRequest("invalid-state", "State is invalid"), replayFinal},
	{page.ErrInvalidLimit, badRequest("invalid-limit", "Limit is invalid"), replayFinal},
	{page.ErrInvalidCursor, badRequest("invalid-cursor", "Cursor is invalid"), replayFinal},

	// The ids are minted by this service, so a 404 on one never turns into a success: FINAL.
	{usecases.ErrVisitNotFound, notFound("visit-not-found", "Visit not found"), replayFinal},
	{usecases.ErrTrailerNotFound, notFound("trailer-not-found", "Trailer not found"), replayFinal},
	{usecases.ErrMoveNotFound, notFound("move-not-found", "Move not found"), replayFinal},
	{usecases.ErrYardCheckNotFound, notFound("yard-check-not-found", "Yard check not found"), replayFinal},
	{usecases.ErrInspectionNotFound, notFound("inspection-not-found", "Inspection not found"), replayFinal},
	// A site and a template are named by the caller and an event or a PUT can still bring them: RETRYABLE.
	{usecases.ErrSiteNotFound, notFound("site-not-found", "Site not found"), replayRetryable},
	{usecases.ErrTemplateNotFound, notFound("template-not-found", "Inspection template not found"), replayRetryable},
	{usecases.ErrNoClaimableMove, notFound("no-claimable-move", "No move waits for a hostler"), replayRetryable},

	{yardvisit.ErrVisitAlreadyOpen, conflict("visit-already-open", "A visit is already open for this tractor plate"), replayRetryable},
	{yardvisit.ErrAppointmentRequired, conflict("appointment-required", "An appointment is required at this site"), replayRetryable},
	{yardvisit.ErrTrailerNotReleased, conflict("trailer-not-released", "A trailer is not released"), replayRetryable},
	{yardvisit.ErrSealRequired, conflict("seal-required", "A loaded outbound trailer needs a seal"), replayRetryable},
	// The one OUTCOME: the gate committed a REJECTED visit and its event, then refused (ADR 0016).
	{yardvisit.ErrCarrierSuspended, conflict("carrier-suspended", "The carrier is suspended"), replayOutcome},
	{trailer.ErrLocationOccupied, conflict("location-occupied", "The location already holds a trailer"), replayRetryable},
	{trailer.ErrDoorClosed, conflict("door-closed", "The door is closed"), replayRetryable},
	{trailer.ErrDoorFlowMismatch, conflict("door-flow-mismatch", "The door flow does not match the trailer"), replayRetryable},
	{trailer.ErrTrailerOnHold, conflict("trailer-on-hold", "The trailer is on hold"), replayRetryable},
	{trailer.ErrTrailerNotOnHold, conflict("trailer-not-on-hold", "The trailer is not on hold"), replayRetryable},
	{trailer.ErrDoorWorkOpen, conflict("door-work-open", "Door work is not done"), replayRetryable},
	{trailer.ErrTrailerNotSecured, conflict("trailer-not-secured", "The trailer is not secured"), replayRetryable},
	{trailer.ErrTrailerStillSecured, conflict("trailer-still-secured", "The trailer is still secured"), replayRetryable},
	{trailer.ErrTrailerAlreadyOnSite, conflict("trailer-already-on-site", "The trailer is already on site"), replayRetryable},
	{trailer.ErrPlacementRuleViolated, conflict("placement-rule-violated", "The spot is not allowed for what the trailer carries"), replayRetryable},
	{shared.ErrSealClassTooLow, conflict("seal-class-too-low", "The seal class is below the minimum of the site"), replayRetryable},
	{inspection.ErrInspectionRequired, conflict("inspection-required", "A passed inspection is required first"), replayRetryable},
	{inspection.ErrInspectionFailed, conflict("inspection-failed", "The latest inspection failed"), replayRetryable},
	{inspection.ErrInspectionClosed, conflict("inspection-closed", "The inspection is already completed"), replayFinal},
	{inspection.ErrRequiredItemPending, conflict("required-item-pending", "A required item has no result yet"), replayRetryable},
	{inspection.ErrInspectionAlreadyOpen, conflict("inspection-already-open", "An inspection is already open for this trailer, template and gate"), replayRetryable},
	{inspection.ErrTemplateVersionImmutable, conflict("template-version-immutable", "A published template version never changes"), replayFinal},
	{inspection.ErrTemplateIsBuiltIn, conflict("template-is-built-in", "A built-in template is read-only"), replayFinal},
	{yardmove.ErrTrailerHasOpenMove, conflict("trailer-has-open-move", "The trailer already has an open move"), replayRetryable},
	{yardmove.ErrDestinationReserved, conflict("destination-reserved", "The destination is reserved by another open move"), replayRetryable},
	{yardmove.ErrHostlerBusy, conflict("hostler-busy", "The hostler already holds a claimed move"), replayRetryable},
	{yardmove.ErrNotLeaseHolder, conflict("not-lease-holder", "Only the lease holder completes the move"), replayRetryable},
	{yardmove.ErrLeaseNotExpired, conflict("lease-not-expired", "The lease has not expired"), replayRetryable},
	{yardcheck.ErrYardCheckOpen, conflict("yard-check-open", "A yard check is already open for this site"), replayRetryable},
	{yardcheck.ErrLocationAlreadyObserved, conflict("location-already-observed", "The location was already observed in this check"), replayFinal},
	{usecases.ErrNoFreeSpot, conflict("no-free-spot", "No free spot is available"), replayRetryable},
	{shared.ErrIllegalTransition, conflict("illegal-transition", "The state machine has no such transition"), replayRetryable},
	{ports.ErrConcurrentModification, conflict("concurrent-modification", "The resource was modified by another request; re-fetch the latest version and retry"), replayRetryable},

	// The If-Match names a policy version and versions only grow: a stale one never passes again.
	{usecases.ErrStalePolicy, problem{http.StatusPreconditionFailed, "precondition-failed", "Precondition failed"}, replayFinal},

	{shared.ErrInvalidCarrier, unprocessable("invalid-carrier", "The carrier is invalid"), replayFinal},
	// Facility and dock-scheduling facts arrive by event: the location, the appointment and the template can still become known.
	{shared.ErrUnknownLocation, unprocessable("unknown-location", "Unknown location"), replayRetryable},
	{shared.ErrInvalidUnitNumber, unprocessable("invalid-unit-number", "The unit number is invalid"), replayFinal},
	{shared.ErrInvalidSealClass, unprocessable("invalid-seal-class", "The seal class is invalid"), replayFinal},
	{shared.ErrInvalidHazmatClass, unprocessable("invalid-hazmat-class", "The hazmat class is invalid"), replayFinal},
	{shared.ErrInvalidUNNumber, unprocessable("invalid-un-number", "The UN number is invalid"), replayFinal},
	{inspection.ErrUnknownItem, unprocessable("unknown-item", "The inspection has no such item"), replayFinal},
	{inspection.ErrNotApplicableNotAllowed, unprocessable("not-applicable-not-allowed", "The item cannot be marked not applicable"), replayFinal},
	{inspection.ErrUnknownTemplate, unprocessable("unknown-template", "Unknown inspection template"), replayRetryable},
	{yardvisit.ErrUnknownAppointment, unprocessable("unknown-appointment", "Unknown appointment"), replayRetryable},
	{yardmove.ErrUnexplainedDestination, unprocessable("unexplained-destination", "A location other than the destination needs an explanation"), replayFinal},
	{shared.ErrValidation, unprocessable("validation", "Validation failed"), replayFinal},
}

// entryFor returns the first catalogue entry err matches.
func entryFor(err error) (catalogueEntry, bool) {
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry, true
		}
	}
	return catalogueEntry{}, false
}

// problemFor returns the problem of the first catalogue entry err matches,
// or the internal-error problem.
func problemFor(err error) problem {
	if entry, ok := entryFor(err); ok {
		return entry.p
	}
	return internalProblem
}

// replayOf returns the replay class of the first catalogue entry err matches;
// replayUnset for an error the catalogue does not know (a 5xx, never stored).
func replayOf(err error) replayClass {
	entry, _ := entryFor(err)
	return entry.replay
}
