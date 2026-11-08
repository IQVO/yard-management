package repocontract

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

// VisitRepo runs the VisitRepo contract.
func VisitRepo(t *testing.T, newStore NewStore) {
	t.Helper()
	run(t, newStore, map[string]func(t *testing.T, s Store){
		"an unknown visit is not found": func(t *testing.T, s Store) {
			_, err := s.Visits.FindByID(bg, "visit-404")
			wantErr(t, err, ports.ErrNotFound)
			_, err = s.Visits.FindOpenByPlate(bg, "WH1", "PLATE1")
			wantErr(t, err, ports.ErrNotFound)
		},
		"save then find round trips a checked-in and a rejected visit": func(t *testing.T, s Store) {
			in := NewVisit(t, "visit-1", "WH1", "PLATE1", 0, Ref("trl-1", "T1"), Ref("trl-2", "T2"))
			rej := Rejected(t, "visit-2", "WH1", "PLATE2")
			must(t, s.Visits.Save(bg, in))
			must(t, s.Visits.Save(bg, rej))
			for _, want := range []*yardvisit.Visit{in, rej} {
				got, err := s.Visits.FindByID(bg, want.ID())
				must(t, err)
				if !reflect.DeepEqual(got.Snapshot(), want.Snapshot()) {
					t.Fatalf("round trip changed the state:\ngot  %+v\nwant %+v", got.Snapshot(), want.Snapshot())
				}
			}
		},
		"a checked-out visit keeps its departure": func(t *testing.T, s Store) {
			v := NewVisit(t, "visit-1", "WH1", "PLATE1", 0, Ref("trl-1", "T1"))
			must(t, s.Visits.Save(bg, v))
			must(t, v.CheckOut([]yardvisit.Departure{{TrailerID: "trl-1", OnSite: true, Released: true, SealNumber: "SL"}}, T0.Add(time.Hour)))
			must(t, s.Visits.Save(bg, v))
			got, err := s.Visits.FindByID(bg, "visit-1")
			must(t, err)
			if got.State() != yardvisit.CheckedOut || got.Version() != 2 || !reflect.DeepEqual(got.Snapshot(), v.Snapshot()) {
				t.Fatalf("stored %+v", got.Snapshot())
			}
		},
		"the version guard refuses stale writers and re-saves": func(t *testing.T, s Store) {
			v := NewVisit(t, "visit-1", "WH1", "PLATE1", 0, Ref("trl-1", "T1"))
			must(t, s.Visits.Save(bg, v))
			wantErr(t, s.Visits.Save(bg, v), ports.ErrConcurrentModification)
			second := NewVisit(t, "visit-1", "WH1", "PLATE1", 0, Ref("trl-1", "T1"))
			must(t, second.CheckOut([]yardvisit.Departure{{TrailerID: "trl-1", OnSite: true, Released: true}}, T0.Add(time.Hour)))
			must(t, v.CheckOut([]yardvisit.Departure{{TrailerID: "trl-1", OnSite: true, Released: true}}, T0.Add(time.Hour)))
			must(t, s.Visits.Save(bg, v))
			wantErr(t, s.Visits.Save(bg, second), ports.ErrConcurrentModification)
			never := NewVisit(t, "visit-9", "WH1", "PLATE9", 0, Ref("trl-9", "T9"))
			must(t, never.CheckOut(nil, T0.Add(time.Hour)))
			wantErr(t, s.Visits.Save(bg, never), ports.ErrConcurrentModification)
		},
		"one open visit per plate and site": func(t *testing.T, s Store) {
			must(t, s.Visits.Save(bg, NewVisit(t, "visit-1", "WH1", "PLATE1", 0, Ref("trl-1", "T1"))))
			wantErr(t, s.Visits.Save(bg, NewVisit(t, "visit-2", "WH1", "PLATE1", 0, Ref("trl-2", "T2"))), yardvisit.ErrVisitAlreadyOpen)
			if _, err := s.Visits.FindByID(bg, "visit-2"); err == nil {
				t.Fatal("the refused visit must not be stored")
			}
			must(t, s.Visits.Save(bg, NewVisit(t, "visit-3", "WH2", "PLATE1", 0, Ref("trl-3", "T3")))) // another site
			must(t, s.Visits.Save(bg, Rejected(t, "visit-4", "WH1", "PLATE1")))                        // a rejection is not open
			got, err := s.Visits.FindOpenByPlate(bg, "WH1", "PLATE1")
			must(t, err)
			if got.ID() != "visit-1" {
				t.Fatalf("open visit = %s", got.ID())
			}
		},
		"a checked-out visit frees the plate": func(t *testing.T, s Store) {
			first := NewVisit(t, "visit-1", "WH1", "PLATE1", 0, Ref("trl-1", "T1"))
			must(t, s.Visits.Save(bg, first))
			must(t, first.CheckOut([]yardvisit.Departure{{TrailerID: "trl-1", OnSite: true, Released: true}}, T0.Add(time.Hour)))
			must(t, s.Visits.Save(bg, first))
			must(t, s.Visits.Save(bg, NewVisit(t, "visit-2", "WH1", "PLATE1", time.Hour, Ref("trl-2", "T2"))))
			got, err := s.Visits.FindOpenByPlate(bg, "WH1", "PLATE1")
			must(t, err)
			if got.ID() != "visit-2" {
				t.Fatalf("open visit = %s", got.ID())
			}
		},
		"list is newest first, filtered and paged by keyset": visitList,
	})
}

