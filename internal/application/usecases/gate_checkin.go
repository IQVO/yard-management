package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

// TrailerInput is a trailer a visit brings (G-B).
type TrailerInput struct {
	UnitNumber, CarrierSCAC, Kind, LoadState, SealNumber string
	// UnitNumberScheme (ISO6346) and SealClass (I, S or H) are optional (ADR 0012).
	UnitNumberScheme, SealClass string
}

// CheckInInput is the body of CheckInVisit (G-B).
type CheckInInput struct {
	SiteID, Carrier, CarrierSCAC, TractorPlate, DriverRef, Purpose, AppointmentID string
	Trailers                                                                      []TrailerInput
}

// CheckInVisit is the gate check-in: it records the visit and its trailers
// and runs DirectOnCheckIn (a VISITING_DRIVER move per trailer, created
// CLAIMED) and SealCheckAtGate, all in one unit of work. A refused check-in
// persists nothing. A seal mismatch under the REJECT policy records a
// REJECTED visit instead (Y-5).
func (y *Yard) CheckInVisit(ctx context.Context, in CheckInInput) (views.CheckIn, error) {
	var out views.CheckIn
	err := y.atomically(ctx, func(ctx context.Context) (err error) {
		out, err = y.checkIn(ctx, in)
		return err
	})
	return out, err
}

func (y *Yard) checkIn(ctx context.Context, in CheckInInput) (views.CheckIn, error) {
	now := y.now()
	pol, err := y.policyOf(ctx, in.SiteID)
	if err != nil {
		return views.CheckIn{}, err
	}
	if _, err := y.d.Visits.FindOpenByPlate(ctx, in.SiteID, in.TractorPlate); err == nil {
		return views.CheckIn{}, fmt.Errorf("%w: plate %s", yardvisit.ErrVisitAlreadyOpen, in.TractorPlate)
	} else if !errors.Is(err, ports.ErrNotFound) {
		return views.CheckIn{}, err
	}
	appt, err := y.appointmentFor(ctx, in.SiteID, in.AppointmentID)
	if err != nil {
		return views.CheckIn{}, err
	}
	refs, err := y.unitRefs(ctx, in)
	if err != nil {
		return views.CheckIn{}, err
	}
	visit, err := yardvisit.CheckIn(yardvisit.CheckInInput{
		ID: y.d.IDs.New("visit"), SiteID: in.SiteID, Carrier: in.Carrier, SCAC: in.CarrierSCAC, TractorPlate: in.TractorPlate, DriverRef: in.DriverRef,
		Purpose: shared.Purpose(in.Purpose), AppointmentID: in.AppointmentID, Trailers: refs, At: now,
		Gate: yardvisit.Gate{RequireAppointment: pol.RequireAppointment(), AppointmentKnown: appt != nil, AppointmentBooked: appt != nil && appt.Status == views.ApptBooked},
	})
	if err != nil {
		return views.CheckIn{}, err
	}
	if visit.AppointmentID() == "" {
		appt = nil
	}
	trailers, mismatches, err := y.arrivals(ctx, visit, in, refs, appt, pol, now)
	if err != nil {
		return views.CheckIn{}, err
	}
	if len(mismatches) > 0 && pol.SealMismatchAction() == yardpolicy.Reject {
		return y.rejectOnSeal(ctx, in, visit.ID(), appt, mismatches, now)
	}
	return y.admit(ctx, visit, trailers, appt, pol, now)
}

// appointmentFor returns the local copy of the named appointment of the site,
// or nil when none is named or the copy does not know it for this site.
func (y *Yard) appointmentFor(ctx context.Context, siteID, id string) (*views.Appointment, error) {
	if id == "" {
		return nil, nil
	}
	a, ok, err := y.d.Appointments.Get(ctx, id)
	if err != nil || !ok || a.SiteID != siteID {
		return nil, err
	}
	return &a, nil
}

