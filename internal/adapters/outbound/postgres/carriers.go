package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/yard-management/internal/application/views"
)

// Carriers is the pgx-backed ports.Carriers (read side) and ports.CarrierFeed
// (write side): the CarrierView local copy of the dock-scheduling carrier
// registry (ADR 0016).
type Carriers struct{ pool *pgxpool.Pool }

// NewCarriers constructs the carrier copy over pool.
func NewCarriers(pool *pgxpool.Pool) *Carriers { return &Carriers{pool: pool} }

// Get implements ports.Carriers.
func (c *Carriers) Get(ctx context.Context, scac string) (views.Carrier, bool, error) {
	var v views.Carrier
	err := queryFor(ctx, c.pool).QueryRow(ctx,
		`SELECT scac, name, usdot, mc, status, reason, version, updated_at FROM carriers WHERE scac = $1`, scac).
		Scan(&v.SCAC, &v.Name, &v.USDOT, &v.MC, &v.Status, &v.Reason, &v.Version, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return views.Carrier{}, false, nil
	}
	if err != nil {
		return views.Carrier{}, false, fmt.Errorf("get carrier: %w", err)
	}
	return v, true, nil
}

// Register implements ports.CarrierFeed: the whole snapshot replaces the
// carrier. The version guard in the WHERE makes a fact that is not newer than
// the stored one a clean no-op, so a replay or a reordering never resurrects
// an older snapshot; a null name / usdot / mc / reason already arrived as ""
// (the migration's convention for a null identifier).
func (c *Carriers) Register(ctx context.Context, in views.Carrier) error {
	if in.Status == "" {
		in.Status = views.CarrierActive // the table CHECKs the standing; the producer sends ACTIVE
	}
	_, err := queryFor(ctx, c.pool).Exec(ctx, `
		INSERT INTO carriers (scac, name, usdot, mc, status, reason, version, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (scac) DO UPDATE
		SET name = EXCLUDED.name, usdot = EXCLUDED.usdot, mc = EXCLUDED.mc, status = EXCLUDED.status,
		    reason = EXCLUDED.reason, version = EXCLUDED.version, updated_at = EXCLUDED.updated_at
		WHERE EXCLUDED.version > carriers.version`,
		in.SCAC, in.Name, in.USDOT, in.MC, in.Status, in.Reason, in.Version, in.UpdatedAt)
	if err != nil {
		return fmt.Errorf("register carrier: %w", err)
	}
	return nil
}

// Suspend implements ports.CarrierFeed: only the standing, the reason and the
// bookkeeping change; the name and identifiers of the stored carrier are kept.
// A carrier first seen through a suspension is inserted with no name (the
// columns default to ”), exactly as the memory adapter does.
func (c *Carriers) Suspend(ctx context.Context, scac, reason string, version int64, at time.Time) error {
	_, err := queryFor(ctx, c.pool).Exec(ctx, `
		INSERT INTO carriers (scac, status, reason, version, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (scac) DO UPDATE
		SET status = EXCLUDED.status, reason = EXCLUDED.reason, version = EXCLUDED.version, updated_at = EXCLUDED.updated_at
		WHERE EXCLUDED.version > carriers.version`,
		scac, views.CarrierSuspended, reason, version, at.UTC())
	if err != nil {
		return fmt.Errorf("suspend carrier: %w", err)
	}
	return nil
}

// Reinstate implements ports.CarrierFeed: the standing becomes ACTIVE and the
// suspension reason is cleared; a carrier first seen through a reinstatement
// is inserted ACTIVE with no name.
func (c *Carriers) Reinstate(ctx context.Context, scac string, version int64, at time.Time) error {
	_, err := queryFor(ctx, c.pool).Exec(ctx, `
		INSERT INTO carriers (scac, status, version, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (scac) DO UPDATE
		SET status = EXCLUDED.status, reason = '', version = EXCLUDED.version, updated_at = EXCLUDED.updated_at
		WHERE EXCLUDED.version > carriers.version`,
		scac, views.CarrierActive, version, at.UTC())
	if err != nil {
		return fmt.Errorf("reinstate carrier: %w", err)
	}
	return nil
}
