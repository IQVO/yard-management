package mcp

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/domain/inspection"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardcheck"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

// Errors the adapter raises about the call itself (the REST adapter's request
// errors, in tool vocabulary).
var (
	errInvalidLimit       = errors.New("limit must be an integer between 1 and 500 (omit it for 100)")
	errInvalidCursor      = errors.New("cursor is not a valid page token")
	errInvalidState       = errors.New("state is not one of the values of this tool")
	errInvalidReportRange = errors.New("from and to must be RFC 3339 instants, to after from and at most 92 days later")
	errInvalidKey         = errors.New("idempotencyKey must be 1 to 128 printable ASCII characters")
	errKeyReused          = errors.New("the idempotencyKey was already used with different arguments")
)

// problem is the stable slug of one error category and the HTTP status the
// REST adapter answers it with (kept so the answers stored under an
// Idempotency-Key read the same on both surfaces).
type problem struct {
	slug   string
	status int
}

// errorCatalogue names each typed error a tool can meet with the same slug
// the REST adapter uses as the last segment of its RFC 7807 "type" URI
// (internal/adapters/inbound/http/errors.go), in the same match order, so a
// client sees one error vocabulary over both surfaces.
var errorCatalogue = []struct {
	target error
	p      problem
	// replay is the replay class of the problem type (ADR 0019), the same as the
	// REST catalogue's for the same slug (replay_class_internal_test.go).
	replay replayClass
}{
	{errInvalidLimit, problem{"invalid-limit", http.StatusBadRequest}, replayFinal},
	{errInvalidCursor, problem{"invalid-cursor", http.StatusBadRequest}, replayFinal},
	{errInvalidState, problem{"invalid-state", http.StatusBadRequest}, replayFinal},
	{errInvalidReportRange, problem{"invalid-report-range", http.StatusBadRequest}, replayFinal},
	{errInvalidKey, problem{"idempotency-key-invalid", http.StatusBadRequest}, replayFinal},
	{errKeyReused, problem{"idempotency-key-reused", http.StatusConflict}, replayFinal},

	{usecases.ErrVisitNotFound, problem{"visit-not-found", http.StatusNotFound}, replayFinal},
	{usecases.ErrTrailerNotFound, problem{"trailer-not-found", http.StatusNotFound}, replayFinal},
	{usecases.ErrMoveNotFound, problem{"move-not-found", http.StatusNotFound}, replayFinal},
	{usecases.ErrYardCheckNotFound, problem{"yard-check-not-found", http.StatusNotFound}, replayFinal},
	{usecases.ErrSiteNotFound, problem{"site-not-found", http.StatusNotFound}, replayRetryable},
	{usecases.ErrInspectionNotFound, problem{"inspection-not-found", http.StatusNotFound}, replayFinal},
	{usecases.ErrTemplateNotFound, problem{"template-not-found", http.StatusNotFound}, replayRetryable},

	{yardvisit.ErrVisitAlreadyOpen, problem{"visit-already-open", http.StatusConflict}, replayRetryable},
	{trailer.ErrLocationOccupied, problem{"location-occupied", http.StatusConflict}, replayRetryable},
	{trailer.ErrDoorClosed, problem{"door-closed", http.StatusConflict}, replayRetryable},
	{trailer.ErrDoorFlowMismatch, problem{"door-flow-mismatch", http.StatusConflict}, replayRetryable},
	{trailer.ErrTrailerOnHold, problem{"trailer-on-hold", http.StatusConflict}, replayRetryable},
	{trailer.ErrTrailerNotOnHold, problem{"trailer-not-on-hold", http.StatusConflict}, replayRetryable},
	{trailer.ErrDoorWorkOpen, problem{"door-work-open", http.StatusConflict}, replayRetryable},
	{trailer.ErrPlacementRuleViolated, problem{"placement-rule-violated", http.StatusConflict}, replayRetryable},
	{trailer.ErrTrailerNotSecured, problem{"trailer-not-secured", http.StatusConflict}, replayRetryable},
	{trailer.ErrTrailerStillSecured, problem{"trailer-still-secured", http.StatusConflict}, replayRetryable},
	{shared.ErrSealClassTooLow, problem{"seal-class-too-low", http.StatusConflict}, replayRetryable},
	{inspection.ErrInspectionRequired, problem{"inspection-required", http.StatusConflict}, replayRetryable},
	{inspection.ErrInspectionFailed, problem{"inspection-failed", http.StatusConflict}, replayRetryable},
	{inspection.ErrInspectionClosed, problem{"inspection-closed", http.StatusConflict}, replayFinal},
	{inspection.ErrRequiredItemPending, problem{"required-item-pending", http.StatusConflict}, replayRetryable},
	{inspection.ErrInspectionAlreadyOpen, problem{"inspection-already-open", http.StatusConflict}, replayRetryable},
	{inspection.ErrTemplateVersionImmutable, problem{"template-version-immutable", http.StatusConflict}, replayFinal},
	{inspection.ErrTemplateIsBuiltIn, problem{"template-is-built-in", http.StatusConflict}, replayFinal},
	{yardmove.ErrTrailerHasOpenMove, problem{"trailer-has-open-move", http.StatusConflict}, replayRetryable},
	{yardmove.ErrDestinationReserved, problem{"destination-reserved", http.StatusConflict}, replayRetryable},
	{yardcheck.ErrYardCheckOpen, problem{"yard-check-open", http.StatusConflict}, replayRetryable},
	{usecases.ErrNoFreeSpot, problem{"no-free-spot", http.StatusConflict}, replayRetryable},
	{shared.ErrIllegalTransition, problem{"illegal-transition", http.StatusConflict}, replayRetryable},
	{ports.ErrConcurrentModification, problem{"concurrent-modification", http.StatusConflict}, replayRetryable},

	{inspection.ErrUnknownItem, problem{"unknown-item", http.StatusUnprocessableEntity}, replayFinal},
	{inspection.ErrNotApplicableNotAllowed, problem{"not-applicable-not-allowed", http.StatusUnprocessableEntity}, replayFinal},
	{inspection.ErrUnknownTemplate, problem{"unknown-template", http.StatusUnprocessableEntity}, replayRetryable},
	{shared.ErrUnknownLocation, problem{"unknown-location", http.StatusUnprocessableEntity}, replayRetryable},
	{shared.ErrInvalidUnitNumber, problem{"invalid-unit-number", http.StatusUnprocessableEntity}, replayFinal},
	{shared.ErrInvalidSealClass, problem{"invalid-seal-class", http.StatusUnprocessableEntity}, replayFinal},
	{shared.ErrInvalidHazmatClass, problem{"invalid-hazmat-class", http.StatusUnprocessableEntity}, replayFinal},
	{shared.ErrInvalidUNNumber, problem{"invalid-un-number", http.StatusUnprocessableEntity}, replayFinal},
	{shared.ErrValidation, problem{"validation", http.StatusUnprocessableEntity}, replayFinal},
}

