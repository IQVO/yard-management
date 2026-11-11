package repocontract

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
)

// TrailerRepo runs the TrailerRepo contract.
func TrailerRepo(t *testing.T, newStore NewStore) {
	t.Helper()
	run(t, newStore, map[string]func(t *testing.T, s Store){
		"an unknown trailer is not found": func(t *testing.T, s Store) {
			_, err := s.Trailers.FindByID(bg, "trl-404")
			wantErr(t, err, ports.ErrNotFound)
			_, err = s.Trailers.FindAt(bg, spot("WH1-P1"))
			wantErr(t, err, ports.ErrNotFound)
			_, err = s.Trailers.FindOnSiteByUnit(bg, "WH1", "ACME", "T1")
			wantErr(t, err, ports.ErrNotFound)
			_, err = s.Trailers.FindByAppointment(bg, "appt-404")
			wantErr(t, err, ports.ErrNotFound)
		},
		"the seal class, the unit number scheme and every seal state round trip": trailerIdentifiers,
		"save then find round trips a trailer in every state":                    trailerRoundTrip,
		"a spot or door holds at most one trailer":                               trailerOneAtALocation,
		"a trailer in a move or departed holds no location":                      trailerFreesLocation,
		"the unit is unique among the trailers on site":                          trailerUnitOnSite,
		"the version guard refuses stale writers":                                trailerVersionGuard,
		"finders and lists":    trailerFinders,
		"list pages by keyset": trailerPages,
	})
}

func trailerRoundTrip(t *testing.T, s Store) {
	arrived := Arrived(t, "trl-1", "WH1", "T1", 0)
	must(t, s.Trailers.Save(bg, arrived))
	check := func(want *trailer.Trailer) {
		t.Helper()
		got, err := s.Trailers.FindByID(bg, want.ID())
		must(t, err)
		if !reflect.DeepEqual(got.Snapshot(), want.Snapshot()) {
			t.Fatalf("round trip changed the state:\ngot  %+v\nwant %+v", got.Snapshot(), want.Snapshot())
		}
	}
	check(arrived)
	ArriveAt(t, arrived, door("WH1-D1"), time.Minute)
	must(t, s.Trailers.Save(bg, arrived))
	check(arrived)
	must(t, arrived.StartDoorWork(T0.Add(2*time.Minute)))
	must(t, s.Trailers.Save(bg, arrived))
	check(arrived)
	must(t, arrived.PlaceHold(trailer.HoldQuality, T0.Add(3*time.Minute)))
	must(t, s.Trailers.Save(bg, arrived))
	check(arrived) // DOCKED, IN_PROGRESS, on hold
	must(t, arrived.StartMoving(T0.Add(4*time.Minute)))
	must(t, s.Trailers.Save(bg, arrived))
	check(arrived) // IN_MOVE remembering its origin and door work
	must(t, arrived.ReturnToOrigin("", T0.Add(5*time.Minute)))
	must(t, s.Trailers.Save(bg, arrived))
	check(arrived)
	must(t, arrived.ReleaseHold(T0.Add(6*time.Minute)))
	must(t, s.Trailers.Save(bg, arrived))
	check(arrived)
	must(t, arrived.CompleteDoorWork(shared.LoadEmpty, T0.Add(7*time.Minute)))
	must(t, s.Trailers.Save(bg, arrived))
	check(arrived)
	if !arrived.SweepDwell(T0.Add(26*time.Hour), 60) {
		t.Fatal("the dwell sweep must alert")
	}
	must(t, s.Trailers.Save(bg, arrived))
	check(arrived)
	must(t, arrived.Depart("visit-2", T0.Add(27*time.Hour)))
	must(t, s.Trailers.Save(bg, arrived))
	check(arrived) // DEPARTED
}

func trailerOneAtALocation(t *testing.T, s Store) {
	a := Arrived(t, "trl-1", "WH1", "T1", 0)
	b := Arrived(t, "trl-2", "WH1", "T2", 0)
	must(t, s.Trailers.Save(bg, a))
	must(t, s.Trailers.Save(bg, b))
	ArriveAt(t, a, spot("WH1-P1"), time.Minute)
	must(t, s.Trailers.Save(bg, a))
	ArriveAt(t, b, spot("WH1-P1"), 2*time.Minute)
	wantErr(t, s.Trailers.Save(bg, b), trailer.ErrLocationOccupied)
	got, err := s.Trailers.FindAt(bg, spot("WH1-P1"))
	must(t, err)
	if got.ID() != "trl-1" {
		t.Fatalf("occupant = %s", got.ID())
	}
	if _, err := s.Trailers.FindAt(bg, door("WH1-P1")); err == nil {
		t.Fatal("a door and a spot are different places even with the same code")
	}
	stored, err := s.Trailers.FindByID(bg, "trl-2")
	must(t, err)
	if stored.State() != trailer.InMove {
		t.Fatalf("the refused write must not have been stored: %s", stored.State())
	}
}

