package analyticsstore

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/claudioed/yard-management/internal/analytics/report"
)

// Kinds of analytics.gate_exceptions rows.
const (
	gateVisitRejected = "VISIT_REJECTED"
	gateSealException = "SEAL_EXCEPTION"
)

// foldKPI writes the facts of the five kinds of the KPI standard (ADR 0011).
// Every row is upserted by its natural key, so a redelivery under a new event
// id and any arrival order across keys converge to the same rows.
func foldKPI(ctx context.Context, tx pgx.Tx, e report.Event) error {
	switch e.Kind {
	case report.KindVisitCheckedOut:
		return foldVisitDeparture(ctx, tx, e)
	case report.KindVisitRejected:
		_, err := tx.Exec(ctx, `
			INSERT INTO analytics.gate_exceptions (kind, subject_id, occurred_at, site_id, reason) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (kind, subject_id, occurred_at) DO NOTHING`, gateVisitRejected, e.VisitID, e.RejectedAt, e.SiteID, e.Reason)
		return err
	case report.KindSealExceptionRaised:
		_, err := tx.Exec(ctx, `
			INSERT INTO analytics.gate_exceptions (kind, subject_id, occurred_at, site_id) VALUES ($1, $2, $3, $4)
			ON CONFLICT (kind, subject_id, occurred_at) DO NOTHING`, gateSealException, e.Subject(), e.RaisedAt, e.SiteID)
		return err
	case report.KindTrailerHeld:
		// The payload has no instant: the hold happened when the event was published.
		_, err := tx.Exec(ctx, `
			INSERT INTO analytics.trailer_holds (trailer_id, version, site_id, reason, held_at) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (trailer_id, version) DO NOTHING`, e.TrailerID, e.Version, e.SiteID, e.Reason, e.At)
		return err
	case report.KindYardCheckClosed:
		_, err := tx.Exec(ctx, `
			INSERT INTO analytics.yard_check_results (yard_check_id, site_id, closed_at, discrepancies) VALUES ($1, $2, $3, $4)
			ON CONFLICT (yard_check_id) DO NOTHING`, e.YardCheckID, e.SiteID, e.ClosedAt, e.Discrepancies)
		return err
	}
	return fmt.Errorf("%w: %s %s: no KPI fold for the kind", report.ErrRejected, e.Kind, e.EventID)
}

// foldVisitArrival upserts the purpose and the gate arrival of a visit. The
// earliest arrival wins and a purpose already known is kept when the event
// carries none, so VisitCheckedOut may overtake VisitCheckedIn.
func foldVisitArrival(ctx context.Context, tx pgx.Tx, e report.Event) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO analytics.visit_facts (visit_id, site_id, purpose, arrived_at) VALUES ($1, $2, NULLIF($3, ''), $4)
		ON CONFLICT (visit_id) DO UPDATE SET
			site_id = COALESCE(analytics.visit_facts.site_id, EXCLUDED.site_id),
			purpose = COALESCE(EXCLUDED.purpose, analytics.visit_facts.purpose),
			arrived_at = LEAST(analytics.visit_facts.arrived_at, EXCLUDED.arrived_at)`,
		e.VisitID, e.SiteID, e.Purpose, e.ArrivedAt)
	return err
}

// foldVisitDeparture upserts the gate departure of a visit: the earliest
// departure wins and carries its own on_site_minutes (gate to gate).
func foldVisitDeparture(ctx context.Context, tx pgx.Tx, e report.Event) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO analytics.visit_facts (visit_id, site_id, departed_at, on_site_minutes) VALUES ($1, $2, $3, $4)
		ON CONFLICT (visit_id) DO UPDATE SET
			site_id = COALESCE(analytics.visit_facts.site_id, EXCLUDED.site_id),
			on_site_minutes = CASE WHEN analytics.visit_facts.departed_at IS NULL OR EXCLUDED.departed_at < analytics.visit_facts.departed_at
				THEN EXCLUDED.on_site_minutes ELSE analytics.visit_facts.on_site_minutes END,
			departed_at = LEAST(analytics.visit_facts.departed_at, EXCLUDED.departed_at)`,
		e.VisitID, e.SiteID, e.DepartedAt, e.OnSiteMinutes)
	return err
}
