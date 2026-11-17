package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/inspection"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
)

// itemJSON is one item of an inspection or template as stored in the JSONB
// column: the snapshot of the template item plus, for an inspection, the result.
type itemJSON struct {
	Code               string   `json:"code"`
	Text               string   `json:"text"`
	Required           bool     `json:"required"`
	AllowNotApplicable bool     `json:"allow_not_applicable"`
	HoldReason         string   `json:"hold_reason"`
	Result             string   `json:"result,omitempty"`
	Note               string   `json:"note,omitempty"`
	EvidenceRefs       []string `json:"evidence_refs,omitempty"`
}

func encodeItems(items []inspection.Item) ([]byte, error) {
	out := make([]itemJSON, 0, len(items))
	for _, it := range items {
		out = append(out, itemJSON{
			Code: it.Code, Text: it.Text, Required: it.Required, AllowNotApplicable: it.AllowNotApplicable, HoldReason: string(it.HoldReason),
			Result: string(it.Result), Note: it.Note, EvidenceRefs: it.EvidenceRefs,
		})
	}
	return json.Marshal(out)
}

func decodeItems(raw []byte) ([]inspection.Item, error) {
	var in []itemJSON
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	out := make([]inspection.Item, 0, len(in))
	for _, it := range in {
		out = append(out, inspection.Item{
			TemplateItem: inspection.TemplateItem{Code: it.Code, Text: it.Text, Required: it.Required, AllowNotApplicable: it.AllowNotApplicable, HoldReason: trailer.HoldReason(it.HoldReason)},
			Result:       inspection.Result(it.Result), Note: it.Note, EvidenceRefs: strs(it.EvidenceRefs),
		})
	}
	return out, nil
}

// Inspections is the pgx-backed ports.InspectionRepo.
type Inspections struct{ pool *pgxpool.Pool }

// NewInspections constructs an Inspections repo over pool.
func NewInspections(pool *pgxpool.Pool) *Inspections { return &Inspections{pool: pool} }

const inspectionColumns = `inspection_id, site_id, trailer_id, visit_id, template_id, template_version, gate, status, items,
	performer_ref, condition_grade, opened_at, completed_at, version`

// indexInspectionOpen is the partial unique index of one OPEN inspection per
// trailer, template and gate.
const indexInspectionOpen = "uq_inspections_one_open"

