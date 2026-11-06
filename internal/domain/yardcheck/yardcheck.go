// Package yardcheck holds the YardCheck aggregate (ADR 0002): a yard audit.
// Someone walks the spots and doors, records what is really there, and
// closing the check reconciles the observations with what the model records.
// Closing only reports; the supervisor corrects with moves. It is pure.
package yardcheck

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// Entity is the aggregate segment of the CloudEvents type of this package.
const Entity = "yardcheck"

// Errors of the YardCheck aggregate (compare with errors.Is).
var (
	// ErrYardCheckOpen: at most one OPEN yard check per site. Raised by the
	// use case and the partial unique index.
	ErrYardCheckOpen = errors.New("a yard check is already open for this site")
	// ErrLocationAlreadyObserved: a location is observed at most once per check.
	ErrLocationAlreadyObserved = errors.New("location was already observed in this check")
)

// State is the lifecycle state of a yard check.
type State string

// The yard check states.
const (
	Open   State = "OPEN"
	Closed State = "CLOSED"
)

// DiscrepancyKind says how an observation differs from the model.
type DiscrepancyKind string

// The discrepancy kinds.
const (
	Missing       DiscrepancyKind = "MISSING"
	Unexpected    DiscrepancyKind = "UNEXPECTED"
	WrongLocation DiscrepancyKind = "WRONG_LOCATION"
)

// Observation is what was found at one location: a unit number, or empty.
type Observation struct {
	LocationCode string
	UnitNumber   string
}

// Discrepancy is one difference between the audit and the model.
type Discrepancy struct {
	Kind         DiscrepancyKind
	LocationCode string
	TrailerID    string
	UnitNumber   string
}

// Expected is what the model records at one location (a PARKED or DOCKED
// trailer of the site).
type Expected struct {
	LocationCode string
	TrailerID    string
	UnitNumber   string
}

// Check is the aggregate root.
type Check struct {
	id, site      string
	state         State
	observations  []Observation
	discrepancies []Discrepancy
	openedAt      time.Time
	closedAt      time.Time
	version       int64
	events        []shared.Event
}

// OpenYardCheck opens a check at version 1. It raises no event.
func OpenYardCheck(id, siteID string, at time.Time) (*Check, error) {
	if err := shared.Require("yard check id", id, 64); err != nil {
		return nil, err
	}
	if err := shared.Require("site id", siteID, 64); err != nil {
		return nil, err
	}
	if at.IsZero() {
		return nil, shared.Invalid("open time is required")
	}
	return &Check{id: id, site: siteID, state: Open, openedAt: at.UTC(), version: 1}, nil
}

// RecordObservation records what was found at one location. A location is
// observed at most once per check.
func (c *Check) RecordObservation(locationCode, unitNumber string) error {
	if c.state != Open {
		return fmt.Errorf("%w: RecordObservation on a %s check", shared.ErrIllegalTransition, c.state)
	}
	if err := shared.Require("location code", locationCode, 128); err != nil {
		return err
	}
	if err := shared.Check("unit number", unitNumber, 64); err != nil {
		return err
	}
	for _, o := range c.observations {
		if o.LocationCode == locationCode {
			return fmt.Errorf("%w: %s", ErrLocationAlreadyObserved, locationCode)
		}
	}
	c.observations = append(c.observations, Observation{LocationCode: locationCode, UnitNumber: unitNumber})
	c.version++
	return nil
}

// Close reconciles the observations with expected (every trailer the model
// places at the site) and raises YardCheckClosed with the discrepancies.
func (c *Check) Close(expected []Expected, at time.Time) error {
	if c.state != Open {
		return fmt.Errorf("%w: Close on a %s check", shared.ErrIllegalTransition, c.state)
	}
	c.discrepancies = Reconcile(c.observations, expected)
	c.state, c.closedAt = Closed, at.UTC()
	c.version++
	list := make([]ClosedDiscrepancy, 0, len(c.discrepancies))
	for _, d := range c.discrepancies {
		list = append(list, ClosedDiscrepancy{Kind: d.Kind, LocationCode: d.LocationCode, TrailerID: shared.Str(d.TrailerID), UnitNumber: shared.Str(d.UnitNumber)})
	}
	c.events = append(c.events, YardCheckClosed{
		Header: shared.NewHeader(Entity, "YardCheckClosed", c.id, at), YardCheckID: c.id, SiteID: c.site,
		ClosedAt: shared.Stamp(at), Discrepancies: list,
	})
	return nil
}

