package memory

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardcheck"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

// guard applies the version rule of every Save: version 1 inserts, a later
// version updates the row stored at version - 1.
func guard(exists bool, stored, version int64) error {
	if version == 1 && !exists {
		return nil
	}
	if version > 1 && exists && stored == version-1 {
		return nil
	}
	return ports.ErrConcurrentModification
}

// Visits implements ports.VisitRepo.
type Visits struct{ s *Store }

// Visits returns the visit repository.
func (s *Store) Visits() *Visits { return &Visits{s} }

// Save implements ports.VisitRepo.
func (r *Visits) Save(_ context.Context, v *yardvisit.Visit) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	snap := v.Snapshot()
	old, exists := r.s.st.visits[snap.ID]
	if err := guard(exists, old.Version, snap.Version); err != nil {
		return err
	}
	if snap.State == yardvisit.CheckedIn {
		for id, o := range r.s.st.visits {
			if id != snap.ID && o.State == yardvisit.CheckedIn && o.SiteID == snap.SiteID && o.Plate == snap.Plate {
				return yardvisit.ErrVisitAlreadyOpen
			}
		}
	}
	r.s.st.visits[snap.ID] = snap
	return nil
}

// FindByID implements ports.VisitRepo.
func (r *Visits) FindByID(_ context.Context, id string) (*yardvisit.Visit, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	snap, ok := r.s.st.visits[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return yardvisit.Restore(snap)
}

// FindOpenByPlate implements ports.VisitRepo.
func (r *Visits) FindOpenByPlate(_ context.Context, siteID, plate string) (*yardvisit.Visit, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for _, snap := range r.s.st.visits {
		if snap.State == yardvisit.CheckedIn && snap.SiteID == siteID && snap.Plate == plate {
			return yardvisit.Restore(snap)
		}
	}
	return nil, ports.ErrNotFound
}

// List implements ports.VisitRepo (newest first).
func (r *Visits) List(_ context.Context, f views.VisitFilter, after *views.Key, limit int) ([]*yardvisit.Visit, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var rows []yardvisit.Snapshot
	for _, snap := range r.s.st.visits {
		if (f.SiteID == "" || snap.SiteID == f.SiteID) && (f.State == "" || string(snap.State) == f.State) && (f.TractorPlate == "" || snap.Plate == f.TractorPlate) {
			rows = append(rows, snap)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return keyBefore(rows[j].ArrivedAt, rows[j].ID, rows[i].ArrivedAt, rows[i].ID) })
	var out []*yardvisit.Visit
	for _, snap := range rows {
		if after != nil && !keyBefore(snap.ArrivedAt, snap.ID, after.At, after.ID) {
			continue
		}
		v, err := yardvisit.Restore(snap)
		if err != nil {
			return nil, err
		}
		if out = append(out, v); len(out) == limit {
			break
		}
	}
	return out, nil
}

// keyBefore orders (at, id) pairs ascending.
func keyBefore(at1 time.Time, id1 string, at2 time.Time, id2 string) bool {
	if !at1.Equal(at2) {
		return at1.Before(at2)
	}
	return id1 < id2
}

// Trailers implements ports.TrailerRepo.
type Trailers struct{ s *Store }

// Trailers returns the trailer repository.
func (s *Store) Trailers() *Trailers { return &Trailers{s} }

// Save implements ports.TrailerRepo.
func (r *Trailers) Save(_ context.Context, t *trailer.Trailer) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	snap := t.Snapshot()
	old, exists := r.s.st.trailers[snap.ID]
	if err := guard(exists, old.Version, snap.Version); err != nil {
		return err
	}
	for id, o := range r.s.st.trailers {
		if id == snap.ID {
			continue
		}
		if !snap.Location.IsZero() && o.Location == snap.Location {
			return fmt.Errorf("%w: %s", trailer.ErrLocationOccupied, snap.Location.Code)
		}
		if snap.State != trailer.Departed && o.State != trailer.Departed && o.SiteID == snap.SiteID && o.SCAC == snap.SCAC && o.Number == snap.Number {
			return fmt.Errorf("%w: %s", trailer.ErrTrailerAlreadyOnSite, snap.Number)
		}
	}
	r.s.st.trailers[snap.ID] = snap
	return nil
}

func (r *Trailers) find(match func(trailer.Snapshot) bool) (*trailer.Trailer, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for _, snap := range r.s.st.trailers {
		if match(snap) {
			return trailer.Restore(snap)
		}
	}
	return nil, ports.ErrNotFound
}

// FindByID implements ports.TrailerRepo.
func (r *Trailers) FindByID(_ context.Context, id string) (*trailer.Trailer, error) {
	return r.find(func(s trailer.Snapshot) bool { return s.ID == id })
}

