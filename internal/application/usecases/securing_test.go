package usecases_test

import (
	"testing"

	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
	"github.com/claudioed/yard-management/internal/testing/yardtest"
)

// interlockEnv is a site whose policy requires securing, with a trailer
// DOCKED at D1 and door work WAITING.
func interlockEnv(t *testing.T, on bool) (*yardtest.Env, views.CheckIn, views.Trailer) {
	t.Helper()
	e := yardEnv()
	declare(t, e, func(in *policyInput) { in.RequireSecuringBeforeDoorWork = on })
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	ci, tr := dockedFor(t, e, "appt-1")
	return e, ci, tr
}

func TestStartDoorWorkIsRefusedUnsecuredOnlyWhereThePolicyIsOn(t *testing.T) {
	e, _, tr := interlockEnv(t, true)
	_, err := e.Yard.StartDoorWork(e.Ctx, tr.TrailerID)
	is(t, err, trailer.ErrTrailerNotSecured)
	if contains(e.Store.EventNames(), "DoorWorkStarted") {
		t.Fatal("a refused start publishes nothing")
	}
	sec, err := e.Yard.SecureTrailer(e.Ctx, tr.TrailerID, "RESTRAINT", "op-1")
	must(t, err)
	if sec.SecuredBy == nil || *sec.SecuredBy != "RESTRAINT" || sec.SecuredAt == nil || sec.SecurerRef == nil || *sec.SecurerRef != "op-1" {
		t.Fatalf("view %+v", sec)
	}
	started, err := e.Yard.StartDoorWork(e.Ctx, tr.TrailerID)
	must(t, err)
	if *started.DoorWork != "IN_PROGRESS" {
		t.Fatalf("trailer %+v", started)
	}

	off, _, tr2 := interlockEnv(t, false)
	if _, err := off.Yard.StartDoorWork(off.Ctx, tr2.TrailerID); err != nil {
		t.Fatalf("policy off starts without securing: %v", err)
	}
}

func TestSecureTrailerIsIdempotentAndSavesNothingTheSecondTime(t *testing.T) {
	e, _, tr := interlockEnv(t, true)
	first, err := e.Yard.SecureTrailer(e.Ctx, tr.TrailerID, "WHEEL_CHOCKS", "")
	must(t, err)
	again, err := e.Yard.SecureTrailer(e.Ctx, tr.TrailerID, "WHEEL_CHOCKS", "other")
	must(t, err)
	if again.Version != first.Version || again.SecurerRef != nil {
		t.Fatalf("repeat changed the trailer: %+v vs %+v", again, first)
	}
	n := 0
	for _, name := range e.Store.EventNames() {
		if name == "TrailerSecured" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("TrailerSecured published %d times, want 1", n)
	}
	_, err = e.Yard.SecureTrailer(e.Ctx, tr.TrailerID, "RESTRAINT", "")
	is(t, err, shared.ErrIllegalTransition)
	_, err = e.Yard.SecureTrailer(e.Ctx, "trl-404", "RESTRAINT", "")
	is(t, err, usecases.ErrTrailerNotFound)
}

func TestSecureTrailerNeedsADockedTrailerWithNoOpenMove(t *testing.T) {
	e := yardEnv()
	_, parkedTr := arrived(t, e)
	_, err := e.Yard.SecureTrailer(e.Ctx, parkedTr.TrailerID, "RESTRAINT", "")
	is(t, err, shared.ErrIllegalTransition)

	e2, _, tr := interlockEnv(t, true)
	_, err = e2.Yard.RequestMove(e2.Ctx, usecases.RequestMoveInput{TrailerID: tr.TrailerID, To: spot("WH1-P1")})
	must(t, err)
	_, err = e2.Yard.SecureTrailer(e2.Ctx, tr.TrailerID, "RESTRAINT", "")
	is(t, err, yardmove.ErrTrailerHasOpenMove)
	_, err = e2.Yard.SecureTrailer(e2.Ctx, tr.TrailerID, "LOCKS", "")
	is(t, err, yardmove.ErrTrailerHasOpenMove)
}

