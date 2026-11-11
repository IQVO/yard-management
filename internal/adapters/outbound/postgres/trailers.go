package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
)

// Trailers is the pgx-backed ports.TrailerRepo.
type Trailers struct{ pool *pgxpool.Pool }

// NewTrailers constructs a Trailers repo over pool.
func NewTrailers(pool *pgxpool.Pool) *Trailers { return &Trailers{pool: pool} }

const trailerColumns = `trailer_id, site_id, scac, unit_number, kind, load_state, asn_numbers, outbound_ref, purpose,
	seal_number, seal_state, hold, state, location_kind, location_code, door_work, origin_kind, origin_code, origin_work,
	visit_id, appointment_id, arrived_at, departed_at, dwell_alerted_at, version, seal_class, unit_number_scheme`

// Names of the unique indexes the trailer table keeps (migration 0001).
const (
	indexTrailerLocation = "uq_trailers_one_per_location"
	indexTrailerUnit     = "uq_trailers_unit_on_site"
)

// Save persists the trailer like Visits.Save. The partial unique index on the
// location of the PARKED and DOCKED rows is trailer.ErrLocationOccupied; the
// one on the unit of a trailer on site is trailer.ErrTrailerAlreadyOnSite.
func (r *Trailers) Save(ctx context.Context, t *trailer.Trailer) error {
	s := t.Snapshot()
	q := queryFor(ctx, r.pool)
	locKind, locCode := location(s.Location)
	originKind, originCode := location(s.Origin)
	args := []any{
		s.ID, s.SiteID, s.SCAC, s.Number, string(s.Kind), string(s.LoadState), strs(s.ASNNumbers), s.OutboundRef, string(s.Purpose),
		s.SealNumber, string(s.SealState), string(s.Hold), string(s.State), locKind, locCode, string(s.DoorWork),
		originKind, originCode, string(s.OriginWork), s.VisitID, s.AppointmentID,
		s.ArrivedAt.UTC(), nullTime(s.DepartedAt), nullTime(s.DwellAlertedAt), s.Version,
		string(s.SealClass), string(s.UnitScheme),
	}
	byConstraint := map[string]error{indexTrailerLocation: trailer.ErrLocationOccupied, indexTrailerUnit: trailer.ErrTrailerAlreadyOnSite}
	if s.Version == 1 {
		_, err := q.Exec(ctx, `INSERT INTO trailers (`+trailerColumns+`)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27)`, args...)
		return saveError("trailer", err, byConstraint)
	}
	tag, err := q.Exec(ctx, `
		UPDATE trailers SET site_id = $2, scac = $3, unit_number = $4, kind = $5, load_state = $6, asn_numbers = $7,
			outbound_ref = $8, purpose = $9, seal_number = $10, seal_state = $11, hold = $12, state = $13,
			location_kind = $14, location_code = $15, door_work = $16, origin_kind = $17, origin_code = $18,
			origin_work = $19, visit_id = $20, appointment_id = $21, arrived_at = $22, departed_at = $23,
			dwell_alerted_at = $24, version = $25, seal_class = $26, unit_number_scheme = $27, updated_at = now()
		WHERE trailer_id = $1 AND version = $28`, append(args, s.Version-1)...)
	if err != nil {
		return saveError("trailer", err, byConstraint)
	}
	if tag.RowsAffected() == 0 {
		return ports.ErrConcurrentModification
	}
	return nil
}

// FindByID implements ports.TrailerRepo.
func (r *Trailers) FindByID(ctx context.Context, id string) (*trailer.Trailer, error) {
	return one(scanTrailer(queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+trailerColumns+` FROM trailers WHERE trailer_id = $1`, id)))
}

// FindAt implements ports.TrailerRepo: the PARKED or DOCKED trailer at the
// location (the partial unique index guarantees at most one).
func (r *Trailers) FindAt(ctx context.Context, loc shared.Location) (*trailer.Trailer, error) {
	return one(scanTrailer(queryFor(ctx, r.pool).QueryRow(ctx, `
		SELECT `+trailerColumns+` FROM trailers
		WHERE location_kind = $1 AND location_code = $2 AND state IN ('PARKED', 'DOCKED')`, string(loc.Kind), loc.Code)))
}

// FindOnSiteByUnit implements ports.TrailerRepo.
func (r *Trailers) FindOnSiteByUnit(ctx context.Context, siteID, scac, number string) (*trailer.Trailer, error) {
	return one(scanTrailer(queryFor(ctx, r.pool).QueryRow(ctx, `
		SELECT `+trailerColumns+` FROM trailers
		WHERE site_id = $1 AND scac = $2 AND unit_number = $3 AND state <> 'DEPARTED'`, siteID, scac, number)))
}

