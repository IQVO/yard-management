package trailer_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
)

func sealed(number string, class shared.SealClass) func(*trailer.ArrivalInput) {
	return func(in *trailer.ArrivalInput) { in.SealNumber, in.SealClass = number, class }
}

func TestSealStateVocabulary(t *testing.T) {
	for _, s := range []trailer.SealState{trailer.SealNone, trailer.SealIntact, trailer.SealBroken, trailer.SealApplied, trailer.SealVerified, trailer.SealMismatch} {
		if !s.Valid() {
			t.Errorf("%s must be valid", s)
		}
	}
	for _, s := range []trailer.SealState{"", "WEIRD", "intact"} {
		if s.Valid() {
			t.Errorf("%q must not be valid", s)
		}
	}
	for want, s := range map[string]trailer.SealState{"NONE": trailer.SealNone, "INTACT": trailer.SealIntact, "BROKEN": trailer.SealBroken, "APPLIED": trailer.SealApplied, "VERIFIED": trailer.SealVerified, "MISMATCH": trailer.SealMismatch} {
		if string(s) != want {
			t.Errorf("%s spelled %q", want, s)
		}
	}
}

// Gate verification ends VERIFIED on a match and MISMATCH on a difference;
// the SealExceptionRaised event is unchanged.
func TestCheckSealStates(t *testing.T) {
	cases := []struct {
		name, seal, expected string
		state                trailer.SealState
		raised               bool
	}{
		{"match", "SL-1", "SL-1", trailer.SealVerified, false},
		{"mismatch", "SL-2", "SL-1", trailer.SealMismatch, true},
		{"nothing expected stays intact", "SL-2", "", trailer.SealIntact, false},
		{"no seal found stays none", "", "SL-1", trailer.SealNone, false},
	}
	for _, c := range cases {
		tr := newTrailer(t, sealed(c.seal, ""))
		if got := tr.CheckSeal(c.expected, at(1)); got != c.raised {
			t.Errorf("%s: raised %v", c.name, got)
		}
		s := tr.Snapshot()
		if s.SealState != c.state || tr.SealState() != c.state || s.Version != 1 {
			t.Errorf("%s: state %s version %d, want %s at version 1", c.name, s.SealState, s.Version, c.state)
		}
		if _, err := trailer.Restore(s); err != nil {
			t.Errorf("%s: the snapshot must restore: %v", c.name, err)
		}
	}
}

func TestCheckSealComparesAnAppliedSealToo(t *testing.T) {
	tr := parked(t)
	must(t, tr.RecordSealWith(trailer.SealRecord{Number: "SL-1", State: trailer.SealApplied}))
	if tr.CheckSeal("SL-1", at(3)) || tr.SealState() != trailer.SealVerified {
		t.Fatalf("an APPLIED seal that matches is VERIFIED, got %s", tr.SealState())
	}
	tr2 := parked(t)
	must(t, tr2.RecordSealWith(trailer.SealRecord{Number: "SL-2", State: trailer.SealApplied}))
	if !tr2.CheckSeal("SL-1", at(3)) || tr2.SealState() != trailer.SealMismatch {
		t.Fatalf("an APPLIED seal that differs is a MISMATCH, got %s", tr2.SealState())
	}
}

func TestCheckSealIgnoresAVerifiedMismatchedOrBrokenSeal(t *testing.T) {
	for _, state := range []trailer.SealState{trailer.SealVerified, trailer.SealMismatch} {
		tr := newTrailer(t, sealed("SL-2", ""))
		tr.CheckSeal(map[trailer.SealState]string{trailer.SealVerified: "SL-2", trailer.SealMismatch: "SL-1"}[state], at(1))
		tr.PullEvents()
		if tr.CheckSeal("SL-9", at(2)) || len(tr.PullEvents()) != 0 || tr.SealState() != state {
			t.Errorf("a %s seal is not compared again", state)
		}
	}
}

func TestRecordSealWith(t *testing.T) {
	tr := parked(t)
	must(t, tr.RecordSealWith(trailer.SealRecord{Number: "SL-1", Class: shared.SealClassHighSecurity, State: trailer.SealApplied, Minimum: shared.SealClassSecurity}))
	s := tr.Snapshot()
	if s.SealNumber != "SL-1" || s.SealClass != shared.SealClassHighSecurity || s.SealState != trailer.SealApplied || s.Version != 3 || tr.SealClass() != shared.SealClassHighSecurity {
		t.Fatalf("applied %+v", s)
	}
	must(t, tr.RecordSealWith(trailer.SealRecord{Number: "SL-2"}))
	if s := tr.Snapshot(); s.SealState != trailer.SealIntact || s.SealClass != "" || s.SealNumber != "SL-2" {
		t.Fatalf("a new seal replaces the class and defaults to INTACT: %+v", s)
	}
}

