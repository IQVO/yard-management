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

// problemCatalogue maps every typed error to its problem, in match order: 400
// for the request itself, 404 for a missing resource, 409 for conflicts and
// illegal states, 412 for a stale If-Match, 422 for value and reference rules.
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{errMalformedRequest, badRequest("malformed-request-body", "Malformed request body")},
	{errUnknownQueryParameter, badRequest("unknown-query-parameter", "Unknown query parameter")},
	{errInvalidIfMatch, badRequest("invalid-if-match", "If-Match is invalid")},
	{errInvalidReportRange, badRequest("invalid-report-range", "Report range is invalid")},
	{errInvalidThreshold, badRequest("invalid-threshold-minutes", "Threshold is invalid")},
	{errInvalidState, badRequest("invalid-state", "State is invalid")},
	{page.ErrInvalidLimit, badRequest("invalid-limit", "Limit is invalid")},
	{page.ErrInvalidCursor, badRequest("invalid-cursor", "Cursor is invalid")},

	{usecases.ErrVisitNotFound, notFound("visit-not-found", "Visit not found")},
	{usecases.ErrTrailerNotFound, notFound("trailer-not-found", "Trailer not found")},
	{usecases.ErrMoveNotFound, notFound("move-not-found", "Move not found")},
	{usecases.ErrYardCheckNotFound, notFound("yard-check-not-found", "Yard check not found")},
	{usecases.ErrSiteNotFound, notFound("site-not-found", "Site not found")},
	{usecases.ErrInspectionNotFound, notFound("inspection-not-found", "Inspection not found")},
	{usecases.ErrTemplateNotFound, notFound("template-not-found", "Inspection template not found")},
	{usecases.ErrNoClaimableMove, notFound("no-claimable-move", "No move waits for a hostler")},

	{yardvisit.ErrVisitAlreadyOpen, conflict("visit-already-open", "A visit is already open for this tractor plate")},
	{yardvisit.ErrAppointmentRequired, conflict("appointment-required", "An appointment is required at this site")},
	{yardvisit.ErrTrailerNotReleased, conflict("trailer-not-released", "A trailer is not released")},
	{yardvisit.ErrSealRequired, conflict("seal-required", "A loaded outbound trailer needs a seal")},
	{yardvisit.ErrCarrierSuspended, conflict("carrier-suspended", "The carrier is suspended")},
	{trailer.ErrLocationOccupied, conflict("location-occupied", "The location already holds a trailer")},
	{trailer.ErrDoorClosed, conflict("door-closed", "The door is closed")},
	{trailer.ErrDoorFlowMismatch, conflict("door-flow-mismatch", "The door flow does not match the trailer")},
	{trailer.ErrTrailerOnHold, conflict("trailer-on-hold", "The trailer is on hold")},
	{trailer.ErrTrailerNotOnHold, conflict("trailer-not-on-hold", "The trailer is not on hold")},
	{trailer.ErrDoorWorkOpen, conflict("door-work-open", "Door work is not done")},
	{trailer.ErrTrailerNotSecured, conflict("trailer-not-secured", "The trailer is not secured")},
	{trailer.ErrTrailerStillSecured, conflict("trailer-still-secured", "The trailer is still secured")},
	{trailer.ErrTrailerAlreadyOnSite, conflict("trailer-already-on-site", "The trailer is already on site")},
	{trailer.ErrPlacementRuleViolated, conflict("placement-rule-violated", "The spot is not allowed for what the trailer carries")},
	{shared.ErrSealClassTooLow, conflict("seal-class-too-low", "The seal class is below the minimum of the site")},
	{inspection.ErrInspectionRequired, conflict("inspection-required", "A passed inspection is required first")},
	{inspection.ErrInspectionFailed, conflict("inspection-failed", "The latest inspection failed")},
	{inspection.ErrInspectionClosed, conflict("inspection-closed", "The inspection is already completed")},
	{inspection.ErrRequiredItemPending, conflict("required-item-pending", "A required item has no result yet")},
	{inspection.ErrInspectionAlreadyOpen, conflict("inspection-already-open", "An inspection is already open for this trailer, template and gate")},
	{inspection.ErrTemplateVersionImmutable, conflict("template-version-immutable", "A published template version never changes")},
	{inspection.ErrTemplateIsBuiltIn, conflict("template-is-built-in", "A built-in template is read-only")},
	{yardmove.ErrTrailerHasOpenMove, conflict("trailer-has-open-move", "The trailer already has an open move")},
	{yardmove.ErrDestinationReserved, conflict("destination-reserved", "The destination is reserved by another open move")},
	{yardmove.ErrHostlerBusy, conflict("hostler-busy", "The hostler already holds a claimed move")},
	{yardmove.ErrNotLeaseHolder, conflict("not-lease-holder", "Only the lease holder completes the move")},
	{yardmove.ErrLeaseNotExpired, conflict("lease-not-expired", "The lease has not expired")},
	{yardcheck.ErrYardCheckOpen, conflict("yard-check-open", "A yard check is already open for this site")},
	{yardcheck.ErrLocationAlreadyObserved, conflict("location-already-observed", "The location was already observed in this check")},
	{usecases.ErrNoFreeSpot, conflict("no-free-spot", "No free spot is available")},
	{shared.ErrIllegalTransition, conflict("illegal-transition", "The state machine has no such transition")},
	{ports.ErrConcurrentModification, conflict("concurrent-modification", "The resource was modified by another request; re-fetch the latest version and retry")},

	{usecases.ErrStalePolicy, problem{http.StatusPreconditionFailed, "precondition-failed", "Precondition failed"}},

	{shared.ErrInvalidCarrier, unprocessable("invalid-carrier", "The carrier is invalid")},
	{shared.ErrUnknownLocation, unprocessable("unknown-location", "Unknown location")},
	{shared.ErrInvalidUnitNumber, unprocessable("invalid-unit-number", "The unit number is invalid")},
	{shared.ErrInvalidSealClass, unprocessable("invalid-seal-class", "The seal class is invalid")},
	{shared.ErrInvalidHazmatClass, unprocessable("invalid-hazmat-class", "The hazmat class is invalid")},
	{shared.ErrInvalidUNNumber, unprocessable("invalid-un-number", "The UN number is invalid")},
	{inspection.ErrUnknownItem, unprocessable("unknown-item", "The inspection has no such item")},
	{inspection.ErrNotApplicableNotAllowed, unprocessable("not-applicable-not-allowed", "The item cannot be marked not applicable")},
	{inspection.ErrUnknownTemplate, unprocessable("unknown-template", "Unknown inspection template")},
	{yardvisit.ErrUnknownAppointment, unprocessable("unknown-appointment", "Unknown appointment")},
	{yardmove.ErrUnexplainedDestination, unprocessable("unexplained-destination", "A location other than the destination needs an explanation")},
	{shared.ErrValidation, unprocessable("validation", "Validation failed")},
}

// problemFor returns the problem of the first catalogue entry err matches,
// or the internal-error problem.
func problemFor(err error) problem {
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry.p
		}
	}
	return internalProblem
}
