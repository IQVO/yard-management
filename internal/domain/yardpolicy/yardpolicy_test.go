package yardpolicy_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
)

func known(codes ...string) func(string) bool {
	return func(c string) bool {
		for _, k := range codes {
			if k == c {
				return true
			}
		}
		return false
	}
}

func declareInput() yardpolicy.DeclareInput {
	return yardpolicy.DeclareInput{
		RequireAppointment: true, HoldingSpotCodes: []string{"H1", "H2"}, ParkingZoneIDs: []string{"z1"},
		DwellThresholdMinutes: map[shared.TrailerKind]int64{shared.KindDryVan: 1440, shared.KindReefer: 720},
		LeaseSeconds:          120, SealMismatchAction: yardpolicy.Reject, FreeTimeMinutes: 90,
	}
}

func TestDefaultPolicy(t *testing.T) {
	p, err := yardpolicy.Default("WH1")
	if err != nil {
		t.Fatal(err)
	}
	if p.SiteID() != "WH1" || p.RequireAppointment() || p.SealMismatchAction() != yardpolicy.Flag || p.Lease() != 300*time.Second || p.Declared() || p.Version() != 1 ||
		p.FreeTimeMinutes() != 0 || len(p.HoldingSpotCodes()) != 0 || len(p.ParkingZoneIDs()) != 0 || p.DwellThreshold(shared.KindDryVan) != 0 || p.HoldingSpotCodes() == nil {
		t.Fatalf("default %+v", p.Snapshot())
	}
	if _, err := yardpolicy.Default(" "); !errors.Is(err, shared.ErrValidation) {
		t.Errorf("blank site: %v", err)
	}
}

func TestDeclare(t *testing.T) {
	p, _ := yardpolicy.Default("WH1")
	if err := p.Declare(declareInput(), known("H1", "H2")); err != nil {
		t.Fatal(err)
	}
	want := yardpolicy.Snapshot{
		SiteID: "WH1", RequireAppointment: true, HoldingSpotCodes: []string{"H1", "H2"}, ParkingZoneIDs: []string{"z1"},
		DwellThresholdMinutes: map[shared.TrailerKind]int64{shared.KindDryVan: 1440, shared.KindReefer: 720},
		LeaseSeconds:          120, SealMismatchAction: yardpolicy.Reject, FreeTimeMinutes: 90, RequiredInspections: []yardpolicy.InspectionRule{}, SuspendedCarrierAction: yardpolicy.CarrierNone, Version: 2, Declared: true,
	}
	if got := p.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("declared\n got %+v\nwant %+v", got, want)
	}
	checks := map[string]bool{
		"require": p.RequireAppointment(), "action": p.SealMismatchAction() == yardpolicy.Reject, "lease": p.Lease() == 120*time.Second,
		"declared": p.Declared(), "version": p.Version() == 2, "free time": p.FreeTimeMinutes() == 90, "dry van": p.DwellThreshold(shared.KindDryVan) == 1440,
		"reefer": p.DwellThreshold(shared.KindReefer) == 720, "flatbed": p.DwellThreshold(shared.KindFlatbed) == 0,
		"spots": reflect.DeepEqual(p.HoldingSpotCodes(), []string{"H1", "H2"}), "zones": reflect.DeepEqual(p.ParkingZoneIDs(), []string{"z1"}),
	}
	for name, ok := range checks {
		if !ok {
			t.Errorf("accessor %s", name)
		}
	}
}

func TestRedeclareReplacesEverything(t *testing.T) {
	p, _ := yardpolicy.Default("WH1")
	if err := p.Declare(declareInput(), known("H1", "H2")); err != nil {
		t.Fatal(err)
	}
	in := declareInput()
	in.HoldingSpotCodes, in.ParkingZoneIDs, in.DwellThresholdMinutes, in.FreeTimeMinutes = nil, nil, nil, 0
	if err := p.Declare(in, known()); err != nil {
		t.Fatal(err)
	}
	if p.Version() != 3 || p.HoldingSpotCodes() == nil || len(p.HoldingSpotCodes()) != 0 || p.ParkingZoneIDs() == nil || p.FreeTimeMinutes() != 0 || p.DwellThreshold(shared.KindDryVan) != 0 {
		t.Fatal("a redeclaration replaces everything and bumps the version")
	}
}

