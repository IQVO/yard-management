package trailer

import (
	"fmt"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// StartMoving takes the trailer off its place (PARKED or DOCKED to IN_MOVE).
// The origin is released now (Y-8) and remembered for ReturnToOrigin; leaving
// a door raises TrailerUndocked. Invoked by the YardMove claim use case in
// the same unit of work as the move.
func (t *Trailer) StartMoving(at time.Time) error {
	if t.state != Parked && t.state != Docked {
		return t.illegal("StartMoving")
	}
	if t.state == Docked {
		t.emit(t.undocked(at))
	}
	t.origin, t.originWork = t.loc, t.work
	t.state, t.loc, t.work = InMove, shared.Location{}, ""
	t.version++
	return nil
}

func (t *Trailer) undocked(at time.Time) TrailerUndocked {
	return TrailerUndocked{
		Header: t.header("TrailerUndocked", at), TrailerID: t.id, SiteID: t.site, DoorCode: t.loc.Code,
		AppointmentID: shared.Str(t.appointmentID), UndockedAt: shared.Stamp(at), Version: t.version + 1,
	}
}

// ArriveAt puts the trailer at p.Target (IN_MOVE to PARKED on a spot, to
// DOCKED on a door) after CanPlaceAt accepts it, raising TrailerParked or
// TrailerDocked. Invoked by the YardMove completion use case in the same
// unit of work as the move.
func (t *Trailer) ArriveAt(p Placement, at time.Time) error {
	if t.state != InMove {
		return t.illegal("ArriveAt")
	}
	if err := t.CanPlaceAt(p); err != nil {
		return err
	}
	t.place(p.Target, WorkWaiting, at)
	return nil
}

// ReturnToOrigin puts the trailer back where StartMoving found it, used when
// a CLAIMED hostler move expires or is cancelled (Y-1). occupiedBy is the
// trailer now holding the origin, if any; an occupied origin is refused.
func (t *Trailer) ReturnToOrigin(occupiedBy string, at time.Time) error {
	if t.state != InMove || t.origin.IsZero() {
		return t.illegal("ReturnToOrigin")
	}
	if err := (Occupancy{}).CheckFree(t.origin, occupiedBy, t.id); err != nil {
		return err
	}
	work := t.originWork
	if work == "" {
		work = WorkWaiting
	}
	t.place(t.origin, work, at)
	return nil
}

func (t *Trailer) place(target shared.Location, work DoorWork, at time.Time) {
	t.version++
	t.origin, t.originWork = shared.Location{}, ""
	t.loc = target
	if target.Kind == shared.Door {
		t.state, t.work = Docked, work
		t.emit(TrailerDocked{
			Header: t.header("TrailerDocked", at), TrailerID: t.id, SiteID: t.site, DoorCode: target.Code,
			AppointmentID: shared.Str(t.appointmentID), ASNNumbers: shared.Strings(t.asn), VisitID: shared.Str(t.visitID),
			DockedAt: shared.Stamp(at), Version: t.version, SealState: t.sealStatePtr(), SealClass: t.sealClassPtr(),
		})
		return
	}
	t.state, t.work = Parked, ""
	t.emit(TrailerParked{Header: t.header("TrailerParked", at), TrailerID: t.id, SiteID: t.site, SpotCode: target.Code, Version: t.version})
}

// Depart takes the trailer off the site (PARKED or DOCKED to DEPARTED) for
// the visit that takes it; a DOCKED trailer departs only when its door work
// is DONE (ErrDoorWorkOpen) and raises TrailerUndocked first. The trailer
// keeps the visit id of the visit that took it.
func (t *Trailer) Depart(visitID string, at time.Time) error {
	switch t.state {
	case Parked:
	case Docked:
		if t.work != WorkDone {
			return fmt.Errorf("%w: door work is %s", ErrDoorWorkOpen, t.work)
		}
	default:
		return t.illegal("Depart")
	}
	if t.state == Docked {
		t.emit(t.undocked(at))
	}
	t.version++
	if visitID != "" {
		t.visitID = visitID
	}
	t.state, t.loc, t.work, t.departedAt = Departed, shared.Location{}, "", at.UTC()
	dwell := int64(at.Sub(t.arrivedAt) / time.Minute)
	t.emit(TrailerDeparted{
		Header: t.header("TrailerDeparted", at), TrailerID: t.id, SiteID: t.site, VisitID: shared.Str(t.visitID),
		DepartedAt: shared.Stamp(at), DwellMinutes: max(dwell, 0), LoadState: t.load, SealNumber: shared.Str(t.sealNumber), Version: t.version,
		SealState: t.sealStatePtr(), SealClass: t.sealClassPtr(),
	})
	return nil
}
