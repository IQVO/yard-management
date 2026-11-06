package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
)

// Locations is the pgx-backed ports.Locations (read side) and
// ports.LocationFeed (write side) over the zones and locations tables: the
// LocationView local copy of facility-layout (ADR 0003).
type Locations struct{ pool *pgxpool.Pool }

// NewLocations constructs the location copy over pool.
func NewLocations(pool *pgxpool.Pool) *Locations { return &Locations{pool: pool} }

// ApplyZone implements ports.LocationFeed: it records the zone's site and
// resolves every slot of the zone that waited for it (X5).
func (l *Locations) ApplyZone(ctx context.Context, zoneID, siteCode string) error {
	return inTx(ctx, l.pool, func(q querier) error {
		if _, err := q.Exec(ctx, `
			INSERT INTO zones (zone_id, site_code) VALUES ($1, $2)
			ON CONFLICT (zone_id) DO UPDATE SET site_code = EXCLUDED.site_code`, zoneID, siteCode); err != nil {
			return fmt.Errorf("apply zone: %w", err)
		}
		if _, err := q.Exec(ctx, `UPDATE locations SET site_code = $2 WHERE zone_id = $1 AND site_code IS NULL`, zoneID, siteCode); err != nil {
			return fmt.Errorf("resolve slots of zone %s: %w", zoneID, err)
		}
		return nil
	})
}

// ApplySlot implements ports.LocationFeed: a slot is (re)registered active; its
// site is the zone's when the zone is known, NULL until then.
func (l *Locations) ApplySlot(ctx context.Context, code, zoneID string, kind shared.LocationKind, dockFlow string) error {
	_, err := queryFor(ctx, l.pool).Exec(ctx, `
		INSERT INTO locations (location_code, zone_id, site_code, kind, dock_flow, active)
		VALUES ($1, $2, (SELECT site_code FROM zones WHERE zone_id = $2), $3, $4, true)
		ON CONFLICT (location_code) DO UPDATE
		SET zone_id = EXCLUDED.zone_id, site_code = EXCLUDED.site_code, kind = EXCLUDED.kind,
		    dock_flow = EXCLUDED.dock_flow, active = true`, code, zoneID, string(kind), dockFlow)
	if err != nil {
		return fmt.Errorf("apply slot: %w", err)
	}
	return nil
}

// Decommission implements ports.LocationFeed.
func (l *Locations) Decommission(ctx context.Context, code string) error {
	if _, err := queryFor(ctx, l.pool).Exec(ctx, `UPDATE locations SET active = false WHERE location_code = $1`, code); err != nil {
		return fmt.Errorf("decommission slot: %w", err)
	}
	return nil
}

const locationColumns = `location_code, zone_id, COALESCE(site_code, ''), kind, dock_flow, active`

func scanLocation(row pgx.Row) (views.LocationInfo, error) {
	var (
		info views.LocationInfo
		kind string
	)
	err := row.Scan(&info.Code, &info.ZoneID, &info.SiteCode, &kind, &info.DockFlow, &info.Active)
	info.Kind = shared.LocationKind(kind)
	return info, err
}

// Get implements ports.Locations.
func (l *Locations) Get(ctx context.Context, code string) (views.LocationInfo, bool, error) {
	info, err := scanLocation(queryFor(ctx, l.pool).QueryRow(ctx, `SELECT `+locationColumns+` FROM locations WHERE location_code = $1`, code))
	if errors.Is(err, pgx.ErrNoRows) {
		return views.LocationInfo{}, false, nil
	}
	if err != nil {
		return views.LocationInfo{}, false, fmt.Errorf("get location: %w", err)
	}
	return info, true, nil
}

// ListBySite implements ports.Locations: the active slots of the site by code.
func (l *Locations) ListBySite(ctx context.Context, siteID string) ([]views.LocationInfo, error) {
	rows, err := queryFor(ctx, l.pool).Query(ctx, `
		SELECT `+locationColumns+` FROM locations WHERE site_code = $1 AND active ORDER BY location_code`, siteID)
	if err != nil {
		return nil, fmt.Errorf("list locations: %w", err)
	}
	defer rows.Close()
	var out []views.LocationInfo
	for rows.Next() {
		info, err := scanLocation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan location: %w", err)
		}
		out = append(out, info)
	}
	return out, rows.Err()
}

// FreeSpots implements ports.Locations: up to limit active Yard slots of the
// site, by code, that hold no trailer and are reserved by no open move; zoneIDs,
// when not empty, restricts them to those zones.
func (l *Locations) FreeSpots(ctx context.Context, siteID string, zoneIDs []string, limit int) ([]string, error) {
	var w whereBuilder
	w.add("l.site_code = ?", siteID)
	w.raw("l.active")
	w.raw("l.kind = 'SPOT'")
	if len(zoneIDs) > 0 {
		w.add("l.zone_id = ANY(?)", zoneIDs)
	}
	w.raw(`NOT EXISTS (SELECT 1 FROM trailers t WHERE t.location_kind = 'SPOT' AND t.location_code = l.location_code AND t.state IN ('PARKED', 'DOCKED'))`)
	w.raw(`NOT EXISTS (SELECT 1 FROM yard_moves m WHERE m.to_kind = 'SPOT' AND m.to_code = l.location_code AND m.state IN ('REQUESTED', 'CLAIMED'))`)
	rows, err := queryFor(ctx, l.pool).Query(ctx,
		`SELECT l.location_code FROM locations l`+w.clause()+` ORDER BY l.location_code LIMIT `+w.limit(limit), w.args...)
	if err != nil {
		return nil, fmt.Errorf("list free spots: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, fmt.Errorf("scan free spot: %w", err)
		}
		out = append(out, code)
	}
	return out, rows.Err()
}
