package yardmove

import (
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// The events of the YardMove aggregate (ADR 0004), all analytics only.

// YardMoveRequested: a move was requested.
type YardMoveRequested struct {
	shared.Header `json:"-"`
	MoveID        string           `json:"move_id"`
	SiteID        string           `json:"site_id"`
	TrailerID     string           `json:"trailer_id"`
	Performer     Performer        `json:"performer"`
	FromLocation  *shared.Location `json:"from_location"`
	ToLocation    shared.Location  `json:"to_location"`
	Reason        Reason           `json:"reason"`
	AppointmentID *string          `json:"appointment_id"`
	DueBy         *string          `json:"due_by"`
	Version       int64            `json:"version"`
}

// YardMoveClaimed: a hostler claimed the move with a lease.
type YardMoveClaimed struct {
	shared.Header `json:"-"`
	MoveID        string `json:"move_id"`
	SiteID        string `json:"site_id"`
	HostlerRef    string `json:"hostler_ref"`
	ClaimedAt     string `json:"claimed_at"`
	Version       int64  `json:"version"`
}

// YardMoveLeaseExpired: a lease ran out and the move went back to REQUESTED.
type YardMoveLeaseExpired struct {
	shared.Header `json:"-"`
	MoveID        string `json:"move_id"`
	SiteID        string `json:"site_id"`
	HostlerRef    string `json:"hostler_ref"`
	Version       int64  `json:"version"`
}

// YardMoveCompleted: the move finished at its actual location.
type YardMoveCompleted struct {
	shared.Header   `json:"-"`
	MoveID          string           `json:"move_id"`
	SiteID          string           `json:"site_id"`
	TrailerID       string           `json:"trailer_id"`
	Performer       Performer        `json:"performer"`
	HostlerRef      *string          `json:"hostler_ref"`
	FromLocation    *shared.Location `json:"from_location"`
	ToLocation      shared.Location  `json:"to_location"`
	ActualLocation  shared.Location  `json:"actual_location"`
	Reason          Reason           `json:"reason"`
	CompletedAt     string           `json:"completed_at"`
	DurationSeconds int64            `json:"duration_seconds"`
	Version         int64            `json:"version"`
}

// YardMoveCancelled: an open move was cancelled.
type YardMoveCancelled struct {
	shared.Header `json:"-"`
	MoveID        string `json:"move_id"`
	SiteID        string `json:"site_id"`
	Reason        string `json:"reason"`
	Version       int64  `json:"version"`
}

// Snapshot is the persisted form of a Move: scalars only. Restore validates it.
type Snapshot struct {
	ID, SiteID, TrailerID, AppointmentID string
	Performer                            Performer
	From, To, Actual                     shared.Location
	Reason                               Reason
	Priority                             Priority
	State                                State
	HostlerRef                           string
	LeaseExpiresAt                       time.Time
	RequestedAt, ClaimedAt, CompletedAt  time.Time
	Version                              int64
}

// Snapshot returns the persisted form (no events).
func (m *Move) Snapshot() Snapshot {
	return Snapshot{
		ID: m.id, SiteID: m.site, TrailerID: m.trailerID, AppointmentID: m.appointmentID, Performer: m.performer,
		From: m.from, To: m.to, Actual: m.actual, Reason: m.reason, Priority: m.prio, State: m.state, HostlerRef: m.hostler,
		LeaseExpiresAt: m.leaseExpires, RequestedAt: m.requestedAt, ClaimedAt: m.claimedAt, CompletedAt: m.completedAt, Version: m.version,
	}
}

// Restore rebuilds a persisted move, validating that state, performer,
// lease and completion facts agree.
func Restore(s Snapshot) (*Move, error) {
	if err := validateRequest(RequestInput{
		ID: s.ID, SiteID: s.SiteID, TrailerID: s.TrailerID, AppointmentID: s.AppointmentID, Performer: s.Performer,
		From: s.From, To: s.To, Reason: s.Reason, Priority: s.Priority, At: s.RequestedAt,
	}); err != nil {
		return nil, err
	}
	if err := validateState(s); err != nil {
		return nil, err
	}
	if s.Version < 1 {
		return nil, shared.Invalid("version must be at least 1")
	}
	return &Move{
		id: s.ID, site: s.SiteID, trailerID: s.TrailerID, appointmentID: s.AppointmentID, performer: s.Performer,
		from: s.From, to: s.To, actual: s.Actual, reason: s.Reason, prio: Priority{Band: s.Priority.Band, DueBy: utcOrZero(s.Priority.DueBy)},
		state: s.State, hostler: s.HostlerRef, leaseExpires: utcOrZero(s.LeaseExpiresAt), requestedAt: s.RequestedAt.UTC(),
		claimedAt: utcOrZero(s.ClaimedAt), completedAt: utcOrZero(s.CompletedAt), version: s.Version,
	}, nil
}

func validateState(s Snapshot) error {
	switch s.State {
	case Requested:
		return validateRequested(s)
	case Claimed:
		return validateClaimed(s)
	case Completed:
		if s.CompletedAt.IsZero() || s.ClaimedAt.IsZero() || s.Actual.IsZero() || !s.LeaseExpiresAt.IsZero() {
			return shared.Invalid("a COMPLETED move has claim and completion times and an actual location")
		}
	case Cancelled:
		if !s.LeaseExpiresAt.IsZero() {
			return shared.Invalid("a CANCELLED move holds no lease")
		}
	default:
		return shared.Invalid("state %q is not valid", s.State)
	}
	return nil
}

func validateRequested(s Snapshot) error {
	if s.HostlerRef != "" || !s.LeaseExpiresAt.IsZero() || !s.ClaimedAt.IsZero() || s.Performer != Hostler {
		return shared.Invalid("a REQUESTED move is a hostler move with no holder, lease or claim time")
	}
	return nil
}

func validateClaimed(s Snapshot) error {
	holds := s.HostlerRef != "" && !s.LeaseExpiresAt.IsZero()
	free := s.HostlerRef == "" && s.LeaseExpiresAt.IsZero()
	if s.ClaimedAt.IsZero() || (s.Performer == Hostler && !holds) || (s.Performer == VisitingDriver && !free) {
		return shared.Invalid("a CLAIMED hostler move has a holder, a lease and a claim time, a visiting-driver move a claim time only")
	}
	return nil
}
