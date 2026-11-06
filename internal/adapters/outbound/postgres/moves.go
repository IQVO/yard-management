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
	"github.com/claudioed/yard-management/internal/domain/yardmove"
)

// Moves is the pgx-backed ports.MoveRepo.
type Moves struct{ pool *pgxpool.Pool }

// NewMoves constructs a Moves repo over pool.
func NewMoves(pool *pgxpool.Pool) *Moves { return &Moves{pool: pool} }

const moveColumns = `move_id, site_id, trailer_id, appointment_id, performer, from_kind, from_code, to_kind, to_code,
	actual_kind, actual_code, reason, priority_band, due_by, state, hostler_ref, lease_expires_at,
	requested_at, claimed_at, completed_at, version`

// Names of the unique indexes the move table keeps (migration 0001).
const (
	indexMoveTrailer     = "uq_yard_moves_one_open_per_trailer"
	indexMoveDestination = "uq_yard_moves_open_destination"
	indexMoveHostler     = "uq_yard_moves_one_claim_per_hostler"
)

// Save persists the move like Visits.Save. The three partial unique indexes
// are yardmove.ErrTrailerHasOpenMove, yardmove.ErrDestinationReserved and
// yardmove.ErrHostlerBusy.
func (r *Moves) Save(ctx context.Context, m *yardmove.Move) error {
	s := m.Snapshot()
	q := queryFor(ctx, r.pool)
	fromKind, fromCode := location(s.From)
	actualKind, actualCode := location(s.Actual)
	args := []any{
		s.ID, s.SiteID, s.TrailerID, s.AppointmentID, string(s.Performer), fromKind, fromCode, string(s.To.Kind), s.To.Code,
		actualKind, actualCode, string(s.Reason), s.Priority.Band, nullTime(s.Priority.DueBy), string(s.State), s.HostlerRef,
		nullTime(s.LeaseExpiresAt), s.RequestedAt.UTC(), nullTime(s.ClaimedAt), nullTime(s.CompletedAt), s.Version,
	}
	byConstraint := map[string]error{
		indexMoveTrailer: yardmove.ErrTrailerHasOpenMove, indexMoveDestination: yardmove.ErrDestinationReserved, indexMoveHostler: yardmove.ErrHostlerBusy,
	}
	if s.Version == 1 {
		_, err := q.Exec(ctx, `INSERT INTO yard_moves (`+moveColumns+`)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)`, args...)
		return saveError("move", err, byConstraint)
	}
	tag, err := q.Exec(ctx, `
		UPDATE yard_moves SET site_id = $2, trailer_id = $3, appointment_id = $4, performer = $5, from_kind = $6, from_code = $7,
			to_kind = $8, to_code = $9, actual_kind = $10, actual_code = $11, reason = $12, priority_band = $13, due_by = $14,
			state = $15, hostler_ref = $16, lease_expires_at = $17, requested_at = $18, claimed_at = $19, completed_at = $20,
			version = $21, updated_at = now()
		WHERE move_id = $1 AND version = $22`, append(args, s.Version-1)...)
	if err != nil {
		return saveError("move", err, byConstraint)
	}
	if tag.RowsAffected() == 0 {
		return ports.ErrConcurrentModification
	}
	return nil
}

// FindByID implements ports.MoveRepo.
func (r *Moves) FindByID(ctx context.Context, id string) (*yardmove.Move, error) {
	return one(scanMove(queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+moveColumns+` FROM yard_moves WHERE move_id = $1`, id)))
}

// FindOpenByTrailer implements ports.MoveRepo (the unique index guarantees one).
func (r *Moves) FindOpenByTrailer(ctx context.Context, trailerID string) (*yardmove.Move, error) {
	return one(scanMove(queryFor(ctx, r.pool).QueryRow(ctx, `
		SELECT `+moveColumns+` FROM yard_moves WHERE trailer_id = $1 AND state IN ('REQUESTED', 'CLAIMED')`, trailerID)))
}

// FindOpenByDestination implements ports.MoveRepo (the unique index guarantees one).
func (r *Moves) FindOpenByDestination(ctx context.Context, loc shared.Location) (*yardmove.Move, error) {
	return one(scanMove(queryFor(ctx, r.pool).QueryRow(ctx, `
		SELECT `+moveColumns+` FROM yard_moves
		WHERE to_kind = $1 AND to_code = $2 AND state IN ('REQUESTED', 'CLAIMED')`, string(loc.Kind), loc.Code)))
}

// FindClaimedByHostler implements ports.MoveRepo.
func (r *Moves) FindClaimedByHostler(ctx context.Context, siteID, hostlerRef string) (*yardmove.Move, error) {
	return one(scanMove(queryFor(ctx, r.pool).QueryRow(ctx, `
		SELECT `+moveColumns+` FROM yard_moves
		WHERE site_id = $1 AND hostler_ref = $2 AND hostler_ref <> '' AND state = 'CLAIMED'`, siteID, hostlerRef)))
}

