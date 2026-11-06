package analyticsstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/yard-management/internal/analytics/report"
)

// Projection is the Postgres report.Projection: the writer of the analytical
// model. Apply claims the CloudEvents id in analytics.processed_events and
// folds the event into the fact tables in ONE transaction, so a redelivery is
// skipped and a rollback un-claims.
type Projection struct {
	pool *pgxpool.Pool
}

// NewProjection constructs the writer over a WRITER pool.
func NewProjection(pool *pgxpool.Pool) *Projection { return &Projection{pool: pool} }

var _ report.Projection = (*Projection)(nil)

// Apply implements report.Projection. An event that fails report validation or
// that Postgres deterministically rejects (SQLSTATE class 22 or 23) is an
// error wrapping report.ErrRejected; anything else is transient.
func (p *Projection) Apply(ctx context.Context, e report.Event) (bool, error) {
	if err := e.Validate(); err != nil {
		return false, err
	}
	applied := false
	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO analytics.processed_events (event_id, event_type, occurred_at) VALUES ($1, $2, $3)
			ON CONFLICT (event_id) DO NOTHING`, e.EventID, string(e.Kind), e.At)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil // already applied
		}
		applied = true
		return fold(ctx, tx, e)
	})
	if err != nil {
		return false, classify(e, err)
	}
	return applied, nil
}

// classify marks a deterministic Postgres rejection as report.ErrRejected;
// anything else (a lost connection, a timeout, a cancelled context) stays
// transient.
func classify(e report.Event, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (strings.HasPrefix(pgErr.Code, "22") || strings.HasPrefix(pgErr.Code, "23")) {
		return fmt.Errorf("%w: %s %s: %v", report.ErrRejected, e.Kind, e.EventID, err)
	}
	return err
}

// fold writes the facts of one event. Pairings (dock/undock, work start/end)
// rely on the per-trailer order of the Kafka key; an end with no open start is
// a no-op.
func fold(ctx context.Context, tx pgx.Tx, e report.Event) error {
	switch e.Kind {
	case report.KindVisitCheckedIn:
		return foldArrival(ctx, tx, e)
	case report.KindTrailerDocked:
		_, err := tx.Exec(ctx, `
			INSERT INTO analytics.door_dockings (trailer_id, docked_at, site_id, door_code) VALUES ($1, $2, $3, $4)
			ON CONFLICT (trailer_id, docked_at) DO NOTHING`, e.TrailerID, e.DockedAt, e.SiteID, e.DoorCode)
		return err
	case report.KindTrailerUndocked:
		_, err := tx.Exec(ctx, `
			UPDATE analytics.door_dockings SET undocked_at = $3
			WHERE (trailer_id, docked_at) = (
				SELECT trailer_id, docked_at FROM analytics.door_dockings
				WHERE trailer_id = $1 AND door_code = $2 AND undocked_at IS NULL AND docked_at <= $3
				ORDER BY docked_at DESC LIMIT 1)`, e.TrailerID, e.DoorCode, e.UndockedAt)
		return err
	case report.KindDoorWorkStarted:
		return foldWorkStarted(ctx, tx, e)
	case report.KindDoorWorkCompleted:
		_, err := tx.Exec(ctx, `
			UPDATE analytics.door_work SET completed_at = $3
			WHERE (trailer_id, started_at) = (
				SELECT trailer_id, started_at FROM analytics.door_work
				WHERE trailer_id = $1 AND door_code = $2 AND completed_at IS NULL AND started_at <= $3
				ORDER BY started_at DESC LIMIT 1)`, e.TrailerID, e.DoorCode, e.CompletedAt)
		return err
	case report.KindTrailerDeparted:
		_, err := tx.Exec(ctx, `
			INSERT INTO analytics.trailer_facts (trailer_id, site_id, visit_id, departed_at, dwell_minutes) VALUES ($1, $2, NULLIF($3, ''), $4, $5)
			ON CONFLICT (trailer_id) DO UPDATE SET
				site_id = COALESCE(analytics.trailer_facts.site_id, EXCLUDED.site_id),
				visit_id = COALESCE(analytics.trailer_facts.visit_id, EXCLUDED.visit_id),
				departed_at = EXCLUDED.departed_at, dwell_minutes = EXCLUDED.dwell_minutes`,
			e.TrailerID, e.SiteID, e.VisitID, e.DepartedAt, e.DwellMinutes)
		return err
	case report.KindYardMoveCompleted:
		if !e.IsHostlerMove() {
			return nil // a visiting driver's move is not yard truck productivity
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO analytics.hostler_moves (move_id, site_id, trailer_id, hostler_ref, completed_at, duration_seconds)
			VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (move_id) DO NOTHING`,
			e.MoveID, e.SiteID, e.TrailerID, e.HostlerRef, e.CompletedAt, e.DurationSeconds)
		return err
	}
	return fmt.Errorf("%w: %s %s: no fold for the kind", report.ErrRejected, e.Kind, e.EventID)
}

// foldArrival upserts the trailers a visit brought: the kind and the dwell
// clock start. The earliest arrival wins (LEAST ignores NULLs), so arrival
// order never matters.
func foldArrival(ctx context.Context, tx pgx.Tx, e report.Event) error {
	for _, a := range e.Arrivals {
		if _, err := tx.Exec(ctx, `
			INSERT INTO analytics.trailer_facts (trailer_id, site_id, kind, unit_number, visit_id, arrived_at) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (trailer_id) DO UPDATE SET
				site_id = COALESCE(analytics.trailer_facts.site_id, EXCLUDED.site_id),
				kind = EXCLUDED.kind, unit_number = EXCLUDED.unit_number, visit_id = EXCLUDED.visit_id,
				arrived_at = LEAST(analytics.trailer_facts.arrived_at, EXCLUDED.arrived_at)`,
			a.TrailerID, e.SiteID, a.Kind, a.UnitNumber, e.VisitID, e.ArrivedAt); err != nil {
			return err
		}
	}
	return nil
}

// foldWorkStarted records the door work and the trailer's FIRST work start
// (the driver wait ends there).
func foldWorkStarted(ctx context.Context, tx pgx.Tx, e report.Event) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO analytics.door_work (trailer_id, started_at, site_id, door_code) VALUES ($1, $2, $3, $4)
		ON CONFLICT (trailer_id, started_at) DO NOTHING`, e.TrailerID, e.StartedAt, e.SiteID, e.DoorCode); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO analytics.trailer_facts (trailer_id, site_id, first_door_work_started_at) VALUES ($1, $2, $3)
		ON CONFLICT (trailer_id) DO UPDATE SET
			first_door_work_started_at = LEAST(analytics.trailer_facts.first_door_work_started_at, EXCLUDED.first_door_work_started_at)`,
		e.TrailerID, e.SiteID, e.StartedAt)
	return err
}

// lastEventAt is the CloudEvents time of the newest event folded; ok is false
// until the first one. It serves GET /reports/freshness.
func lastEventAt(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}) (time.Time, bool, error) {
	var at *time.Time
	if err := q.QueryRow(ctx, `SELECT max(occurred_at) FROM analytics.processed_events`).Scan(&at); err != nil {
		return time.Time{}, false, err
	}
	if at == nil {
		return time.Time{}, false, nil
	}
	return at.UTC(), true, nil
}