// FindAt implements ports.TrailerRepo.
func (r *Trailers) FindAt(_ context.Context, loc shared.Location) (*trailer.Trailer, error) {
	return r.find(func(s trailer.Snapshot) bool { return s.Location == loc })
}

// FindOnSiteByUnit implements ports.TrailerRepo.
func (r *Trailers) FindOnSiteByUnit(_ context.Context, siteID, scac, number string) (*trailer.Trailer, error) {
	return r.find(func(s trailer.Snapshot) bool {
		return s.State != trailer.Departed && s.SiteID == siteID && s.SCAC == scac && s.Number == number
	})
}

// FindByAppointment implements ports.TrailerRepo.
func (r *Trailers) FindByAppointment(_ context.Context, appointmentID string) (*trailer.Trailer, error) {
	return r.find(func(s trailer.Snapshot) bool { return s.State != trailer.Departed && s.AppointmentID == appointmentID })
}

func (r *Trailers) filtered(match func(trailer.Snapshot) bool) ([]trailer.Snapshot, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var rows []trailer.Snapshot
	for _, snap := range r.s.st.trailers {
		if match(snap) {
			rows = append(rows, snap)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return keyBefore(rows[i].ArrivedAt, rows[i].ID, rows[j].ArrivedAt, rows[j].ID) })
	return rows, nil
}

func restoreAll(rows []trailer.Snapshot, limit int) ([]*trailer.Trailer, error) {
	var out []*trailer.Trailer
	for _, snap := range rows {
		t, err := trailer.Restore(snap)
		if err != nil {
			return nil, err
		}
		if out = append(out, t); limit > 0 && len(out) == limit {
			break
		}
	}
	return out, nil
}

// ListOnSite implements ports.TrailerRepo.
func (r *Trailers) ListOnSite(_ context.Context, siteID string) ([]*trailer.Trailer, error) {
	rows, _ := r.filtered(func(s trailer.Snapshot) bool {
		return s.State != trailer.Departed && (siteID == "" || s.SiteID == siteID)
	})
	return restoreAll(rows, 0)
}

// List implements ports.TrailerRepo.
func (r *Trailers) List(_ context.Context, f views.TrailerFilter, after *views.Key, limit int) ([]*trailer.Trailer, error) {
	rows, _ := r.filtered(func(s trailer.Snapshot) bool {
		stateOK := (f.State == "" && s.State != trailer.Departed) || string(s.State) == f.State
		afterOK := after == nil || keyBefore(after.At, after.ID, s.ArrivedAt, s.ID)
		return stateOK && afterOK && (f.SiteID == "" || s.SiteID == f.SiteID) && (f.Kind == "" || string(s.Kind) == f.Kind) &&
			(f.LocationCode == "" || s.Location.Code == f.LocationCode) && (f.UnitNumber == "" || s.Number == f.UnitNumber) && (f.VisitID == "" || s.VisitID == f.VisitID)
	})
	return restoreAll(rows, limit)
}

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

// Checks implements ports.CheckRepo.
type Checks struct{ s *Store }

// Checks returns the yard check repository.
func (s *Store) Checks() *Checks { return &Checks{s} }

// Save implements ports.CheckRepo.
func (r *Checks) Save(_ context.Context, c *yardcheck.Check) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	snap := c.Snapshot()
	old, exists := r.s.st.checks[snap.ID]
	if err := guard(exists, old.Version, snap.Version); err != nil {
		return err
	}
	if snap.State == yardcheck.Open {
		for id, o := range r.s.st.checks {
			if id != snap.ID && o.State == yardcheck.Open && o.SiteID == snap.SiteID {
				return yardcheck.ErrYardCheckOpen
			}
		}
	}
	r.s.st.checks[snap.ID] = snap
	return nil
}

// FindByID implements ports.CheckRepo.
func (r *Checks) FindByID(_ context.Context, id string) (*yardcheck.Check, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	snap, ok := r.s.st.checks[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return yardcheck.Restore(snap)
}

// Policies implements ports.PolicyRepo.
type Policies struct{ s *Store }

// Policies returns the policy repository.
func (s *Store) Policies() *Policies { return &Policies{s} }

// Save implements ports.PolicyRepo: the first declaration is version 2.
func (r *Policies) Save(_ context.Context, p *yardpolicy.Policy) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	snap := p.Snapshot()
	old, exists := r.s.st.policies[snap.SiteID]
	if (!exists && snap.Version != 2) || (exists && old.Version != snap.Version-1) {
		return ports.ErrConcurrentModification
	}
	r.s.st.policies[snap.SiteID] = snap
	return nil
}

// Find implements ports.PolicyRepo.
func (r *Policies) Find(_ context.Context, siteID string) (*yardpolicy.Policy, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	snap, ok := r.s.st.policies[siteID]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return yardpolicy.Restore(snap)
}
