package trailer_test

import (
	"errors"
	"testing"

	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
)

func TestDoorWorkLifecycle(t *testing.T) {
	tr := docked(t)
	must(t, tr.StartDoorWork(at(5)))
	if tr.DoorWork() != trailer.WorkInProgress || tr.Version() != 3 {
		t.Fatal("started")
	}
	e := only(t, tr)
	header(t, e, "DoorWorkStarted", at(5))
	eq(t, data(t, e), map[string]any{
		"trailer_id": "trl-1", "site_id": "WH1", "door_code": "WH1-D1", "appointment_id": "appt-1", "started_at": "2026-11-05T14:05:00Z", "version": float64(3),
	})
	must(t, tr.CompleteDoorWork(shared.LoadEmpty, at(25)))
	if tr.DoorWork() != trailer.WorkDone || tr.Version() != 4 || tr.Snapshot().LoadState != shared.LoadEmpty {
		t.Fatal("completed")
	}
	e = only(t, tr)
	header(t, e, "DoorWorkCompleted", at(25))
	eq(t, data(t, e), map[string]any{
		"trailer_id": "trl-1", "site_id": "WH1", "door_code": "WH1-D1", "appointment_id": "appt-1", "completed_at": "2026-11-05T14:25:00Z",
		"load_state": "EMPTY", "version": float64(4),
	})
}

func TestDoorWorkRefusals(t *testing.T) {
	d := docked(t)
	if err := d.CompleteDoorWork(shared.LoadEmpty, at(2)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("complete before start: %v", err)
	}
	must(t, d.StartDoorWork(at(2)))
	if err := d.StartDoorWork(at(3)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("start twice: %v", err)
	}
	if err := d.CompleteDoorWork("FULL", at(4)); !errors.Is(err, shared.ErrValidation) {
		t.Errorf("bad load: %v", err)
	}
	if d.DoorWork() != trailer.WorkInProgress {
		t.Error("refusals change nothing")
	}
	for _, tr := range []*trailer.Trailer{newTrailer(t), parked(t)} {
		if err := tr.StartDoorWork(at(2)); !errors.Is(err, shared.ErrIllegalTransition) {
			t.Errorf("start off a door (%s): %v", tr.State(), err)
		}
		if err := tr.CompleteDoorWork(shared.LoadEmpty, at(2)); !errors.Is(err, shared.ErrIllegalTransition) {
			t.Errorf("complete off a door (%s): %v", tr.State(), err)
		}
	}
}

func TestHoldBlocksDoorWorkUntilReleased(t *testing.T) {
	tr := docked(t)
	must(t, tr.PlaceHold(trailer.HoldDamaged, at(2)))
	tr.PullEvents()
	if err := tr.StartDoorWork(at(3)); !errors.Is(err, trailer.ErrTrailerOnHold) {
		t.Fatalf("held: %v", err)
	}
	must(t, tr.ReleaseHold(at(4)))
	must(t, tr.StartDoorWork(at(5)))
}

func TestPlaceAndReleaseHold(t *testing.T) {
	tr := docked(t)
	must(t, tr.PlaceHold(trailer.HoldSecurity, at(2)))
	if tr.Hold() != trailer.HoldSecurity || tr.Version() != 3 {
		t.Fatal("held")
	}
	e := only(t, tr)
	d := data(t, e)
	if e.EventName() != "TrailerHeld" || d["reason"] != "SECURITY" || d["version"] != float64(3) || d["trailer_id"] != "trl-1" || d["site_id"] != "WH1" || len(d) != 4 {
		t.Fatalf("held data %v", d)
	}
	if err := tr.PlaceHold(trailer.HoldQuality, at(3)); !errors.Is(err, trailer.ErrTrailerOnHold) {
		t.Fatalf("second hold: %v", err)
	}
	must(t, tr.ReleaseHold(at(4)))
	e = only(t, tr)
	d = data(t, e)
	if tr.Hold() != "" || e.EventName() != "TrailerHoldReleased" || d["version"] != float64(4) || len(d) != 3 {
		t.Fatalf("released %v", d)
	}
	if err := tr.ReleaseHold(at(5)); !errors.Is(err, trailer.ErrTrailerNotOnHold) {
		t.Fatalf("release without hold: %v", err)
	}
}

