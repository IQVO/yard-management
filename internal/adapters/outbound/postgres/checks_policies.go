package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/yardcheck"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
)

// Checks is the pgx-backed ports.CheckRepo.
type Checks struct{ pool *pgxpool.Pool }

// NewChecks constructs a Checks repo over pool.
func NewChecks(pool *pgxpool.Pool) *Checks { return &Checks{pool: pool} }

const checkColumns = `yard_check_id, site_id, state, observations, discrepancies, opened_at, closed_at, version`

// indexCheckOpen is the partial unique index of one OPEN check per site.
const indexCheckOpen = "uq_yard_checks_one_open_per_site"

type observationJSON struct {
	LocationCode string `json:"location_code"`
	UnitNumber   string `json:"unit_number"`
}

type discrepancyJSON struct {
	Kind         string `json:"kind"`
	LocationCode string `json:"location_code"`
	TrailerID    string `json:"trailer_id"`
	UnitNumber   string `json:"unit_number"`
}

// Save persists the check like Visits.Save; a second OPEN check of the site is
// yardcheck.ErrYardCheckOpen.
func (r *Checks) Save(ctx context.Context, c *yardcheck.Check) error {
	s := c.Snapshot()
	obs := make([]observationJSON, 0, len(s.Observations))
	for _, o := range s.Observations {
		obs = append(obs, observationJSON{LocationCode: o.LocationCode, UnitNumber: o.UnitNumber})
	}
	dis := make([]discrepancyJSON, 0, len(s.Discrepancies))
	for _, d := range s.Discrepancies {
		dis = append(dis, discrepancyJSON{Kind: string(d.Kind), LocationCode: d.LocationCode, TrailerID: d.TrailerID, UnitNumber: d.UnitNumber})
	}
	obsRaw, err := json.Marshal(obs)
	if err != nil {
		return fmt.Errorf("encode observations: %w", err)
	}
	disRaw, err := json.Marshal(dis)
	if err != nil {
		return fmt.Errorf("encode discrepancies: %w", err)
	}
	args := []any{s.ID, s.SiteID, string(s.State), obsRaw, disRaw, s.OpenedAt.UTC(), nullTime(s.ClosedAt), s.Version}
	byConstraint := map[string]error{indexCheckOpen: yardcheck.ErrYardCheckOpen}
	q := queryFor(ctx, r.pool)
	if s.Version == 1 {
		_, err := q.Exec(ctx, `INSERT INTO yard_checks (`+checkColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, args...)
		return saveError("yard check", err, byConstraint)
	}
	tag, err := q.Exec(ctx, `
		UPDATE yard_checks SET site_id = $2, state = $3, observations = $4, discrepancies = $5, opened_at = $6,
			closed_at = $7, version = $8, updated_at = now()
		WHERE yard_check_id = $1 AND version = $9`, append(args, s.Version-1)...)
	if err != nil {
		return saveError("yard check", err, byConstraint)
	}
	if tag.RowsAffected() == 0 {
		return ports.ErrConcurrentModification
	}
	return nil
}

// FindByID implements ports.CheckRepo.
func (r *Checks) FindByID(ctx context.Context, id string) (*yardcheck.Check, error) {
	return one(scanCheck(queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+checkColumns+` FROM yard_checks WHERE yard_check_id = $1`, id)))
}

func scanCheck(row pgx.Row) (*yardcheck.Check, error) {
	var (
		s        yardcheck.Snapshot
		state    string
		obsRaw   []byte
		disRaw   []byte
		closedAt *time.Time
	)
	if err := row.Scan(&s.ID, &s.SiteID, &state, &obsRaw, &disRaw, &s.OpenedAt, &closedAt, &s.Version); err != nil {
		return nil, err
	}
	var obs []observationJSON
	var dis []discrepancyJSON
	if err := json.Unmarshal(obsRaw, &obs); err != nil {
		return nil, fmt.Errorf("stored yard check %s has bad observations: %w", s.ID, err)
	}
	if err := json.Unmarshal(disRaw, &dis); err != nil {
		return nil, fmt.Errorf("stored yard check %s has bad discrepancies: %w", s.ID, err)
	}
	s.State, s.OpenedAt, s.ClosedAt = yardcheck.State(state), s.OpenedAt.UTC(), timeOrZero(closedAt)
	for _, o := range obs {
		s.Observations = append(s.Observations, yardcheck.Observation{LocationCode: o.LocationCode, UnitNumber: o.UnitNumber})
	}
	for _, d := range dis {
		s.Discrepancies = append(s.Discrepancies, yardcheck.Discrepancy{
			Kind: yardcheck.DiscrepancyKind(d.Kind), LocationCode: d.LocationCode, TrailerID: d.TrailerID, UnitNumber: d.UnitNumber,
		})
	}
	c, err := yardcheck.Restore(s)
	if err != nil {
		return nil, fmt.Errorf("stored yard check %s is invalid: %w", s.ID, err)
	}
	return c, nil
}

