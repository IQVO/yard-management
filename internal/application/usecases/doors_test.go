package usecases_test

import (
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
	"github.com/claudioed/yard-management/internal/testing/yardtest"
)

// dockedFor checks a visit in for appointment id at door D1 (on time) and
// completes the gate move: the trailer is DOCKED with door work WAITING.
func dockedFor(t *testing.T, e *yardtest.Env, id string, mod ...func(*usecases.CheckInInput)) (views.CheckIn, views.Trailer) {
	t.Helper()
	return arrived(t, e, append([]func(*usecases.CheckInInput){withAppointment(id)}, mod...)...)
}

func startAndFinish(t *testing.T, e *yardtest.Env, trailerID string) views.Trailer {
	t.Helper()
	_, err := e.Yard.StartDoorWork(e.Ctx, trailerID)
	must(t, err)
	done, err := e.Yard.CompleteDoorWork(e.Ctx, trailerID, "")
	must(t, err)
	return done
}

func TestDoorWorkRunsWaitingInProgressDone(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	_, docked := dockedFor(t, e, "appt-1")
	if docked.State != "DOCKED" || *docked.DoorWork != "WAITING" {
		t.Fatalf("trailer %+v", docked)
	}
	_, err := e.Yard.CompleteDoorWork(e.Ctx, docked.TrailerID, shared.LoadEmpty)
	is(t, err, shared.ErrIllegalTransition)
	started, err := e.Yard.StartDoorWork(e.Ctx, docked.TrailerID)
	must(t, err)
	if *started.DoorWork != "IN_PROGRESS" {
		t.Fatalf("trailer %+v", started)
	}
	done, err := e.Yard.CompleteDoorWork(e.Ctx, docked.TrailerID, "")
	must(t, err)
	if *done.DoorWork != "DONE" || done.LoadState != shared.LoadEmpty {
		t.Fatalf("an unload leaves the trailer EMPTY: %+v", done)
	}
	names := e.Store.EventNames()
	if !contains(names, "DoorWorkStarted") || !contains(names, "DoorWorkCompleted") {
		t.Fatalf("events %v", names)
	}
	_, err = e.Yard.StartDoorWork(e.Ctx, "trl-404")
	is(t, err, usecases.ErrTrailerNotFound)
}

func TestClearDoorAfterWorkSendsADropTrailerToASpot(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	_, docked := dockedFor(t, e, "appt-1", func(in *usecases.CheckInInput) { in.Purpose = "DROP" })
	startAndFinish(t, e, docked.TrailerID)
	page, err := e.Yard.ListMoves(e.Ctx, views.MoveFilter{State: "REQUESTED"}, nil, 10)
	must(t, err)
	if len(page.Items) != 1 {
		t.Fatalf("moves %+v", page.Items)
	}
	mv := page.Items[0]
	if mv.Performer != "HOSTLER" || mv.Reason != "FROM_DOOR" || mv.To != spot("WH1-H1") || mv.From == nil || *mv.From != door("WH1-D1") {
		t.Fatalf("move %+v", mv)
	}
}

func TestClearDoorAfterWorkStandsDownWithoutAFreeSpot(t *testing.T) {
	e := yardtest.New()
	e.Site("WH1", []string{"WH1-H1"}, map[string]string{"WH1-D1": "Both"})
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	_, docked := dockedFor(t, e, "appt-1", func(in *usecases.CheckInInput) { in.Purpose = "DROP" })
	// park a second trailer on the only spot, then finish the door work
	other := checkIn(t, e, secondUnit("PLATE2", "T2"))
	complete(t, e, other.Moves[0].MoveID)
	startAndFinish(t, e, docked.TrailerID)
	page, err := e.Yard.ListMoves(e.Ctx, views.MoveFilter{State: "REQUESTED"}, nil, 10)
	must(t, err)
	if len(page.Items) != 0 {
		t.Fatalf("the policy stands down, the supervisor decides: %+v", page.Items)
	}
}

func TestALiveTrailerStaysAtTheDoorAfterDoorWork(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	_, docked := dockedFor(t, e, "appt-1")
	done := startAndFinish(t, e, docked.TrailerID)
	if done.OpenMoveID != nil {
		t.Fatalf("a live trailer stays until its visit checks out: %+v", done)
	}
}

