package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

// Visits is the pgx-backed ports.VisitRepo.
type Visits struct{ pool *pgxpool.Pool }

// NewVisits constructs a Visits repo over pool.
func NewVisits(pool *pgxpool.Pool) *Visits { return &Visits{pool: pool} }

const visitColumns = `visit_id, site_id, carrier, carrier_scac, tractor_plate, driver_ref, purpose, appointment_id,
	state, rejection_reason, trailers_in, trailers_out, arrived_at, departed_at, version`

// Save persists the visit: version 1 INSERTs (a taken id is
// ports.ErrConcurrentModification), a later version UPDATEs the row stored at
// version - 1 (no such row is ports.ErrConcurrentModification). The partial
// unique index of one CHECKED_IN visit per plate and site is
// yardvisit.ErrVisitAlreadyOpen.
func (r *Visits) Save(ctx context.Context, v *yardvisit.Visit) error {
	s := v.Snapshot()
	q := queryFor(ctx, r.pool)
	args := []any{
		s.ID, s.SiteID, s.Carrier, s.SCAC, s.Plate, s.DriverRef, string(s.Purpose), s.AppointmentID,
		string(s.State), string(s.Reason), strs(s.TrailersIn), strs(s.TrailersOut), s.ArrivedAt.UTC(), nullTime(s.DepartedAt), s.Version,
	}
	byConstraint := map[string]error{"uq_yard_visits_one_open_per_plate": yardvisit.ErrVisitAlreadyOpen}
	if s.Version == 1 {
		_, err := q.Exec(ctx, `INSERT INTO yard_visits (`+visitColumns+`)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`, args...)
		return saveError("visit", err, byConstraint)
	}
	tag, err := q.Exec(ctx, `
		UPDATE yard_visits SET site_id = $2, carrier = $3, carrier_scac = $4, tractor_plate = $5, driver_ref = $6,
			purpose = $7, appointment_id = $8, state = $9, rejection_reason = $10, trailers_in = $11, trailers_out = $12,
			arrived_at = $13, departed_at = $14, version = $15, updated_at = now()
		WHERE visit_id = $1 AND version = $16`, append(args, s.Version-1)...)
	if err != nil {
		return saveError("visit", err, byConstraint)
	}
	if tag.RowsAffected() == 0 {
		return ports.ErrConcurrentModification
	}
	return nil
}

// FindByID implements ports.VisitRepo.
func (r *Visits) FindByID(ctx context.Context, id string) (*yardvisit.Visit, error) {
	return one(scanVisit(queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+visitColumns+` FROM yard_visits WHERE visit_id = $1`, id)))
}

// FindOpenByPlate implements ports.VisitRepo: the partial unique index
// guarantees at most one row.
func (r *Visits) FindOpenByPlate(ctx context.Context, siteID, plate string) (*yardvisit.Visit, error) {
	return one(scanVisit(queryFor(ctx, r.pool).QueryRow(ctx, `
		SELECT `+visitColumns+` FROM yard_visits
		WHERE site_id = $1 AND tractor_plate = $2 AND state = 'CHECKED_IN'`, siteID, plate)))
}

// List implements ports.VisitRepo: newest first by (arrival, id), strictly
// after the keyset position.
func (r *Visits) List(ctx context.Context, f views.VisitFilter, after *views.Key, limit int) ([]*yardvisit.Visit, error) {
	var w whereBuilder
	if f.SiteID != "" {
		w.add("site_id = ?", f.SiteID)
	}
	if f.State != "" {
		w.add("state = ?", f.State)
	}
	if f.TractorPlate != "" {
		w.add("tractor_plate = ?", f.TractorPlate)
	}
	if after != nil {
		w.args = append(w.args, after.At.UTC(), after.ID)
		w.raw(fmt.Sprintf("(arrived_at, visit_id) < ($%d, $%d)", len(w.args)-1, len(w.args)))
	}
	query := `SELECT ` + visitColumns + ` FROM yard_visits` + w.clause() + ` ORDER BY arrived_at DESC, visit_id DESC LIMIT ` + w.limit(limit)
	rows, err := queryFor(ctx, r.pool).Query(ctx, query, w.args...)
	if err != nil {
		return nil, fmt.Errorf("list visits: %w", err)
	}
	defer rows.Close()
	out := []*yardvisit.Visit{}
	for rows.Next() {
		v, err := scanVisit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// one maps a scan of a single row: no row is ports.ErrNotFound.
func one[T any](v *T, err error) (*T, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ports.ErrNotFound
	}
	return v, err
}

// scanVisit hydrates one row through the domain's Snapshot/Restore pair; a
// corrupt row surfaces as an error, never as a live aggregate.
func scanVisit(row pgx.Row) (*yardvisit.Visit, error) {
	var (
		s                    yardvisit.Snapshot
		purpose, state, rsn  string
		departed             *time.Time
		trailersIn, trailers []string
	)
	if err := row.Scan(&s.ID, &s.SiteID, &s.Carrier, &s.SCAC, &s.Plate, &s.DriverRef, &purpose, &s.AppointmentID,
		&state, &rsn, &trailersIn, &trailers, &s.ArrivedAt, &departed, &s.Version); err != nil {
		return nil, err
	}
	s.Purpose, s.State, s.Reason = shared.Purpose(purpose), yardvisit.State(state), yardvisit.RejectionReason(rsn)
	s.TrailersIn, s.TrailersOut, s.ArrivedAt, s.DepartedAt = trailersIn, trailers, s.ArrivedAt.UTC(), timeOrZero(departed)
	v, err := yardvisit.Restore(s)
	if err != nil {
		return nil, fmt.Errorf("stored visit %s is invalid: %w", s.ID, err)
	}
	return v, nil
}
