package usecases

import (
	"context"
	"fmt"
	"time"

	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

// ApplyCarrierRegistered folds CarrierRegistered, the whole carrier snapshot,
// into the carrier copy (ADR 0016). at is the CloudEvents time of the event.
func (y *Yard) ApplyCarrierRegistered(ctx context.Context, eventID string, c views.Carrier, at time.Time) error {
	return y.consume(ctx, eventID, func(ctx context.Context) error {
		c.UpdatedAt = y.eventTime(at)
		return y.d.CarrierFeed.Register(ctx, c)
	})
}

// ApplyCarrierSuspended folds CarrierSuspended into the carrier copy.
func (y *Yard) ApplyCarrierSuspended(ctx context.Context, eventID, scac, reason string, version int64, at time.Time) error {
	return y.consume(ctx, eventID, func(ctx context.Context) error {
		return y.d.CarrierFeed.Suspend(ctx, scac, reason, version, y.eventTime(at))
	})
}

// ApplyCarrierReinstated folds CarrierReinstated into the carrier copy.
func (y *Yard) ApplyCarrierReinstated(ctx context.Context, eventID, scac string, version int64, at time.Time) error {
	return y.consume(ctx, eventID, func(ctx context.Context) error {
		return y.d.CarrierFeed.Reinstate(ctx, scac, version, y.eventTime(at))
	})
}

func (y *Yard) eventTime(at time.Time) time.Time {
	if at.IsZero() {
		return y.now()
	}
	return at.UTC()
}

// gateSCAC is the SCAC the carrier rule judges: the one the check-in declares,
// else the carrier of the BOOKED appointment the visit keeps. "" means no SCAC
// is known for the visit and the rule does not apply.
func gateSCAC(in CheckInInput, appt *views.Appointment) string {
	if in.CarrierSCAC != "" {
		return in.CarrierSCAC
	}
	if appt != nil && appt.Status == views.ApptBooked {
		return appt.CarrierSCAC
	}
	return ""
}

// carrierSuspended is the carrier rule of the gate (ADR 0016): true only when
// the site policy asks for it (FLAG or REJECT), a SCAC is known for the visit
// and the carrier copy says that SCAC is SUSPENDED. With the default policy
// (NONE) or no SCAC the copy is not even read; with a SCAC the copy does not
// know, the copy is read and finds nothing. Either way the visit is admitted
// exactly as before the rule existed.
func (y *Yard) carrierSuspended(ctx context.Context, pol *yardpolicy.Policy, scac string) (bool, error) {
	if pol.SuspendedCarrierAction() == yardpolicy.CarrierNone || scac == "" {
		return false, nil
	}
	c, ok, err := y.d.Carriers.Get(ctx, scac)
	if err != nil || !ok {
		return false, err
	}
	return c.Status == views.CarrierSuspended, nil
}

// rejectSuspendedCarrier records a REJECTED visit (reason CARRIER_SUSPENDED,
// VisitRejected published, nothing else persisted) and answers the refusal
// ErrCarrierSuspended. The caller commits the unit of work and then returns
// the refusal, so the turn-away is on record although the call is a 409.
func (y *Yard) rejectSuspendedCarrier(ctx context.Context, in CheckInInput, visitID, scac string, now time.Time) error {
	rej, err := yardvisit.RejectAtGate(yardvisit.RejectInput{
		ID: visitID, SiteID: in.SiteID, Carrier: in.Carrier, SCAC: scac, TractorPlate: in.TractorPlate, DriverRef: in.DriverRef,
		AppointmentID: in.AppointmentID, Reason: yardvisit.ReasonCarrierSuspended, At: now,
	})
	if err != nil {
		return err
	}
	if err := y.d.Visits.Save(ctx, rej); err != nil {
		return err
	}
	if err := y.publish(ctx, rej.PullEvents()); err != nil {
		return err
	}
	return fmt.Errorf("%w: SCAC %s is suspended; visit %s is recorded as REJECTED", yardvisit.ErrCarrierSuspended, scac, visitID)
}

// carrierFlagOf is the annotation an admitted visit gets: SUSPENDED under the
// FLAG action when the carrier is suspended, none otherwise.
func carrierFlagOf(pol *yardpolicy.Policy, suspended bool) yardvisit.CarrierFlag {
	if suspended && pol.SuspendedCarrierAction() == yardpolicy.CarrierFlag {
		return yardvisit.FlagCarrierSuspended
	}
	return ""
}
