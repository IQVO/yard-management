// Package yardmove holds the YardMove aggregate (ADR 0002): one instruction
// to take a trailer from where it is to a spot or a door, done by a hostler
// (pulled with a lease, never pushed) or by the visiting driver (the first
// move from the gate, created CLAIMED). It is pure: no I/O, no clock reads.
package yardmove

import (
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// Entity is the aggregate segment of the CloudEvents type of this package.
const Entity = "yardmove"

// Errors of the YardMove aggregate (compare with errors.Is).
var (
	// ErrTrailerHasOpenMove: a trailer has at most one REQUESTED or CLAIMED
	// move. Raised by the use case and the unique index.
	ErrTrailerHasOpenMove = errors.New("trailer already has an open move")
	// ErrDestinationReserved: no two open moves share a destination. Raised
	// by the use case and the unique index.
	ErrDestinationReserved = errors.New("destination is reserved by another open move")
	// ErrHostlerBusy: a hostler holds at most one CLAIMED move.
	ErrHostlerBusy = errors.New("hostler already holds a claimed move")
	// ErrNotLeaseHolder: only the lease holder completes a CLAIMED hostler move.
	ErrNotLeaseHolder = errors.New("only the lease holder completes the move")
	// ErrUnexplainedDestination: completing somewhere other than the
	// destination needs a note.
	ErrUnexplainedDestination = errors.New("a location other than the destination needs an explanation")
	// ErrLeaseNotExpired: the lease still runs.
	ErrLeaseNotExpired = errors.New("lease has not expired")
)

// State is the lifecycle state of a move.
type State string

// The move states.
const (
	Requested State = "REQUESTED"
	Claimed   State = "CLAIMED"
	Completed State = "COMPLETED"
	Cancelled State = "CANCELLED"
)

// Performer says who drives the move.
type Performer string

// The performers.
const (
	Hostler        Performer = "HOSTLER"
	VisitingDriver Performer = "VISITING_DRIVER"
)

// Reason says why the trailer moves.
type Reason string

// The move reasons.
const (
	ToDoor    Reason = "TO_DOOR"
	FromDoor  Reason = "FROM_DOOR"
	ToSpot    Reason = "TO_SPOT"
	Relocate  Reason = "RELOCATE"
	ToHolding Reason = "TO_HOLDING"
)

// Valid reports whether r is a move reason.
func (r Reason) Valid() bool {
	switch r {
	case ToDoor, FromDoor, ToSpot, Relocate, ToHolding:
		return true
	}
	return false
}

// DefaultBand is the priority band of a move that names none.
const DefaultBand = 5

// Priority orders the claim: the lowest band first (1 is the most urgent),
// then the earliest DueBy (none last), then the request time.
type Priority struct {
	Band  int
	DueBy time.Time
}

// Move is the aggregate root.
type Move struct {
	id, site, trailerID, appointmentID  string
	performer                           Performer
	from, to, actual                    shared.Location
	reason                              Reason
	prio                                Priority
	state                               State
	hostler                             string
	leaseExpires                        time.Time
	requestedAt, claimedAt, completedAt time.Time
	version                             int64
	events                              []shared.Event
}

// RequestInput is everything Request needs.
type RequestInput struct {
	ID, SiteID, TrailerID, AppointmentID string
	Performer                            Performer
	From, To                             shared.Location
	Reason                               Reason
	Priority                             Priority
	At                                   time.Time
}

// Request creates a move at version 1 and raises YardMoveRequested. A HOSTLER
// move is REQUESTED (waiting to be pulled); a VISITING_DRIVER move is created
// already CLAIMED, from the gate (From zero), with no lease.
func Request(in RequestInput) (*Move, error) {
	if err := validateRequest(in); err != nil {
		return nil, err
	}
	m := &Move{
		id: in.ID, site: in.SiteID, trailerID: in.TrailerID, appointmentID: in.AppointmentID, performer: in.Performer,
		from: in.From, to: in.To, reason: in.Reason, prio: in.Priority, state: Requested, requestedAt: in.At.UTC(), version: 1,
	}
	m.prio.DueBy = utcOrZero(in.Priority.DueBy)
	if in.Performer == VisitingDriver {
		m.state, m.claimedAt = Claimed, m.requestedAt
	}
	m.emit(YardMoveRequested{
		Header: m.header("YardMoveRequested", in.At), MoveID: m.id, SiteID: m.site, TrailerID: m.trailerID, Performer: m.performer,
		FromLocation: m.from.Ptr(), ToLocation: m.to, Reason: m.reason, AppointmentID: shared.Str(m.appointmentID),
		DueBy: shared.StampPtr(m.prio.DueBy), Version: m.version,
	})
	return m, nil
}

func utcOrZero(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.UTC()
}

func validateRequest(in RequestInput) error {
	for _, c := range []struct {
		name, v string
		max     int
	}{{"move id", in.ID, 64}, {"site id", in.SiteID, 64}, {"trailer id", in.TrailerID, 64}} {
		if err := shared.Require(c.name, c.v, c.max); err != nil {
			return err
		}
	}
	if err := shared.Check("appointment id", in.AppointmentID, 64); err != nil {
		return err
	}
	if in.Performer != Hostler && in.Performer != VisitingDriver {
		return shared.Invalid("performer must be HOSTLER or VISITING_DRIVER")
	}
	if err := validateRoute(in); err != nil {
		return err
	}
	if in.Priority.Band < 1 || in.Priority.Band > 9 {
		return shared.Invalid("priority band must be 1..9")
	}
	if in.At.IsZero() {
		return shared.Invalid("request time is required")
	}
	return nil
}

func validateRoute(in RequestInput) error {
	if !in.Reason.Valid() {
		return shared.Invalid("reason %q is not valid", in.Reason)
	}
	if _, err := shared.NewLocation(in.To.Kind, in.To.Code); err != nil {
		return err
	}
	if (in.Reason == ToDoor) != (in.To.Kind == shared.Door) {
		return shared.Invalid("reason %s does not fit a %s destination", in.Reason, in.To.Kind)
	}
	if in.From.IsZero() {
		return nil
	}
	if _, err := shared.NewLocation(in.From.Kind, in.From.Code); err != nil {
		return err
	}
	if in.Performer == VisitingDriver {
		return shared.Invalid("a visiting driver's move starts at the gate")
	}
	return nil
}

func (m *Move) emit(e shared.Event) { m.events = append(m.events, e) }

func (m *Move) header(name string, at time.Time) shared.Header {
	return shared.NewHeader(Entity, name, m.id, at)
}

func (m *Move) illegal(command string) error {
	return fmt.Errorf("%w: %s on a %s %s move", shared.ErrIllegalTransition, command, m.state, m.performer)
}

// PullEvents returns the pending domain events and clears them.
func (m *Move) PullEvents() []shared.Event {
	ev := m.events
	m.events = nil
	return ev
}

// ID returns the move id.
func (m *Move) ID() string { return m.id }

// SiteID returns the site code.
func (m *Move) SiteID() string { return m.site }

// TrailerID returns the trailer the move carries.
func (m *Move) TrailerID() string { return m.trailerID }

// AppointmentID returns the appointment the move serves ("" for none).
func (m *Move) AppointmentID() string { return m.appointmentID }

// State returns the lifecycle state.
func (m *Move) State() State { return m.state }

// Performer returns who drives the move.
func (m *Move) Performer() Performer { return m.performer }

// From returns where the trailer was (zero when it came from the gate).
func (m *Move) From() shared.Location { return m.from }

// To returns the destination.
func (m *Move) To() shared.Location { return m.to }

// Reason returns why the trailer moves.
func (m *Move) Reason() Reason { return m.reason }

// HostlerRef returns the lease holder ("" unless a HOSTLER move is CLAIMED or done).
func (m *Move) HostlerRef() string { return m.hostler }

// Version returns the optimistic-concurrency version.
func (m *Move) Version() int64 { return m.version }

// Open reports whether the move is REQUESTED or CLAIMED.
func (m *Move) Open() bool { return m.state == Requested || m.state == Claimed }

// LeaseExpired reports whether a CLAIMED hostler move's lease has run out.
func (m *Move) LeaseExpired(now time.Time) bool {
	return m.state == Claimed && m.performer == Hostler && !now.Before(m.leaseExpires)
}
