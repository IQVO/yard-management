package usecases_test

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
	"github.com/claudioed/yard-management/internal/testing/yardtest"
)

// secondUnit turns a check-in into another tractor with another trailer.
func secondUnit(plate, unit string) func(*usecases.CheckInInput) {
	return func(in *usecases.CheckInInput) { in.TractorPlate, in.Trailers[0].UnitNumber = plate, unit }
}

// parked checks a walk-in trailer in and rests it; the n-th call lands on H1, P1, P2.
func parked(t *testing.T, e *yardtest.Env, plate, unit string) views.Trailer {
	t.Helper()
	_, tr := arrived(t, e, secondUnit(plate, unit))
	return tr
}

func request(t *testing.T, e *yardtest.Env, trailerID string, to shared.Location, band int, due time.Time) views.Move {
	t.Helper()
	mv, err := e.Yard.RequestMove(e.Ctx, usecases.RequestMoveInput{TrailerID: trailerID, To: to, Band: band, DueBy: due})
	must(t, err)
	return mv
}

func claim(t *testing.T, e *yardtest.Env, hostler string) views.Move {
	t.Helper()
	mv, err := e.Yard.ClaimNextMove(e.Ctx, "WH1", hostler)
	must(t, err)
	return mv
}

func trailerOf(t *testing.T, e *yardtest.Env, id string) views.Trailer {
	t.Helper()
	tr, err := e.Yard.GetTrailer(e.Ctx, id)
	must(t, err)
	return tr
}

func TestRequestMoveReservesItsDestination(t *testing.T) {
	e := yardEnv()
	first := parked(t, e, "PLATE1", "T1")
	second := parked(t, e, "PLATE2", "T2")
	mv := request(t, e, first.TrailerID, spot("WH1-P2"), 0, time.Time{})
	if mv.Performer != "HOSTLER" || mv.State != "REQUESTED" || mv.Reason != "RELOCATE" || mv.Priority.Band != yardmove.DefaultBand || mv.From == nil || *mv.From != spot("WH1-H1") {
		t.Fatalf("move %+v", mv)
	}
	_, err := e.Yard.RequestMove(e.Ctx, usecases.RequestMoveInput{TrailerID: second.TrailerID, To: spot("WH1-P2")})
	is(t, err, yardmove.ErrDestinationReserved)
	_, err = e.Yard.RequestMove(e.Ctx, usecases.RequestMoveInput{TrailerID: first.TrailerID, To: door("WH1-D1")})
	is(t, err, yardmove.ErrTrailerHasOpenMove)
	if got := trailerOf(t, e, first.TrailerID); got.OpenMoveID == nil || *got.OpenMoveID != mv.MoveID {
		t.Fatalf("trailer %+v", got)
	}
}

func TestRequestMoveRefusals(t *testing.T) {
	cases := []struct {
		name string
		prep func(*testing.T, *yardtest.Env)
		to   shared.Location
		want error
	}{
		{"unknown location", nil, spot("WH1-NOPE"), shared.ErrUnknownLocation},
		{"a door is not a spot", nil, spot("WH1-D1"), shared.ErrUnknownLocation},
		{"occupied", func(t *testing.T, e *yardtest.Env) { parked(t, e, "PLATE2", "T2") }, spot("WH1-P1"), trailer.ErrLocationOccupied},
		{"already there", nil, spot("WH1-H1"), shared.ErrValidation},
		{"closed door", func(t *testing.T, e *yardtest.Env) {
			must(t, e.Yard.ScheduleClosure(e.Ctx, "ev-c", views.Closure{ID: "clo-1", SiteID: "WH1", DoorCode: "WH1-D1", From: yardtest.T0.Add(-time.Hour), To: yardtest.T0.Add(time.Hour)}))
		}, door("WH1-D1"), trailer.ErrDoorClosed},
		{"decommissioned slot", func(t *testing.T, e *yardtest.Env) { must(t, e.Yard.DecommissionSlot(e.Ctx, "ev-d", "WH1-P2")) }, spot("WH1-P2"), shared.ErrUnknownLocation},
	}
	for _, c := range cases {
		e := yardEnv()
		tr := parked(t, e, "PLATE1", "T1")
		if c.prep != nil {
			c.prep(t, e)
		}
		_, err := e.Yard.RequestMove(e.Ctx, usecases.RequestMoveInput{TrailerID: tr.TrailerID, To: c.to})
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	e := yardEnv()
	_, err := e.Yard.RequestMove(e.Ctx, usecases.RequestMoveInput{TrailerID: "trl-404", To: spot("WH1-P1")})
	is(t, err, usecases.ErrTrailerNotFound)
}

func TestRequestMoveJudgesTheDoorFlowAndTheHold(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0.Add(3*time.Hour)))
	_, in := arrived(t, e, withAppointment("appt-1")) // early: parked in holding, carries ASN-1
	_, err := e.Yard.RequestMove(e.Ctx, usecases.RequestMoveInput{TrailerID: in.TrailerID, To: door("WH1-D2")})
	is(t, err, trailer.ErrDoorFlowMismatch)
	_, err = e.Yard.PlaceHold(e.Ctx, in.TrailerID, trailer.HoldQuality)
	must(t, err)
	_, err = e.Yard.RequestMove(e.Ctx, usecases.RequestMoveInput{TrailerID: in.TrailerID, To: door("WH1-D3")})
	is(t, err, trailer.ErrTrailerOnHold)
	_, err = e.Yard.RequestMove(e.Ctx, usecases.RequestMoveInput{TrailerID: in.TrailerID, To: spot("WH1-P1")})
	must(t, err) // a held trailer can still move between spots
}

