package trailer_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
)

func classified(t *testing.T, hazmat, un string, cold bool, mod ...func(*trailer.ArrivalInput)) *trailer.Trailer {
	t.Helper()
	c, err := trailer.NewClassification(hazmat, un, cold)
	must(t, err)
	return newTrailer(t, append([]func(*trailer.ArrivalInput){func(in *trailer.ArrivalInput) { in.Classification = c }}, mod...)...)
}

func TestNewClassification(t *testing.T) {
	c, err := trailer.NewClassification("3", "UN1203", true)
	if err != nil || c.HazmatClass != "3" || c.UNNumber != "UN1203" || !c.RequiresTemperatureControl || !c.Declared() {
		t.Fatalf("%+v %v", c, err)
	}
	if c, err := trailer.NewClassification("", "", false); err != nil || c.Declared() {
		t.Fatalf("nothing declared: %+v %v", c, err)
	}
	if c, err := trailer.NewClassification("", "", true); err != nil || !c.Declared() {
		t.Fatalf("a temperature need is a declaration: %+v %v", c, err)
	}
	if _, err := trailer.NewClassification("1.7", "", false); !errors.Is(err, shared.ErrInvalidHazmatClass) {
		t.Errorf("class: %v", err)
	}
	if _, err := trailer.NewClassification("3", "1203", false); !errors.Is(err, shared.ErrInvalidUNNumber) {
		t.Errorf("un: %v", err)
	}
	if _, err := trailer.NewClassification("", "UN1203", false); !errors.Is(err, shared.ErrValidation) || !strings.Contains(err.Error(), "needs a hazmat class") {
		t.Errorf("a UN number needs a class: %v", err)
	}
}

func TestRecordArrivalCarriesTheClassification(t *testing.T) {
	tr := classified(t, "3", "UN1203", true)
	if got := tr.Classification(); got.HazmatClass != "3" || got.UNNumber != "UN1203" || !got.RequiresTemperatureControl {
		t.Fatalf("%+v", got)
	}
	if sn := tr.Snapshot(); sn.Classification != tr.Classification() {
		t.Fatalf("snapshot %+v", sn)
	}
	for name, in := range map[string]trailer.Classification{
		"class":       {HazmatClass: "11"},
		"un":          {HazmatClass: "3", UNNumber: "UN12"},
		"un no class": {UNNumber: "UN1203"},
	} {
		arr := arrival()
		arr.Classification = in
		if _, err := trailer.RecordArrival(arr); err == nil {
			t.Errorf("%s: an invalid classification refuses the arrival", name)
		}
	}
}

func TestClassify(t *testing.T) {
	tr := parked(t)
	c := trailer.Classification{HazmatClass: "8", RequiresTemperatureControl: true}
	changed, err := tr.Classify(c)
	if err != nil || !changed || tr.Classification() != c || tr.Version() != 3 || len(tr.PullEvents()) != 0 {
		t.Fatalf("classify: changed=%v err=%v version=%d", changed, err, tr.Version())
	}
	if changed, err := tr.Classify(c); err != nil || changed || tr.Version() != 3 {
		t.Fatalf("the same classification again changes nothing: changed=%v err=%v version=%d", changed, err, tr.Version())
	}
	if changed, err := tr.Classify(trailer.Classification{}); err != nil || !changed || tr.Classification().Declared() {
		t.Fatalf("clearing: changed=%v err=%v", changed, err)
	}
	if _, err := tr.Classify(trailer.Classification{HazmatClass: "x"}); !errors.Is(err, shared.ErrInvalidHazmatClass) || tr.Version() != 4 {
		t.Fatalf("invalid: %v version=%d", err, tr.Version())
	}
}

func TestClassifyInAnyLiveStateButNotAfterDeparture(t *testing.T) {
	c := trailer.Classification{HazmatClass: "8", RequiresTemperatureControl: true}
	for name, tr := range map[string]*trailer.Trailer{"in move": newTrailer(t), "docked": docked(t)} {
		if changed, err := tr.Classify(c); err != nil || !changed {
			t.Errorf("%s may be classified: %v", name, err)
		}
	}
	dep := parked(t)
	must(t, dep.Depart("v", at(5)))
	if _, err := dep.Classify(c); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("departed: %v", err)
	}
}