func TestHoldsBlockDoorWorkUntilReleased(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	_, docked := dockedFor(t, e, "appt-1")
	held, err := e.Yard.PlaceHold(e.Ctx, docked.TrailerID, trailer.HoldDamaged)
	must(t, err)
	if held.Hold == nil || *held.Hold != "DAMAGED" {
		t.Fatalf("trailer %+v", held)
	}
	_, err = e.Yard.StartDoorWork(e.Ctx, docked.TrailerID)
	is(t, err, trailer.ErrTrailerOnHold)
	_, err = e.Yard.PlaceHold(e.Ctx, docked.TrailerID, trailer.HoldQuality)
	is(t, err, trailer.ErrTrailerOnHold)
	released, err := e.Yard.ReleaseHold(e.Ctx, docked.TrailerID)
	must(t, err)
	if released.Hold != nil {
		t.Fatalf("trailer %+v", released)
	}
	_, err = e.Yard.ReleaseHold(e.Ctx, docked.TrailerID)
	is(t, err, trailer.ErrTrailerNotOnHold)
	_, err = e.Yard.PlaceHold(e.Ctx, docked.TrailerID, "BORED")
	is(t, err, shared.ErrValidation)
	names := e.Store.EventNames()
	if !contains(names, "TrailerHeld") || !contains(names, "TrailerHoldReleased") {
		t.Fatalf("events %v", names)
	}
}

func TestSealsAreRecordedAndABrokenSealPlacesAHold(t *testing.T) {
	e := yardEnv()
	tr := parked(t, e, "PLATE1", "T1")
	sealed, err := e.Yard.RecordSeal(e.Ctx, tr.TrailerID, "INTACT", "SL-7", "")
	must(t, err)
	if sealed.Seal.State != "INTACT" || sealed.Seal.Number == nil || *sealed.Seal.Number != "SL-7" {
		t.Fatalf("trailer %+v", sealed)
	}
	broken, err := e.Yard.RecordSeal(e.Ctx, tr.TrailerID, "BROKEN", "", "")
	must(t, err)
	if broken.Seal.State != "BROKEN" || broken.Hold == nil || *broken.Hold != "SEAL_BROKEN" {
		t.Fatalf("a broken seal places a SEAL_BROKEN hold: %+v", broken)
	}
	_, err = e.Yard.RecordSeal(e.Ctx, tr.TrailerID, "WET", "", "")
	is(t, err, shared.ErrValidation)
	_, err = e.Yard.RecordSeal(e.Ctx, tr.TrailerID, "INTACT", " ", "")
	is(t, err, shared.ErrValidation)
}

func TestUndockingCallsTheNextAppointmentToTheDoor(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	e.Book(appointment("appt-2", "WH1-D1", yardtest.T0.Add(30*time.Minute), func(a *views.Appointment) { a.ASNNumbers = []string{"ASN-2"} }))
	first, docked := dockedFor(t, e, "appt-1")
	_, waiting := arrived(t, e, withAppointment("appt-2"), secondUnit("PLATE2", "T2")) // the door is taken: parks on a spot
	if waiting.State != "PARKED" {
		t.Fatalf("trailer %+v", waiting)
	}
	startAndFinish(t, e, docked.TrailerID)
	out, err := e.Yard.CheckOutVisit(e.Ctx, first.Visit.VisitID, []string{docked.TrailerID})
	must(t, err)
	if out.State != "CHECKED_OUT" {
		t.Fatalf("visit %+v", out)
	}
	names := e.Store.EventNames()
	if !contains(names, "TrailerUndocked") || !contains(names, "TrailerDeparted") || !contains(names, "VisitCheckedOut") {
		t.Fatalf("events %v", names)
	}
	page, err := e.Yard.ListMoves(e.Ctx, views.MoveFilter{State: "REQUESTED", TrailerID: waiting.TrailerID}, nil, 10)
	must(t, err)
	if len(page.Items) != 1 {
		t.Fatalf("CallToDoor requests one move: %+v", page.Items)
	}
	mv := page.Items[0]
	if mv.Performer != "HOSTLER" || mv.Reason != "TO_DOOR" || mv.To != door("WH1-D1") || mv.Priority.DueBy == nil || !mv.Priority.DueBy.Equal(yardtest.T0.Add(30*time.Minute)) || mv.Priority.Band != yardMoveBand(30) {
		t.Fatalf("move %+v", mv)
	}
	a, _, _ := e.Store.Appointments().Get(e.Ctx, "appt-1")
	if a.Status != views.ApptUsed {
		t.Fatalf("the departed appointment is USED: %s", a.Status)
	}
}

func TestCallToDoorSkipsATrailerThatAlreadyHasAMove(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	e.Book(appointment("appt-2", "WH1-D1", yardtest.T0, func(a *views.Appointment) { a.ASNNumbers = []string{"ASN-2"} }))
	first, docked := dockedFor(t, e, "appt-1")
	_, waiting := arrived(t, e, withAppointment("appt-2"), secondUnit("PLATE2", "T2"))
	request(t, e, waiting.TrailerID, spot("WH1-P2"), 0, time.Time{})
	startAndFinish(t, e, docked.TrailerID)
	_, err := e.Yard.CheckOutVisit(e.Ctx, first.Visit.VisitID, []string{docked.TrailerID})
	must(t, err)
	page, err := e.Yard.ListMoves(e.Ctx, views.MoveFilter{State: "REQUESTED", TrailerID: waiting.TrailerID}, nil, 10)
	must(t, err)
	if len(page.Items) != 1 || page.Items[0].To != spot("WH1-P2") {
		t.Fatalf("moves %+v", page.Items)
	}
}