func TestRequestMoveDerivesFromDoorForADockedTrailer(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	_, docked := arrived(t, e, withAppointment("appt-1"))
	mv := request(t, e, docked.TrailerID, spot("WH1-P1"), 3, time.Time{})
	if mv.Reason != "FROM_DOOR" || mv.From == nil || *mv.From != door("WH1-D1") || mv.Priority.Band != 3 {
		t.Fatalf("move %+v", mv)
	}
}

func TestClaimNextPullsByBandThenDueByThenRequestTime(t *testing.T) {
	e := yardEnv()
	t1 := parked(t, e, "PLATE1", "T1")
	t2 := parked(t, e, "PLATE2", "T2")
	t3 := parked(t, e, "PLATE3", "T3")
	late := request(t, e, t1.TrailerID, door("WH1-D1"), 5, time.Time{})
	soon := request(t, e, t2.TrailerID, door("WH1-D2"), 2, yardtest.T0.Add(30*time.Minute))
	later := request(t, e, t3.TrailerID, door("WH1-D3"), 2, yardtest.T0.Add(time.Hour))
	for i, want := range []string{soon.MoveID, later.MoveID, late.MoveID} {
		got := claim(t, e, "hostler-"+string(rune('a'+i)))
		if got.MoveID != want || got.State != "CLAIMED" || got.HostlerRef == nil || got.LeaseExpiresAt == nil || !got.LeaseExpiresAt.Equal(yardtest.T0.Add(300*time.Second)) {
			t.Fatalf("claim %d = %+v, want move %s", i, got, want)
		}
	}
	_, err := e.Yard.ClaimNextMove(e.Ctx, "WH1", "hostler-z")
	is(t, err, usecases.ErrNoClaimableMove)
	if got := trailerOf(t, e, t2.TrailerID); got.State != "IN_MOVE" || got.Location != nil {
		t.Fatalf("the trailer leaves its place at the claim: %+v", got)
	}
}

func TestAHostlerHoldsOneClaimedMove(t *testing.T) {
	e := yardEnv()
	t1 := parked(t, e, "PLATE1", "T1")
	t2 := parked(t, e, "PLATE2", "T2")
	request(t, e, t1.TrailerID, door("WH1-D1"), 0, time.Time{})
	request(t, e, t2.TrailerID, door("WH1-D2"), 0, time.Time{})
	claim(t, e, "hostler-a")
	_, err := e.Yard.ClaimNextMove(e.Ctx, "WH1", "hostler-a")
	is(t, err, yardmove.ErrHostlerBusy)
	_, err = e.Yard.ClaimNextMove(e.Ctx, "WH1", " ")
	is(t, err, shared.ErrValidation)
}

func TestClaimUsesTheDeclaredLease(t *testing.T) {
	e := yardEnv()
	declare(t, e, func(in *policyInput) { in.LeaseSeconds = 60 })
	tr := parked(t, e, "PLATE1", "T1")
	request(t, e, tr.TrailerID, door("WH1-D1"), 0, time.Time{})
	if got := claim(t, e, "hostler-a"); got.LeaseExpiresAt == nil || !got.LeaseExpiresAt.Equal(yardtest.T0.Add(time.Minute)) {
		t.Fatalf("lease %+v", got.LeaseExpiresAt)
	}
}