func TestASecuredTrailerIsNotMovedOffItsDoorOrChecksOut(t *testing.T) {
	e, ci, tr := interlockEnv(t, true)
	_, err := e.Yard.SecureTrailer(e.Ctx, tr.TrailerID, "RESTRAINT", "")
	must(t, err)
	_, err = e.Yard.StartDoorWork(e.Ctx, tr.TrailerID)
	must(t, err)
	_, err = e.Yard.CompleteDoorWork(e.Ctx, tr.TrailerID, "")
	must(t, err)

	_, err = e.Yard.RequestMove(e.Ctx, usecases.RequestMoveInput{TrailerID: tr.TrailerID, To: spot("WH1-P1")})
	is(t, err, trailer.ErrTrailerStillSecured)
	_, err = e.Yard.CheckOutVisit(e.Ctx, ci.Visit.VisitID, []string{tr.TrailerID})
	is(t, err, trailer.ErrTrailerStillSecured)
	if got, _ := e.Yard.GetTrailer(e.Ctx, tr.TrailerID); got.State != "DOCKED" {
		t.Fatalf("a refused check-out leaves the trailer docked: %+v", got)
	}

	rel, err := e.Yard.ReleaseTrailerSecuring(e.Ctx, tr.TrailerID)
	must(t, err)
	if rel.SecuredBy != nil || rel.SecuredAt != nil || rel.SecurerRef != nil {
		t.Fatalf("released view %+v", rel)
	}
	_, err = e.Yard.CheckOutVisit(e.Ctx, ci.Visit.VisitID, []string{tr.TrailerID})
	must(t, err)
	_, err = e.Yard.ReleaseTrailerSecuring(e.Ctx, tr.TrailerID)
	is(t, err, trailer.ErrTrailerNotSecured)
}

func TestReleasingTheSecuringOfAClearedDropTrailerRequestsItsMove(t *testing.T) {
	e := yardEnv()
	_, tr := arrived(t, e, func(in *usecases.CheckInInput) { in.Purpose = "DROP"; in.TractorPlate = "P-DROP" })
	// A walk-in drop trailer rests in a holding spot: the supervisor docks it.
	mv, err := e.Yard.RequestMove(e.Ctx, usecases.RequestMoveInput{TrailerID: tr.TrailerID, To: door("WH1-D1")})
	must(t, err)
	_, err = e.Yard.ClaimNextMove(e.Ctx, "WH1", "h1")
	must(t, err)
	_, err = e.Yard.CompleteMove(e.Ctx, usecases.CompleteMoveInput{MoveID: mv.MoveID, HostlerRef: "h1"})
	must(t, err)
	_, err = e.Yard.SecureTrailer(e.Ctx, tr.TrailerID, "RESTRAINT", "")
	must(t, err)
	done := startAndFinish(t, e, tr.TrailerID)
	if done.OpenMoveID != nil {
		t.Fatalf("a secured trailer is not cleared: open move %v", *done.OpenMoveID)
	}
	rel, err := e.Yard.ReleaseTrailerSecuring(e.Ctx, tr.TrailerID)
	must(t, err)
	if rel.OpenMoveID == nil {
		t.Fatal("releasing the securing runs ClearDoorAfterWork")
	}
}

func TestAutoCompleteUnloadIsNotBlockedByTheInterlock(t *testing.T) {
	e, _, tr := interlockEnv(t, true)
	must(t, e.Yard.OpenReceipt(e.Ctx, "evt-r1", views.Receipt{ID: "rcpt-1", ASNNumber: "ASN-1", AppointmentID: "appt-1", DoorCode: "WH1-D1"}))
	must(t, e.Yard.RegisterAsn(e.Ctx, "evt-a1", "ASN-1"))
	must(t, e.Yard.CloseReceipt(e.Ctx, "evt-r2", "rcpt-1", "ASN-1", yardtest.T0))
	got, err := e.Yard.GetTrailer(e.Ctx, tr.TrailerID)
	must(t, err)
	if got.DoorWork == nil || *got.DoorWork != "DONE" || got.SecuredBy != nil {
		t.Fatalf("the unload completed without securing: %+v", got)
	}
}