// Save persists the inspection like Visits.Save; a second OPEN inspection of
// the same trailer, template and gate is inspection.ErrInspectionAlreadyOpen.
func (r *Inspections) Save(ctx context.Context, i *inspection.Inspection) error {
	s := i.Snapshot()
	items, err := encodeItems(s.Items)
	if err != nil {
		return fmt.Errorf("encode inspection items: %w", err)
	}
	args := []any{
		s.ID, s.SiteID, s.TrailerID, s.VisitID, s.TemplateID, s.TemplateVersion, string(s.Gate), string(s.Status), items,
		s.PerformerRef, string(s.Grade), s.OpenedAt.UTC(), nullTime(s.CompletedAt), s.Version,
	}
	byConstraint := map[string]error{indexInspectionOpen: inspection.ErrInspectionAlreadyOpen}
	q := queryFor(ctx, r.pool)
	if s.Version == 1 {
		_, err := q.Exec(ctx, `INSERT INTO inspections (`+inspectionColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`, args...)
		return saveError("inspection", err, byConstraint)
	}
	tag, err := q.Exec(ctx, `
		UPDATE inspections SET site_id = $2, trailer_id = $3, visit_id = $4, template_id = $5, template_version = $6, gate = $7,
			status = $8, items = $9, performer_ref = $10, condition_grade = $11, opened_at = $12, completed_at = $13,
			version = $14, updated_at = now()
		WHERE inspection_id = $1 AND version = $15`, append(args, s.Version-1)...)
	if err != nil {
		return saveError("inspection", err, byConstraint)
	}
	if tag.RowsAffected() == 0 {
		return ports.ErrConcurrentModification
	}
	return nil
}

// FindByID implements ports.InspectionRepo.
func (r *Inspections) FindByID(ctx context.Context, id string) (*inspection.Inspection, error) {
	return one(scanInspection(queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+inspectionColumns+` FROM inspections WHERE inspection_id = $1`, id)))
}

// FindOpen implements ports.InspectionRepo.
func (r *Inspections) FindOpen(ctx context.Context, trailerID, templateID string, gate shared.Gate) (*inspection.Inspection, error) {
	return one(scanInspection(queryFor(ctx, r.pool).QueryRow(ctx, `
		SELECT `+inspectionColumns+` FROM inspections
		WHERE trailer_id = $1 AND template_id = $2 AND gate = $3 AND status = 'OPEN'`, trailerID, templateID, string(gate))))
}

// ListByTrailer implements ports.InspectionRepo: newest first by (opened at,
// id), strictly after the keyset position.
func (r *Inspections) ListByTrailer(ctx context.Context, trailerID string, after *views.Key, limit int) ([]*inspection.Inspection, error) {
	var w whereBuilder
	w.add("trailer_id = ?", trailerID)
	if after != nil {
		w.args = append(w.args, after.At.UTC(), after.ID)
		n := len(w.args)
		w.raw(fmt.Sprintf("(opened_at, inspection_id) < ($%d, $%d)", n-1, n))
	}
	lim := w.limit(limit)
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT `+inspectionColumns+` FROM inspections`+w.clause()+` ORDER BY opened_at DESC, inspection_id DESC LIMIT `+lim, w.args...)
	if err != nil {
		return nil, fmt.Errorf("list inspections: %w", err)
	}
	defer rows.Close()
	var out []*inspection.Inspection
	for rows.Next() {
		i, err := scanInspection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Completed implements ports.InspectionRepo: the PASSED and FAILED inspections
// of the trailer in the order they completed.
func (r *Inspections) Completed(ctx context.Context, trailerID string) ([]inspection.Record, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
		SELECT inspection_id, template_id, gate, status FROM inspections
		WHERE trailer_id = $1 AND status <> 'OPEN' ORDER BY completed_at, inspection_id`, trailerID)
	if err != nil {
		return nil, fmt.Errorf("list completed inspections: %w", err)
	}
	defer rows.Close()
	var out []inspection.Record
	for rows.Next() {
		var rec inspection.Record
		var gate, status string
		if err := rows.Scan(&rec.InspectionID, &rec.TemplateID, &gate, &status); err != nil {
			return nil, err
		}
		rec.Gate, rec.Status = shared.Gate(gate), inspection.Status(status)
		out = append(out, rec)
	}
	return out, rows.Err()
}

func scanInspection(row pgx.Row) (*inspection.Inspection, error) {
	var (
		s                   inspection.Snapshot
		gate, status, grade string
		itemsRaw            []byte
		completedAt         *time.Time
	)
	if err := row.Scan(&s.ID, &s.SiteID, &s.TrailerID, &s.VisitID, &s.TemplateID, &s.TemplateVersion, &gate, &status, &itemsRaw,
		&s.PerformerRef, &grade, &s.OpenedAt, &completedAt, &s.Version); err != nil {
		return nil, err
	}
	items, err := decodeItems(itemsRaw)
	if err != nil {
		return nil, fmt.Errorf("stored inspection %s has bad items: %w", s.ID, err)
	}
	s.Gate, s.Status, s.Grade, s.Items = shared.Gate(gate), inspection.Status(status), inspection.ConditionGrade(grade), items
	s.OpenedAt, s.CompletedAt = s.OpenedAt.UTC(), timeOrZero(completedAt)
	i, err := inspection.Restore(s)
	if err != nil {
		return nil, fmt.Errorf("stored inspection %s is invalid: %w", s.ID, err)
	}
	return i, nil
}

// Templates is the pgx-backed ports.TemplateRepo.
type Templates struct{ pool *pgxpool.Pool }

// NewTemplates constructs a Templates repo over pool.
func NewTemplates(pool *pgxpool.Pool) *Templates { return &Templates{pool: pool} }

const templateColumns = `template_id, version, title, items, published_at`

// Save implements ports.TemplateRepo: it only INSERTs, and a taken
// (template, version) is ports.ErrConcurrentModification.
func (r *Templates) Save(ctx context.Context, t *inspection.Template) error {
	s := t.Snapshot()
	items := make([]inspection.Item, 0, len(s.Items))
	for _, it := range s.Items {
		items = append(items, inspection.Item{TemplateItem: it})
	}
	raw, err := encodeItems(items)
	if err != nil {
		return fmt.Errorf("encode template items: %w", err)
	}
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `INSERT INTO inspection_templates (`+templateColumns+`) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (template_id, version) DO NOTHING`, s.ID, s.Version, s.Title, raw, s.PublishedAt.UTC())
	if err != nil {
		return fmt.Errorf("save inspection template: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ports.ErrConcurrentModification
	}
	return nil
}

// Find implements ports.TemplateRepo.
func (r *Templates) Find(ctx context.Context, id string, version int) (*inspection.Template, error) {
	return one(scanTemplate(queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+templateColumns+` FROM inspection_templates WHERE template_id = $1 AND version = $2`, id, version)))
}

// Latest implements ports.TemplateRepo.
func (r *Templates) Latest(ctx context.Context, id string) (*inspection.Template, error) {
	return one(scanTemplate(queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+templateColumns+` FROM inspection_templates WHERE template_id = $1 ORDER BY version DESC LIMIT 1`, id)))
}

// ListLatest implements ports.TemplateRepo.
func (r *Templates) ListLatest(ctx context.Context, afterID string, limit int) ([]*inspection.Template, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
		SELECT DISTINCT ON (template_id) `+templateColumns+` FROM inspection_templates
		WHERE template_id > $1 ORDER BY template_id, version DESC LIMIT $2`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list inspection templates: %w", err)
	}
	defer rows.Close()
	var out []*inspection.Template
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func scanTemplate(row pgx.Row) (*inspection.Template, error) {
	var (
		s        inspection.TemplateSnapshot
		itemsRaw []byte
	)
	if err := row.Scan(&s.ID, &s.Version, &s.Title, &itemsRaw, &s.PublishedAt); err != nil {
		return nil, err
	}
	items, err := decodeItems(itemsRaw)
	if err != nil {
		return nil, fmt.Errorf("stored template %s has bad items: %w", s.ID, err)
	}
	for _, it := range items {
		s.Items = append(s.Items, it.TemplateItem)
	}
	s.PublishedAt = s.PublishedAt.UTC()
	t, err := inspection.RestoreTemplate(s)
	if err != nil {
		return nil, fmt.Errorf("stored template %s v%d is invalid: %w", s.ID, s.Version, err)
	}
	return t, nil
}
