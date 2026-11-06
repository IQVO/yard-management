package yardmove

import (
	"fmt"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// Claim leases a REQUESTED hostler move to hostler for lease. The trailer
// leaves its place in the same unit of work (Trailer.StartMoving). That a
// hostler holds at most one CLAIMED move is the use case's rule
// (ErrHostlerBusy) with a unique index behind it.
func (m *Move) Claim(hostler string, now time.Time, lease time.Duration) error {
	if m.state != Requested || m.performer != Hostler {
		return m.illegal("Claim")
	}
	if err := shared.Require("hostler reference", hostler, 64); err != nil {
		return err
	}
	if lease <= 0 {
		return shared.Invalid("lease must be positive")
	}
	m.state, m.hostler, m.claimedAt, m.leaseExpires = Claimed, hostler, now.UTC(), now.Add(lease).UTC()
	m.version++
	m.emit(YardMoveClaimed{Header: m.header("YardMoveClaimed", now), MoveID: m.id, SiteID: m.site, HostlerRef: hostler, ClaimedAt: shared.Stamp(now), Version: m.version})
	return nil
}

// ExpireLease returns a CLAIMED hostler move whose lease ran out to
// REQUESTED (the trailer goes back where it was, in the same unit of work).
func (m *Move) ExpireLease(now time.Time) error {
	if m.state != Claimed || m.performer != Hostler {
		return m.illegal("ExpireLease")
	}
	if !m.LeaseExpired(now) {
		return fmt.Errorf("%w: it runs until %s", ErrLeaseNotExpired, shared.Stamp(m.leaseExpires))
	}
	holder := m.hostler
	m.state, m.hostler, m.claimedAt, m.leaseExpires = Requested, "", time.Time{}, time.Time{}
	m.version++
	m.emit(YardMoveLeaseExpired{Header: m.header("YardMoveLeaseExpired", now), MoveID: m.id, SiteID: m.site, HostlerRef: holder, Version: m.version})
	return nil
}

// CompleteInput is what completing a move records.
type CompleteInput struct {
	// Hostler is the caller; it must be the lease holder of a HOSTLER move
	// and is ignored for a VISITING_DRIVER move (the gate clerk or dock
	// operator completes it).
	Hostler string
	// Actual is where the trailer really is; zero means the destination.
	Actual shared.Location
	// Note explains an Actual other than the destination.
	Note string
	At   time.Time
}

// Complete finishes a CLAIMED move at its actual location. Only the lease
// holder completes a hostler move (ErrNotLeaseHolder); a location other than
// the destination needs a note (ErrUnexplainedDestination). The trailer
// arrives in the same unit of work (Trailer.ArriveAt at Actual()).
func (m *Move) Complete(c CompleteInput) error {
	if m.state != Claimed {
		return m.illegal("Complete")
	}
	if m.performer == Hostler && c.Hostler != m.hostler {
		return ErrNotLeaseHolder
	}
	actual := c.Actual
	if actual.IsZero() {
		actual = m.to
	}
	if _, err := shared.NewLocation(actual.Kind, actual.Code); err != nil {
		return err
	}
	if actual != m.to && shared.IsBlank(c.Note) {
		return ErrUnexplainedDestination
	}
	if err := shared.Check("note", c.Note, 200); err != nil {
		return err
	}
	m.state, m.actual, m.completedAt, m.leaseExpires = Completed, actual, c.At.UTC(), time.Time{}
	m.version++
	m.emit(YardMoveCompleted{
		Header: m.header("YardMoveCompleted", c.At), MoveID: m.id, SiteID: m.site, TrailerID: m.trailerID, Performer: m.performer,
		HostlerRef: shared.Str(m.hostler), FromLocation: m.from.Ptr(), ToLocation: m.to, ActualLocation: actual, Reason: m.reason,
		CompletedAt: shared.Stamp(c.At), DurationSeconds: max(int64(c.At.Sub(m.claimedAt)/time.Second), 0), Version: m.version,
	})
	return nil
}

// Actual returns where a COMPLETED move left the trailer (zero before).
func (m *Move) Actual() shared.Location { return m.actual }

// Cancel withdraws an open move. A VISITING_DRIVER move cannot be cancelled
// once created: the trailer is at the gate and its driver is on the way
// (Y-1). Cancelling a CLAIMED hostler move returns the trailer to its origin
// in the same unit of work.
func (m *Move) Cancel(reason string, at time.Time) error {
	if !m.Open() || m.performer == VisitingDriver {
		return m.illegal("Cancel")
	}
	if err := shared.Check("reason", reason, 200); err != nil {
		return err
	}
	if reason == "" {
		reason = "cancelled"
	}
	m.state, m.leaseExpires = Cancelled, time.Time{}
	m.version++
	m.emit(YardMoveCancelled{Header: m.header("YardMoveCancelled", at), MoveID: m.id, SiteID: m.site, Reason: reason, Version: m.version})
	return nil
}

// BandForMinutes is the CallToDoor priority: the minutes to the window start
// bucketed 1 (due now) .. 9 (more than four hours away).
func BandForMinutes(minutes int) int {
	for i, limit := range []int{0, 5, 10, 15, 30, 60, 120, 240} {
		if minutes <= limit {
			return i + 1
		}
	}
	return 9
}
