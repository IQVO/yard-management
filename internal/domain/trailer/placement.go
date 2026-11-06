package trailer

import (
	"errors"
	"fmt"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// Errors of the placement rules (compare with errors.Is).
var (
	// ErrLocationOccupied: a spot or a door holds at most one trailer.
	ErrLocationOccupied = errors.New("location is occupied")
	// ErrUnknownLocation: the location is not a known, active Yard or Dock
	// slot of the trailer's site.
	ErrUnknownLocation = shared.ErrUnknownLocation
	// ErrDoorClosed: a door under a closure takes no trailer.
	ErrDoorClosed = errors.New("door is closed")
	// ErrDoorFlowMismatch: the trailer's direction does not match the door's
	// dock flow.
	ErrDoorFlowMismatch = errors.New("door flow does not match the trailer")
	// ErrTrailerOnHold: a held trailer is not docked and its door work does
	// not start.
	ErrTrailerOnHold = errors.New("trailer is on hold")
)

// DockFlow is the facility-layout dockFlow of a Dock slot.
type DockFlow string

// The dock flows. An empty flow is read as Both.
const (
	FlowInbound  DockFlow = "Inbound"
	FlowOutbound DockFlow = "Outbound"
	FlowBoth     DockFlow = "Both"
)

// Slot is the local-copy fact about one location: its place, its site, whether
// it is still active and, for a door, its dock flow.
type Slot struct {
	Location shared.Location
	Site     string
	Active   bool
	DockFlow DockFlow
}

// Placement is everything the placement rules need to judge putting a
// trailer at Target: the slot fact, whether the door is under a closure and
// the trailer (if any) that currently holds the location.
type Placement struct {
	Target     shared.Location
	Slot       Slot
	Closed     bool
	OccupiedBy string
}

// Occupancy is the domain service of the rule "a spot or a door holds at
// most one trailer". The use case loads the occupant under the per-location
// advisory lock and asks; a partial unique index is the backstop.
type Occupancy struct{}

// CheckFree returns ErrLocationOccupied when the location is held by a
// trailer other than requester.
func (Occupancy) CheckFree(loc shared.Location, occupiedBy, requester string) error {
	if occupiedBy != "" && occupiedBy != requester {
		return fmt.Errorf("%w: %s %s holds %s", ErrLocationOccupied, loc.Kind, loc.Code, occupiedBy)
	}
	return nil
}

// direction is the way a trailer goes through a door.
type direction int

const (
	unconstrained direction = iota
	inbound
	outbound
)

// direction derives the door direction (Y-9): inbound when ASN numbers are
// on the trailer and it is not empty; outbound when an outbound reference is
// set or the visit purpose is LIVE_LOAD or PICKUP.
func (t *Trailer) direction() direction {
	if len(t.asn) > 0 && t.load != shared.LoadEmpty {
		return inbound
	}
	if t.outboundRef != "" || t.purpose.Outbound() {
		return outbound
	}
	return unconstrained
}

func flowAllows(f DockFlow, d direction) bool {
	switch d {
	case inbound:
		return f == FlowInbound || f == FlowBoth || f == ""
	case outbound:
		return f == FlowOutbound || f == FlowBoth || f == ""
	}
	return true
}

// CanPlaceAt judges putting the trailer at p.Target: the location must be a
// known, active slot of the trailer's site (ErrUnknownLocation), free
// (ErrLocationOccupied) and, for a door, open (ErrDoorClosed), flow-compatible
// (ErrDoorFlowMismatch) and the trailer must not be on hold (ErrTrailerOnHold).
// ArriveAt runs it at completion and RequestMove at request time.
func (t *Trailer) CanPlaceAt(p Placement) error {
	if p.Target.IsZero() || p.Slot.Location != p.Target || !p.Slot.Active || p.Slot.Site != t.site {
		return fmt.Errorf("%w: %s %q is not an active slot of site %s", ErrUnknownLocation, p.Target.Kind, p.Target.Code, t.site)
	}
	if err := (Occupancy{}).CheckFree(p.Target, p.OccupiedBy, t.id); err != nil {
		return err
	}
	if p.Target.Kind != shared.Door {
		return nil
	}
	if p.Closed {
		return fmt.Errorf("%w: %s", ErrDoorClosed, p.Target.Code)
	}
	if !flowAllows(p.Slot.DockFlow, t.direction()) {
		return fmt.Errorf("%w: door %s is %s", ErrDoorFlowMismatch, p.Target.Code, p.Slot.DockFlow)
	}
	if t.hold != "" {
		return fmt.Errorf("%w: %s", ErrTrailerOnHold, t.hold)
	}
	return nil
}
