package usecases

import (
	"context"
	"errors"
	"strings"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

// internalErrorReason is the reason of every refusal no problem slug names (a
// database error, a bug): the service is at fault, not the caller.
const internalErrorReason = "internal-error"

// carrierSuspendedReason is the reason of a visit rejected because the carrier
// is suspended (ADR 0016's rule, answered as ErrCarrierSuspended).
const carrierSuspendedReason = "carrier-suspended"

// checkInRefusals maps the typed refusals of CheckInVisit to the problem slug
// the REST adapter answers with, in the match order of its problem catalogue
// (internal/adapters/inbound/http/errors.go, which asserts the two agree).
var checkInRefusals = []struct {
	err    error
	reason string
}{
	{ErrSiteNotFound, "site-not-found"},
	{yardvisit.ErrVisitAlreadyOpen, "visit-already-open"},
	{yardvisit.ErrAppointmentRequired, "appointment-required"},
	{trailer.ErrLocationOccupied, "location-occupied"},
	{trailer.ErrTrailerAlreadyOnSite, "trailer-already-on-site"},
	{trailer.ErrPlacementRuleViolated, "placement-rule-violated"},
	{shared.ErrSealClassTooLow, "seal-class-too-low"},
	{ErrNoFreeSpot, "no-free-spot"},
	{ports.ErrConcurrentModification, "concurrent-modification"},
	{shared.ErrInvalidCarrier, "invalid-carrier"},
	{shared.ErrUnknownLocation, "unknown-location"},
	{shared.ErrInvalidUnitNumber, "invalid-unit-number"},
	{shared.ErrInvalidSealClass, "invalid-seal-class"},
	{shared.ErrInvalidHazmatClass, "invalid-hazmat-class"},
	{shared.ErrInvalidUNNumber, "invalid-un-number"},
	{yardvisit.ErrUnknownAppointment, "unknown-appointment"},
	{shared.ErrValidation, "validation"},
}

// CheckInRefusalReason is the reason attribute of a refused check-in: the
// problem slug of err, or internal-error when no slug names it.
func CheckInRefusalReason(err error) string {
	for _, r := range checkInRefusals {
		if errors.Is(err, r.err) {
			return r.reason
		}
	}
	return internalErrorReason
}

// CheckInRefusalErrors lists the typed refusals CheckInRefusalReason names, so
// the REST adapter can assert that its problem catalogue gives each one the
// same slug.
func CheckInRefusalErrors() []error {
	out := make([]error, 0, len(checkInRefusals))
	for _, r := range checkInRefusals {
		out = append(out, r.err)
	}
	return out
}

// recordCheckIn counts one check-in attempt, once, after its unit of work ended:
//
//   - admitted: a CHECKED_IN visit was recorded;
//   - rejected: a REJECTED visit was recorded (the gate turned the truck away),
//     reason is why: seal-mismatch, or carrier-suspended (answered as an error
//     but the turn-away is committed);
//   - refused: nothing was persisted; reason is the problem slug of the error.
func (y *Yard) recordCheckIn(ctx context.Context, out views.CheckIn, err error) {
	m := y.d.metrics()
	switch {
	case errors.Is(err, yardvisit.ErrCarrierSuspended):
		m.CheckInRejected(ctx, carrierSuspendedReason)
	case err != nil:
		m.CheckInRefused(ctx, CheckInRefusalReason(err))
	case out.Visit.State == string(yardvisit.Rejected):
		m.CheckInRejected(ctx, rejectionSlug(out.Visit.RejectionReason))
	default:
		m.CheckInAdmitted(ctx)
	}
}

// rejectionSlug is the kebab-case of a visit's rejection reason
// (SEAL_MISMATCH -> seal-mismatch), the vocabulary of the problem slugs.
func rejectionSlug(reason *string) string {
	if reason == nil || *reason == "" {
		return internalErrorReason
	}
	return strings.ToLower(strings.ReplaceAll(*reason, "_", "-"))
}

// metrics returns the configured CheckInMetrics or a no-op, so a Deps built
// without one (unit tests, the in-memory doubles) records nothing.
func (d Deps) metrics() ports.CheckInMetrics {
	if d.Metrics != nil {
		return d.Metrics
	}
	return noMetrics{}
}

// noMetrics is the CheckInMetrics of a Deps without one.
type noMetrics struct{}

func (noMetrics) CheckInAdmitted(context.Context)         {}
func (noMetrics) CheckInRejected(context.Context, string) {}
func (noMetrics) CheckInRefused(context.Context, string)  {}