func trailerFreesLocation(t *testing.T, s Store) {
	a := Arrived(t, "trl-1", "WH1", "T1", 0)
	must(t, s.Trailers.Save(bg, a))
	ArriveAt(t, a, spot("WH1-P1"), time.Minute)
	must(t, s.Trailers.Save(bg, a))
	must(t, a.StartMoving(T0.Add(2*time.Minute)))
	must(t, s.Trailers.Save(bg, a))
	b := Arrived(t, "trl-2", "WH1", "T2", 0)
	must(t, s.Trailers.Save(bg, b))
	ArriveAt(t, b, spot("WH1-P1"), 3*time.Minute)
	must(t, s.Trailers.Save(bg, b)) // the origin was released at StartMoving
	Depart(t, b, 4*time.Minute)
	must(t, s.Trailers.Save(bg, b))
	if _, err := s.Trailers.FindAt(bg, spot("WH1-P1")); err == nil {
		t.Fatal("a departed trailer holds no location")
	}
}

func trailerUnitOnSite(t *testing.T, s Store) {
	must(t, s.Trailers.Save(bg, Arrived(t, "trl-1", "WH1", "T1", 0)))
	wantErr(t, s.Trailers.Save(bg, Arrived(t, "trl-2", "WH1", "T1", 0)), trailer.ErrTrailerAlreadyOnSite)
	must(t, s.Trailers.Save(bg, Arrived(t, "trl-3", "WH2", "T1", 0))) // another site
	got, err := s.Trailers.FindOnSiteByUnit(bg, "WH1", "ACME", "T1")
	must(t, err)
	if got.ID() != "trl-1" {
		t.Fatalf("unit = %s", got.ID())
	}
	first, err := s.Trailers.FindByID(bg, "trl-1")
	must(t, err)
	ArriveAt(t, first, spot("WH1-P1"), time.Minute)
	must(t, s.Trailers.Save(bg, first))
	Depart(t, first, 2*time.Minute)
	must(t, s.Trailers.Save(bg, first))
	must(t, s.Trailers.Save(bg, Arrived(t, "trl-4", "WH1", "T1", 3*time.Minute))) // the unit left, it can come back
	if _, err := s.Trailers.FindOnSiteByUnit(bg, "WH1", "ACME", "T1"); err != nil {
		t.Fatal(err)
	}
}

func trailerVersionGuard(t *testing.T, s Store) {
	a := Arrived(t, "trl-1", "WH1", "T1", 0)
	must(t, s.Trailers.Save(bg, a))
	wantErr(t, s.Trailers.Save(bg, a), ports.ErrConcurrentModification)
	one, err := s.Trailers.FindByID(bg, "trl-1")
	must(t, err)
	two, err := s.Trailers.FindByID(bg, "trl-1")
	must(t, err)
	ArriveAt(t, one, spot("WH1-P1"), time.Minute)
	ArriveAt(t, two, spot("WH1-P2"), time.Minute)
	must(t, s.Trailers.Save(bg, one))
	wantErr(t, s.Trailers.Save(bg, two), ports.ErrConcurrentModification)
	ghost := Arrived(t, "trl-9", "WH1", "T9", 0)
	ArriveAt(t, ghost, spot("WH1-P9"), time.Minute)
	wantErr(t, s.Trailers.Save(bg, ghost), ports.ErrConcurrentModification) // version 2 of an id never stored
}

func trailerFinders(t *testing.T, s Store) {
	a, b, c := Arrived(t, "trl-a", "WH1", "T1", 0), Arrived(t, "trl-b", "WH1", "T2", time.Minute), Arrived(t, "trl-c", "WH2", "T3", 2*time.Minute)
	for _, tr := range []*trailer.Trailer{a, b, c} {
		must(t, s.Trailers.Save(bg, tr))
	}
	ArriveAt(t, b, spot("WH1-P1"), 3*time.Minute)
	must(t, s.Trailers.Save(bg, b))
	ids := func(ts []*trailer.Trailer) []string {
		out := []string{}
		for _, tr := range ts {
			out = append(out, tr.ID())
		}
		return out
	}
	onSite, err := s.Trailers.ListOnSite(bg, "WH1")
	must(t, err)
	if got := ids(onSite); !reflect.DeepEqual(got, []string{"trl-a", "trl-b"}) {
		t.Fatalf("on site WH1 = %v", got)
	}
	everywhere, err := s.Trailers.ListOnSite(bg, "")
	must(t, err)
	if got := ids(everywhere); !reflect.DeepEqual(got, []string{"trl-a", "trl-b", "trl-c"}) {
		t.Fatalf("on site anywhere = %v", got)
	}
	// the three trailers share appointment appt-1: the finder returns one of the not-departed
	if _, err := s.Trailers.FindByAppointment(bg, "appt-1"); err != nil {
		t.Fatal(err)
	}
	filtered, err := s.Trailers.List(bg, views.TrailerFilter{SiteID: "WH1", Kind: "DRY_VAN", LocationCode: "WH1-P1", UnitNumber: "T2"}, nil, 10)
	must(t, err)
	if got := ids(filtered); !reflect.DeepEqual(got, []string{"trl-b"}) {
		t.Fatalf("filtered = %v", got)
	}
	byVisit, err := s.Trailers.List(bg, views.TrailerFilter{VisitID: "visit-1"}, nil, 10)
	must(t, err)
	if got := ids(byVisit); !reflect.DeepEqual(got, []string{"trl-a", "trl-b", "trl-c"}) {
		t.Fatalf("by visit = %v", got)
	}
	noVisit, err := s.Trailers.List(bg, views.TrailerFilter{VisitID: "visit-404"}, nil, 10)
	must(t, err)
	if len(noVisit) != 0 {
		t.Fatalf("unknown visit = %v", ids(noVisit))
	}
	inMove, err := s.Trailers.List(bg, views.TrailerFilter{State: "IN_MOVE"}, nil, 10)
	must(t, err)
	if got := ids(inMove); !reflect.DeepEqual(got, []string{"trl-a", "trl-c"}) {
		t.Fatalf("in move = %v", got)
	}
	Depart(t, b, 4*time.Minute)
	must(t, s.Trailers.Save(bg, b))
	departed, err := s.Trailers.List(bg, views.TrailerFilter{State: "DEPARTED"}, nil, 10)
	must(t, err)
	if got := ids(departed); !reflect.DeepEqual(got, []string{"trl-b"}) {
		t.Fatalf("departed = %v", got)
	}
	live, err := s.Trailers.List(bg, views.TrailerFilter{}, nil, 10)
	must(t, err)
	if got := ids(live); !reflect.DeepEqual(got, []string{"trl-a", "trl-c"}) {
		t.Fatalf("default list is not-departed = %v", got)
	}
}

