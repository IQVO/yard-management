// Package report is the PURE core of the analytics read side (ADR 0006): the
// kinds of events the projector folds, their validation, the Projection port
// the Kafka consumer calls, and the arithmetic of the reports. It imports
// nothing from this module, so the consumer (inbound) and the Postgres store
// (outbound) share it without depending on each other.
package report

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Kind is the EventName of a published type the projection folds. Every other
// type on the analytics topic is acknowledged and ignored.
type Kind string

// The twelve kinds the reports are built from: the first seven are the dwell,
// door and hostler reports (ADR 0006), the last five are the KPI standard
// (ADR 0011).
const (
	KindVisitCheckedIn    Kind = "VisitCheckedIn"
	KindTrailerDocked     Kind = "TrailerDocked"
	KindTrailerUndocked   Kind = "TrailerUndocked"
	KindDoorWorkStarted   Kind = "DoorWorkStarted"
	KindDoorWorkCompleted Kind = "DoorWorkCompleted"
	KindTrailerDeparted   Kind = "TrailerDeparted"
	KindYardMoveCompleted Kind = "YardMoveCompleted"

	KindVisitCheckedOut     Kind = "VisitCheckedOut"
	KindVisitRejected       Kind = "VisitRejected"
	KindSealExceptionRaised Kind = "SealExceptionRaised"
	KindTrailerHeld         Kind = "TrailerHeld"
	KindYardCheckClosed     Kind = "YardCheckClosed"
)

// Performer value of a yard move done by a yard truck driver.
const PerformerHostler = "HOSTLER"

// ErrRejected marks an event the projection can never apply (an unusable
// payload, or a row the store deterministically refuses). The consumer
// dead-letters it; every other error is transient and retried.
var ErrRejected = errors.New("report: event rejected")

// Arrival is one trailer of a VisitCheckedIn.
type Arrival struct {
	TrailerID  string
	UnitNumber string
	Kind       string // DRY_VAN | REEFER | FLATBED | CONTAINER
}

// Event is one analytics event in the terms the projection needs. Fields not
// used by a Kind are zero.
type Event struct {
	Kind    Kind
	EventID string
	// At is the CloudEvents `time` (occurrence), the freshness clock.
	At     time.Time
	SiteID string

	TrailerID string
	VisitID   string
	DoorCode  string

	// VisitCheckedIn. Purpose is DROP, LIVE_UNLOAD, LIVE_LOAD, PICKUP or
	// BOBTAIL; it is blank only for a payload that predates the field.
	Arrivals  []Arrival
	ArrivedAt time.Time
	Purpose   string

	// VisitCheckedOut: DepartedAt (below) and the minutes the visit was on
	// site. VisitRejected: RejectedAt and Reason. SealExceptionRaised:
	// RaisedAt. TrailerHeld: the hold Reason and the trailer Version.
	// YardCheckClosed: YardCheckID, ClosedAt and the number of discrepancies.
	OnSiteMinutes int64
	RejectedAt    time.Time
	RaisedAt      time.Time
	ClosedAt      time.Time
	Reason        string
	Version       int64
	YardCheckID   string
	Discrepancies int64

	// TrailerDocked / TrailerUndocked / DoorWorkStarted / DoorWorkCompleted /
	// TrailerDeparted instants.
	DockedAt, UndockedAt, StartedAt, CompletedAt, DepartedAt time.Time
	DwellMinutes                                             int64

	// YardMoveCompleted.
	MoveID          string
	Performer       string
	HostlerRef      string
	DurationSeconds int64
}

// Projection folds events into the analytical model. Apply is idempotent per
// EventID: applied is false when the id had already been applied.
type Projection interface {
	Apply(ctx context.Context, e Event) (applied bool, err error)
}

