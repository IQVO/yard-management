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

// placementFor gathers the facts CanPlaceAt judges for tr: the slot copy (it
// must be of the requested kind), the closure of a door, the current occupant
// and, for a classified trailer on a yard spot, the spots of its site (ADR
// 0014). A trailer with no classification costs no extra read.
func (y *Yard) placementFor(ctx context.Context, tr *trailer.Trailer, target shared.Location, now time.Time) (trailer.Placement, error) {
	p := trailer.Placement{Target: target}
	info, ok, err := y.d.Locations.Get(ctx, target.Code)
	if err != nil {
		return p, err
	}
	if ok && info.Kind == target.Kind {
		p.Slot = trailer.Slot{Location: target, Site: info.SiteCode, Active: info.Active, DockFlow: trailer.DockFlow(info.DockFlow), SpotKind: info.SpotKind}
	}
	if target.Kind == shared.Door {
		if p.Closed, err = y.d.Closures.Closed(ctx, target.Code, now); err != nil {
			return p, err
		}
	}
	if target.Kind == shared.Spot && tr.Classification().Declared() {
		if p.Spots, err = y.siteSpots(ctx, tr.SiteID()); err != nil {
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

// siteSpots reads what the classification rules need to know about the spots
// of a site: whether it has any active HAZMAT or REEFER_PLUG spot (from the
// location copy) and the policy allowHazmatOnUnclassifiedSpots.
func (y *Yard) siteSpots(ctx context.Context, siteID string) (trailer.SiteSpots, error) {
	slots, err := y.d.Locations.ListBySite(ctx, siteID)
	if err != nil {
		return trailer.SiteSpots{}, err
	}
	pol, err := y.policyOf(ctx, siteID)
	if err != nil {
		return trailer.SiteSpots{}, err
	}
	f := trailer.SiteSpots{AllowHazmatOnUnclassified: pol.AllowHazmatOnUnclassifiedSpots()}
	for _, s := range slots {
		if s.Kind != shared.Spot {
			continue
		}
		f.HasHazmat = f.HasHazmat || s.SpotKind == shared.SpotKindHazmat
		f.HasReeferPlug = f.HasReeferPlug || s.SpotKind == shared.SpotKindReeferPlug
	}
	return f, nil
}

// spotKindsFor says which kinds of spot a search for a free one is limited to
// for tr: nil for a trailer the rules leave alone (no classification, or no
// rule at its site), the allowed kinds otherwise, and the rule's refusal when
// the site offers no spot the trailer may take at all (ADR 0014).
func (y *Yard) spotKindsFor(ctx context.Context, tr *trailer.Trailer) ([]shared.SpotKind, error) {
	if !tr.Classification().Declared() {
		return nil, nil
	}
	facts, err := y.siteSpots(ctx, tr.SiteID())
	if err != nil {
		return nil, err
	}
	rule := tr.SpotRule(facts)
	switch {
	case !rule.Restricted:
		return nil, nil
	case len(rule.Allowed) == 0:
		return nil, rule.Refusal()
	}
	return rule.Allowed, nil
}

// canTake takes the location lock and judges putting tr at loc: the placement
// rules of the Trailer and the reservation rule of the moves (no two open
// moves share a destination). exceptMove is a move allowed to hold the
// reservation (the one being completed).
func (y *Yard) canTake(ctx context.Context, tr *trailer.Trailer, loc shared.Location, exceptMove string) error {
	if err := y.d.Locks.Lock(ctx, loc.Code); err != nil {
		return err
	}
	p, err := y.placementFor(ctx, tr, loc, y.now())
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
// order, then the free Yard slots of the site's parking zones by code. A
// classified trailer is offered only the spots its rule allows; when the site
// has none at all the refusal is ErrPlacementRuleViolated, and when they are
// all taken it is ErrNoFreeSpot (ADR 0014).
func (y *Yard) firstFreeSpot(ctx context.Context, tr *trailer.Trailer, preferred, zones []string) (shared.Location, error) {
	kinds, err := y.spotKindsFor(ctx, tr)
	if err != nil {
		return shared.Location{}, err
	}
	free, err := y.d.Locations.FreeSpots(ctx, tr.SiteID(), zones, kinds, 25)
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
	for _, target := range []error{trailer.ErrLocationOccupied, trailer.ErrUnknownLocation, trailer.ErrDoorClosed, trailer.ErrDoorFlowMismatch, trailer.ErrTrailerOnHold, trailer.ErrPlacementRuleViolated, yardmove.ErrDestinationReserved} {
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
