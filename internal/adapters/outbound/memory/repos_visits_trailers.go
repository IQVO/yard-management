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
