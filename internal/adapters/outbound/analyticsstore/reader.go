package analyticsstore

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/yard-management/internal/analytics/report"
	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
)

// Reader is the Postgres ports.ReportReader: the reports and the
// freshness over the analytical model, through a READ-ONLY pool. SQL only sums
// and averages; the ratios live in internal/analytics/report.
type Reader struct {
	pool *pgxpool.Pool
}

// NewReader constructs the reader over a read-only pool.
func NewReader(pool *pgxpool.Pool) *Reader { return &Reader{pool: pool} }

var _ ports.ReportReader = (*Reader)(nil)

// YardDwell is the dwell of the trailers that DEPARTED inside the window, per
// trailer kind (the kind comes from VisitCheckedIn; a trailer whose arrival the
// projection never saw is left out). The driver wait is the mean of arrival to
// the first DoorWorkStarted over the trailers that had door work; the
// processing is the mean of DoorWorkStarted to DoorWorkCompleted over the
// completed door work sessions of these trailers. The median and the 90th
// percentile are computed by Postgres (percentile_cont, ADR 0011).
func (r *Reader) YardDwell(ctx context.Context, w views.ReportWindow) ([]views.DwellRow, error) {
	rows, err := r.pool.Query(ctx, `
		WITH t AS (
			SELECT trailer_id, kind, dwell_minutes, arrived_at, first_door_work_started_at
			FROM analytics.trailer_facts
			WHERE departed_at >= $1 AND departed_at < $2 AND kind IS NOT NULL AND ($3 = '' OR site_id = $3)
		), proc AS (
			SELECT t.kind, avg(EXTRACT(EPOCH FROM (k.completed_at - k.started_at)) / 60.0)::float8 AS avg_minutes
			FROM t JOIN analytics.door_work k ON k.trailer_id = t.trailer_id
			WHERE k.completed_at >= k.started_at
			GROUP BY t.kind
		)
		SELECT t.kind, count(*)::int, COALESCE(avg(t.dwell_minutes), 0)::float8, COALESCE(max(t.dwell_minutes), 0)::bigint,
		       (avg(EXTRACT(EPOCH FROM (t.first_door_work_started_at - t.arrived_at)) / 60.0)
		            FILTER (WHERE t.first_door_work_started_at >= t.arrived_at))::float8,
		       (percentile_cont(0.5) WITHIN GROUP (ORDER BY t.dwell_minutes))::float8,
		       (percentile_cont(0.9) WITHIN GROUP (ORDER BY t.dwell_minutes))::float8,
		       max(proc.avg_minutes)
		FROM t LEFT JOIN proc ON proc.kind = t.kind
		GROUP BY t.kind ORDER BY t.kind`, w.From, w.To, w.SiteID)
	if err != nil {
		return nil, fmt.Errorf("yard dwell report: %w", err)
	}
	defer rows.Close()
	out := []views.DwellRow{}
	for rows.Next() {
		var row views.DwellRow
		if err := rows.Scan(&row.Kind, &row.Trailers, &row.AvgDwellMinutes, &row.MaxDwellMinutes, &row.AvgDriverWaitMinutes,
			&row.P50DwellMinutes, &row.P90DwellMinutes, &row.AvgProcessingMinutes); err != nil {
			return nil, fmt.Errorf("scan yard dwell row: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// DoorUtilisation is, per dock door with a docking inside the window, the
// dockings overlapping it, the time a trailer was docked there (clipped to the
// window; a docking still open counts up to now) and the door work time within
// that. Utilisation is the docked share of the window.
func (r *Reader) DoorUtilisation(ctx context.Context, w views.ReportWindow) ([]views.DoorRow, error) {
	rows, err := r.pool.Query(ctx, `
		WITH dock AS (
			SELECT site_id, door_code, count(*) AS dockings,
			       sum(EXTRACT(EPOCH FROM (LEAST(COALESCE(undocked_at, now()), $2::timestamptz) - GREATEST(docked_at, $1::timestamptz)))) AS occupied_s
			FROM analytics.door_dockings
			WHERE docked_at < $2 AND COALESCE(undocked_at, now()) > $1 AND ($3 = '' OR site_id = $3)
			GROUP BY site_id, door_code
		), work AS (
			SELECT site_id, door_code,
			       sum(EXTRACT(EPOCH FROM (LEAST(COALESCE(completed_at, now()), $2::timestamptz) - GREATEST(started_at, $1::timestamptz)))) AS work_s
			FROM analytics.door_work
			WHERE started_at < $2 AND COALESCE(completed_at, now()) > $1 AND ($3 = '' OR site_id = $3)
			GROUP BY site_id, door_code
		)
		SELECT d.site_id, d.door_code, d.dockings::int, d.occupied_s::float8, COALESCE(k.work_s, 0)::float8
		FROM dock d LEFT JOIN work k USING (site_id, door_code)
		ORDER BY d.site_id, d.door_code`, w.From, w.To, w.SiteID)
	if err != nil {
		return nil, fmt.Errorf("door utilisation report: %w", err)
	}
	defer rows.Close()
	out := []views.DoorRow{}
	window := w.To.Sub(w.From)
	for rows.Next() {
		var (
			row              views.DoorRow
			occupiedS, workS float64
		)
		if err := rows.Scan(&row.SiteID, &row.DoorCode, &row.Dockings, &occupiedS, &workS); err != nil {
			return nil, fmt.Errorf("scan door utilisation row: %w", err)
		}
		occupied := seconds(occupiedS)
		row.OccupiedMinutes = int64(math.Round(occupied.Minutes()))
		row.WorkMinutes = int64(math.Round(seconds(workS).Minutes()))
		row.Utilisation = report.Utilisation(occupied, window)
		out = append(out, row)
	}
	return out, rows.Err()
}

// seconds converts SQL epoch seconds to a duration, a negative one (a clock
// skewed fact) being zero.
func seconds(s float64) time.Duration {
	if s <= 0 {
		return 0
	}
	return time.Duration(s * float64(time.Second))
}

// HostlerProductivity is, per hostler, the moves completed inside the window,
// the mean move duration and the moves per hour actually worked (an hour in
// which the hostler completed at least one move).
func (r *Reader) HostlerProductivity(ctx context.Context, w views.ReportWindow) ([]views.HostlerRow, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT site_id, hostler_ref, count(*)::int, avg(duration_seconds)::float8,
		       count(DISTINCT date_trunc('hour', completed_at AT TIME ZONE 'UTC'))::int
		FROM analytics.hostler_moves
		WHERE completed_at >= $1 AND completed_at < $2 AND ($3 = '' OR site_id = $3)
		GROUP BY site_id, hostler_ref ORDER BY site_id, hostler_ref`, w.From, w.To, w.SiteID)
	if err != nil {
		return nil, fmt.Errorf("hostler productivity report: %w", err)
	}
	defer rows.Close()
	out := []views.HostlerRow{}
	for rows.Next() {
		var (
			row       views.HostlerRow
			busyHours int
		)
		if err := rows.Scan(&row.SiteID, &row.HostlerRef, &row.Moves, &row.AvgMoveSeconds, &busyHours); err != nil {
			return nil, fmt.Errorf("scan hostler productivity row: %w", err)
		}
		row.MovesPerHour = report.PerBusyHour(row.Moves, busyHours)
		out = append(out, row)
	}
	return out, rows.Err()
}

// LastEventAt implements ports.ReportReader.
func (r *Reader) LastEventAt(ctx context.Context) (time.Time, bool, error) {
	return lastEventAt(ctx, r.pool)
}