func visitList(t *testing.T, s Store) {
	for i, id := range []string{"visit-a", "visit-b", "visit-c"} {
		plate := "PLATE" + string(rune('1'+i))
		must(t, s.Visits.Save(bg, NewVisit(t, id, "WH1", plate, time.Duration(i)*time.Minute, Ref("trl-"+id, "T"+plate))))
	}
	must(t, s.Visits.Save(bg, NewVisit(t, "visit-d", "WH2", "PLATE9", 10*time.Minute, Ref("trl-d", "TD"))))
	ids := func(vs []*yardvisit.Visit) []string {
		out := []string{}
		for _, v := range vs {
			out = append(out, v.ID())
		}
		return out
	}
	all, err := s.Visits.List(bg, views.VisitFilter{}, nil, 10)
	must(t, err)
	if got := ids(all); !reflect.DeepEqual(got, []string{"visit-d", "visit-c", "visit-b", "visit-a"}) {
		t.Fatalf("list = %v", got)
	}
	site, err := s.Visits.List(bg, views.VisitFilter{SiteID: "WH1", State: "CHECKED_IN"}, nil, 2)
	must(t, err)
	if got := ids(site); !reflect.DeepEqual(got, []string{"visit-c", "visit-b"}) {
		t.Fatalf("site page 1 = %v", got)
	}
	s2 := site[len(site)-1].Snapshot()
	rest, err := s.Visits.List(bg, views.VisitFilter{SiteID: "WH1"}, &views.Key{At: s2.ArrivedAt, ID: s2.ID}, 10)
	must(t, err)
	if got := ids(rest); !reflect.DeepEqual(got, []string{"visit-a"}) {
		t.Fatalf("site page 2 = %v", got)
	}
	plate, err := s.Visits.List(bg, views.VisitFilter{TractorPlate: "PLATE2"}, nil, 10)
	must(t, err)
	if got := ids(plate); !reflect.DeepEqual(got, []string{"visit-b"}) {
		t.Fatalf("plate filter = %v", got)
	}
	none, err := s.Visits.List(bg, views.VisitFilter{State: "REJECTED"}, nil, 10)
	must(t, err)
	if len(none) != 0 {
		t.Fatalf("rejected = %v", ids(none))
	}
}