// problemFor returns the problem of the first catalogue entry err matches;
// ok is false for an error outside the catalogue.
func problemFor(err error) (p problem, ok bool) {
	for _, entry := range errorCatalogue {
		if errors.Is(err, entry.target) {
			return entry.p, true
		}
	}
	return problem{}, false
}

// replayOf returns the replay class of the first catalogue entry err matches;
// ok is false for an error outside the catalogue.
func replayOf(err error) (class replayClass, ok bool) {
	for _, entry := range errorCatalogue {
		if errors.Is(err, entry.target) {
			return entry.replay, true
		}
	}
	return replayUnset, false
}

// toolErr is a tool-level error "<slug>: <detail>". Returned from a handler
// it becomes an isError tool result, never a transport failure.
type toolErr struct{ slug, detail string }

func (e *toolErr) Error() string { return fmt.Sprintf("%s: %s", e.slug, e.detail) }

func toolError(slug, detail string) error { return &toolErr{slug: slug, detail: detail} }

// mapError turns a use-case, domain or adapter error into the tool error the
// model sees. Typed errors are prefixed with their REST slug and keep their
// message; anything else is logged and reported generically so
// infrastructure details (DSNs, SQL) never reach the model.
func mapError(err error) error {
	var already *toolErr
	if errors.As(err, &already) {
		return already
	}
	if p, ok := problemFor(err); ok {
		return toolError(p.slug, err.Error())
	}
	slog.Error("mcp tool failed with an unexpected error", "error", logReplacer.Replace(err.Error()))
	return toolError("internal-error", "an unexpected internal error occurred")
}
