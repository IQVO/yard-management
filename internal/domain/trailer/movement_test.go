package trailer_test

import (
	"errors"
	"testing"

	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
)

func TestOccupancyCheckFree(t *testing.T) {
	var o trailer.Occupancy
	must(t, o.CheckFree(spot1, "", "trl-1"))
	must(t, o.CheckFree(spot1, "trl-1", "trl-1"))
	if err := o.CheckFree(spot1, "trl-2", "trl-1"); !errors.Is(err, trailer.ErrLocationOccupied) {
		t.Fatalf("want ErrLocationOccupied, got %v", err)
	}
}

func TestCanPlaceAt(t *testing.T) {
	cases := []struct {
		name string
		tr   func() *trailer.Trailer
		p    func(*trailer.Placement)
		want error
	}{
		{"free spot", func() *trailer.Trailer { return newTrailer(t) }, func(p *trailer.Placement) { *p = placeAt(spot1) }, nil},
		{"free door", func() *trailer.Trailer { return newTrailer(t) }, nil, nil},
		{"zero target", nil, func(p *trailer.Placement) { *p = trailer.Placement{} }, trailer.ErrUnknownLocation},
		{"slot is another location", nil, func(p *trailer.Placement) { p.Slot.Location = door1; p.Target = spot1 }, trailer.ErrUnknownLocation},
		{"inactive slot", nil, func(p *trailer.Placement) { p.Slot.Active = false }, trailer.ErrUnknownLocation},
		{"slot of another site", nil, func(p *trailer.Placement) { p.Slot.Site = "WH2" }, trailer.ErrUnknownLocation},
		{"occupied spot", nil, func(p *trailer.Placement) { *p = placeAt(spot1); p.OccupiedBy = "trl-9" }, trailer.ErrLocationOccupied},
		{"occupied by itself", nil, func(p *trailer.Placement) { p.OccupiedBy = "trl-1" }, nil},
		{"closed door", nil, func(p *trailer.Placement) { p.Closed = true }, trailer.ErrDoorClosed},
		{"closed flag is irrelevant for a spot", nil, func(p *trailer.Placement) { *p = placeAt(spot1); p.Closed = true }, nil},
		{"inbound at outbound door", nil, func(p *trailer.Placement) { p.Slot.DockFlow = trailer.FlowOutbound }, trailer.ErrDoorFlowMismatch},
		{"inbound at inbound door", nil, func(p *trailer.Placement) { p.Slot.DockFlow = trailer.FlowInbound }, nil},
		{"inbound at door without flow", nil, func(p *trailer.Placement) { p.Slot.DockFlow = "" }, nil},
		{"outbound by purpose at inbound door", func() *trailer.Trailer {
			return newTrailer(t, func(in *trailer.ArrivalInput) {
				in.Purpose, in.ASNNumbers, in.Load = shared.PurposeLiveLoad, nil, shared.LoadEmpty
			})
		}, func(p *trailer.Placement) { p.Slot.DockFlow = trailer.FlowInbound }, trailer.ErrDoorFlowMismatch},
		{"outbound by purpose at outbound door", func() *trailer.Trailer {
			return newTrailer(t, func(in *trailer.ArrivalInput) {
				in.Purpose, in.ASNNumbers, in.Load = shared.PurposePickup, nil, shared.LoadEmpty
			})
		}, func(p *trailer.Placement) { p.Slot.DockFlow = trailer.FlowOutbound }, nil},
		{"outbound by reference at inbound door", func() *trailer.Trailer {
			return newTrailer(t, func(in *trailer.ArrivalInput) {
				in.ASNNumbers, in.OutboundRef, in.Purpose = nil, "SHP-1", shared.PurposeDrop
			})
		}, func(p *trailer.Placement) { p.Slot.DockFlow = trailer.FlowInbound }, trailer.ErrDoorFlowMismatch},
		{"empty trailer that held ASNs is unconstrained", func() *trailer.Trailer {
			return newTrailer(t, func(in *trailer.ArrivalInput) { in.Load, in.Purpose = shared.LoadEmpty, shared.PurposeDrop })
		}, func(p *trailer.Placement) { p.Slot.DockFlow = trailer.FlowOutbound }, nil},
		{"no contents is unconstrained", func() *trailer.Trailer {
			return newTrailer(t, func(in *trailer.ArrivalInput) { in.ASNNumbers, in.Purpose = nil, shared.PurposeDrop })
		}, func(p *trailer.Placement) { p.Slot.DockFlow = trailer.FlowOutbound }, nil},
		{"held trailer at a door", func() *trailer.Trailer {
			tr := newTrailer(t)
			must(t, tr.PlaceHold(trailer.HoldQuality, at(1)))
			return tr
		}, nil, trailer.ErrTrailerOnHold},
		{"held trailer on a spot", func() *trailer.Trailer {
			tr := newTrailer(t)
			must(t, tr.PlaceHold(trailer.HoldQuality, at(1)))
			return tr
		}, func(p *trailer.Placement) { *p = placeAt(spot1) }, nil},
	}
	for _, c := range cases {
		tr := newTrailer(t)
		if c.tr != nil {
			tr = c.tr()
		}
		p := placeAt(door1)
		if c.p != nil {
			c.p(&p)
		}
		err := tr.CanPlaceAt(p)
		if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
}

func TestArriveAtSpot(t *testing.T) {
	tr := newTrailer(t)
	must(t, tr.ArriveAt(placeAt(spot1), at(2)))
	s := tr.Snapshot()
	if s.State != trailer.Parked || s.Location != spot1 || s.Version != 2 || s.DoorWork != "" {
		t.Fatalf("snapshot %+v", s)
	}
	e := only(t, tr)
	if e.EventName() != "TrailerParked" || e.Entity() != "trailer" || e.AggregateID() != "trl-1" || !e.OccurredAt().Equal(at(2)) {
		t.Fatalf("event header %+v", e)
	}
	d := data(t, e)
	want := map[string]any{"trailer_id": "trl-1", "site_id": "WH1", "spot_code": "WH1-P1", "version": float64(2)}
	if len(d) != len(want) {
		t.Fatalf("data %v", d)
	}
	for k, v := range want {
		if d[k] != v {
			t.Errorf("%s = %v, want %v", k, d[k], v)
		}
	}
}

func TestArriveAtDoor(t *testing.T) {
	tr := newTrailer(t, func(in *trailer.ArrivalInput) { in.ASNNumbers = []string{"ASN-1", "ASN-2"} })
	must(t, tr.ArriveAt(placeAt(door1), at(2)))
	s := tr.Snapshot()
	if s.State != trailer.Docked || s.Location != door1 || s.DoorWork != trailer.WorkWaiting || s.Version != 2 {
		t.Fatalf("snapshot %+v", s)
	}
	eq(t, data(t, only(t, tr)), map[string]any{
		"trailer_id": "trl-1", "site_id": "WH1", "door_code": "WH1-D1", "appointment_id": "appt-1", "asn_numbers": []any{"ASN-1", "ASN-2"},
		"visit_id": "visit-1", "docked_at": "2026-11-05T14:02:00Z", "version": float64(2), "seal_state": nil, "seal_class": nil,
		"hazmat_class": nil, "un_number": nil, "requires_temperature_control": false,
	})
}

func TestArriveAtDoorWalkInHasNullsAndEmptyList(t *testing.T) {
	tr := newTrailer(t, func(in *trailer.ArrivalInput) { in.AppointmentID, in.ASNNumbers = "", nil })
	must(t, tr.ArriveAt(placeAt(door1), at(2)))
	d := data(t, only(t, tr))
	if d["appointment_id"] != nil {
		t.Errorf("appointment_id must be null, got %v", d["appointment_id"])
	}
	if l, ok := d["asn_numbers"].([]any); !ok || len(l) != 0 {
		t.Errorf("asn_numbers must be [], got %v", d["asn_numbers"])
	}
}

func TestArriveAtRefusals(t *testing.T) {
	if err := parked(t).ArriveAt(placeAt(spot2), at(3)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("a PARKED trailer cannot arrive: %v", err)
	}
	tr := newTrailer(t)
	p := placeAt(spot1)
	p.OccupiedBy = "trl-9"
	if err := tr.ArriveAt(p, at(2)); !errors.Is(err, trailer.ErrLocationOccupied) {
		t.Fatalf("occupied: %v", err)
	}
	if tr.State() != trailer.InMove || tr.Version() != 1 || len(tr.PullEvents()) != 0 {
		t.Fatal("a refused arrival changes nothing")
	}
}

func TestStartMoving(t *testing.T) {
	tr := parked(t)
	must(t, tr.StartMoving(at(5)))
	s := tr.Snapshot()
	if s.State != trailer.InMove || !s.Location.IsZero() || s.Origin != spot1 || s.Version != 3 {
		t.Fatalf("snapshot %+v", s)
	}
	if len(tr.PullEvents()) != 0 {
		t.Fatal("leaving a spot raises nothing")
	}
	if err := tr.StartMoving(at(6)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("IN_MOVE cannot start moving: %v", err)
	}
}

func TestStartMovingFromDoorRaisesUndocked(t *testing.T) {
	tr := docked(t)
	must(t, tr.StartMoving(at(9)))
	s := tr.Snapshot()
	if s.State != trailer.InMove || s.Origin != door1 || s.OriginWork != trailer.WorkWaiting || s.DoorWork != "" || s.Version != 3 {
		t.Fatalf("snapshot %+v", s)
	}
	e := only(t, tr)
	d := data(t, e)
	if e.EventName() != "TrailerUndocked" || d["door_code"] != "WH1-D1" || d["undocked_at"] != "2026-11-05T14:09:00Z" || d["version"] != float64(3) ||
		d["appointment_id"] != "appt-1" || d["trailer_id"] != "trl-1" || d["site_id"] != "WH1" || len(d) != 6 {
		t.Fatalf("data %v", d)
	}
}

func TestStartMovingRefusals(t *testing.T) {
	if err := newTrailer(t).StartMoving(at(2)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("IN_MOVE: %v", err)
	}
	g := parked(t)
	must(t, g.Depart("visit-2", at(5)))
	if err := g.StartMoving(at(6)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("DEPARTED: %v", err)
	}
}

func TestReturnToOrigin(t *testing.T) {
	tr := docked(t)
	must(t, tr.StartDoorWork(at(2)))
	tr.PullEvents()
	must(t, tr.StartMoving(at(3)))
	tr.PullEvents()
	must(t, tr.ReturnToOrigin("", at(4)))
	s := tr.Snapshot()
	if s.State != trailer.Docked || s.Location != door1 || s.DoorWork != trailer.WorkInProgress || !s.Origin.IsZero() || s.OriginWork != "" || s.Version != 5 {
		t.Fatalf("snapshot %+v", s)
	}
	if e := only(t, tr); e.EventName() != "TrailerDocked" {
		t.Fatalf("event %s", e.EventName())
	}
}

func TestReturnToOriginSpotAndRefusals(t *testing.T) {
	tr := parked(t)
	must(t, tr.StartMoving(at(3)))
	if err := tr.ReturnToOrigin("trl-9", at(4)); !errors.Is(err, trailer.ErrLocationOccupied) {
		t.Fatalf("occupied origin: %v", err)
	}
	if tr.State() != trailer.InMove {
		t.Fatal("refused return keeps the trailer in the move")
	}
	must(t, tr.ReturnToOrigin("trl-1", at(5)))
	if tr.State() != trailer.Parked || tr.Location() != spot1 {
		t.Fatal("returned to the spot")
	}
	if e := only(t, tr); e.EventName() != "TrailerParked" {
		t.Fatalf("event %s", e.EventName())
	}
	if err := newTrailer(t).ReturnToOrigin("", at(2)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("a gate trailer has no origin: %v", err)
	}
	if err := parked(t).ReturnToOrigin("", at(2)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("a PARKED trailer has nothing to return from: %v", err)
	}
}

func TestDepartFromSpot(t *testing.T) {
	tr := parked(t, func(in *trailer.ArrivalInput) { in.SealNumber = "SL-9" })
	must(t, tr.Depart("visit-2", at(61)))
	s := tr.Snapshot()
	if s.State != trailer.Departed || !s.Location.IsZero() || s.VisitID != "visit-2" || !s.DepartedAt.Equal(at(61)) || s.Version != 3 {
		t.Fatalf("snapshot %+v", s)
	}
	e := only(t, tr)
	header(t, e, "TrailerDeparted", at(61))
	eq(t, data(t, e), map[string]any{
		"trailer_id": "trl-1", "site_id": "WH1", "visit_id": "visit-2", "departed_at": "2026-11-05T15:01:00Z", "dwell_minutes": float64(61),
		"load_state": "LOADED", "seal_number": "SL-9", "version": float64(3), "seal_state": "INTACT", "seal_class": nil,
	})
}

func TestDepartKeepsBringingVisitWhenNoneGiven(t *testing.T) {
	tr := parked(t)
	must(t, tr.Depart("", at(2)))
	if tr.Snapshot().VisitID != "visit-1" {
		t.Fatal("visit id kept")
	}
	if d := data(t, only(t, tr)); d["seal_number"] != nil {
		t.Fatalf("no seal is null: %v", d["seal_number"])
	}
}

func TestDepartFromDoor(t *testing.T) {
	tr := worked(t)
	must(t, tr.Depart("visit-1", at(30)))
	ev := tr.PullEvents()
	if len(ev) != 2 || ev[0].EventName() != "TrailerUndocked" || ev[1].EventName() != "TrailerDeparted" {
		t.Fatalf("events %+v", ev)
	}
	if data(t, ev[0])["version"] != data(t, ev[1])["version"] || data(t, ev[1])["load_state"] != "EMPTY" {
		t.Fatal("one change, one version, load state EMPTY after the unload")
	}
}

func TestDepartRefusals(t *testing.T) {
	d := docked(t)
	if err := d.Depart("visit-1", at(5)); !errors.Is(err, trailer.ErrDoorWorkOpen) {
		t.Fatalf("WAITING: %v", err)
	}
	must(t, d.StartDoorWork(at(6)))
	if err := d.Depart("visit-1", at(7)); !errors.Is(err, trailer.ErrDoorWorkOpen) {
		t.Fatalf("IN_PROGRESS: %v", err)
	}
	if d.State() != trailer.Docked || d.Version() != 3 {
		t.Fatal("a refused departure changes nothing")
	}
	if err := newTrailer(t).Depart("visit-1", at(2)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("IN_MOVE: %v", err)
	}
	g := parked(t)
	must(t, g.Depart("visit-1", at(2)))
	if err := g.Depart("visit-1", at(3)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("DEPARTED: %v", err)
	}
}

func TestDepartBeforeArrivalClampsDwellToZero(t *testing.T) {
	tr := parked(t)
	must(t, tr.Depart("v", at(-30)))
	if data(t, only(t, tr))["dwell_minutes"] != float64(0) {
		t.Fatal("dwell never negative")
	}
}