func TestCheckOutRefusesAnUnreleasedTrailer(t *testing.T) {
	cases := []struct {
		name string
		prep func(*testing.T, *yardtest.Env) (views.CheckIn, string)
	}{
		{"still in its first move", func(t *testing.T, e *yardtest.Env) (views.CheckIn, string) {
			ci := checkIn(t, e)
			return ci, ci.Trailers[0].TrailerID
		}},
		{"docked with the door work open", func(t *testing.T, e *yardtest.Env) (views.CheckIn, string) {
			e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
			ci, docked := dockedFor(t, e, "appt-1")
			return ci, docked.TrailerID
		}},
		{"with an open move", func(t *testing.T, e *yardtest.Env) (views.CheckIn, string) {
			ci, tr := arrived(t, e)
			request(t, e, tr.TrailerID, spot("WH1-P1"), 0, time.Time{})
			return ci, tr.TrailerID
		}},
		{"unknown to the yard", func(t *testing.T, e *yardtest.Env) (views.CheckIn, string) {
			return checkIn(t, e), "trl-404"
		}},
	}
	for _, c := range cases {
		e := yardEnv()
		ci, id := c.prep(t, e)
		_, err := e.Yard.CheckOutVisit(e.Ctx, ci.Visit.VisitID, []string{id})
		if !isErr(err, yardvisit.ErrTrailerNotReleased) {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
}

func TestCheckOutOfALoadedOutboundTrailerNeedsASeal(t *testing.T) {
	e := yardEnv()
	ci, tr := arrived(t, e, func(in *usecases.CheckInInput) { in.Purpose = "LIVE_LOAD" })
	_, err := e.Yard.CheckOutVisit(e.Ctx, ci.Visit.VisitID, []string{tr.TrailerID})
	is(t, err, yardvisit.ErrSealRequired)
	_, err = e.Yard.RecordSeal(e.Ctx, tr.TrailerID, "INTACT", "SL-3", "")
	must(t, err)
	out, err := e.Yard.CheckOutVisit(e.Ctx, ci.Visit.VisitID, []string{tr.TrailerID})
	must(t, err)
	if out.State != "CHECKED_OUT" || len(out.TrailersOut) != 1 || out.DepartedAt == nil {
		t.Fatalf("visit %+v", out)
	}
	if got := trailerOf(t, e, tr.TrailerID); got.State != "DEPARTED" || got.DepartedAt == nil {
		t.Fatalf("trailer %+v", got)
	}
}

func TestADropVisitLeavesItsTrailerBehind(t *testing.T) {
	e := yardEnv()
	ci, tr := arrived(t, e, func(in *usecases.CheckInInput) { in.Purpose = "DROP" })
	out, err := e.Yard.CheckOutVisit(e.Ctx, ci.Visit.VisitID, nil)
	must(t, err)
	if out.State != "CHECKED_OUT" || len(out.TrailersOut) != 0 {
		t.Fatalf("visit %+v", out)
	}
	if got := trailerOf(t, e, tr.TrailerID); got.State != "PARKED" || got.VisitID == nil || *got.VisitID != ci.Visit.VisitID {
		t.Fatalf("a drop trailer stays PARKED and keeps its visit (G-D): %+v", got)
	}
	_, err = e.Yard.CheckOutVisit(e.Ctx, ci.Visit.VisitID, nil)
	is(t, err, shared.ErrIllegalTransition)
	_, err = e.Yard.CheckOutVisit(e.Ctx, "visit-404", nil)
	is(t, err, usecases.ErrVisitNotFound)
}

func TestACheckOutReleasesTheDoorOfALiveVisit(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	ci, docked := dockedFor(t, e, "appt-1")
	startAndFinish(t, e, docked.TrailerID)
	_, err := e.Yard.CheckOutVisit(e.Ctx, ci.Visit.VisitID, []string{docked.TrailerID, docked.TrailerID})
	is(t, err, shared.ErrValidation)
	_, err = e.Yard.CheckOutVisit(e.Ctx, ci.Visit.VisitID, []string{docked.TrailerID})
	must(t, err)
	board, err := e.Yard.GetYardBoard(e.Ctx, "WH1")
	must(t, err)
	for _, d := range board.Doors {
		if d.Trailer != nil {
			t.Fatalf("door %s still holds a trailer", d.Code)
		}
	}
}