// unitRefs mints the trailer ids and refuses a unit already on site (the
// natural key is scac + number; the scac defaults to the visit's).
func (y *Yard) unitRefs(ctx context.Context, in CheckInInput) ([]yardvisit.TrailerRef, error) {
	refs := make([]yardvisit.TrailerRef, 0, len(in.Trailers))
	seen := map[string]bool{}
	for _, tr := range in.Trailers {
		scac := tr.CarrierSCAC
		if scac == "" {
			scac = in.CarrierSCAC
		}
		key := scac + "/" + tr.UnitNumber
		if _, err := y.d.Trailers.FindOnSiteByUnit(ctx, in.SiteID, scac, tr.UnitNumber); err == nil || seen[key] {
			return nil, fmt.Errorf("%w: %s", trailer.ErrTrailerAlreadyOnSite, key)
		} else if !errors.Is(err, ports.ErrNotFound) {
			return nil, err
		}
		seen[key] = true
		refs = append(refs, yardvisit.TrailerRef{
			ID: y.d.IDs.New("trl"), UnitNumber: tr.UnitNumber, SCAC: scac, Kind: shared.TrailerKind(tr.Kind), Load: shared.LoadState(tr.LoadState),
			UnitScheme: shared.UnitScheme(tr.UnitNumberScheme), SealClass: shared.SealClass(tr.SealClass),
		})
	}
	return refs, nil
}

// arrivals records every trailer's arrival at the gate and runs the seal check
// against the appointment's expected seal; it returns the trailers and the
// found seal numbers that differ from the expected one.
func (y *Yard) arrivals(ctx context.Context, v *yardvisit.Visit, in CheckInInput, refs []yardvisit.TrailerRef, appt *views.Appointment, pol *yardpolicy.Policy, now time.Time) ([]*trailer.Trailer, []string, error) {
	var asns []string
	var outbound string
	if appt != nil {
		active, err := y.d.Asns.FilterActive(ctx, appt.ASNNumbers)
		if err != nil {
			return nil, nil, err
		}
		asns = active
		if len(appt.ShipmentRefs) > 0 {
			outbound = appt.ShipmentRefs[0]
		}
	}
	out := make([]*trailer.Trailer, 0, len(refs))
	var mismatches []string
	for i, ref := range refs {
		tr, err := trailer.RecordArrival(trailer.ArrivalInput{
			ID: ref.ID, SiteID: v.SiteID(), SCAC: ref.SCAC, Number: ref.UnitNumber, Kind: ref.Kind, Load: ref.Load, ASNNumbers: asns, OutboundRef: outbound,
			Purpose: v.Purpose(), SealNumber: in.Trailers[i].SealNumber, VisitID: v.ID(), AppointmentID: v.AppointmentID(), At: now,
			UnitScheme: ref.UnitScheme, SealClass: ref.SealClass, MinimumSealClass: pol.MinimumSealClass(),
		})
		if err != nil {
			return nil, nil, err
		}
		if appt != nil && tr.CheckSeal(appt.ExpectedSeal, now) {
			mismatches = append(mismatches, in.Trailers[i].SealNumber)
		}
		out = append(out, tr)
	}
	return out, mismatches, nil
}

// rejectOnSeal records a REJECTED visit for a seal mismatch under the REJECT
// policy: SealExceptionRaised (no trailer persisted) then VisitRejected.
func (y *Yard) rejectOnSeal(ctx context.Context, in CheckInInput, visitID string, appt *views.Appointment, found []string, now time.Time) (views.CheckIn, error) {
	rej, err := yardvisit.RejectAtGate(yardvisit.RejectInput{
		ID: visitID, SiteID: in.SiteID, Carrier: in.Carrier, SCAC: in.CarrierSCAC, TractorPlate: in.TractorPlate, DriverRef: in.DriverRef,
		AppointmentID: in.AppointmentID, Reason: yardvisit.ReasonSealMismatch, At: now,
	})
	if err != nil {
		return views.CheckIn{}, err
	}
	if err := y.d.Visits.Save(ctx, rej); err != nil {
		return views.CheckIn{}, err
	}
	events := make([]shared.Event, 0, len(found)+1)
	for _, number := range found {
		events = append(events, trailer.NewSealException(visitID, in.SiteID, rej.AppointmentID(), appt.ExpectedSeal, number, now))
	}
	if err := y.publish(ctx, append(events, rej.PullEvents()...)); err != nil {
		return views.CheckIn{}, err
	}
	return views.CheckIn{Visit: visitView(rej), Trailers: []views.Trailer{}, Moves: []views.Move{}}, nil
}