func TestCompleteMoveDocksTheTrailerForTheLeaseHolder(t *testing.T) {
	e := yardEnv()
	tr := parked(t, e, "PLATE1", "T1")
	mv := request(t, e, tr.TrailerID, door("WH1-D1"), 0, time.Time{})
	claim(t, e, "hostler-a")
	_, err := e.Yard.CompleteMove(e.Ctx, usecases.CompleteMoveInput{MoveID: mv.MoveID, HostlerRef: "hostler-b"})
	is(t, err, yardmove.ErrNotLeaseHolder)
	done, err := e.Yard.CompleteMove(e.Ctx, usecases.CompleteMoveInput{MoveID: mv.MoveID, HostlerRef: "hostler-a"})
	must(t, err)
	if done.State != "COMPLETED" || done.ActualLocation == nil || *done.ActualLocation != door("WH1-D1") {
		t.Fatalf("move %+v", done)
	}
	docked := trailerOf(t, e, tr.TrailerID)
	if docked.State != "DOCKED" || docked.Location == nil || *docked.Location != door("WH1-D1") || docked.DoorWork == nil || *docked.DoorWork != "WAITING" || docked.OpenMoveID != nil {
		t.Fatalf("trailer %+v", docked)
	}
	names := e.Store.EventNames()
	if !contains(names, "TrailerDocked") || !contains(names, "YardMoveCompleted") {
		t.Fatalf("events %v", names)
	}
	_, err = e.Yard.CompleteMove(e.Ctx, usecases.CompleteMoveInput{MoveID: "mv-404"})
	is(t, err, usecases.ErrMoveNotFound)
	_, err = e.Yard.CompleteMove(e.Ctx, usecases.CompleteMoveInput{MoveID: mv.MoveID, HostlerRef: "hostler-a"})
	is(t, err, shared.ErrIllegalTransition)
}

func TestCompleteMoveElsewhereNeedsANote(t *testing.T) {
	e := yardEnv()
	tr := parked(t, e, "PLATE1", "T1")
	mv := request(t, e, tr.TrailerID, door("WH1-D1"), 0, time.Time{})
	claim(t, e, "hostler-a")
	_, err := e.Yard.CompleteMove(e.Ctx, usecases.CompleteMoveInput{MoveID: mv.MoveID, HostlerRef: "hostler-a", Actual: spot("WH1-P2")})
	is(t, err, yardmove.ErrUnexplainedDestination)
	done, err := e.Yard.CompleteMove(e.Ctx, usecases.CompleteMoveInput{MoveID: mv.MoveID, HostlerRef: "hostler-a", Actual: spot("WH1-P2"), Note: "door was busy"})
	must(t, err)
	if got := trailerOf(t, e, tr.TrailerID); done.ActualLocation == nil || *done.ActualLocation != spot("WH1-P2") || got.State != "PARKED" || *got.Location != spot("WH1-P2") {
		t.Fatalf("move %+v trailer %+v", done, got)
	}
}

func TestCompleteMoveOnAnOccupiedLocationIsRefusedAndStaysClaimed(t *testing.T) {
	e := yardEnv()
	t1 := parked(t, e, "PLATE1", "T1")
	parked(t, e, "PLATE2", "T2") // rests on P1
	mv := request(t, e, t1.TrailerID, door("WH1-D1"), 0, time.Time{})
	claim(t, e, "hostler-a")
	_, err := e.Yard.CompleteMove(e.Ctx, usecases.CompleteMoveInput{MoveID: mv.MoveID, HostlerRef: "hostler-a", Actual: spot("WH1-P1"), Note: "wrong spot"})
	is(t, err, trailer.ErrLocationOccupied)
	page, err := e.Yard.ListMoves(e.Ctx, views.MoveFilter{State: "CLAIMED"}, nil, 10)
	must(t, err)
	if len(page.Items) != 1 || page.Items[0].MoveID != mv.MoveID {
		t.Fatalf("the move stays CLAIMED: %+v", page.Items)
	}
	if got := trailerOf(t, e, t1.TrailerID); got.State != "IN_MOVE" {
		t.Fatalf("trailer %+v", got)
	}
}

func TestCompleteMoveRefusesAnotherReservedLocation(t *testing.T) {
	e := yardEnv()
	t1 := parked(t, e, "PLATE1", "T1")
	t2 := parked(t, e, "PLATE2", "T2")
	m1 := request(t, e, t1.TrailerID, door("WH1-D1"), 0, time.Time{})
	request(t, e, t2.TrailerID, door("WH1-D2"), 1, time.Time{})
	claim(t, e, "hostler-b") // t2's move comes first (band 1)
	claim(t, e, "hostler-a")
	_, err := e.Yard.CompleteMove(e.Ctx, usecases.CompleteMoveInput{MoveID: m1.MoveID, HostlerRef: "hostler-a", Actual: door("WH1-D2"), Note: "swap"})
	is(t, err, yardmove.ErrDestinationReserved)
}