func TestPlaceHoldRefusals(t *testing.T) {
	if err := parked(t).PlaceHold("BORED", at(2)); !errors.Is(err, shared.ErrValidation) {
		t.Errorf("bad reason: %v", err)
	}
	g := parked(t)
	must(t, g.Depart("v", at(2)))
	if err := g.PlaceHold(trailer.HoldQuality, at(3)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("departed: %v", err)
	}
	for _, r := range []trailer.HoldReason{trailer.HoldDamaged, trailer.HoldSealBroken, trailer.HoldNotFound, trailer.HoldQuality, trailer.HoldSecurity} {
		if !r.Valid() {
			t.Errorf("%s must be valid", r)
		}
	}
	if trailer.HoldReason("").Valid() {
		t.Error("empty reason is not valid")
	}
}

func TestSeals(t *testing.T) {
	tr := parked(t)
	must(t, tr.RecordSeal("SL-1"))
	if s := tr.Snapshot(); s.SealNumber != "SL-1" || s.SealState != trailer.SealIntact || s.Version != 3 {
		t.Fatalf("recorded %+v", s)
	}
	must(t, tr.BreakSeal(at(5)))
	s := tr.Snapshot()
	if s.SealState != trailer.SealBroken || s.SealNumber != "SL-1" || s.Hold != trailer.HoldSealBroken || s.Version != 4 {
		t.Fatalf("broken %+v", s)
	}
	e := only(t, tr)
	if d := data(t, e); e.EventName() != "TrailerHeld" || d["reason"] != "SEAL_BROKEN" || d["version"] != float64(4) {
		t.Fatalf("break places a SEAL_BROKEN hold: %v", d)
	}
	if err := tr.BreakSeal(at(6)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("break twice: %v", err)
	}
}

func TestBreakSealKeepsAnExistingHold(t *testing.T) {
	tr := parked(t, func(in *trailer.ArrivalInput) { in.SealNumber = "SL-1" })
	must(t, tr.PlaceHold(trailer.HoldDamaged, at(2)))
	tr.PullEvents()
	must(t, tr.BreakSeal(at(3)))
	if tr.Hold() != trailer.HoldDamaged || len(tr.PullEvents()) != 0 {
		t.Fatal("an existing hold stays and nothing is raised twice")
	}
}

func TestSealRefusals(t *testing.T) {
	tr := parked(t)
	if err := tr.BreakSeal(at(2)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("no seal to break: %v", err)
	}
	if err := tr.RecordSeal(" "); !errors.Is(err, shared.ErrValidation) {
		t.Errorf("blank seal: %v", err)
	}
	g := parked(t, func(in *trailer.ArrivalInput) { in.SealNumber = "S" })
	must(t, g.Depart("v", at(2)))
	if err := g.RecordSeal("SL-2"); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("record on a departed trailer: %v", err)
	}
	if err := g.BreakSeal(at(3)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("break on a departed trailer: %v", err)
	}
}

func TestCheckSeal(t *testing.T) {
	cases := []struct {
		name     string
		seal     string
		expected string
		want     bool
	}{
		{"matches", "SL-1", "SL-1", false},
		{"differs", "SL-2", "SL-1", true},
		{"no seal found", "", "SL-1", false},
		{"nothing expected", "SL-2", "", false},
	}
	for _, c := range cases {
		tr := newTrailer(t, func(in *trailer.ArrivalInput) { in.SealNumber = c.seal })
		got := tr.CheckSeal(c.expected, at(1))
		if got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
		ev := tr.PullEvents()
		if got != (len(ev) == 1) {
			t.Errorf("%s: events %d", c.name, len(ev))
		}
		if got {
			d := data(t, ev[0])
			if ev[0].EventName() != "SealExceptionRaised" || d["trailer_id"] != "trl-1" || d["expected_seal_number"] != "SL-1" || d["found_seal_number"] != "SL-2" ||
				d["appointment_id"] != "appt-1" || d["raised_at"] != "2026-11-05T14:01:00Z" || d["site_id"] != "WH1" || len(d) != 6 {
				t.Errorf("%s: data %v", c.name, d)
			}
			if _, has := d["version"]; has {
				t.Error("SealExceptionRaised carries no version")
			}
		}
	}
	b := newTrailer(t, func(in *trailer.ArrivalInput) { in.SealNumber = "SL-2" })
	must(t, b.BreakSeal(at(1)))
	b.PullEvents()
	if b.CheckSeal("SL-1", at(2)) {
		t.Error("a broken seal is not compared")
	}
}

