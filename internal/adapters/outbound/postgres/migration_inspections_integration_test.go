//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/claudioed/yard-management/internal/adapters/outbound/postgres"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
)

const inspectionRow = `INSERT INTO inspections (inspection_id, site_id, trailer_id, template_id, template_version, gate, status, items, opened_at, version)
	VALUES ($1, 'WH1', 'trl-old-1', 'yard-entry', 1, $2, $3, '[]'::jsonb, now(), 1)`

// Migration 0006 (ADR 0015) is additive: a policy written by the previous
// release loads with no required inspection (so no gate is ever closed), the
// two new tables take templates and inspections and refuse what the domain
// refuses, and going down drops only what 0006 added.
func TestMigration0006KeepsExistingRowsAndGoesDown(t *testing.T) {
	url := pgtest.EmptyURL(t)
	m, err := postgres.Migrator(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Migrate(5); err != nil {
		t.Fatalf("migrate to 5: %v", err)
	}
	pool := pgtest.Open(t, url)
	if err := pgtest.Exec(pool, oldTrailerRow, "trl-old-1", "TR-1", "DRY_VAN", "SL-1", "INTACT"); err != nil {
		t.Fatalf("insert the old trailer: %v", err)
	}
	if err := pgtest.Exec(pool, `INSERT INTO yard_policies (site_id, require_appointment, lease_seconds, seal_mismatch_action, declared, version)
		VALUES ('WH1', false, 300, 'FLAG', true, 2)`); err != nil {
		t.Fatal(err)
	}
	if err := m.Migrate(6); err != nil {
		t.Fatalf("migrate to 6: %v", err)
	}

	p, err := postgres.NewPolicies(pool).Find(context.Background(), "WH1")
	if err != nil || len(p.RequiredInspections()) != 0 {
		t.Fatalf("the old policy requires no inspection: %v %v", err, p)
	}

	if err := pgtest.Exec(pool, `INSERT INTO inspection_templates (template_id, version, title, items, published_at) VALUES ('yard-entry', 1, 'T', '[]'::jsonb, now())`); err != nil {
		t.Fatalf("a template must be storable: %v", err)
	}
	if err := pgtest.Exec(pool, `INSERT INTO inspection_templates (template_id, version, title, items, published_at) VALUES ('yard-entry', 1, 'Other', '[]'::jsonb, now())`); err == nil {
		t.Error("a published template version is never written twice")
	}
	if err := pgtest.Exec(pool, inspectionRow, "insp-1", "PRE_DOCK", "OPEN"); err != nil {
		t.Fatalf("an open inspection must be storable: %v", err)
	}
	if err := pgtest.Exec(pool, inspectionRow, "insp-2", "PRE_DOCK", "OPEN"); err == nil {
		t.Error("a second OPEN inspection for the same trailer, template and gate must be refused")
	}
	if err := pgtest.Exec(pool, inspectionRow, "insp-3", "PRE_LOAD", "OPEN"); err != nil {
		t.Errorf("another gate may be open at once: %v", err)
	}
	for name, args := range map[string][]any{
		"an unknown gate":   {"insp-4", "SOMETIME", "OPEN"},
		"an unknown status": {"insp-5", "PRE_DEPARTURE", "DONE"},
		"a closed inspection without a completion instant": {"insp-6", "PRE_DEPARTURE", "PASSED"},
	} {
		if err := pgtest.Exec(pool, inspectionRow, args...); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}

	if err := m.Steps(-1); err != nil {
		t.Fatalf("down from 6: %v", err)
	}
	var tables, cols int
	pgtest.Scan(t, pool, `SELECT count(*) FROM information_schema.tables WHERE table_name IN ('inspections', 'inspection_templates')`, nil, &tables)
	pgtest.Scan(t, pool, `SELECT count(*) FROM information_schema.columns WHERE table_name = 'yard_policies' AND column_name = 'required_inspections'`, nil, &cols)
	if tables+cols != 0 {
		t.Fatalf("%d tables and %d columns of 0006 left after down", tables, cols)
	}
	if err := m.Migrate(6); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