func TestSnapshotRestoreKeepsAndValidatesTheClassification(t *testing.T) {
	tr := classified(t, "6.1", "UN2810", false)
	got, err := trailer.Restore(tr.Snapshot())
	if err != nil || got.Classification() != tr.Classification() {
		t.Fatalf("restore: %v", err)
	}
	if _, err := trailer.Restore(parked(t).Snapshot()); err != nil {
		t.Fatalf("a row from before ADR 0014 loads: %v", err)
	}
	for name, c := range map[string]trailer.Classification{
		"bad class": {HazmatClass: "99"}, "bad un": {HazmatClass: "3", UNNumber: "x"}, "un no class": {UNNumber: "UN1203"},
	} {
		s := parked(t).Snapshot()
		s.Classification = c
		if _, err := trailer.Restore(s); err == nil {
			t.Errorf("%s: a corrupt row must not load", name)
		}
	}
}

func TestSpotRule(t *testing.T) {
	haz := classified(t, "3", "", false)
	cold := classified(t, "", "", true)
	both := classified(t, "5.2", "", true)
	plain := newTrailer(t)
	none, hazSite, plugSite, bothSite := trailer.SiteSpots{}, trailer.SiteSpots{HasHazmat: true}, trailer.SiteSpots{HasReeferPlug: true}, trailer.SiteSpots{HasHazmat: true, HasReeferPlug: true}
	allow := trailer.SiteSpots{AllowHazmatOnUnclassified: true}
	unk, hz, rp, pk := shared.SpotKindUnknown, shared.SpotKindHazmat, shared.SpotKindReeferPlug, shared.SpotKindParking
	for _, c := range []struct {
		name   string
		tr     *trailer.Trailer
		site   trailer.SiteSpots
		allows map[shared.SpotKind]bool // kinds checked; absent = not asserted
		free   bool                     // not restricted
	}{
		{"plain trailer at a classified site", plain, bothSite, nil, true},
		{"hazmat at a site with a HAZMAT spot", haz, hazSite, map[shared.SpotKind]bool{hz: true, pk: false, rp: false, unk: false}, false},
		{"hazmat with a HAZMAT spot ignores the policy", haz, trailer.SiteSpots{HasHazmat: true, AllowHazmatOnUnclassified: true}, map[shared.SpotKind]bool{hz: true, pk: false, unk: false}, false},
		{"hazmat, no HAZMAT spot, policy off", haz, none, map[shared.SpotKind]bool{hz: false, pk: false, unk: false}, false},
		{"hazmat, no HAZMAT spot, policy on", haz, allow, map[shared.SpotKind]bool{unk: true, pk: false, hz: false, rp: false}, false},
		{"reefer at a site with plugs", cold, plugSite, map[shared.SpotKind]bool{rp: true, pk: false, unk: false, hz: false}, false},
		{"reefer at a site with no plug", cold, none, nil, true},
		{"reefer at a site with only hazmat spots", cold, hazSite, nil, true},
		{"both is judged by the hazmat rule", both, bothSite, map[shared.SpotKind]bool{hz: true, rp: false, pk: false}, false},
		{"both at a site with plugs only", both, plugSite, map[shared.SpotKind]bool{hz: false, rp: false, unk: false}, false},
	} {
		rule := c.tr.SpotRule(c.site)
		if rule.Restricted == c.free {
			t.Errorf("%s: restricted=%v", c.name, rule.Restricted)
		}
		if !c.free && rule.Why == "" {
			t.Errorf("%s: a restricted rule names itself", c.name)
		}
		for kind, want := range c.allows {
			if got := rule.Allows(kind); got != want {
				t.Errorf("%s: Allows(%q) = %v, want %v", c.name, kind, got, want)
			}
		}
		if c.free && !rule.Allows(pk) || c.free && !rule.Allows(unk) {
			t.Errorf("%s: an unrestricted rule allows every kind", c.name)
		}
	}
}

