package mcp

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/usecases"
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
}{
	{errInvalidLimit, problem{"invalid-limit", http.StatusBadRequest}},
	{errInvalidCursor, problem{"invalid-cursor", http.StatusBadRequest}},
	{errInvalidState, problem{"invalid-state", http.StatusBadRequest}},
	{errInvalidReportRange, problem{"invalid-report-range", http.StatusBadRequest}},
	{errInvalidKey, problem{"idempotency-key-invalid", http.StatusBadRequest}},
	{errKeyReused, problem{"idempotency-key-reused", http.StatusConflict}},

	{usecases.ErrVisitNotFound, problem{"visit-not-found", http.StatusNotFound}},
	{usecases.ErrTrailerNotFound, problem{"trailer-not-found", http.StatusNotFound}},
	{usecases.ErrMoveNotFound, problem{"move-not-found", http.StatusNotFound}},
	{usecases.ErrYardCheckNotFound, problem{"yard-check-not-found", http.StatusNotFound}},
	{usecases.ErrSiteNotFound, problem{"site-not-found", http.StatusNotFound}},

	{yardvisit.ErrVisitAlreadyOpen, problem{"visit-already-open", http.StatusConflict}},
	{trailer.ErrLocationOccupied, problem{"location-occupied", http.StatusConflict}},
	{trailer.ErrDoorClosed, problem{"door-closed", http.StatusConflict}},
	{trailer.ErrDoorFlowMismatch, problem{"door-flow-mismatch", http.StatusConflict}},
	{trailer.ErrTrailerOnHold, problem{"trailer-on-hold", http.StatusConflict}},
	{trailer.ErrTrailerNotOnHold, problem{"trailer-not-on-hold", http.StatusConflict}},
	{trailer.ErrDoorWorkOpen, problem{"door-work-open", http.StatusConflict}},
	{trailer.ErrTrailerNotSecured, problem{"trailer-not-secured", http.StatusConflict}},
	{trailer.ErrTrailerStillSecured, problem{"trailer-still-secured", http.StatusConflict}},
	{shared.ErrSealClassTooLow, problem{"seal-class-too-low", http.StatusConflict}},
	{yardmove.ErrTrailerHasOpenMove, problem{"trailer-has-open-move", http.StatusConflict}},
	{yardmove.ErrDestinationReserved, problem{"destination-reserved", http.StatusConflict}},
	{yardcheck.ErrYardCheckOpen, problem{"yard-check-open", http.StatusConflict}},
	{usecases.ErrNoFreeSpot, problem{"no-free-spot", http.StatusConflict}},
	{shared.ErrIllegalTransition, problem{"illegal-transition", http.StatusConflict}},
	{ports.ErrConcurrentModification, problem{"concurrent-modification", http.StatusConflict}},

	{shared.ErrUnknownLocation, problem{"unknown-location", http.StatusUnprocessableEntity}},
	{shared.ErrInvalidUnitNumber, problem{"invalid-unit-number", http.StatusUnprocessableEntity}},
	{shared.ErrInvalidSealClass, problem{"invalid-seal-class", http.StatusUnprocessableEntity}},
	{shared.ErrValidation, problem{"validation", http.StatusUnprocessableEntity}},
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
	slog.Error("mcp tool failed with an unexpected error", "error", err)
	return toolError("internal-error", "an unexpected internal error occurred")
}
