package repocontract

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/yardcheck"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
)

// CheckRepo runs the CheckRepo contract.
func CheckRepo(t *testing.T, newStore NewStore) {
	t.Helper()
	run(t, newStore, map[string]func(t *testing.T, s Store){
		"an unknown check is not found": func(t *testing.T, s Store) {
			_, err := s.Checks.FindByID(bg, "yck-404")
			wantErr(t, err, ports.ErrNotFound)
		},
		"save then find round trips an open and a closed check": func(t *testing.T, s Store) {
			c := NewCheck(t, "yck-1", "WH1")
			must(t, s.Checks.Save(bg, c))
			for _, o := range [][2]string{{"WH1-P1", "T1"}, {"WH1-P2", ""}, {"WH1-P3", "ROGUE"}} {
				must(t, c.RecordObservation(o[0], o[1]))
				must(t, s.Checks.Save(bg, c))
			}
			check := func() {
				t.Helper()
				got, err := s.Checks.FindByID(bg, "yck-1")
				must(t, err)
				if !reflect.DeepEqual(got.Snapshot(), c.Snapshot()) {
					t.Fatalf("round trip changed the state:\ngot  %+v\nwant %+v", got.Snapshot(), c.Snapshot())
				}
			}
			check()
			must(t, c.Close([]yardcheck.Expected{{LocationCode: "WH1-P1", TrailerID: "trl-1", UnitNumber: "T1"}, {LocationCode: "WH1-P2", TrailerID: "trl-2", UnitNumber: "T2"}}, T0.Add(time.Hour)))
			must(t, s.Checks.Save(bg, c))
			check()
			if len(c.Snapshot().Discrepancies) != 2 {
				t.Fatalf("discrepancies %+v", c.Snapshot().Discrepancies)
			}
		},
		"one open check per site": func(t *testing.T, s Store) {
			first := NewCheck(t, "yck-1", "WH1")
			must(t, s.Checks.Save(bg, first))
			wantErr(t, s.Checks.Save(bg, NewCheck(t, "yck-2", "WH1")), yardcheck.ErrYardCheckOpen)
			must(t, s.Checks.Save(bg, NewCheck(t, "yck-3", "WH2")))
			must(t, first.Close(nil, T0.Add(time.Hour)))
			must(t, s.Checks.Save(bg, first))
			must(t, s.Checks.Save(bg, NewCheck(t, "yck-4", "WH1"))) // closed: a new audit may open
		},
		"the version guard refuses stale writers": func(t *testing.T, s Store) {
			c := NewCheck(t, "yck-1", "WH1")
			must(t, s.Checks.Save(bg, c))
			wantErr(t, s.Checks.Save(bg, c), ports.ErrConcurrentModification)
			one, err := s.Checks.FindByID(bg, "yck-1")
			must(t, err)
			two, err := s.Checks.FindByID(bg, "yck-1")
			must(t, err)
			must(t, one.RecordObservation("WH1-P1", ""))
			must(t, two.RecordObservation("WH1-P2", ""))
			must(t, s.Checks.Save(bg, one))
			wantErr(t, s.Checks.Save(bg, two), ports.ErrConcurrentModification)
		},
	})
}

// PolicyRepo runs the PolicyRepo contract.
func PolicyRepo(t *testing.T, newStore NewStore) {
	t.Helper()
	run(t, newStore, map[string]func(t *testing.T, s Store){
		"an undeclared site is not found": func(t *testing.T, s Store) {
			_, err := s.Policies.Find(bg, "WH1")
			wantErr(t, err, ports.ErrNotFound)
		},
		"save then find round trips a declared policy": func(t *testing.T, s Store) {
			p := Declared(t, "WH1")
			must(t, s.Policies.Save(bg, p))
			got, err := s.Policies.Find(bg, "WH1")
			must(t, err)
			if !reflect.DeepEqual(got.Snapshot(), p.Snapshot()) {
				t.Fatalf("round trip changed the state:\ngot  %+v\nwant %+v", got.Snapshot(), p.Snapshot())
			}
			if got.Version() != 2 || !got.Declared() || got.Lease() != 2*time.Minute || got.SealMismatchAction() != yardpolicy.Reject || got.MinimumSealClass() != shared.SealClassHighSecurity || !got.RequireSecuringBeforeDoorWork() || !got.AllowHazmatOnUnclassifiedSpots() {
				t.Fatalf("stored %+v", got.Snapshot())
			}
		},
		"an empty declaration round trips": func(t *testing.T, s Store) {
			p, err := yardpolicy.Default("WH1")
			must(t, err)
			must(t, p.Declare(yardpolicy.DeclareInput{LeaseSeconds: 300, SealMismatchAction: yardpolicy.Flag}, func(string) bool { return true }))
			must(t, s.Policies.Save(bg, p))
			got, err := s.Policies.Find(bg, "WH1")
			must(t, err)
			if !reflect.DeepEqual(got.Snapshot(), p.Snapshot()) {
				t.Fatalf("round trip changed the state:\ngot  %+v\nwant %+v", got.Snapshot(), p.Snapshot())
			}
		},
		"the first stored version is 2 and later ones are version guarded": func(t *testing.T, s Store) {
			def, err := yardpolicy.Default("WH1")
			must(t, err)
			wantErr(t, s.Policies.Save(bg, def), ports.ErrConcurrentModification) // the default (version 1) is never stored
			p := Declared(t, "WH1")
			must(t, s.Policies.Save(bg, p))
			wantErr(t, s.Policies.Save(bg, p), ports.ErrConcurrentModification) // re-saving version 2
			stale, err := s.Policies.Find(bg, "WH1")
			must(t, err)
			again := redeclare(t, p, 60)
			must(t, s.Policies.Save(bg, again))
			wantErr(t, s.Policies.Save(bg, redeclare(t, stale, 30)), ports.ErrConcurrentModification)
			got, err := s.Policies.Find(bg, "WH1")
			must(t, err)
			if got.Version() != 3 || got.Lease() != time.Minute {
				t.Fatalf("stored %+v", got.Snapshot())
			}
			future := Declared(t, "WH2")
			must(t, future.Declare(yardpolicy.DeclareInput{LeaseSeconds: 10, SealMismatchAction: yardpolicy.Flag}, func(string) bool { return true }))
			wantErr(t, s.Policies.Save(bg, future), ports.ErrConcurrentModification) // version 3 of a site never stored
		},
	})
}

func redeclare(t *testing.T, p *yardpolicy.Policy, lease int) *yardpolicy.Policy {
	t.Helper()
	cp, err := yardpolicy.Restore(p.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if err := cp.Declare(yardpolicy.DeclareInput{LeaseSeconds: lease, SealMismatchAction: yardpolicy.Flag, DwellThresholdMinutes: map[shared.TrailerKind]int64{}}, func(string) bool { return true }); err != nil {
		t.Fatal(err)
	}
	return cp
}