func TestNewSealExceptionForARejectedVisit(t *testing.T) {
	e := trailer.NewSealException("visit-9", "WH1", "", "SL-1", "SL-2", at(1))
	d := data(t, e)
	if e.EventName() != "SealExceptionRaised" || e.Entity() != "trailer" || e.AggregateID() != "visit-9" || d["trailer_id"] != nil || d["appointment_id"] != nil ||
		d["expected_seal_number"] != "SL-1" || d["found_seal_number"] != "SL-2" || d["site_id"] != "WH1" || d["raised_at"] != "2026-11-05T14:01:00Z" || len(d) != 6 {
		t.Fatalf("data %v", d)
	}
}

func TestRecordLoadState(t *testing.T) {
	tr := parked(t)
	must(t, tr.RecordLoadState(shared.LoadPartial))
	if tr.Snapshot().LoadState != shared.LoadPartial || tr.Version() != 3 {
		t.Fatal("recorded")
	}
	if err := tr.RecordLoadState("FULL"); !errors.Is(err, shared.ErrValidation) {
		t.Errorf("bad load: %v", err)
	}
	g := parked(t)
	must(t, g.Depart("v", at(2)))
	if err := g.RecordLoadState(shared.LoadEmpty); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("departed: %v", err)
	}
}

func TestDwellMinutes(t *testing.T) {
	tr := parked(t)
	if tr.DwellMinutes(at(90)) != 90 || tr.DwellMinutes(at(-5)) != 0 {
		t.Fatal("dwell while on site")
	}
	must(t, tr.Depart("v", at(30)))
	if tr.DwellMinutes(at(500)) != 30 {
		t.Fatal("dwell stops at departure")
	}
}

func TestSweepDwell(t *testing.T) {
	tr := parked(t)
	if tr.SweepDwell(at(59), 60) || len(tr.PullEvents()) != 0 {
		t.Fatal("below the threshold")
	}
	if tr.SweepDwell(at(100), 0) {
		t.Fatal("a non-positive threshold never alerts")
	}
	if !tr.SweepDwell(at(60), 60) {
		t.Fatal("at the threshold")
	}
	e := only(t, tr)
	header(t, e, "TrailerDwellExceeded", at(60))
	eq(t, data(t, e), map[string]any{
		"trailer_id": "trl-1", "site_id": "WH1", "kind": "DRY_VAN", "dwell_minutes": float64(60), "threshold_minutes": float64(60), "location_code": "WH1-P1",
	})
	if tr.SweepDwell(at(600), 60) || len(tr.PullEvents()) != 0 {
		t.Fatal("alerted once per trailer")
	}
	if tr.Version() != 3 || tr.Snapshot().DwellAlertedAt.IsZero() {
		t.Fatal("the alert marker is persisted and is an accepted change (version +1)")
	}
}

func TestSweepDwellThresholdOfOneMinuteIsValid(t *testing.T) {
	tr := parked(t)
	if !tr.SweepDwell(at(1), 1) {
		t.Fatal("a one-minute threshold alerts at one minute of dwell")
	}
}

func TestSweepDwellSkipsDepartedAndAlertsInMove(t *testing.T) {
	g := parked(t)
	must(t, g.Depart("v", at(10)))
	g.PullEvents()
	if g.SweepDwell(at(1000), 5) {
		t.Fatal("departed trailers are never alerted")
	}
	m := newTrailer(t)
	if !m.SweepDwell(at(100), 60) {
		t.Fatal("a trailer in a move still has a dwell")
	}
	if d := data(t, only(t, m)); d["location_code"] != nil {
		t.Fatalf("no location is null: %v", d["location_code"])
	}
}
