package trailer_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
)

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	build := map[string]func() *trailer.Trailer{
		"in move":  func() *trailer.Trailer { return newTrailer(t) },
		"parked":   func() *trailer.Trailer { return parked(t) },
		"docked":   func() *trailer.Trailer { return docked(t) },
		"worked":   func() *trailer.Trailer { return worked(t) },
		"departed": func() *trailer.Trailer { tr := parked(t); must(t, tr.Depart("v", at(9))); return tr },
		"held and sealed": func() *trailer.Trailer {
			tr := parked(t, func(in *trailer.ArrivalInput) { in.SealNumber = "S1" })
			must(t, tr.BreakSeal(at(3)))
			return tr
		},
		"remembers origin": func() *trailer.Trailer { tr := docked(t); must(t, tr.StartMoving(at(3))); return tr },
		"alerted":          func() *trailer.Trailer { tr := parked(t); tr.SweepDwell(at(500), 10); return tr },
	}
	for name, mk := range build {
		want := mk().Snapshot()
		got, err := trailer.Restore(want)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(got.Snapshot(), want) {
			t.Errorf("%s: round trip differs:\n got %+v\nwant %+v", name, got.Snapshot(), want)
		}
		if len(got.PullEvents()) != 0 {
			t.Errorf("%s: a restored trailer has no pending events", name)
		}
	}
}

func TestRestoreRejectsCorruptRows(t *testing.T) {
	good := func() trailer.Snapshot { return parked(t).Snapshot() }
	dockedSnap := func() trailer.Snapshot { return docked(t).Snapshot() }
	cases := []struct {
		name string
		s    trailer.Snapshot
	}{
		{"blank id", func() trailer.Snapshot { s := good(); s.ID = ""; return s }()},
		{"blank site", func() trailer.Snapshot { s := good(); s.SiteID = ""; return s }()},
		{"blank number", func() trailer.Snapshot { s := good(); s.Number = ""; return s }()},
		{"bad scac", func() trailer.Snapshot { s := good(); s.SCAC = "x"; return s }()},
		{"bad kind", func() trailer.Snapshot { s := good(); s.Kind = "BOX"; return s }()},
		{"bad load", func() trailer.Snapshot { s := good(); s.LoadState = "FULL"; return s }()},
		{"bad purpose", func() trailer.Snapshot { s := good(); s.Purpose = ""; return s }()},
		{"bad state", func() trailer.Snapshot { s := good(); s.State = "LOST"; return s }()},
		{"parked without a spot", func() trailer.Snapshot { s := good(); s.Location = shared.Location{}; return s }()},
		{"parked on a door", func() trailer.Snapshot { s := good(); s.Location = door1; return s }()},
		{"parked with a spot of no code", func() trailer.Snapshot { s := good(); s.Location.Code = ""; return s }()},
		{"parked with door work", func() trailer.Snapshot { s := good(); s.DoorWork = trailer.WorkDone; return s }()},
		{"docked on a spot", func() trailer.Snapshot { s := dockedSnap(); s.Location = spot1; return s }()},
		{"docked with a door of no code", func() trailer.Snapshot { s := dockedSnap(); s.Location.Code = ""; return s }()},
		{"docked without door work", func() trailer.Snapshot { s := dockedSnap(); s.DoorWork = ""; return s }()},
		{"in move with a place", func() trailer.Snapshot { s := newTrailer(t).Snapshot(); s.Location = spot1; return s }()},
		{"in move with door work", func() trailer.Snapshot { s := newTrailer(t).Snapshot(); s.DoorWork = trailer.WorkDone; return s }()},
		{"origin on a parked trailer", func() trailer.Snapshot { s := good(); s.Origin = spot2; return s }()},
		{"origin of a bad kind", func() trailer.Snapshot {
			s := newTrailer(t).Snapshot()
			s.Origin = shared.Location{Kind: "GATE", Code: "g"}
			return s
		}()},
		{"bad seal state", func() trailer.Snapshot { s := good(); s.SealState, s.SealNumber = "WEIRD", "S"; return s }()},
		{"seal number without state", func() trailer.Snapshot { s := good(); s.SealNumber = "S"; return s }()},
		{"seal state without number", func() trailer.Snapshot { s := good(); s.SealState = trailer.SealIntact; return s }()},
		{"bad hold", func() trailer.Snapshot { s := good(); s.Hold = "BORED"; return s }()},
		{"version zero", func() trailer.Snapshot { s := good(); s.Version = 0; return s }()},
		{"no arrival time", func() trailer.Snapshot { s := good(); s.ArrivedAt = time.Time{}; return s }()},
	}
	for _, c := range cases {
		if _, err := trailer.Restore(c.s); err == nil {
			t.Errorf("%s: Restore accepted a corrupt snapshot", c.name)
		}
	}
}

func TestRestoreBoundaries(t *testing.T) {
	s := parked(t).Snapshot()
	s.Version = 1
	if _, err := trailer.Restore(s); err != nil {
		t.Errorf("version 1 is the lowest valid: %v", err)
	}
	s.SCAC = ""
	if _, err := trailer.Restore(s); err != nil {
		t.Errorf("an empty scac is valid: %v", err)
	}
	d := docked(t).Snapshot()
	for _, w := range []trailer.DoorWork{trailer.WorkWaiting, trailer.WorkInProgress, trailer.WorkDone} {
		d.DoorWork = w
		if _, err := trailer.Restore(d); err != nil {
			t.Errorf("door work %s: %v", w, err)
		}
	}
	m := newTrailer(t).Snapshot()
	m.Origin = door1
	if _, err := trailer.Restore(m); err != nil {
		t.Errorf("an IN_MOVE trailer may remember a door: %v", err)
	}
}
