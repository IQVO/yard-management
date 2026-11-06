package usecases_test

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
	"github.com/claudioed/yard-management/internal/testing/yardtest"
)

func TestCheckInWalkInGoesToAHoldingSpot(t *testing.T) {
	e := yardEnv()
	ci := checkIn(t, e)
	if ci.Visit.State != "CHECKED_IN" || len(ci.Trailers) != 1 || len(ci.Moves) != 1 {
		t.Fatalf("check-in %+v", ci)
	}
	mv := ci.Moves[0]
	if mv.Performer != "VISITING_DRIVER" || mv.State != "CLAIMED" || mv.Reason != "TO_HOLDING" || mv.To != spot("WH1-H1") || mv.From != nil || mv.HostlerRef != nil {
		t.Fatalf("move %+v", mv)
	}
	assertInMove(t, ci.Trailers[0], mv.MoveID)
	names := e.Store.EventNames()
	if len(names) != 2 || names[0] != "VisitCheckedIn" || names[1] != "YardMoveRequested" {
		t.Fatalf("events %v", names)
	}
}

func TestCheckInPrefersDeclaredHoldingSpots(t *testing.T) {
	e := yardEnv()
	_, err := e.Yard.DeclareYardPolicy(e.Ctx, usecases.DeclarePolicyInput{SiteID: "WH1", Policy: yardpolicy.DeclareInput{
		HoldingSpotCodes: []string{"WH1-P2"}, LeaseSeconds: 300, SealMismatchAction: yardpolicy.Flag}})
	must(t, err)
	ci := checkIn(t, e)
	if ci.Moves[0].To != spot("WH1-P2") {
		t.Fatalf("holding spot %+v", ci.Moves[0].To)
	}
}

func TestCheckInWithAnOnTimeAppointmentGoesToItsDoor(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	ci := checkIn(t, e, withAppointment("appt-1"))
	mv := ci.Moves[0]
	if mv.Reason != "TO_DOOR" || mv.To != door("WH1-D1") || mv.AppointmentID == nil || *mv.AppointmentID != "appt-1" || mv.Priority.Band != 1 || mv.Priority.DueBy == nil {
		t.Fatalf("move %+v", mv)
	}
	if got := ci.Trailers[0].ASNNumbers; len(got) != 1 || got[0] != "ASN-1" {
		t.Fatalf("contents %v", got)
	}
	a, _, _ := e.Store.Appointments().Get(e.Ctx, "appt-1")
	if a.Status != views.ApptCheckedIn {
		t.Fatalf("appointment %s", a.Status)
	}
}

func TestCheckInEarlyArrivalGoesToHolding(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0.Add(2*time.Hour)))
	ci := checkIn(t, e, withAppointment("appt-1"))
	if ci.Moves[0].Reason != "TO_HOLDING" {
		t.Fatalf("an early arrival is held: %+v", ci.Moves[0])
	}
}

func TestCheckInFallsBackToASpotWhenTheDoorIsTaken(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	e.Book(appointment("appt-2", "WH1-D1", yardtest.T0, func(a *views.Appointment) { a.ASNNumbers = []string{"ASN-2"} }))
	arrived(t, e, withAppointment("appt-1"))
	second := checkIn(t, e, withAppointment("appt-2"), func(in *usecases.CheckInInput) {
		in.TractorPlate, in.Trailers[0].UnitNumber = "PLATE2", "T2"
	})
	if second.Moves[0].Reason != "TO_SPOT" || second.Moves[0].To.Kind != shared.Spot {
		t.Fatalf("move %+v", second.Moves[0])
	}
}

func TestCheckInFallsBackWhenTheDoorFlowDoesNotFit(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D2", yardtest.T0))
	ci := checkIn(t, e, withAppointment("appt-1"))
	if ci.Moves[0].Reason != "TO_SPOT" {
		t.Fatalf("an inbound trailer does not go to an Outbound door: %+v", ci.Moves[0])
	}
}