func TestDeclareDoesNotAliasItsInput(t *testing.T) {
	p, _ := yardpolicy.Default("WH1")
	in := declareInput()
	if err := p.Declare(in, known("H1", "H2")); err != nil {
		t.Fatal(err)
	}
	in.HoldingSpotCodes[0] = "HX"
	in.DwellThresholdMinutes[shared.KindDryVan] = 1
	got := p.HoldingSpotCodes()
	got[1] = "HY"
	snap := p.Snapshot()
	snap.DwellThresholdMinutes[shared.KindReefer] = 1
	if p.HoldingSpotCodes()[0] != "H1" || p.HoldingSpotCodes()[1] != "H2" || p.DwellThreshold(shared.KindDryVan) != 1440 || p.DwellThreshold(shared.KindReefer) != 720 {
		t.Fatal("the policy must own its data")
	}
}

func TestDeclareRefusals(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*yardpolicy.DeclareInput)
		err  error
	}{
		{"zero lease", func(in *yardpolicy.DeclareInput) { in.LeaseSeconds = 0 }, shared.ErrValidation},
		{"bad seal action", func(in *yardpolicy.DeclareInput) { in.SealMismatchAction = "IGNORE" }, shared.ErrValidation},
		{"negative free time", func(in *yardpolicy.DeclareInput) { in.FreeTimeMinutes = -1 }, shared.ErrValidation},
		{"zero threshold", func(in *yardpolicy.DeclareInput) { in.DwellThresholdMinutes[shared.KindDryVan] = 0 }, shared.ErrValidation},
		{"bad kind", func(in *yardpolicy.DeclareInput) { in.DwellThresholdMinutes["BOX"] = 5 }, shared.ErrValidation},
		{"blank holding spot", func(in *yardpolicy.DeclareInput) { in.HoldingSpotCodes = []string{" "} }, shared.ErrValidation},
		{"duplicate holding spot", func(in *yardpolicy.DeclareInput) { in.HoldingSpotCodes = []string{"H1", "H1"} }, shared.ErrValidation},
		{"blank zone", func(in *yardpolicy.DeclareInput) { in.ParkingZoneIDs = []string{""} }, shared.ErrValidation},
		{"duplicate zone", func(in *yardpolicy.DeclareInput) { in.ParkingZoneIDs = []string{"z", "z"} }, shared.ErrValidation},
		{"long zone", func(in *yardpolicy.DeclareInput) { in.ParkingZoneIDs = []string{strings.Repeat("z", 129)} }, shared.ErrValidation},
		{"unknown holding spot", func(in *yardpolicy.DeclareInput) { in.HoldingSpotCodes = []string{"H1", "H9"} }, shared.ErrUnknownLocation},
	}
	for _, c := range cases {
		p, _ := yardpolicy.Default("WH1")
		in := declareInput()
		c.mod(&in)
		if err := p.Declare(in, known("H1", "H2")); !errors.Is(err, c.err) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.err)
		}
		if p.Declared() || p.Version() != 1 || p.RequireAppointment() {
			t.Errorf("%s: a refused declaration changes nothing", c.name)
		}
	}
	p, _ := yardpolicy.Default("WH1")
	in := declareInput()
	in.LeaseSeconds, in.DwellThresholdMinutes = 1, map[shared.TrailerKind]int64{shared.KindContainer: 1}
	if err := p.Declare(in, known("H1", "H2")); err != nil {
		t.Errorf("lease and threshold of 1 are valid: %v", err)
	}
	in = declareInput()
	in.FreeTimeMinutes = 1
	if err := p.Declare(in, known("H1", "H2")); err != nil {
		t.Errorf("free time of 1 is valid: %v", err)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	d, _ := yardpolicy.Default("WH1")
	decl, _ := yardpolicy.Default("WH1")
	if err := decl.Declare(declareInput(), known("H1", "H2")); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]*yardpolicy.Policy{"default": d, "declared": decl} {
		want := p.Snapshot()
		got, err := yardpolicy.Restore(want)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(got.Snapshot(), want) {
			t.Errorf("%s: round trip differs:\n%+v\n%+v", name, got.Snapshot(), want)
		}
	}
}