// Policies is the pgx-backed ports.PolicyRepo.
type Policies struct{ pool *pgxpool.Pool }

// NewPolicies constructs a Policies repo over pool.
func NewPolicies(pool *pgxpool.Pool) *Policies { return &Policies{pool: pool} }

const policyColumns = `site_id, require_appointment, holding_spot_codes, parking_zone_ids, dwell_threshold_minutes,
	lease_seconds, seal_mismatch_action, free_time_minutes, declared, version, minimum_seal_class,
	require_securing_before_door_work, allow_hazmat_on_unclassified_spots, required_inspections`

// Save implements ports.PolicyRepo: the first declaration is version 2 and
// INSERTs (a stored row, or any other first version, is
// ports.ErrConcurrentModification); a later version UPDATEs the row stored at
// version - 1.
func (r *Policies) Save(ctx context.Context, p *yardpolicy.Policy) error {
	s := p.Snapshot()
	dwell := make(map[string]int64, len(s.DwellThresholdMinutes))
	for k, v := range s.DwellThresholdMinutes {
		dwell[string(k)] = v
	}
	dwellRaw, err := json.Marshal(dwell)
	if err != nil {
		return fmt.Errorf("encode dwell thresholds: %w", err)
	}
	rulesRaw, err := encodeRules(s.RequiredInspections)
	if err != nil {
		return fmt.Errorf("encode required inspections: %w", err)
	}
	args := []any{
		s.SiteID, s.RequireAppointment, strs(s.HoldingSpotCodes), strs(s.ParkingZoneIDs), dwellRaw,
		s.LeaseSeconds, string(s.SealMismatchAction), s.FreeTimeMinutes, s.Declared, s.Version, string(s.MinimumSealClass),
		s.RequireSecuring, s.AllowHazmatOnUnclassified, rulesRaw,
	}
	q := queryFor(ctx, r.pool)
	if s.Version == 2 {
		tag, err := q.Exec(ctx, `INSERT INTO yard_policies (`+policyColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
			ON CONFLICT (site_id) DO NOTHING`, args...)
		if err != nil {
			return fmt.Errorf("save yard policy: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ports.ErrConcurrentModification
		}
		return nil
	}
	tag, err := q.Exec(ctx, `
		UPDATE yard_policies SET require_appointment = $2, holding_spot_codes = $3, parking_zone_ids = $4,
			dwell_threshold_minutes = $5, lease_seconds = $6, seal_mismatch_action = $7, free_time_minutes = $8,
			declared = $9, version = $10, minimum_seal_class = $11,
			require_securing_before_door_work = $12,
			allow_hazmat_on_unclassified_spots = $13, required_inspections = $14, updated_at = now()
		WHERE site_id = $1 AND version = $15`, append(args, s.Version-1)...)
	if err != nil {
		return fmt.Errorf("save yard policy: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ports.ErrConcurrentModification
	}
	return nil
}

// Find implements ports.PolicyRepo.
func (r *Policies) Find(ctx context.Context, siteID string) (*yardpolicy.Policy, error) {
	return one(scanPolicy(queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+policyColumns+` FROM yard_policies WHERE site_id = $1`, siteID)))
}

func scanPolicy(row pgx.Row) (*yardpolicy.Policy, error) {
	var (
		s        yardpolicy.Snapshot
		action   string
		minClass string
		dwellRaw []byte
		rulesRaw []byte
	)
	if err := row.Scan(&s.SiteID, &s.RequireAppointment, &s.HoldingSpotCodes, &s.ParkingZoneIDs, &dwellRaw,
		&s.LeaseSeconds, &action, &s.FreeTimeMinutes, &s.Declared, &s.Version, &minClass, &s.RequireSecuring, &s.AllowHazmatOnUnclassified, &rulesRaw); err != nil {
		return nil, err
	}
	var err error
	if s.RequiredInspections, err = decodeRules(rulesRaw); err != nil {
		return nil, fmt.Errorf("stored yard policy %s has bad required inspections: %w", s.SiteID, err)
	}
	dwell := map[string]int64{}
	if err := json.Unmarshal(dwellRaw, &dwell); err != nil {
		return nil, fmt.Errorf("stored yard policy %s has bad thresholds: %w", s.SiteID, err)
	}
	s.SealMismatchAction, s.MinimumSealClass = yardpolicy.SealAction(action), shared.SealClass(minClass)
	s.DwellThresholdMinutes = make(map[shared.TrailerKind]int64, len(dwell))
	for k, v := range dwell {
		s.DwellThresholdMinutes[shared.TrailerKind(k)] = v
	}
	p, err := yardpolicy.Restore(s)
	if err != nil {
		return nil, fmt.Errorf("stored yard policy %s is invalid: %w", s.SiteID, err)
	}
	return p, nil
}
