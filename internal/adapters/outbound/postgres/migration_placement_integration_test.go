//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/claudioed/yard-management/internal/adapters/outbound/postgres"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
)

// Migration 0005 (ADR 0014) is additive: rows written by the previous release
// load unchanged as "nothing declared", "kind unknown" and "policy off" (so the
// placement rules leave them alone), a classification is storable and
// consistent, and going down drops only the new columns.
func TestMigration0005KeepsExistingRowsAndGoesDown(t *testing.T) {
	url := pgtest.EmptyURL(t)
	m, err := postgres.Migrator(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Migrate(4); err != nil {
		t.Fatalf("migrate to 4: %v", err)
	}
	pool := pgtest.Open(t, url)
	if err := pgtest.Exec(pool, oldTrailerRow, "trl-old-1", "TR-1", "DRY_VAN", "SL-1", "INTACT"); err != nil {
		t.Fatalf("insert the old trailer: %v", err)
	}
	if err := pgtest.Exec(pool, `INSERT INTO zones (zone_id, site_code) VALUES ('z-yard', 'WH1')`); err != nil {
		t.Fatal(err)
	}
	if err := pgtest.Exec(pool, `INSERT INTO locations (location_code, zone_id, site_code, kind, dock_flow, active) VALUES ('WH1-P1', 'z-yard', 'WH1', 'SPOT', '', true)`); err != nil {
		t.Fatal(err)
	}
	if err := pgtest.Exec(pool, `INSERT INTO yard_policies (site_id, require_appointment, lease_seconds, seal_mismatch_action, declared, version)
		VALUES ('WH1', false, 300, 'FLAG', true, 2)`); err != nil {
		t.Fatal(err)
	}
	if err := m.Migrate(5); err != nil {
		t.Fatalf("migrate to 5: %v", err)
	}
	// The repositories read the latest schema (0006 added the required inspections).
	if err := m.Migrate(6); err != nil {
		t.Fatalf("migrate to 6: %v", err)
	}

	ctx := context.Background()
	tr, err := postgres.NewTrailers(pool).FindByID(ctx, "trl-old-1")
	if err != nil || tr.Classification().Declared() {
		t.Fatalf("an old trailer declares nothing: %v %+v", err, tr.Classification())
	}
	loc, ok, err := postgres.NewLocations(pool).Get(ctx, "WH1-P1")
	if err != nil || !ok || loc.SpotKind != shared.SpotKindUnknown {
		t.Fatalf("an old spot has no kind: %v ok=%v %+v", err, ok, loc)
	}
	p, err := postgres.NewPolicies(pool).Find(ctx, "WH1")
	if err != nil || p.AllowHazmatOnUnclassifiedSpots() {
		t.Fatalf("the old policy refuses hazmat on unclassified spots: %v", err)
	}

	if err := m.Migrate(5); err != nil {
		t.Fatalf("back to 5: %v", err)
	}
	if err := pgtest.Exec(pool, `UPDATE trailers SET hazmat_class = '1.4', un_number = 'UN0012', requires_temperature_control = true WHERE trailer_id = 'trl-old-1'`); err != nil {
		t.Fatalf("a classification must be storable: %v", err)
	}
	if err := pgtest.Exec(pool, `UPDATE locations SET spot_kind = 'REEFER_PLUG' WHERE location_code = 'WH1-P1'`); err != nil {
		t.Fatalf("a spot kind must be storable: %v", err)
	}
	for name, stmt := range map[string]string{
		"an unknown hazard class":     `UPDATE trailers SET hazmat_class = '1.7'`,
		"a malformed UN number":       `UPDATE trailers SET un_number = 'UN12'`,
		"a UN number without a class": `UPDATE trailers SET hazmat_class = '', un_number = 'UN1203'`,
		"an unknown spot kind":        `UPDATE locations SET spot_kind = 'COLD_STORE'`,
	} {
		if err := pgtest.Exec(pool, stmt); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}

	if err := m.Steps(-1); err != nil {
		t.Fatalf("down from 5: %v", err)
	}
	var cols int
	pgtest.Scan(t, pool, `SELECT count(*) FROM information_schema.columns WHERE column_name IN
		('hazmat_class', 'un_number', 'requires_temperature_control', 'spot_kind', 'allow_hazmat_on_unclassified_spots')`, nil, &cols)
	if cols != 0 {
		t.Fatalf("%d new columns left after down", cols)
	}
	if err := m.Migrate(5); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