// Reconcile compares observations with the model (Y-11). An observed empty
// location with a trailer recorded is MISSING; an observed unit that the
// model places elsewhere is WRONG_LOCATION, one it does not know on site is
// UNEXPECTED; a location with a different unit makes the recorded trailer
// MISSING; a location never observed is MISSING when a trailer is recorded
// there. A trailer found elsewhere (WRONG_LOCATION) is not also MISSING.
// The result is ordered: UNEXPECTED and WRONG_LOCATION in observation order, then MISSING by
// location code.
func Reconcile(observations []Observation, expected []Expected) []Discrepancy {
	byLoc := make(map[string]Expected, len(expected))
	byUnit := make(map[string]Expected, len(expected))
	for _, e := range expected {
		byLoc[e.LocationCode] = e
		byUnit[e.UnitNumber] = e
	}
	var out []Discrepancy
	foundElsewhere := map[string]bool{}
	observed := map[string]bool{}
	var missing []Discrepancy
	for _, o := range observations {
		observed[o.LocationCode] = true
		exp, has := byLoc[o.LocationCode]
		if has && exp.UnitNumber == o.UnitNumber {
			continue
		}
		if has {
			missing = append(missing, Discrepancy{Kind: Missing, LocationCode: o.LocationCode, TrailerID: exp.TrailerID, UnitNumber: exp.UnitNumber})
		}
		if o.UnitNumber == "" {
			continue
		}
		if known, ok := byUnit[o.UnitNumber]; ok {
			foundElsewhere[known.TrailerID] = true
			out = append(out, Discrepancy{Kind: WrongLocation, LocationCode: o.LocationCode, TrailerID: known.TrailerID, UnitNumber: o.UnitNumber})
		} else {
			out = append(out, Discrepancy{Kind: Unexpected, LocationCode: o.LocationCode, UnitNumber: o.UnitNumber})
		}
	}
	for _, e := range expected {
		if !observed[e.LocationCode] {
			missing = append(missing, Discrepancy{Kind: Missing, LocationCode: e.LocationCode, TrailerID: e.TrailerID, UnitNumber: e.UnitNumber})
		}
	}
	sort.SliceStable(missing, func(i, j int) bool { return missing[i].LocationCode < missing[j].LocationCode })
	res := make([]Discrepancy, 0, len(out)+len(missing))
	for _, m := range missing {
		if !foundElsewhere[m.TrailerID] {
			res = append(res, m)
		}
	}
	return append(out, res...)
}

// PullEvents returns the pending domain events and clears them.
func (c *Check) PullEvents() []shared.Event {
	ev := c.events
	c.events = nil
	return ev
}

// ID returns the yard check id.
func (c *Check) ID() string { return c.id }

// SiteID returns the site code.
func (c *Check) SiteID() string { return c.site }

// State returns OPEN or CLOSED.
func (c *Check) State() State { return c.state }

// Version returns the optimistic-concurrency version.
func (c *Check) Version() int64 { return c.version }

// ClosedDiscrepancy is the wire form of a Discrepancy.
type ClosedDiscrepancy struct {
	Kind         DiscrepancyKind `json:"kind"`
	LocationCode string          `json:"location_code"`
	TrailerID    *string         `json:"trailer_id"`
	UnitNumber   *string         `json:"unit_number"`
}

// YardCheckClosed: an audit closed with its discrepancies (integration, no
// version).
type YardCheckClosed struct {
	shared.Header `json:"-"`
	YardCheckID   string              `json:"yard_check_id"`
	SiteID        string              `json:"site_id"`
	ClosedAt      string              `json:"closed_at"`
	Discrepancies []ClosedDiscrepancy `json:"discrepancies"`
}

// Snapshot is the persisted form of a Check: scalars only.
type Snapshot struct {
	ID, SiteID    string
	State         State
	Observations  []Observation
	Discrepancies []Discrepancy
	OpenedAt      time.Time
	ClosedAt      time.Time
	Version       int64
}

// Snapshot returns the persisted form (no events).
func (c *Check) Snapshot() Snapshot {
	return Snapshot{
		ID: c.id, SiteID: c.site, State: c.state, Observations: append([]Observation{}, c.observations...),
		Discrepancies: append([]Discrepancy{}, c.discrepancies...), OpenedAt: c.openedAt, ClosedAt: c.closedAt, Version: c.version,
	}
}

// Restore rebuilds a persisted check, validating state, observations and times.
func Restore(s Snapshot) (*Check, error) {
	c, err := OpenYardCheck(s.ID, s.SiteID, s.OpenedAt)
	if err != nil {
		return nil, err
	}
	if err := validateSnapshot(s); err != nil {
		return nil, err
	}
	c.state, c.version, c.closedAt = s.State, s.Version, s.ClosedAt.UTC()
	c.observations = append([]Observation{}, s.Observations...)
	c.discrepancies = append([]Discrepancy{}, s.Discrepancies...)
	return c, nil
}

func validateSnapshot(s Snapshot) error {
	switch {
	case s.Version < 1:
		return shared.Invalid("version must be at least 1")
	case s.State == Open && (!s.ClosedAt.IsZero() || len(s.Discrepancies) > 0):
		return shared.Invalid("an OPEN check has not closed")
	case s.State == Closed && s.ClosedAt.IsZero():
		return shared.Invalid("a CLOSED check has a closing time")
	case s.State != Open && s.State != Closed:
		return shared.Invalid("state %q is not valid", s.State)
	}
	return validateLists(s)
}

func validateLists(s Snapshot) error {
	seen := map[string]bool{}
	for _, o := range s.Observations {
		if shared.IsBlank(o.LocationCode) || seen[o.LocationCode] {
			return shared.Invalid("observations name each location once")
		}
		seen[o.LocationCode] = true
	}
	for _, d := range s.Discrepancies {
		if d.Kind != Missing && d.Kind != Unexpected && d.Kind != WrongLocation {
			return shared.Invalid("discrepancy kind %q is not valid", d.Kind)
		}
	}
	return nil
}
