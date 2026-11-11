//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/claudioed/yard-management/internal/adapters/outbound/postgres"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
)

const oldTrailerRow = `INSERT INTO trailers (trailer_id, site_id, scac, unit_number, kind, load_state, purpose, seal_number, seal_state, state,
	visit_id, arrived_at, version) VALUES ($1, 'WH1', 'ACME', $2, $3, 'LOADED', 'LIVE_UNLOAD', $4, $5, 'IN_MOVE', 'visit-1', now(), 1)`

// Migration 0003 (ADR 0012) is additive: rows written by the previous release
// (a CONTAINER with a free-form number, INTACT and BROKEN seals, a policy with
// no minimum class) load unchanged, the new vocabulary is then storable, and
// going down collapses the new seal states to INTACT.
func TestMigration0003KeepsExistingRowsAndGoesDown(t *testing.T) {
	url := pgtest.EmptyURL(t)
	m, err := postgres.Migrator(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Migrate(2); err != nil {
		t.Fatalf("migrate to 2: %v", err)
	}
	pool := pgtest.Open(t, url)
	for _, r := range []struct{ id, unit, kind, seal, state string }{
		{"trl-old-1", "OLD-BOX-1", "CONTAINER", "SL-1", "INTACT"},
		{"trl-old-2", "TR-77", "DRY_VAN", "SL-2", "BROKEN"},
		{"trl-old-3", "TR-78", "DRY_VAN", "", "NONE"},
	} {
		if err := pgtest.Exec(pool, oldTrailerRow, r.id, r.unit, r.kind, r.seal, r.state); err != nil {
			t.Fatalf("insert the old row %s: %v", r.id, err)
		}
	}
	if err := pgtest.Exec(pool, `INSERT INTO yard_policies (site_id, require_appointment, lease_seconds, seal_mismatch_action, declared, version)
		VALUES ('WH1', false, 300, 'FLAG', true, 2)`); err != nil {
		t.Fatal(err)
	}
	if err := m.Migrate(3); err != nil {
		t.Fatalf("migrate to 3: %v", err)
	}

	trailers, policies := postgres.NewTrailers(pool), postgres.NewPolicies(pool)
	ctx := context.Background()
	for id, want := range map[string]trailer.SealState{"trl-old-1": trailer.SealIntact, "trl-old-2": trailer.SealBroken, "trl-old-3": trailer.SealNone} {
		tr, err := trailers.FindByID(ctx, id)
		if err != nil {
			t.Fatalf("%s must load after the migration: %v", id, err)
		}
		if s := tr.Snapshot(); s.SealState != want || s.SealClass != "" || s.UnitScheme != "" {
			t.Errorf("%s: %+v", id, s)
		}
	}
	p, err := policies.Find(ctx, "WH1")
	if err != nil || p.MinimumSealClass() != "" {
		t.Fatalf("the old policy has no minimum: %v", err)
	}

	for _, state := range []string{"APPLIED", "VERIFIED", "MISMATCH"} {
		if err := pgtest.Exec(pool, oldTrailerRow, "trl-new-"+state, "N-"+state, "DRY_VAN", "SL-9", state); err != nil {
			t.Fatalf("the new seal state %s must be storable: %v", state, err)
		}
	}
	if err := pgtest.Exec(pool, oldTrailerRow, "trl-bad", "N-BAD", "DRY_VAN", "SL-9", "WEIRD"); err == nil {
		t.Fatal("an unknown seal state must still be refused")
	}
	if err := pgtest.Exec(pool, `UPDATE trailers SET seal_class = 'X' WHERE trailer_id = 'trl-old-1'`); err == nil {
		t.Fatal("an unknown seal class must be refused")
	}
	if err := pgtest.Exec(pool, `UPDATE yard_policies SET minimum_seal_class = 'X'`); err == nil {
		t.Fatal("an unknown minimum seal class must be refused")
	}
	var restored int
	for _, id := range []string{"trl-new-APPLIED", "trl-new-VERIFIED", "trl-new-MISMATCH"} {
		if _, err := trailers.FindByID(ctx, id); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		restored++
	}

	// Down: the new states collapse to INTACT, the old rows stay.
	if err := m.Steps(-1); err != nil {
		t.Fatalf("down from 3: %v", err)
	}
	var intact int
	pgtest.Scan(t, pool, `SELECT count(*) FROM trailers WHERE seal_state = 'INTACT'`, nil, &intact)
	if intact != 1+restored {
		t.Fatalf("INTACT rows after down = %d, want %d", intact, 1+restored)
	}
	var cols int
	pgtest.Scan(t, pool, `SELECT count(*) FROM information_schema.columns WHERE table_name IN ('trailers', 'yard_policies') AND column_name IN ('seal_class', 'unit_number_scheme', 'minimum_seal_class')`, nil, &cols)
	if cols != 0 {
		t.Fatalf("%d new columns left after down", cols)
	}
	if err := m.Migrate(3); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