// Subject is the CloudEvents subject (and Kafka key) the event must carry: the
// visit id, the trailer id, the move id or the yard check id. A seal exception
// raised when the visit was rejected at the gate has no trailer: its subject is
// the visit id (ADR 0004), which the payload does not repeat, so the consumer
// copies it into VisitID.
func (e Event) Subject() string {
	switch e.Kind {
	case KindVisitCheckedIn, KindVisitCheckedOut, KindVisitRejected:
		return e.VisitID
	case KindYardMoveCompleted:
		return e.MoveID
	case KindYardCheckClosed:
		return e.YardCheckID
	case KindSealExceptionRaised:
		if e.TrailerID != "" {
			return e.TrailerID
		}
		return e.VisitID
	default:
		return e.TrailerID
	}
}

// Validate refuses an event that can never be projected. The error wraps
// ErrRejected.
func (e Event) Validate() error {
	reject := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s %s: %s", ErrRejected, e.Kind, e.EventID, fmt.Sprintf(format, args...))
	}
	if strings.TrimSpace(e.EventID) == "" {
		return reject("no event id")
	}
	if e.At.IsZero() {
		return reject("no CloudEvents time")
	}
	if strings.TrimSpace(e.SiteID) == "" {
		return reject("no site_id")
	}
	switch e.Kind {
	case KindVisitCheckedIn:
		return e.validateArrival(reject)
	case KindTrailerDocked:
		return require(reject, "trailer_id", e.TrailerID, "door_code", e.DoorCode, "docked_at", instant(e.DockedAt))
	case KindTrailerUndocked:
		return require(reject, "trailer_id", e.TrailerID, "door_code", e.DoorCode, "undocked_at", instant(e.UndockedAt))
	case KindDoorWorkStarted:
		return require(reject, "trailer_id", e.TrailerID, "door_code", e.DoorCode, "started_at", instant(e.StartedAt))
	case KindDoorWorkCompleted:
		return require(reject, "trailer_id", e.TrailerID, "door_code", e.DoorCode, "completed_at", instant(e.CompletedAt))
	case KindTrailerDeparted:
		if e.DwellMinutes < 0 {
			return reject("dwell_minutes %d is negative", e.DwellMinutes)
		}
		return require(reject, "trailer_id", e.TrailerID, "departed_at", instant(e.DepartedAt))
	case KindYardMoveCompleted:
		if e.DurationSeconds < 0 {
			return reject("duration_seconds %d is negative", e.DurationSeconds)
		}
		return require(reject, "move_id", e.MoveID, "trailer_id", e.TrailerID, "completed_at", instant(e.CompletedAt))
	}
	return e.validateKPI(reject)
}

func (e Event) validateArrival(reject func(string, ...any) error) error {
	if strings.TrimSpace(e.VisitID) == "" {
		return reject("no visit_id")
	}
	if e.ArrivedAt.IsZero() {
		return reject("no arrived_at")
	}
	if e.Purpose != "" && !validPurposes[e.Purpose] {
		return reject("purpose %q is not a visit purpose", e.Purpose)
	}
	for i, a := range e.Arrivals {
		if strings.TrimSpace(a.TrailerID) == "" {
			return reject("trailers[%d] has no trailer_id", i)
		}
		if !validKinds[a.Kind] {
			return reject("trailers[%d] kind %q is not a trailer kind", i, a.Kind)
		}
	}
	return nil
}

var validKinds = map[string]bool{"DRY_VAN": true, "REEFER": true, "FLATBED": true, "CONTAINER": true}

// instant renders a time as a presence marker for require.
func instant(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return "set"
}

// require rejects the first blank value of the (name, value) pairs.
func require(reject func(string, ...any) error, pairs ...string) error {
	for i := 0; i+1 < len(pairs); i += 2 {
		if strings.TrimSpace(pairs[i+1]) == "" {
			return reject("no %s", pairs[i])
		}
	}
	return nil
}

// IsHostlerMove reports whether a YardMoveCompleted counts as yard truck
// productivity: done by a HOSTLER with a hostler reference.
func (e Event) IsHostlerMove() bool {
	return e.Kind == KindYardMoveCompleted && e.Performer == PerformerHostler && strings.TrimSpace(e.HostlerRef) != ""
}
