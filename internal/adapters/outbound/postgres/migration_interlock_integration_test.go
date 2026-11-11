//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/claudioed/yard-management/internal/adapters/outbound/postgres"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
)

// Migration 0004 (ADR 0013) is additive: rows written by the previous release
// load unchanged as "not secured" with the policy off, a securing is then
// storable and consistent, and going down drops only the new columns.
func TestMigration0004KeepsExistingRowsAndGoesDown(t *testing.T) {
	url := pgtest.EmptyURL(t)
	m, err := postgres.Migrator(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Migrate(3); err != nil {
		t.Fatalf("migrate to 3: %v", err)
	}
	pool := pgtest.Open(t, url)
	if err := pgtest.Exec(pool, oldTrailerRow, "trl-old-1", "TR-1", "DRY_VAN", "SL-1", "INTACT"); err != nil {
		t.Fatalf("insert the old row: %v", err)
	}
	if err := pgtest.Exec(pool, `INSERT INTO yard_policies (site_id, require_appointment, lease_seconds, seal_mismatch_action, declared, version)
		VALUES ('WH1', false, 300, 'FLAG', true, 2)`); err != nil {
		t.Fatal(err)
	}
	if err := m.Migrate(4); err != nil {
		t.Fatalf("migrate to 4: %v", err)
	}

	ctx := context.Background()
	tr, err := postgres.NewTrailers(pool).FindByID(ctx, "trl-old-1")
	if err != nil {
		t.Fatalf("the old trailer must load after the migration: %v", err)
	}
	if s := tr.Snapshot(); s.SecuredBy != "" || !s.SecuredAt.IsZero() || s.SecurerRef != "" {
		t.Fatalf("an old trailer is not secured: %+v", s)
	}
	p, err := postgres.NewPolicies(pool).Find(ctx, "WH1")
	if err != nil || p.RequireSecuringBeforeDoorWork() {
		t.Fatalf("the old policy has the interlock off: %v", err)
	}

	if err := pgtest.Exec(pool, `UPDATE trailers SET secured_by = 'RESTRAINT', secured_at = now() WHERE trailer_id = 'trl-old-1'`); err != nil {
		t.Fatalf("a securing must be storable: %v", err)
	}
	for name, stmt := range map[string]string{
		"an unknown method":            `UPDATE trailers SET secured_by = 'LOCKS', secured_at = now()`,
		"a method without an instant":  `UPDATE trailers SET secured_by = 'WHEEL_CHOCKS', secured_at = NULL`,
		"an instant without a method":  `UPDATE trailers SET secured_by = '', secured_at = now()`,
		"a performer without a method": `UPDATE trailers SET secured_by = '', secured_at = NULL, secured_by_ref = 'op-1'`,
	} {
		if err := pgtest.Exec(pool, stmt); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}

	if err := m.Steps(-1); err != nil {
		t.Fatalf("down from 4: %v", err)
	}
	var cols int
	pgtest.Scan(t, pool, `SELECT count(*) FROM information_schema.columns WHERE table_name IN ('trailers', 'yard_policies')
		AND column_name IN ('secured_by', 'secured_at', 'secured_by_ref', 'require_securing_before_door_work')`, nil, &cols)
	if cols != 0 {
		t.Fatalf("%d new columns left after down", cols)
	}
	if err := m.Migrate(4); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
