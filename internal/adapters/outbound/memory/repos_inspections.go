package memory

import (
	"context"
	"sort"
	"strconv"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/inspection"
	"github.com/claudioed/yard-management/internal/domain/shared"
)

// Inspections implements ports.InspectionRepo.
type Inspections struct{ s *Store }

// Inspections returns the inspection repository.
func (s *Store) Inspections() *Inspections { return &Inspections{s} }

// Save implements ports.InspectionRepo: version 1 inserts, a later version
// updates; a second OPEN inspection of the same trailer, template and gate is
// inspection.ErrInspectionAlreadyOpen (the partial unique index of Postgres).
func (r *Inspections) Save(_ context.Context, i *inspection.Inspection) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	snap := i.Snapshot()
	old, exists := r.s.st.inspections[snap.ID]
	if err := guard(exists, old.Version, snap.Version); err != nil {
		return err
	}
	if snap.Status == inspection.StatusOpen {
		for id, o := range r.s.st.inspections {
			if id != snap.ID && o.Status == inspection.StatusOpen && o.TrailerID == snap.TrailerID && o.TemplateID == snap.TemplateID && o.Gate == snap.Gate {
				return inspection.ErrInspectionAlreadyOpen
			}
		}
	}
	r.s.st.inspections[snap.ID] = snap
	return nil
}

// FindByID implements ports.InspectionRepo.
func (r *Inspections) FindByID(_ context.Context, id string) (*inspection.Inspection, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	snap, ok := r.s.st.inspections[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return inspection.Restore(snap)
}

// FindOpen implements ports.InspectionRepo.
func (r *Inspections) FindOpen(_ context.Context, trailerID, templateID string, gate shared.Gate) (*inspection.Inspection, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for _, snap := range r.s.st.inspections {
		if snap.Status == inspection.StatusOpen && snap.TrailerID == trailerID && snap.TemplateID == templateID && snap.Gate == gate {
			return inspection.Restore(snap)
		}
	}
	return nil, ports.ErrNotFound
}

// ListByTrailer implements ports.InspectionRepo (newest first by opened at, id).
func (r *Inspections) ListByTrailer(_ context.Context, trailerID string, after *views.Key, limit int) ([]*inspection.Inspection, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var rows []inspection.Snapshot
	for _, snap := range r.s.st.inspections {
		if snap.TrailerID != trailerID {
			continue
		}
		if after != nil && (snap.OpenedAt.After(after.At) || (snap.OpenedAt.Equal(after.At) && snap.ID >= after.ID)) {
			continue
		}
		rows = append(rows, snap)
	}
	sort.Slice(rows, func(a, b int) bool {
		if !rows[a].OpenedAt.Equal(rows[b].OpenedAt) {
			return rows[a].OpenedAt.After(rows[b].OpenedAt)
		}
		return rows[a].ID > rows[b].ID
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	out := make([]*inspection.Inspection, 0, len(rows))
	for _, snap := range rows {
		i, err := inspection.Restore(snap)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, nil
}

// Completed implements ports.InspectionRepo (in the order they completed).
func (r *Inspections) Completed(_ context.Context, trailerID string) ([]inspection.Record, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var rows []inspection.Snapshot
	for _, snap := range r.s.st.inspections {
		if snap.TrailerID == trailerID && snap.Status != inspection.StatusOpen {
			rows = append(rows, snap)
		}
	}
	sort.Slice(rows, func(a, b int) bool {
		if !rows[a].CompletedAt.Equal(rows[b].CompletedAt) {
			return rows[a].CompletedAt.Before(rows[b].CompletedAt)
		}
		return rows[a].ID < rows[b].ID
	})
	out := make([]inspection.Record, 0, len(rows))
	for _, snap := range rows {
		out = append(out, inspection.Record{InspectionID: snap.ID, TemplateID: snap.TemplateID, Gate: snap.Gate, Status: snap.Status})
	}
	return out, nil
}

// Templates implements ports.TemplateRepo.
type Templates struct{ s *Store }

// Templates returns the custom template repository.
func (s *Store) Templates() *Templates { return &Templates{s} }

func templateKey(id string, version int) string { return id + "#" + strconv.Itoa(version) }

// Save implements ports.TemplateRepo: a taken (id, version) is ErrConcurrentModification.
func (r *Templates) Save(_ context.Context, t *inspection.Template) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	key := templateKey(t.ID(), t.Version())
	if _, taken := r.s.st.templates[key]; taken {
		return ports.ErrConcurrentModification
	}
	r.s.st.templates[key] = t.Snapshot()
	return nil
}

// Find implements ports.TemplateRepo.
func (r *Templates) Find(_ context.Context, id string, version int) (*inspection.Template, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	snap, ok := r.s.st.templates[templateKey(id, version)]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return inspection.RestoreTemplate(snap)
}

// Latest implements ports.TemplateRepo.
func (r *Templates) Latest(_ context.Context, id string) (*inspection.Template, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	best, found := inspection.TemplateSnapshot{}, false
	for _, snap := range r.s.st.templates {
		if snap.ID == id && (!found || snap.Version > best.Version) {
			best, found = snap, true
		}
	}
	if !found {
		return nil, ports.ErrNotFound
	}
	return inspection.RestoreTemplate(best)
}

// ListLatest implements ports.TemplateRepo.
func (r *Templates) ListLatest(_ context.Context, afterID string, limit int) ([]*inspection.Template, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	latest := map[string]inspection.TemplateSnapshot{}
	for _, snap := range r.s.st.templates {
		if cur, ok := latest[snap.ID]; snap.ID > afterID && (!ok || snap.Version > cur.Version) {
			latest[snap.ID] = snap
		}
	}
	ids := make([]string, 0, len(latest))
	for id := range latest {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	out := make([]*inspection.Template, 0, len(ids))
	for _, id := range ids {
		t, err := inspection.RestoreTemplate(latest[id])
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}