func TestSpotRuleViolationNamesTheRuleAndTheSpot(t *testing.T) {
	haz := classified(t, "2.1", "", false)
	err := haz.SpotRuleViolation("WH1-P9", shared.SpotKindParking, trailer.SiteSpots{HasHazmat: true})
	if !errors.Is(err, trailer.ErrPlacementRuleViolated) || !strings.Contains(err.Error(), "hazmat class 2.1 may only be placed on a HAZMAT spot") || !strings.Contains(err.Error(), "WH1-P9 is a PARKING spot") {
		t.Fatalf("%v", err)
	}
	err = haz.SpotRuleViolation("WH1-P9", shared.SpotKindUnknown, trailer.SiteSpots{HasHazmat: true})
	if err == nil || !strings.Contains(err.Error(), "WH1-P9 is not classified") {
		t.Fatalf("%v", err)
	}
	if err := haz.SpotRuleViolation("WH1-H1", shared.SpotKindHazmat, trailer.SiteSpots{HasHazmat: true}); err != nil {
		t.Fatalf("allowed: %v", err)
	}
	if err := haz.SpotRule(trailer.SiteSpots{}).Refusal(); !errors.Is(err, trailer.ErrPlacementRuleViolated) || !strings.Contains(err.Error(), "allowHazmatOnUnclassifiedSpots") {
		t.Fatalf("refusal: %v", err)
	}
	allow := haz.SpotRule(trailer.SiteSpots{AllowHazmatOnUnclassified: true})
	if !strings.Contains(allow.Why, "no HAZMAT spot") || !strings.Contains(allow.Why, "no kind") {
		t.Fatalf("policy rule: %s", allow.Why)
	}
}

func TestCanPlaceAtAppliesTheSpotRuleToYardSpotsOnly(t *testing.T) {
	haz := classified(t, "3", "", false)
	site := trailer.SiteSpots{HasHazmat: true}
	onSpot := func(kind shared.SpotKind) trailer.Placement {
		p := placeAt(spot1)
		p.Slot.SpotKind, p.Spots = kind, site
		return p
	}
	if err := haz.CanPlaceAt(onSpot(shared.SpotKindParking)); !errors.Is(err, trailer.ErrPlacementRuleViolated) {
		t.Errorf("parking: %v", err)
	}
	if err := haz.CanPlaceAt(onSpot(shared.SpotKindHazmat)); err != nil {
		t.Errorf("hazmat spot: %v", err)
	}
	door := placeAt(door1)
	door.Spots = site
	if err := haz.CanPlaceAt(door); err != nil {
		t.Errorf("a door is exempt: %v", err)
	}
	if err := haz.ArriveAt(onSpot(shared.SpotKindParking), at(1)); !errors.Is(err, trailer.ErrPlacementRuleViolated) || haz.State() != trailer.InMove {
		t.Errorf("ArriveAt judges the spot too: %v %s", err, haz.State())
	}
	plain := newTrailer(t)
	if err := plain.CanPlaceAt(onSpot(shared.SpotKindParking)); err != nil {
		t.Errorf("a trailer with no classification is never judged: %v", err)
	}
	// Occupancy and unknown-location come first.
	busy := onSpot(shared.SpotKindParking)
	busy.OccupiedBy = "trl-other"
	if err := haz.CanPlaceAt(busy); !errors.Is(err, trailer.ErrLocationOccupied) {
		t.Errorf("an occupied spot is reported as occupied: %v", err)
	}
}

func TestTrailerDockedCarriesTheClassification(t *testing.T) {
	tr := classified(t, "3", "UN1203", true)
	must(t, tr.ArriveAt(placeAt(door1), at(1)))
	d := data(t, only(t, tr))
	if d["hazmat_class"] != "3" || d["un_number"] != "UN1203" || d["requires_temperature_control"] != true {
		t.Fatalf("TrailerDocked data %v", d)
	}
}
