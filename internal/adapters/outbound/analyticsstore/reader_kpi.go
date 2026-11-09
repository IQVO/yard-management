package analyticsstore

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/claudioed/yard-management/internal/analytics/report"
	"github.com/claudioed/yard-management/internal/application/views"
)

// TurnTime is the gate-to-gate turn time (ADR 0011) of the visits that checked
// OUT inside the window, per visit purpose: on_site_minutes of
// VisitCheckedOut, which runs from VisitCheckedIn.arrived_at to
// VisitCheckedOut.departed_at. A visit whose check-in the projection never saw
// has no purpose and is left out. The median and the 90th percentile are
// computed by Postgres (percentile_cont).
func (r *Reader) TurnTime(ctx context.Context, w views.ReportWindow) ([]views.TurnTimeRow, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT purpose, count(*)::int, avg(on_site_minutes)::float8,
		       (percentile_cont(0.5) WITHIN GROUP (ORDER BY on_site_minutes))::float8,
		       (percentile_cont(0.9) WITHIN GROUP (ORDER BY on_site_minutes))::float8,
		       max(on_site_minutes)::bigint
		FROM analytics.visit_facts
		WHERE departed_at >= $1 AND departed_at < $2 AND purpose IS NOT NULL AND on_site_minutes IS NOT NULL AND ($3 = '' OR site_id = $3)
		GROUP BY purpose ORDER BY purpose`, w.From, w.To, w.SiteID)
	if err != nil {
		return nil, fmt.Errorf("turn time report: %w", err)
	}
	defer rows.Close()
	out := []views.TurnTimeRow{}
	for rows.Next() {
		var row views.TurnTimeRow
		if err := rows.Scan(&row.Purpose, &row.Visits, &row.AvgMinutes, &row.P50Minutes, &row.P90Minutes, &row.MaxMinutes); err != nil {
			return nil, fmt.Errorf("scan turn time row: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// DetentionExposure is, per trailer kind, the trailers that DEPARTED inside the
// window and the ones whose dwell was OVER thresholdMinutes (a dwell equal to
// the threshold does not exceed it), with the minutes over the threshold summed
// (ADR 0011). Trailers whose kind is unknown are left out like in the dwell
// report. SQL counts and sums; the share is report.Share.
func (r *Reader) DetentionExposure(ctx context.Context, w views.ReportWindow, thresholdMinutes int) ([]views.DetentionRow, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT kind, count(*)::int, (count(*) FILTER (WHERE dwell_minutes > $4))::int,
		       COALESCE(sum(dwell_minutes - $4) FILTER (WHERE dwell_minutes > $4), 0)::bigint
		FROM analytics.trailer_facts
		WHERE departed_at >= $1 AND departed_at < $2 AND kind IS NOT NULL AND dwell_minutes IS NOT NULL AND ($3 = '' OR site_id = $3)
		GROUP BY kind ORDER BY kind`, w.From, w.To, w.SiteID, int64(thresholdMinutes))
	if err != nil {
		return nil, fmt.Errorf("detention exposure report: %w", err)
	}
	defer rows.Close()
	out := []views.DetentionRow{}
	for rows.Next() {
		var row views.DetentionRow
		if err := rows.Scan(&row.Kind, &row.Trailers, &row.Exceeding, &row.ExcessMinutes); err != nil {
			return nil, fmt.Errorf("scan detention exposure row: %w", err)
		}
		row.Share = report.Share(int64(row.Exceeding), int64(row.Trailers))
		out = append(out, row)
	}
	return out, rows.Err()
}

// exceptionCounts is the long form of the exceptions query: one count per site
// and metric, pivoted in Go.
const exceptionCounts = `
	SELECT site_id, 'checked_in', count(*) FROM analytics.visit_facts
	WHERE arrived_at >= $1 AND arrived_at < $2 AND site_id IS NOT NULL AND ($3 = '' OR site_id = $3) GROUP BY site_id
	UNION ALL
	SELECT site_id, CASE kind WHEN 'VISIT_REJECTED' THEN 'rejected' ELSE 'seal' END, count(*) FROM analytics.gate_exceptions
	WHERE occurred_at >= $1 AND occurred_at < $2 AND ($3 = '' OR site_id = $3) GROUP BY site_id, kind
	UNION ALL
	SELECT site_id, 'hold:' || reason, count(*) FROM analytics.trailer_holds
	WHERE held_at >= $1 AND held_at < $2 AND ($3 = '' OR site_id = $3) GROUP BY site_id, reason
	UNION ALL
	SELECT site_id, 'yard_checks', count(*) FROM analytics.yard_check_results
	WHERE closed_at >= $1 AND closed_at < $2 AND ($3 = '' OR site_id = $3) GROUP BY site_id
	UNION ALL
	SELECT site_id, 'discrepancies', COALESCE(sum(discrepancies), 0) FROM analytics.yard_check_results
	WHERE closed_at >= $1 AND closed_at < $2 AND ($3 = '' OR site_id = $3) GROUP BY site_id`

// Exceptions is, per site, the exception counts of the window and their rates
// (ADR 0011): visits checked in and rejected (a rejection counts when it
// happened, a check-in when the visit arrived), seal exceptions, holds placed
// by reason, yard checks closed and the discrepancies they found. A site with
// none of them has no row.
func (r *Reader) Exceptions(ctx context.Context, w views.ReportWindow) ([]views.ExceptionRow, error) {
	rows, err := r.pool.Query(ctx, exceptionCounts, w.From, w.To, w.SiteID)
	if err != nil {
		return nil, fmt.Errorf("exceptions report: %w", err)
	}
	defer rows.Close()
	bySite := map[string]*views.ExceptionRow{}
	for rows.Next() {
		var (
			site, metric string
			n            int64
		)
		if err := rows.Scan(&site, &metric, &n); err != nil {
			return nil, fmt.Errorf("scan exceptions row: %w", err)
		}
		row := bySite[site]
		if row == nil {
			row = &views.ExceptionRow{SiteID: site, HoldsByReason: emptyHolds()}
			bySite[site] = row
		}
		addMetric(row, metric, int(n))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]views.ExceptionRow, 0, len(bySite))
	for _, row := range bySite {
		row.RejectionRate = report.RejectionRate(int64(row.VisitsCheckedIn), int64(row.VisitsRejected))
		row.SealExceptionRate = report.SealExceptionRate(int64(row.SealExceptions), int64(row.VisitsCheckedIn))
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SiteID < out[j].SiteID })
	return out, nil
}

func emptyHolds() map[string]int {
	holds := make(map[string]int, len(report.HoldReasons))
	for _, reason := range report.HoldReasons {
		holds[reason] = 0
	}
	return holds
}

// addMetric adds one counted metric to the site's row.
func addMetric(row *views.ExceptionRow, metric string, n int) {
	switch metric {
	case "checked_in":
		row.VisitsCheckedIn += n
	case "rejected":
		row.VisitsRejected += n
	case "seal":
		row.SealExceptions += n
	case "yard_checks":
		row.YardChecksClosed += n
	case "discrepancies":
		row.Discrepancies += n
	default:
		if reason, ok := strings.CutPrefix(metric, "hold:"); ok {
			row.HoldsByReason[reason] += n
		}
	}
}
