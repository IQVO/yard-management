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
)

// policyOf returns the site's declared policy, or its default.
func (y *Yard) policyOf(ctx context.Context, siteID string) (*yardpolicy.Policy, error) {
	p, err := y.d.Policies.Find(ctx, siteID)
	if errors.Is(err, ports.ErrNotFound) {
		return yardpolicy.Default(siteID)
	}
	return p, err
}

// placementFor gathers the facts CanPlaceAt judges: the slot copy (it must be
// of the requested kind), the closure of a door and the current occupant.
func (y *Yard) placementFor(ctx context.Context, target shared.Location, now time.Time) (trailer.Placement, error) {
	p := trailer.Placement{Target: target}
	info, ok, err := y.d.Locations.Get(ctx, target.Code)
	if err != nil {
		return p, err
	}
	if ok && info.Kind == target.Kind {
		p.Slot = trailer.Slot{Location: target, Site: info.SiteCode, Active: info.Active, DockFlow: trailer.DockFlow(info.DockFlow)}
	}
	if target.Kind == shared.Door {
		if p.Closed, err = y.d.Closures.Closed(ctx, target.Code, now); err != nil {
			return p, err
		}
	}
	occ, err := y.d.Trailers.FindAt(ctx, target)
	switch {
	case err == nil:
		p.OccupiedBy = occ.ID()
	case !errors.Is(err, ports.ErrNotFound):
		return p, err
	}
	return p, nil
}

// canTake takes the location lock and judges putting tr at loc: the placement
// rules of the Trailer and the reservation rule of the moves (no two open
// moves share a destination). exceptMove is a move allowed to hold the
// reservation (the one being completed).
func (y *Yard) canTake(ctx context.Context, tr *trailer.Trailer, loc shared.Location, exceptMove string) error {
	if err := y.d.Locks.Lock(ctx, loc.Code); err != nil {
		return err
	}
	p, err := y.placementFor(ctx, loc, y.now())
	if err != nil {
		return err
	}
	if err := tr.CanPlaceAt(p); err != nil {
		return err
	}
	mv, err := y.d.Moves.FindOpenByDestination(ctx, loc)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return nil
	case err != nil:
		return err
	case mv.ID() == exceptMove:
		return nil
	}
	return fmt.Errorf("%w: %s is reserved by %s", yardmove.ErrDestinationReserved, loc.Code, mv.ID())
}

// firstFreeSpot returns the first spot tr can take: the preferred codes in
// order, then the free Yard slots of the site's parking zones by code.
func (y *Yard) firstFreeSpot(ctx context.Context, tr *trailer.Trailer, preferred, zones []string) (shared.Location, error) {
	free, err := y.d.Locations.FreeSpots(ctx, tr.SiteID(), zones, 25)
	if err != nil {
		return shared.Location{}, err
	}
	for _, code := range append(append([]string{}, preferred...), free...) {
		loc := shared.Location{Kind: shared.Spot, Code: code}
		switch err := y.canTake(ctx, tr, loc, ""); {
		case err == nil:
			return loc, nil
		case !isPlacementRefusal(err):
			return shared.Location{}, err
		}
	}
	return shared.Location{}, ErrNoFreeSpot
}

// isPlacementRefusal reports the refusals that only mean "not this place".
func isPlacementRefusal(err error) bool {
	for _, target := range []error{trailer.ErrLocationOccupied, trailer.ErrUnknownLocation, trailer.ErrDoorClosed, trailer.ErrDoorFlowMismatch, trailer.ErrTrailerOnHold, yardmove.ErrDestinationReserved} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// target is where DirectOnCheckIn sends a trailer.
type target struct {
	to     shared.Location
	reason yardmove.Reason
	dueBy  time.Time
	band   int
}

// directTarget is policy DirectOnCheckIn (G-C, A17): a trailer with an
// on-time appointment goes to its door when free, open and flow-compatible,
// otherwise to the first free spot; a walk-in or an arrival earlier than the
// appointment tolerance goes to a holding spot.
func (y *Yard) directTarget(ctx context.Context, tr *trailer.Trailer, appt *views.Appointment, pol *yardpolicy.Policy, now time.Time) (target, error) {
	zones := pol.ParkingZoneIDs()
	if appt == nil || now.Before(appt.WindowStart.Add(-time.Duration(appt.ToleranceMinutes)*time.Minute)) {
		loc, err := y.firstFreeSpot(ctx, tr, pol.HoldingSpotCodes(), zones)
		return target{to: loc, reason: yardmove.ToHolding, band: yardmove.DefaultBand}, err
	}
	door := shared.Location{Kind: shared.Door, Code: appt.DoorCode}
	switch err := y.canTake(ctx, tr, door, ""); {
	case err == nil:
		minutes := int(appt.WindowStart.Sub(now) / time.Minute)
		return target{to: door, reason: yardmove.ToDoor, dueBy: appt.WindowStart, band: yardmove.BandForMinutes(minutes)}, nil
	case !isPlacementRefusal(err):
		return target{}, err
	}
	loc, err := y.firstFreeSpot(ctx, tr, nil, zones)
	return target{to: loc, reason: yardmove.ToSpot, band: yardmove.DefaultBand}, err
}