func TestRestoreRejectsCorruptRows(t *testing.T) {
	good := func() yardpolicy.Snapshot {
		p, _ := yardpolicy.Default("WH1")
		_ = p.Declare(declareInput(), known("H1", "H2"))
		return p.Snapshot()
	}
	mod := func(f func(*yardpolicy.Snapshot)) yardpolicy.Snapshot { s := good(); f(&s); return s }
	for name, s := range map[string]yardpolicy.Snapshot{
		"blank site": mod(func(s *yardpolicy.Snapshot) { s.SiteID = "" }),
		"version 0":  mod(func(s *yardpolicy.Snapshot) { s.Version = 0 }),
		"zero lease": mod(func(s *yardpolicy.Snapshot) { s.LeaseSeconds = 0 }),
		"bad action": mod(func(s *yardpolicy.Snapshot) { s.SealMismatchAction = "" }),
		"bad dwell": mod(func(s *yardpolicy.Snapshot) {
			s.DwellThresholdMinutes = map[shared.TrailerKind]int64{shared.KindDryVan: -1}
		}),
	} {
		if _, err := yardpolicy.Restore(s); err == nil {
			t.Errorf("%s: Restore accepted a corrupt snapshot", name)
		}
	}
}

func TestMinimumSealClass(t *testing.T) {
	p, _ := yardpolicy.Default("WH1")
	if p.MinimumSealClass() != "" {
		t.Fatal("the default policy has no minimum seal class")
	}
	in := declareInput()
	in.MinimumSealClass = shared.SealClassHighSecurity
	if err := p.Declare(in, known("H1", "H2")); err != nil {
		t.Fatal(err)
	}
	if p.MinimumSealClass() != shared.SealClassHighSecurity || p.Snapshot().MinimumSealClass != shared.SealClassHighSecurity {
		t.Fatalf("declared %+v", p.Snapshot())
	}
	restored, err := yardpolicy.Restore(p.Snapshot())
	if err != nil || restored.MinimumSealClass() != shared.SealClassHighSecurity {
		t.Fatalf("round trip: %v", err)
	}
	if err := p.Declare(declareInput(), known("H1", "H2")); err != nil || p.MinimumSealClass() != "" {
		t.Fatalf("a replacement without the field clears the minimum: %v", err)
	}
	in.MinimumSealClass = "X"
	if err := p.Declare(in, known("H1", "H2")); !errors.Is(err, shared.ErrInvalidSealClass) {
		t.Errorf("unknown class: %v", err)
	}
}

func TestRequireSecuringBeforeDoorWork(t *testing.T) {
	p, _ := yardpolicy.Default("WH1")
	if p.RequireSecuringBeforeDoorWork() {
		t.Fatal("the default policy has the interlock off: door work starts as it always did")
	}
	in := declareInput()
	in.RequireSecuringBeforeDoorWork = true
	if err := p.Declare(in, known("H1", "H2")); err != nil {
		t.Fatal(err)
	}
	if !p.RequireSecuringBeforeDoorWork() || !p.Snapshot().RequireSecuring {
		t.Fatalf("declared %+v", p.Snapshot())
	}
	restored, err := yardpolicy.Restore(p.Snapshot())
	if err != nil || !restored.RequireSecuringBeforeDoorWork() {
		t.Fatalf("round trip: %v", err)
	}
	if err := p.Declare(declareInput(), known("H1", "H2")); err != nil || p.RequireSecuringBeforeDoorWork() {
		t.Fatalf("a replacement without the field turns the interlock off: %v", err)
	}
}

func TestAllowHazmatOnUnclassifiedSpots(t *testing.T) {
	p, _ := yardpolicy.Default("WH1")
	if p.AllowHazmatOnUnclassifiedSpots() {
		t.Fatal("the default policy refuses a hazmat trailer on a spot with no kind")
	}
	in := declareInput()
	in.AllowHazmatOnUnclassifiedSpots = true
	if err := p.Declare(in, known("H1", "H2")); err != nil {
		t.Fatal(err)
	}
	if !p.AllowHazmatOnUnclassifiedSpots() || !p.Snapshot().AllowHazmatOnUnclassified {
		t.Fatalf("declared %+v", p.Snapshot())
	}
	restored, err := yardpolicy.Restore(p.Snapshot())
	if err != nil || !restored.AllowHazmatOnUnclassifiedSpots() {
		t.Fatalf("round trip: %v", err)
	}
	if err := p.Declare(declareInput(), known("H1", "H2")); err != nil || p.AllowHazmatOnUnclassifiedSpots() {
		t.Fatalf("a replacement without the field turns it off: %v", err)
	}
}