func trailerPages(t *testing.T, s Store) {
	for i, id := range []string{"trl-1", "trl-2", "trl-3"} {
		must(t, s.Trailers.Save(bg, Arrived(t, id, "WH1", "T"+id, time.Duration(i)*time.Minute)))
	}
	first, err := s.Trailers.List(bg, views.TrailerFilter{}, nil, 2)
	must(t, err)
	if len(first) != 2 || first[0].ID() != "trl-1" || first[1].ID() != "trl-2" {
		t.Fatalf("first page = %d", len(first))
	}
	last := first[1].Snapshot()
	rest, err := s.Trailers.List(bg, views.TrailerFilter{}, &views.Key{At: last.ArrivedAt, ID: last.ID}, 10)
	must(t, err)
	if len(rest) != 1 || rest[0].ID() != "trl-3" {
		t.Fatalf("second page = %d", len(rest))
	}
}

// trailerIdentifiers: an ISO 6346 container with a class H seal, then the seal
// walks through APPLIED and BROKEN; two more containers end VERIFIED and
// MISMATCH at the gate; a free-form trailer keeps an empty scheme and class
// (ADR 0012).
func trailerIdentifiers(t *testing.T, s Store) {
	check := func(tr *trailer.Trailer, state trailer.SealState) {
		t.Helper()
		got, err := s.Trailers.FindByID(bg, tr.ID())
		must(t, err)
		if !reflect.DeepEqual(got.Snapshot(), tr.Snapshot()) || got.SealState() != state {
			t.Fatalf("round trip changed the state:\ngot  %+v\nwant %+v", got.Snapshot(), tr.Snapshot())
		}
	}
	box := Container(t, "trl-1", "WH1", "CSQU3054383", 0)
	must(t, s.Trailers.Save(bg, box))
	check(box, trailer.SealIntact)
	if sn := box.Snapshot(); sn.UnitScheme != shared.SchemeISO6346 || sn.SealClass != shared.SealClassHighSecurity {
		t.Fatalf("scheme and class %+v", sn)
	}
	ArriveAt(t, box, spot("WH1-P1"), time.Minute)
	must(t, s.Trailers.Save(bg, box))
	must(t, box.RecordSealWith(trailer.SealRecord{Number: "SL-2", Class: shared.SealClassSecurity, State: trailer.SealApplied}))
	must(t, s.Trailers.Save(bg, box))
	check(box, trailer.SealApplied)
	must(t, box.BreakSeal(T0.Add(5*time.Minute)))
	must(t, s.Trailers.Save(bg, box))
	check(box, trailer.SealBroken)

	for _, c := range []struct {
		id, unit, expected string
		state              trailer.SealState
	}{{"trl-2", "MSCU0000007", "SL-1", trailer.SealVerified}, {"trl-3", "ABCU0000070", "SL-9", trailer.SealMismatch}} {
		tr := Container(t, c.id, "WH1", c.unit, 0)
		tr.CheckSeal(c.expected, T0)
		must(t, s.Trailers.Save(bg, tr))
		check(tr, c.state)
	}
	van := Arrived(t, "trl-van", "WH1", "free form 7", 0)
	must(t, s.Trailers.Save(bg, van))
	got, err := s.Trailers.FindByID(bg, "trl-van")
	must(t, err)
	if sn := got.Snapshot(); sn.UnitScheme != "" || sn.SealClass != "" || sn.Number != "free form 7" {
		t.Fatalf("a free-form trailer keeps no scheme and no class: %+v", sn)
	}
}
