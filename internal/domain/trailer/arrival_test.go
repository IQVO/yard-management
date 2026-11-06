package trailer_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
)

func TestRecordArrival(t *testing.T) {
	tr := newTrailer(t)
	want := trailer.Snapshot{
		ID: "trl-1", SiteID: "WH1", SCAC: "ACME", Number: "TR-1042", Kind: shared.KindDryVan, LoadState: shared.LoadLoaded,
		ASNNumbers: []string{"ASN-1"}, Purpose: shared.PurposeLiveUnload, SealState: trailer.SealNone, State: trailer.InMove,
		VisitID: "visit-1", AppointmentID: "appt-1", ArrivedAt: t0, Version: 1,
	}
	if got := tr.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot\n got %+v\nwant %+v", got, want)
	}
	if len(tr.PullEvents()) != 0 {
		t.Fatal("arrival raises no event: VisitCheckedIn covers it")
	}
	sealed := newTrailer(t, func(in *trailer.ArrivalInput) { in.SealNumber = "SL-1" })
	if ss := sealed.Snapshot(); ss.SealState != trailer.SealIntact || ss.SealNumber != "SL-1" {
		t.Fatalf("a seal number at arrival is INTACT: %+v", ss)
	}
}

func TestArrivalAccessors(t *testing.T) {
	tr := newTrailer(t)
	checks := map[string]bool{
		"id": tr.ID() == "trl-1", "site": tr.SiteID() == "WH1", "state": tr.State() == trailer.InMove, "version": tr.Version() == 1,
		"appointment": tr.AppointmentID() == "appt-1", "kind": tr.Kind() == shared.KindDryVan, "hold": tr.Hold() == "",
		"door work": tr.DoorWork() == "", "location": tr.Location().IsZero(), "origin": tr.Origin().IsZero(),
	}
	for name, ok := range checks {
		if !ok {
			t.Errorf("accessor %s", name)
		}
	}
}

func TestRecordArrivalRejects(t *testing.T) {
	long := strings.Repeat("x", 65)
	cases := []struct {
		name string
		mod  func(*trailer.ArrivalInput)
		is   error
	}{
		{"blank id", func(in *trailer.ArrivalInput) { in.ID = " " }, shared.ErrValidation},
		{"long id", func(in *trailer.ArrivalInput) { in.ID = long }, shared.ErrValidation},
		{"blank site", func(in *trailer.ArrivalInput) { in.SiteID = "" }, shared.ErrValidation},
		{"blank number", func(in *trailer.ArrivalInput) { in.Number = "" }, shared.ErrValidation},
		{"long number", func(in *trailer.ArrivalInput) { in.Number = long }, shared.ErrValidation},
		{"blank visit", func(in *trailer.ArrivalInput) { in.VisitID = "" }, shared.ErrValidation},
		{"long seal", func(in *trailer.ArrivalInput) { in.SealNumber = long }, shared.ErrValidation},
		{"long appointment", func(in *trailer.ArrivalInput) { in.AppointmentID = long }, shared.ErrValidation},
		{"long outbound ref", func(in *trailer.ArrivalInput) { in.OutboundRef = strings.Repeat("x", 129) }, shared.ErrValidation},
		{"bad scac", func(in *trailer.ArrivalInput) { in.SCAC = "acme" }, shared.ErrInvalidCarrier},
		{"bad kind", func(in *trailer.ArrivalInput) { in.Kind = "BOX" }, shared.ErrValidation},
		{"bad load", func(in *trailer.ArrivalInput) { in.Load = "FULL" }, shared.ErrValidation},
		{"bad purpose", func(in *trailer.ArrivalInput) { in.Purpose = "VISIT" }, shared.ErrValidation},
		{"zero time", func(in *trailer.ArrivalInput) { in.At = time.Time{} }, shared.ErrValidation},
	}
	for _, c := range cases {
		in := arrival()
		c.mod(&in)
		if _, err := trailer.RecordArrival(in); !errors.Is(err, c.is) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.is)
		}
	}
	ok := arrival()
	ok.SCAC, ok.AppointmentID, ok.ASNNumbers = "", "", nil
	if _, err := trailer.RecordArrival(ok); err != nil {
		t.Errorf("scac, appointment and ASNs are optional: %v", err)
	}
}

func TestAccessorsAfterMovement(t *testing.T) {
	tr := parked(t)
	if tr.State() != trailer.Parked || tr.Location() != spot1 || tr.Version() != 2 {
		t.Fatalf("parked: %v %v %d", tr.State(), tr.Location(), tr.Version())
	}
	must(t, tr.StartMoving(at(5)))
	if tr.Origin() != spot1 || tr.State() != trailer.InMove {
		t.Fatal("origin remembered")
	}
}

func TestReleasedForDeparture(t *testing.T) {
	if parked(t).ReleasedForDeparture(true) {
		t.Error("an open move blocks release")
	}
	if !parked(t).ReleasedForDeparture(false) {
		t.Error("parked and free is released")
	}
	if newTrailer(t).ReleasedForDeparture(false) {
		t.Error("IN_MOVE is not released")
	}
	if docked(t).ReleasedForDeparture(false) {
		t.Error("docked with door work WAITING is not released")
	}
	d := docked(t)
	must(t, d.StartDoorWork(at(2)))
	if d.ReleasedForDeparture(false) {
		t.Error("docked with door work IN_PROGRESS is not released")
	}
	w := worked(t)
	if !w.ReleasedForDeparture(false) || w.ReleasedForDeparture(true) {
		t.Error("docked with door work DONE is released unless a move is open")
	}
}