func TestCancelARequestedMoveFreesTheDestination(t *testing.T) {
	e := yardEnv()
	t1 := parked(t, e, "PLATE1", "T1")
	t2 := parked(t, e, "PLATE2", "T2")
	mv := request(t, e, t1.TrailerID, door("WH1-D1"), 0, time.Time{})
	got, err := e.Yard.CancelMove(e.Ctx, mv.MoveID, "")
	must(t, err)
	if got.State != "CANCELLED" || !contains(e.Store.EventNames(), "YardMoveCancelled") {
		t.Fatalf("move %+v events %v", got, e.Store.EventNames())
	}
	request(t, e, t2.TrailerID, door("WH1-D1"), 0, time.Time{})
	_, err = e.Yard.CancelMove(e.Ctx, "mv-404", "")
	is(t, err, usecases.ErrMoveNotFound)
}

func TestCancelAClaimedHostlerMoveReturnsTheTrailerToItsOrigin(t *testing.T) {
	e := yardEnv()
	tr := parked(t, e, "PLATE1", "T1")
	mv := request(t, e, tr.TrailerID, door("WH1-D1"), 0, time.Time{})
	claim(t, e, "hostler-a")
	_, err := e.Yard.CancelMove(e.Ctx, mv.MoveID, "changed plan")
	must(t, err)
	if got := trailerOf(t, e, tr.TrailerID); got.State != "PARKED" || *got.Location != spot("WH1-H1") || got.OpenMoveID != nil {
		t.Fatalf("trailer %+v", got)
	}
}

func TestCancelledClaimLeavesTheTrailerInMoveWhenItsOriginWasTaken(t *testing.T) {
	e := yardEnv()
	tr := parked(t, e, "PLATE1", "T1") // on H1
	mv := request(t, e, tr.TrailerID, door("WH1-D1"), 0, time.Time{})
	claim(t, e, "hostler-a") // H1 is free again
	parked(t, e, "PLATE2", "T2")
	if _, err := e.Yard.CancelMove(e.Ctx, mv.MoveID, ""); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got := trailerOf(t, e, tr.TrailerID); got.State != "IN_MOVE" {
		t.Fatalf("the supervisor re-plans: %+v", got)
	}
}

func TestAVisitingDriverMoveCannotBeCancelled(t *testing.T) {
	e := yardEnv()
	ci := checkIn(t, e)
	_, err := e.Yard.CancelMove(e.Ctx, ci.Moves[0].MoveID, "")
	is(t, err, shared.ErrIllegalTransition)
}

func TestLeaseExpirySweepReturnsTheMoveAndTheTrailer(t *testing.T) {
	e := yardEnv()
	tr := parked(t, e, "PLATE1", "T1")
	mv := request(t, e, tr.TrailerID, door("WH1-D1"), 0, time.Time{})
	claim(t, e, "hostler-a")
	e.Store.Advance(100 * time.Second)
	if sweep, err := e.Yard.SweepMoveLeases(e.Ctx); err != nil || sweep.Count != 0 {
		t.Fatalf("a running lease is left alone: %+v %v", sweep, err)
	}
	e.Store.Advance(201 * time.Second)
	sweep, err := e.Yard.SweepMoveLeases(e.Ctx)
	must(t, err)
	if sweep.Count != 1 {
		t.Fatalf("sweep %+v", sweep)
	}
	page, err := e.Yard.ListMoves(e.Ctx, views.MoveFilter{State: "REQUESTED"}, nil, 10)
	must(t, err)
	if len(page.Items) != 1 || page.Items[0].MoveID != mv.MoveID || page.Items[0].HostlerRef != nil {
		t.Fatalf("moves %+v", page.Items)
	}
	if got := trailerOf(t, e, tr.TrailerID); got.State != "PARKED" || *got.Location != spot("WH1-H1") {
		t.Fatalf("trailer %+v", got)
	}
	if !contains(e.Store.EventNames(), "YardMoveLeaseExpired") {
		t.Fatalf("events %v", e.Store.EventNames())
	}
	again, err := e.Yard.SweepMoveLeases(e.Ctx)
	must(t, err)
	if again.Count != 0 {
		t.Fatalf("a sweep is idempotent: %+v", again)
	}
}

func TestSweepSkipsAMoveThatChangedMeanwhile(t *testing.T) {
	e := yardEnv()
	tr := parked(t, e, "PLATE1", "T1")
	mv := request(t, e, tr.TrailerID, door("WH1-D1"), 0, time.Time{})
	claim(t, e, "hostler-a")
	e.Store.Advance(time.Hour)
	_, err := e.Yard.CompleteMove(e.Ctx, usecases.CompleteMoveInput{MoveID: mv.MoveID, HostlerRef: "hostler-a"})
	must(t, err)
	sweep, err := e.Yard.SweepMoveLeases(e.Ctx)
	must(t, err)
	if sweep.Count != 0 {
		t.Fatalf("sweep %+v", sweep)
	}
}