func TestCheckInRefusals(t *testing.T) {
	cases := []struct {
		name string
		prep func(*testing.T, *yardtest.Env)
		mod  func(*usecases.CheckInInput)
		want error
	}{
		{"required and none named", func(t *testing.T, y *yardtest.Env) { requireAppointments(t, y) }, nil, yardvisit.ErrAppointmentRequired},
		{"unknown appointment", nil, withAppointment("appt-x"), yardvisit.ErrUnknownAppointment},
		{"open visit of the plate", func(t *testing.T, y *yardtest.Env) { checkIn(t, y) }, nil, yardvisit.ErrVisitAlreadyOpen},
		{"unit already on site", func(t *testing.T, y *yardtest.Env) { checkIn(t, y) }, func(in *usecases.CheckInInput) { in.TractorPlate = "OTHER" }, trailer.ErrTrailerAlreadyOnSite},
		{"same unit twice in one visit", nil, func(in *usecases.CheckInInput) { in.Trailers = append(in.Trailers, in.Trailers[0]) }, trailer.ErrTrailerAlreadyOnSite},
		{"invalid carrier", nil, func(in *usecases.CheckInInput) { in.Carrier = " " }, shared.ErrInvalidCarrier},
		{"bad trailer kind", nil, func(in *usecases.CheckInInput) { in.Trailers[0].Kind = "BOX" }, shared.ErrValidation},
	}
	for _, c := range cases {
		e := yardEnv()
		if c.prep != nil {
			c.prep(t, e)
		}
		in := checkInput()
		if c.mod != nil {
			c.mod(&in)
		}
		before := len(e.Store.Events())
		_, err := e.Yard.CheckInVisit(e.Ctx, in)
		if err == nil || !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
		if len(e.Store.Events()) != before {
			t.Errorf("%s: a refused check-in publishes nothing", c.name)
		}
	}
}

func TestCheckInWithNoFreeSpotQueuesTheGate(t *testing.T) {
	e := yardtest.New()
	e.Site("WH1", nil, map[string]string{"WH1-D1": "Both"})
	_, err := e.Yard.CheckInVisit(e.Ctx, checkInput())
	is(t, err, usecases.ErrNoFreeSpot)
	page, err := e.Yard.ListVisits(e.Ctx, views.VisitFilter{}, nil, 10)
	must(t, err)
	if len(page.Items) != 0 {
		t.Fatal("a refused check-in persists nothing")
	}
}

func TestSealMismatchFlagsAndStillAdmits(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	ci := checkIn(t, e, withAppointment("appt-1"), func(in *usecases.CheckInInput) { in.Trailers[0].SealNumber = "SL-9" })
	if ci.Visit.State != "CHECKED_IN" {
		t.Fatalf("FLAG admits: %+v", ci.Visit)
	}
	if !contains(e.Store.EventNames(), "SealExceptionRaised") || contains(e.Store.EventNames(), "VisitRejected") {
		t.Fatalf("events %v", e.Store.EventNames())
	}
}

func TestSealMismatchRejectsUnderThePolicy(t *testing.T) {
	e := yardEnv()
	_, err := e.Yard.DeclareYardPolicy(e.Ctx, usecases.DeclarePolicyInput{SiteID: "WH1", Policy: yardpolicy.DeclareInput{LeaseSeconds: 300, SealMismatchAction: yardpolicy.Reject}})
	must(t, err)
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	ci := checkIn(t, e, withAppointment("appt-1"), func(in *usecases.CheckInInput) { in.Trailers[0].SealNumber = "SL-9" })
	if ci.Visit.State != "REJECTED" || ci.Visit.RejectionReason == nil || *ci.Visit.RejectionReason != "SEAL_MISMATCH" || len(ci.Trailers) != 0 || len(ci.Moves) != 0 {
		t.Fatalf("check-in %+v", ci)
	}
	names := e.Store.EventNames()
	if len(names) != 2 || names[0] != "SealExceptionRaised" || names[1] != "VisitRejected" {
		t.Fatalf("events %v", names)
	}
	page, err := e.Yard.ListTrailers(e.Ctx, views.TrailerFilter{}, nil, 10)
	must(t, err)
	if len(page.Items) != 0 {
		t.Fatal("no trailer is persisted for a rejected visit")
	}
}

func TestRejectAtGate(t *testing.T) {
	e := yardEnv()
	v, err := e.Yard.RejectAtGate(e.Ctx, usecases.RejectInput{SiteID: "WH1", Carrier: "Acme", TractorPlate: "PLATE9", Reason: "SITE_REQUEST"})
	must(t, err)
	if v.State != "REJECTED" || v.RejectionReason == nil || *v.RejectionReason != "SITE_REQUEST" {
		t.Fatalf("visit %+v", v)
	}
	if names := e.Store.EventNames(); len(names) != 1 || names[0] != "VisitRejected" {
		t.Fatalf("events %v", names)
	}
	_, err = e.Yard.RejectAtGate(e.Ctx, usecases.RejectInput{SiteID: "WH1", Carrier: "Acme", TractorPlate: "PLATE9", Reason: "BORED"})
	is(t, err, shared.ErrValidation)
}

func assertInMove(t *testing.T, tr views.Trailer, moveID string) {
	t.Helper()
	if tr.State != "IN_MOVE" || tr.OpenMoveID == nil || *tr.OpenMoveID != moveID {
		t.Fatalf("trailer %+v", tr)
	}
}