// FindByAppointment implements ports.TrailerRepo.
func (r *Trailers) FindByAppointment(ctx context.Context, appointmentID string) (*trailer.Trailer, error) {
	return one(scanTrailer(queryFor(ctx, r.pool).QueryRow(ctx, `
		SELECT `+trailerColumns+` FROM trailers
		WHERE appointment_id = $1 AND appointment_id <> '' AND state <> 'DEPARTED'
		ORDER BY arrived_at, trailer_id LIMIT 1`, appointmentID)))
}

// ListOnSite implements ports.TrailerRepo.
func (r *Trailers) ListOnSite(ctx context.Context, siteID string) ([]*trailer.Trailer, error) {
	var w whereBuilder
	w.raw("state <> 'DEPARTED'")
	if siteID != "" {
		w.add("site_id = ?", siteID)
	}
	return r.list(ctx, `SELECT `+trailerColumns+` FROM trailers`+w.clause()+` ORDER BY arrived_at, trailer_id`, w.args)
}

// List implements ports.TrailerRepo: oldest arrival first by (arrival, id),
// strictly after the keyset position; an empty state means "not DEPARTED".
func (r *Trailers) List(ctx context.Context, f views.TrailerFilter, after *views.Key, limit int) ([]*trailer.Trailer, error) {
	var w whereBuilder
	if f.State == "" {
		w.raw("state <> 'DEPARTED'")
	} else {
		w.add("state = ?", f.State)
	}
	if f.SiteID != "" {
		w.add("site_id = ?", f.SiteID)
	}
	if f.Kind != "" {
		w.add("kind = ?", f.Kind)
	}
	if f.LocationCode != "" {
		w.add("location_code = ?", f.LocationCode)
	}
	if f.UnitNumber != "" {
		w.add("unit_number = ?", f.UnitNumber)
	}
	if f.VisitID != "" {
		w.add("visit_id = ?", f.VisitID)
	}
	if after != nil {
		w.args = append(w.args, after.At.UTC(), after.ID)
		w.raw(fmt.Sprintf("(arrived_at, trailer_id) > ($%d, $%d)", len(w.args)-1, len(w.args)))
	}
	query := `SELECT ` + trailerColumns + ` FROM trailers` + w.clause() + ` ORDER BY arrived_at, trailer_id LIMIT ` + w.limit(limit)
	return r.list(ctx, query, w.args)
}

func (r *Trailers) list(ctx context.Context, query string, args []any) ([]*trailer.Trailer, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list trailers: %w", err)
	}
	defer rows.Close()
	out := []*trailer.Trailer{}
	for rows.Next() {
		t, err := scanTrailer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// scanTrailer hydrates one row through the domain's Snapshot/Restore pair.
func scanTrailer(row pgx.Row) (*trailer.Trailer, error) {
	var (
		s                                                                                    trailer.Snapshot
		kind, load, purpose, sealState, hold, state, door, originWork, sealClass, unitScheme string
		locKind, originKind                                                                  string
		departed, alerted                                                                    *time.Time
	)
	if err := row.Scan(&s.ID, &s.SiteID, &s.SCAC, &s.Number, &kind, &load, &s.ASNNumbers, &s.OutboundRef, &purpose,
		&s.SealNumber, &sealState, &hold, &state, &locKind, &s.Location.Code, &door, &originKind, &s.Origin.Code, &originWork,
		&s.VisitID, &s.AppointmentID, &s.ArrivedAt, &departed, &alerted, &s.Version, &sealClass, &unitScheme); err != nil {
		return nil, err
	}
	s.Kind, s.LoadState, s.Purpose = shared.TrailerKind(kind), shared.LoadState(load), shared.Purpose(purpose)
	s.SealState, s.Hold, s.State, s.DoorWork, s.OriginWork = trailer.SealState(sealState), trailer.HoldReason(hold), trailer.State(state), trailer.DoorWork(door), trailer.DoorWork(originWork)
	s.SealClass, s.UnitScheme = shared.SealClass(sealClass), shared.UnitScheme(unitScheme)
	s.Location.Kind, s.Origin.Kind = shared.LocationKind(locKind), shared.LocationKind(originKind)
	s.ArrivedAt, s.DepartedAt, s.DwellAlertedAt = s.ArrivedAt.UTC(), timeOrZero(departed), timeOrZero(alerted)
	t, err := trailer.Restore(s)
	if err != nil {
		return nil, fmt.Errorf("stored trailer %s is invalid: %w", s.ID, err)
	}
	return t, nil
}
