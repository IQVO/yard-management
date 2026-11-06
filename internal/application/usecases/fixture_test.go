package usecases_test

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
	"github.com/claudioed/yard-management/internal/testing/yardtest"
)

// yardEnv is a site WH1 with spots H1 (holding), P1, P2 and doors D1 (Both),
// D2 (Outbound), D3 (Inbound).
func yardEnv() *yardtest.Env {
	e := yardtest.New()
	e.Site("WH1", []string{"WH1-H1", "WH1-P1", "WH1-P2"}, map[string]string{"WH1-D1": "Both", "WH1-D2": "Outbound", "WH1-D3": "Inbound"})
	return e
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func is(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func checkInput(mod ...func(*usecases.CheckInInput)) usecases.CheckInInput {
	in := usecases.CheckInInput{
		SiteID: "WH1", Carrier: "Acme Freight", CarrierSCAC: "ACME", TractorPlate: "PLATE1", DriverRef: "drv-1", Purpose: "LIVE_UNLOAD",
		Trailers: []usecases.TrailerInput{{UnitNumber: "T1", Kind: "DRY_VAN", LoadState: "LOADED"}},
	}
	for _, m := range mod {
		m(&in)
	}
	return in
}

func checkIn(t *testing.T, e *yardtest.Env, mod ...func(*usecases.CheckInInput)) views.CheckIn {
	t.Helper()
	out, err := e.Yard.CheckInVisit(e.Ctx, checkInput(mod...))
	must(t, err)
	return out
}

// complete finishes a VISITING_DRIVER move at its destination.
func complete(t *testing.T, e *yardtest.Env, moveID string) views.Move {
	t.Helper()
	mv, err := e.Yard.CompleteMove(e.Ctx, usecases.CompleteMoveInput{MoveID: moveID})
	must(t, err)
	return mv
}

// arrived checks a visit in and completes its first move: the trailer rests.
func arrived(t *testing.T, e *yardtest.Env, mod ...func(*usecases.CheckInInput)) (views.CheckIn, views.Trailer) {
	t.Helper()
	ci := checkIn(t, e, mod...)
	complete(t, e, ci.Moves[0].MoveID)
	tr, err := e.Yard.GetTrailer(e.Ctx, ci.Trailers[0].TrailerID)
	must(t, err)
	return ci, tr
}

func appointment(id, door string, start time.Time, mod ...func(*views.Appointment)) views.Appointment {
	a := views.Appointment{
		ID: id, SiteID: "WH1", Direction: "INBOUND", DoorCode: door, CarrierSCAC: "ACME", WindowStart: start, WindowEnd: start.Add(time.Hour),
		ASNNumbers: []string{"ASN-1"}, ExpectedSeal: "SL-1",
	}
	for _, m := range mod {
		m(&a)
	}
	return a
}

func withAppointment(id string) func(*usecases.CheckInInput) {
	return func(in *usecases.CheckInInput) { in.AppointmentID = id }
}

func spot(code string) shared.Location { return shared.Location{Kind: shared.Spot, Code: code} }
func door(code string) shared.Location { return shared.Location{Kind: shared.Door, Code: code} }

func contains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// requireAppointments declares a policy that refuses walk-ins.
func requireAppointments(t *testing.T, e *yardtest.Env) {
	t.Helper()
	_, err := e.Yard.DeclareYardPolicy(e.Ctx, usecases.DeclarePolicyInput{SiteID: "WH1", Policy: yardpolicy.DeclareInput{
		RequireAppointment: true, LeaseSeconds: 300, SealMismatchAction: yardpolicy.Flag}})
	must(t, err)
}