// admit persists the visit, its trailers and a VISITING_DRIVER move per
// trailer, and publishes the events.
func (y *Yard) admit(ctx context.Context, v *yardvisit.Visit, trailers []*trailer.Trailer, appt *views.Appointment, pol *yardpolicy.Policy, now time.Time) (views.CheckIn, error) {
	if err := y.d.Visits.Save(ctx, v); err != nil {
		return views.CheckIn{}, err
	}
	events := v.PullEvents()
	out := views.CheckIn{Visit: visitView(v), Trailers: make([]views.Trailer, 0, len(trailers)), Moves: make([]views.Move, 0, len(trailers))}
	for _, tr := range trailers {
		tgt, err := y.directTarget(ctx, tr, appt, pol, now)
		if err != nil {
			return views.CheckIn{}, err
		}
		mv, err := yardmove.Request(yardmove.RequestInput{
			ID: y.d.IDs.New("mv"), SiteID: tr.SiteID(), TrailerID: tr.ID(), AppointmentID: tr.AppointmentID(), Performer: yardmove.VisitingDriver, To: tgt.to,
			Reason: tgt.reason, Priority: yardmove.Priority{Band: tgt.band, DueBy: tgt.dueBy}, At: now,
		})
		if err != nil {
			return views.CheckIn{}, err
		}
		events = append(events, tr.PullEvents()...)
		if err := y.d.Trailers.Save(ctx, tr); err != nil {
			return views.CheckIn{}, err
		}
		if err := y.d.Moves.Save(ctx, mv); err != nil {
			return views.CheckIn{}, err
		}
		events = append(events, mv.PullEvents()...)
		out.Trailers = append(out.Trailers, trailerView(tr, mv.ID()))
		out.Moves = append(out.Moves, moveView(mv))
	}
	if appt != nil {
		if _, err := y.markAppointment(ctx, appt.ID, views.ApptCheckedIn); err != nil {
			return views.CheckIn{}, err
		}
	}
	return out, y.publish(ctx, events)
}

// RejectInput is the body of RejectAtGate.
type RejectInput struct {
	SiteID, Carrier, CarrierSCAC, TractorPlate, DriverRef, AppointmentID, Reason string
}

// RejectAtGate records a REJECTED visit and raises VisitRejected (Y-5).
func (y *Yard) RejectAtGate(ctx context.Context, in RejectInput) (views.Visit, error) {
	var out views.Visit
	err := y.atomically(ctx, func(ctx context.Context) error {
		v, err := yardvisit.RejectAtGate(yardvisit.RejectInput{
			ID: y.d.IDs.New("visit"), SiteID: in.SiteID, Carrier: in.Carrier, SCAC: in.CarrierSCAC, TractorPlate: in.TractorPlate, DriverRef: in.DriverRef,
			AppointmentID: in.AppointmentID, Reason: yardvisit.RejectionReason(in.Reason), At: y.now(),
		})
		if err != nil {
			return err
		}
		if err := y.d.Visits.Save(ctx, v); err != nil {
			return err
		}
		out = visitView(v)
		return y.publish(ctx, v.PullEvents())
	})
	return out, err
}

// markAppointment sets the local CHECKED_IN / USED status of an appointment
// copy (X4); an unknown or terminal appointment is a no-op.
func (y *Yard) markAppointment(ctx context.Context, id, status string) (bool, error) {
	return y.d.ApptFeed.MarkStatus(ctx, id, status)
}