// ClaimNext implements ports.MoveRepo: the REQUESTED hostler move that comes
// first by band, due-by (none last) and request time, row-locked so two
// concurrent claims never take the same move (SKIP LOCKED).
func (r *Moves) ClaimNext(ctx context.Context, siteID string) (*yardmove.Move, error) {
	return one(scanMove(queryFor(ctx, r.pool).QueryRow(ctx, `
		SELECT `+moveColumns+` FROM yard_moves
		WHERE site_id = $1 AND state = 'REQUESTED' AND performer = 'HOSTLER'
		ORDER BY priority_band, due_by NULLS LAST, requested_at, move_id
		LIMIT 1 FOR UPDATE SKIP LOCKED`, siteID)))
}

// ListExpiredClaims implements ports.MoveRepo.
func (r *Moves) ListExpiredClaims(ctx context.Context, now time.Time, limit int) ([]*yardmove.Move, error) {
	return r.list(ctx, `SELECT `+moveColumns+` FROM yard_moves
		WHERE state = 'CLAIMED' AND performer = 'HOSTLER' AND lease_expires_at <= $1
		ORDER BY requested_at, move_id LIMIT $2`, []any{now.UTC(), limit})
}

// ListOpenByAppointment implements ports.MoveRepo.
func (r *Moves) ListOpenByAppointment(ctx context.Context, appointmentID string) ([]*yardmove.Move, error) {
	return r.list(ctx, `SELECT `+moveColumns+` FROM yard_moves
		WHERE appointment_id = $1 AND appointment_id <> '' AND state IN ('REQUESTED', 'CLAIMED')
		ORDER BY requested_at, move_id`, []any{appointmentID})
}

// ListOpen implements ports.MoveRepo.
func (r *Moves) ListOpen(ctx context.Context, siteID string) ([]*yardmove.Move, error) {
	return r.list(ctx, `SELECT `+moveColumns+` FROM yard_moves
		WHERE site_id = $1 AND state IN ('REQUESTED', 'CLAIMED')
		ORDER BY requested_at, move_id`, []any{siteID})
}

// List implements ports.MoveRepo: oldest first by (request time, id),
// strictly after the keyset position.
func (r *Moves) List(ctx context.Context, f views.MoveFilter, after *views.Key, limit int) ([]*yardmove.Move, error) {
	var w whereBuilder
	if f.SiteID != "" {
		w.add("site_id = ?", f.SiteID)
	}
	if f.State != "" {
		w.add("state = ?", f.State)
	}
	if f.TrailerID != "" {
		w.add("trailer_id = ?", f.TrailerID)
	}
	if f.HostlerRef != "" {
		w.add("hostler_ref = ?", f.HostlerRef)
	}
	if after != nil {
		w.args = append(w.args, after.At.UTC(), after.ID)
		w.raw(fmt.Sprintf("(requested_at, move_id) > ($%d, $%d)", len(w.args)-1, len(w.args)))
	}
	query := `SELECT ` + moveColumns + ` FROM yard_moves` + w.clause() + ` ORDER BY requested_at, move_id LIMIT ` + w.limit(limit)
	return r.list(ctx, query, w.args)
}

func (r *Moves) list(ctx context.Context, query string, args []any) ([]*yardmove.Move, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list moves: %w", err)
	}
	defer rows.Close()
	out := []*yardmove.Move{}
	for rows.Next() {
		m, err := scanMove(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// scanMove hydrates one row through the domain's Snapshot/Restore pair.
func scanMove(row pgx.Row) (*yardmove.Move, error) {
	var (
		s                                       yardmove.Snapshot
		performer, fromKind, toKind, actualKind string
		reason, state                           string
		due, lease, claimed, completed          *time.Time
	)
	if err := row.Scan(&s.ID, &s.SiteID, &s.TrailerID, &s.AppointmentID, &performer, &fromKind, &s.From.Code, &toKind, &s.To.Code,
		&actualKind, &s.Actual.Code, &reason, &s.Priority.Band, &due, &state, &s.HostlerRef, &lease,
		&s.RequestedAt, &claimed, &completed, &s.Version); err != nil {
		return nil, err
	}
	s.Performer, s.Reason, s.State = yardmove.Performer(performer), yardmove.Reason(reason), yardmove.State(state)
	s.From.Kind, s.To.Kind, s.Actual.Kind = shared.LocationKind(fromKind), shared.LocationKind(toKind), shared.LocationKind(actualKind)
	s.Priority.DueBy, s.LeaseExpiresAt = timeOrZero(due), timeOrZero(lease)
	s.RequestedAt, s.ClaimedAt, s.CompletedAt = s.RequestedAt.UTC(), timeOrZero(claimed), timeOrZero(completed)
	m, err := yardmove.Restore(s)
	if err != nil {
		return nil, fmt.Errorf("stored move %s is invalid: %w", s.ID, err)
	}
	return m, nil
}