func TestRecordSealWithRefusals(t *testing.T) {
	cases := []struct {
		name string
		rec  trailer.SealRecord
		want error
	}{
		{"unknown class", trailer.SealRecord{Number: "S", Class: "X"}, shared.ErrInvalidSealClass},
		{"class too low", trailer.SealRecord{Number: "S", Class: shared.SealClassIndicative, Minimum: shared.SealClassHighSecurity}, shared.ErrSealClassTooLow},
		{"no class under a minimum", trailer.SealRecord{Number: "S", Minimum: shared.SealClassIndicative}, shared.ErrSealClassTooLow},
		{"verified is not recordable", trailer.SealRecord{Number: "S", State: trailer.SealVerified}, shared.ErrValidation},
		{"mismatch is not recordable", trailer.SealRecord{Number: "S", State: trailer.SealMismatch}, shared.ErrValidation},
		{"broken is not recordable here", trailer.SealRecord{Number: "S", State: trailer.SealBroken}, shared.ErrValidation},
		{"blank number", trailer.SealRecord{Number: " "}, shared.ErrValidation},
	}
	for _, c := range cases {
		tr := parked(t, sealed("OLD", shared.SealClassSecurity))
		before := tr.Snapshot()
		if err := tr.RecordSealWith(c.rec); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
		if after := tr.Snapshot(); after.Version != before.Version || after.SealNumber != "OLD" || after.SealClass != shared.SealClassSecurity {
			t.Errorf("%s: refused but changed %+v", c.name, after)
		}
	}
}

func TestBreakSealFromEveryUnbrokenState(t *testing.T) {
	states := map[string]func() *trailer.Trailer{
		"INTACT": func() *trailer.Trailer { return parked(t, sealed("S1", "")) },
		"APPLIED": func() *trailer.Trailer {
			tr := parked(t)
			must(t, tr.RecordSealWith(trailer.SealRecord{Number: "S1", State: trailer.SealApplied}))
			return tr
		},
		"VERIFIED": func() *trailer.Trailer { tr := parked(t, sealed("S1", "")); tr.CheckSeal("S1", at(1)); return tr },
		"MISMATCH": func() *trailer.Trailer { tr := parked(t, sealed("S1", "")); tr.CheckSeal("S2", at(1)); return tr },
	}
	for name, mk := range states {
		tr := mk()
		must(t, tr.BreakSeal(at(5)))
		if tr.SealState() != trailer.SealBroken || tr.Hold() != trailer.HoldSealBroken {
			t.Errorf("%s: broke to %s hold %s", name, tr.SealState(), tr.Hold())
		}
		if err := tr.BreakSeal(at(6)); !errors.Is(err, shared.ErrIllegalTransition) {
			t.Errorf("%s: a broken seal breaks once: %v", name, err)
		}
	}
}

func TestArrivalSealClassAndUnitNumber(t *testing.T) {
	tr := newTrailer(t, sealed("SL-1", shared.SealClassHighSecurity), func(in *trailer.ArrivalInput) { in.MinimumSealClass = shared.SealClassHighSecurity })
	if s := tr.Snapshot(); s.SealClass != shared.SealClassHighSecurity || s.SealState != trailer.SealIntact {
		t.Fatalf("snapshot %+v", s)
	}
	box := newTrailer(t, func(in *trailer.ArrivalInput) { in.Kind, in.Number = shared.KindContainer, "CSQU3054383" })
	if s := box.Snapshot(); s.UnitScheme != shared.SchemeISO6346 || s.Number != "CSQU3054383" || box.UnitScheme() != shared.SchemeISO6346 {
		t.Fatalf("container snapshot %+v", s)
	}
	if van := newTrailer(t); van.UnitScheme() != "" || van.Snapshot().Number != "TR-1042" {
		t.Fatal("a dry van keeps its free-form number and no scheme")
	}
	declared := newTrailer(t, func(in *trailer.ArrivalInput) { in.Number, in.UnitScheme = "MSCU0000007", shared.SchemeISO6346 })
	if declared.UnitScheme() != shared.SchemeISO6346 {
		t.Fatal("a declared ISO6346 dry van is validated")
	}
}

func TestArrivalRefusals(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*trailer.ArrivalInput)
		want error
	}{
		{"container with a bad check digit", func(in *trailer.ArrivalInput) { in.Kind, in.Number = shared.KindContainer, "CSQU3054380" }, shared.ErrInvalidUnitNumber},
		{"container with a free-form number", func(in *trailer.ArrivalInput) { in.Kind = shared.KindContainer }, shared.ErrInvalidUnitNumber},
		{"declared ISO on a free-form number", func(in *trailer.ArrivalInput) { in.UnitScheme = shared.SchemeISO6346 }, shared.ErrInvalidUnitNumber},
		{"unknown scheme", func(in *trailer.ArrivalInput) { in.UnitScheme = "GS1" }, shared.ErrValidation},
		{"unknown seal class", sealed("S", "X"), shared.ErrInvalidSealClass},
		{"class without a seal", sealed("", shared.SealClassSecurity), shared.ErrValidation},
		{"class too low", func(in *trailer.ArrivalInput) {
			sealed("S", shared.SealClassIndicative)(in)
			in.MinimumSealClass = shared.SealClassHighSecurity
		}, shared.ErrSealClassTooLow},
		{"no class under a minimum", func(in *trailer.ArrivalInput) { sealed("S", "")(in); in.MinimumSealClass = shared.SealClassIndicative }, shared.ErrSealClassTooLow},
	}
	for _, c := range cases {
		in := arrival()
		c.mod(&in)
		if _, err := trailer.RecordArrival(in); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	// A minimum with no seal at the gate asks nothing of the class.
	if _, err := trailer.RecordArrival(func() trailer.ArrivalInput {
		in := arrival()
		in.MinimumSealClass = shared.SealClassHighSecurity
		return in
	}()); err != nil {
		t.Errorf("no seal, no class rule: %v", err)
	}
}

