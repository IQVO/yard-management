package memory

import (
	"context"
	"sort"
	"time"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
)

// Moves implements ports.MoveRepo.
type Moves struct{ s *Store }

// Moves returns the move repository.
func (s *Store) Moves() *Moves { return &Moves{s} }

func open(m yardmove.Snapshot) bool {
	return m.State == yardmove.Requested || m.State == yardmove.Claimed
}

// Save implements ports.MoveRepo.
func (r *Moves) Save(_ context.Context, m *yardmove.Move) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	snap := m.Snapshot()
	old, exists := r.s.st.moves[snap.ID]
	if err := guard(exists, old.Version, snap.Version); err != nil {
		return err
	}
	if open(snap) {
		for id, o := range r.s.st.moves {
			if id == snap.ID || !open(o) {
				continue
			}
			switch {
			case o.TrailerID == snap.TrailerID:
				return yardmove.ErrTrailerHasOpenMove
			case o.To == snap.To:
				return yardmove.ErrDestinationReserved
			case snap.State == yardmove.Claimed && snap.HostlerRef != "" && o.State == yardmove.Claimed && o.HostlerRef == snap.HostlerRef && o.SiteID == snap.SiteID:
				return yardmove.ErrHostlerBusy
			}
		}
	}
	r.s.st.moves[snap.ID] = snap
	return nil
}

func (r *Moves) sorted(match func(yardmove.Snapshot) bool, less func(a, b yardmove.Snapshot) bool) ([]*yardmove.Move, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var rows []yardmove.Snapshot
	for _, snap := range r.s.st.moves {
		if match(snap) {
			rows = append(rows, snap)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return less(rows[i], rows[j]) })
	out := make([]*yardmove.Move, 0, len(rows))
	for _, snap := range rows {
		m, err := yardmove.Restore(snap)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func byRequest(a, b yardmove.Snapshot) bool {
	return keyBefore(a.RequestedAt, a.ID, b.RequestedAt, b.ID)
}

func first(moves []*yardmove.Move, err error) (*yardmove.Move, error) {
	if err != nil {
		return nil, err
	}
	if len(moves) == 0 {
		return nil, ports.ErrNotFound
	}
	return moves[0], nil
}

// FindByID implements ports.MoveRepo.
func (r *Moves) FindByID(_ context.Context, id string) (*yardmove.Move, error) {
	return first(r.sorted(func(s yardmove.Snapshot) bool { return s.ID == id }, byRequest))
}

// FindOpenByTrailer implements ports.MoveRepo.
func (r *Moves) FindOpenByTrailer(_ context.Context, trailerID string) (*yardmove.Move, error) {
	return first(r.sorted(func(s yardmove.Snapshot) bool { return open(s) && s.TrailerID == trailerID }, byRequest))
}

// FindOpenByDestination implements ports.MoveRepo.
func (r *Moves) FindOpenByDestination(_ context.Context, loc shared.Location) (*yardmove.Move, error) {
	return first(r.sorted(func(s yardmove.Snapshot) bool { return open(s) && s.To == loc }, byRequest))
}

// FindClaimedByHostler implements ports.MoveRepo.
func (r *Moves) FindClaimedByHostler(_ context.Context, siteID, hostlerRef string) (*yardmove.Move, error) {
	return first(r.sorted(func(s yardmove.Snapshot) bool {
		return s.State == yardmove.Claimed && s.SiteID == siteID && s.HostlerRef == hostlerRef
	}, byRequest))
}

// ClaimNext implements ports.MoveRepo: band, then due-by (none last), then request time.
func (r *Moves) ClaimNext(_ context.Context, siteID string) (*yardmove.Move, error) {
	return first(r.sorted(func(s yardmove.Snapshot) bool {
		return s.State == yardmove.Requested && s.Performer == yardmove.Hostler && s.SiteID == siteID
	}, func(a, b yardmove.Snapshot) bool {
		switch {
		case a.Priority.Band != b.Priority.Band:
			return a.Priority.Band < b.Priority.Band
		case a.Priority.DueBy.IsZero() != b.Priority.DueBy.IsZero():
			return !a.Priority.DueBy.IsZero()
		case !a.Priority.DueBy.Equal(b.Priority.DueBy):
			return a.Priority.DueBy.Before(b.Priority.DueBy)
		}
		return byRequest(a, b)
	}))
}

// ListExpiredClaims implements ports.MoveRepo.
func (r *Moves) ListExpiredClaims(_ context.Context, now time.Time, limit int) ([]*yardmove.Move, error) {
	moves, err := r.sorted(func(s yardmove.Snapshot) bool {
		return s.State == yardmove.Claimed && s.Performer == yardmove.Hostler && !s.LeaseExpiresAt.After(now)
	}, byRequest)
	if len(moves) > limit {
		moves = moves[:limit]
	}
	return moves, err
}

// ListOpenByAppointment implements ports.MoveRepo.
func (r *Moves) ListOpenByAppointment(_ context.Context, appointmentID string) ([]*yardmove.Move, error) {
	return r.sorted(func(s yardmove.Snapshot) bool { return open(s) && s.AppointmentID == appointmentID }, byRequest)
}

// ListOpen implements ports.MoveRepo.
func (r *Moves) ListOpen(_ context.Context, siteID string) ([]*yardmove.Move, error) {
	return r.sorted(func(s yardmove.Snapshot) bool { return open(s) && s.SiteID == siteID }, byRequest)
}

// List implements ports.MoveRepo.
func (r *Moves) List(_ context.Context, f views.MoveFilter, after *views.Key, limit int) ([]*yardmove.Move, error) {
	moves, err := r.sorted(func(s yardmove.Snapshot) bool {
		afterOK := after == nil || keyBefore(after.At, after.ID, s.RequestedAt, s.ID)
		return afterOK && (f.SiteID == "" || s.SiteID == f.SiteID) && (f.State == "" || string(s.State) == f.State) && (f.TrailerID == "" || s.TrailerID == f.TrailerID) && (f.HostlerRef == "" || s.HostlerRef == f.HostlerRef)
	}, byRequest)
	if limit > 0 && len(moves) > limit {
		moves = moves[:limit]
	}
	return moves, err
}
