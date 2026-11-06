// Package trailer holds the Trailer aggregate (ADR 0002): a towable unit on
// site, where it is, what it holds, its seal and hold. It is pure: no I/O, no
// clock reads (every command is handed the instant), ids come from the
// caller, and the persisted form is Snapshot / Restore.
package trailer

import (
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// Entity is the aggregate segment of the CloudEvents type of this package.
const Entity = "trailer"

// Errors of the Trailer aggregate (compare with errors.Is).
var (
	// ErrDoorWorkOpen: a DOCKED trailer departs only when its door work is DONE.
	ErrDoorWorkOpen = errors.New("door work is not done")
	// ErrTrailerAlreadyOnSite: the natural key (scac, number) is unique among
	// trailers on site. Raised by the use case and the partial unique index.
	ErrTrailerAlreadyOnSite = errors.New("trailer is already on site")
	// ErrTrailerNotOnHold: there is no hold to release.
	ErrTrailerNotOnHold = errors.New("trailer is not on hold")
)

// State is the lifecycle state of a trailer.
type State string

// The trailer states.
const (
	Parked   State = "PARKED"
	InMove   State = "IN_MOVE"
	Docked   State = "DOCKED"
	Departed State = "DEPARTED"
)

// DoorWork is the progress of unloading or loading, only while DOCKED.
type DoorWork string

// The door work states.
const (
	WorkWaiting    DoorWork = "WAITING"
	WorkInProgress DoorWork = "IN_PROGRESS"
	WorkDone       DoorWork = "DONE"
)

// SealState is INTACT, BROKEN or NONE.
type SealState string

// The seal states.
const (
	SealNone   SealState = "NONE"
	SealIntact SealState = "INTACT"
	SealBroken SealState = "BROKEN"
)

// HoldReason says why a trailer is held.
type HoldReason string

// The hold reasons.
const (
	HoldDamaged    HoldReason = "DAMAGED"
	HoldSealBroken HoldReason = "SEAL_BROKEN"
	HoldNotFound   HoldReason = "NOT_FOUND"
	HoldQuality    HoldReason = "QUALITY"
	HoldSecurity   HoldReason = "SECURITY"
)

// Valid reports whether r is a hold reason.
func (r HoldReason) Valid() bool {
	switch r {
	case HoldDamaged, HoldSealBroken, HoldNotFound, HoldQuality, HoldSecurity:
		return true
	}
	return false
}

// Trailer is the aggregate root.
type Trailer struct {
	id, site       string
	scac, number   string
	kind           shared.TrailerKind
	load           shared.LoadState
	asn            []string
	outboundRef    string
	purpose        shared.Purpose
	sealNumber     string
	sealState      SealState
	hold           HoldReason
	state          State
	loc            shared.Location
	work           DoorWork
	origin         shared.Location
	originWork     DoorWork
	visitID        string
	appointmentID  string
	arrivedAt      time.Time
	departedAt     time.Time
	dwellAlertedAt time.Time
	version        int64
	events         []shared.Event
}

// ArrivalInput is everything RecordArrival needs.
type ArrivalInput struct {
	ID, SiteID    string
	SCAC, Number  string
	Kind          shared.TrailerKind
	Load          shared.LoadState
	ASNNumbers    []string
	OutboundRef   string
	Purpose       shared.Purpose
	SealNumber    string
	VisitID       string
	AppointmentID string
	At            time.Time
}

// RecordArrival creates a trailer IN_MOVE at version 1: it just passed the
// gate and its first move (already CLAIMED by the visiting driver) takes it
// to its first place. A seal number makes the seal INTACT.
func RecordArrival(in ArrivalInput) (*Trailer, error) {
	for _, c := range []struct {
		name, v string
		max     int
		req     bool
	}{{"trailer id", in.ID, 64, true}, {"site id", in.SiteID, 64, true}, {"unit number", in.Number, 64, true}, {"visit id", in.VisitID, 64, true},
		{"seal number", in.SealNumber, 64, false}, {"appointment id", in.AppointmentID, 64, false}, {"outbound reference", in.OutboundRef, 128, false}} {
		check := shared.Check
		if c.req {
			check = shared.Require
		}
		if err := check(c.name, c.v, c.max); err != nil {
			return nil, err
		}
	}
	if err := shared.ValidateSCAC(in.SCAC); err != nil {
		return nil, err
	}
	if !in.Kind.Valid() || !in.Load.Valid() || !in.Purpose.Valid() {
		return nil, shared.Invalid("kind, load state and purpose must be valid enumeration values")
	}
	if in.At.IsZero() {
		return nil, shared.Invalid("arrival time is required")
	}
	t := &Trailer{
		id: in.ID, site: in.SiteID, scac: in.SCAC, number: in.Number, kind: in.Kind, load: in.Load,
		asn: shared.Strings(in.ASNNumbers), outboundRef: in.OutboundRef, purpose: in.Purpose,
		sealNumber: in.SealNumber, sealState: SealNone, state: InMove, visitID: in.VisitID,
		appointmentID: in.AppointmentID, arrivedAt: in.At.UTC(), version: 1,
	}
	if in.SealNumber != "" {
		t.sealState = SealIntact
	}
	return t, nil
}

func (t *Trailer) emit(e shared.Event) { t.events = append(t.events, e) }

func (t *Trailer) header(name string, at time.Time) shared.Header {
	return shared.NewHeader(Entity, name, t.id, at)
}

func (t *Trailer) illegal(command string) error {
	return fmt.Errorf("%w: %s on a %s trailer", shared.ErrIllegalTransition, command, t.state)
}

// PullEvents returns the pending domain events and clears them.
func (t *Trailer) PullEvents() []shared.Event {
	ev := t.events
	t.events = nil
	return ev
}

// ID returns the trailer id.
func (t *Trailer) ID() string { return t.id }

// SiteID returns the site code.
func (t *Trailer) SiteID() string { return t.site }

// State returns the lifecycle state.
func (t *Trailer) State() State { return t.state }

// Location returns where the trailer rests (zero while IN_MOVE or DEPARTED).
func (t *Trailer) Location() shared.Location { return t.loc }

// Origin returns where StartMoving found the trailer (zero when it came from
// the gate or is not in a move).
func (t *Trailer) Origin() shared.Location { return t.origin }

// AppointmentID returns the appointment the trailer came for ("" for a walk-in).
func (t *Trailer) AppointmentID() string { return t.appointmentID }

// DoorWork returns the door work progress ("" unless DOCKED).
func (t *Trailer) DoorWork() DoorWork { return t.work }

// Hold returns the hold reason ("" when none).
func (t *Trailer) Hold() HoldReason { return t.hold }

// Kind returns the trailer kind.
func (t *Trailer) Kind() shared.TrailerKind { return t.kind }

// Version returns the optimistic-concurrency version.
func (t *Trailer) Version() int64 { return t.version }

// ReleasedForDeparture reports whether the trailer may leave with its visit
// (Y-2): no open move, and either PARKED or DOCKED with door work DONE.
func (t *Trailer) ReleasedForDeparture(hasOpenMove bool) bool {
	if hasOpenMove {
		return false
	}
	return t.state == Parked || (t.state == Docked && t.work == WorkDone)
}