func TestSnapshotSealClassAndStatesRoundTrip(t *testing.T) {
	for _, tr := range []*trailer.Trailer{
		parked(t, sealed("S1", shared.SealClassSecurity)),
		func() *trailer.Trailer { tr := parked(t, sealed("S1", "")); tr.CheckSeal("S1", at(1)); return tr }(),
		func() *trailer.Trailer { tr := parked(t, sealed("S1", "")); tr.CheckSeal("S2", at(1)); return tr }(),
		parked(t, func(in *trailer.ArrivalInput) { in.Kind, in.Number = shared.KindContainer, "CSQU3054383" }),
	} {
		s := tr.Snapshot()
		got, err := trailer.Restore(s)
		if err != nil || !reflect.DeepEqual(got.Snapshot(), s) {
			t.Errorf("round trip of %+v: %v", s, err)
		}
	}
}

// Rows stored before ADR 0012 have no class and no scheme and use only the
// three original seal states: they restore, even a CONTAINER whose number is
// not an ISO 6346 number (the rule applies to new arrivals, not to stored rows).
func TestRestoreAcceptsRowsFromBeforeTheNewVocabulary(t *testing.T) {
	for _, state := range []trailer.SealState{trailer.SealNone, trailer.SealIntact, trailer.SealBroken} {
		s := parked(t).Snapshot()
		s.Kind, s.Number = shared.KindContainer, "OLD-BOX-1"
		if state != trailer.SealNone {
			s.SealState, s.SealNumber = state, "S-1"
		}
		if _, err := trailer.Restore(s); err != nil {
			t.Errorf("%s: %v", state, err)
		}
	}
}

func TestRestoreRejectsCorruptSealFacts(t *testing.T) {
	good := func() trailer.Snapshot { return parked(t, sealed("S1", "")).Snapshot() }
	cases := map[string]func(*trailer.Snapshot){
		"unknown class": func(s *trailer.Snapshot) { s.SealClass = "X" },
		"class without a seal": func(s *trailer.Snapshot) {
			s.SealState, s.SealNumber, s.SealClass = trailer.SealNone, "", shared.SealClassSecurity
		},
		"unknown scheme":            func(s *trailer.Snapshot) { s.UnitScheme = "GS1" },
		"verified without a number": func(s *trailer.Snapshot) { s.SealState, s.SealNumber = trailer.SealVerified, "" },
		"none with a number":        func(s *trailer.Snapshot) { s.SealState = trailer.SealNone },
		"mismatch without a number": func(s *trailer.Snapshot) { s.SealState, s.SealNumber = trailer.SealMismatch, "" },
		"applied without a number":  func(s *trailer.Snapshot) { s.SealState, s.SealNumber = trailer.SealApplied, "" },
	}
	for name, mod := range cases {
		s := good()
		mod(&s)
		if _, err := trailer.Restore(s); err == nil {
			t.Errorf("%s: Restore accepted a corrupt snapshot", name)
		}
	}
	for _, state := range []trailer.SealState{trailer.SealApplied, trailer.SealVerified, trailer.SealMismatch} {
		s := good()
		s.SealState = state
		if _, err := trailer.Restore(s); err != nil {
			t.Errorf("%s with a number: %v", state, err)
		}
	}
}

func TestDockedAndDepartedEventsCarryTheSealFields(t *testing.T) {
	tr := newTrailer(t, sealed("SL-1", shared.SealClassSecurity))
	tr.CheckSeal("SL-1", at(1))
	must(t, tr.ArriveAt(placeAt(door1), at(2)))
	d := data(t, only(t, tr))
	if d["seal_state"] != "VERIFIED" || d["seal_class"] != "S" {
		t.Fatalf("TrailerDocked %v", d)
	}
	must(t, tr.StartDoorWork(at(3)))
	must(t, tr.CompleteDoorWork(shared.LoadEmpty, at(4)))
	tr.PullEvents()
	must(t, tr.Depart("visit-1", at(5)))
	var departed map[string]any
	for _, e := range tr.PullEvents() {
		if e.EventName() == "TrailerDeparted" {
			departed = data(t, e)
		}
	}
	if departed["seal_state"] != "VERIFIED" || departed["seal_class"] != "S" || departed["seal_number"] != "SL-1" {
		t.Fatalf("TrailerDeparted %v", departed)
	}
	plain := parked(t)
	must(t, plain.Depart("v", at(9)))
	if d := data(t, only(t, plain)); d["seal_state"] != nil || d["seal_class"] != nil || d["seal_number"] != nil {
		t.Fatalf("a trailer with no seal has null seal fields: %v", d)
	}
}
