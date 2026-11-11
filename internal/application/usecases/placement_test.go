package usecases_test

import (
	"fmt"
	"testing"

	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/testing/yardtest"
)

func hazmat(class string) func(*usecases.CheckInInput) {
	return func(in *usecases.CheckInInput) { in.Trailers[0].HazmatClass = class }
}

func reefer(in *usecases.CheckInInput) { in.Trailers[0].RequiresTemperatureControl = true }

// bigYard has forty ordinary spots and one HAZMAT spot after them by code, so a
// search that only looked at the first free spots would never find it.
func bigYard() *yardtest.Env {
	e := yardtest.New()
	codes := make([]string, 0, 41)
	for i := 1; i <= 40; i++ {
		codes = append(codes, fmt.Sprintf("WH1-P%02d", i))
	}
	codes = append(codes, "WH1-Z99")
	e.Site("WH1", codes, map[string]string{"WH1-D1": "Both"})
	e.SpotKind("WH1", "WH1-Z99", shared.SpotKindHazmat)
	return e
}

func TestApplySlotStoresTheSpotKindTolerantly(t *testing.T) {
	e := yardEnv()
	ctx := e.Ctx
	for i, c := range []struct{ in, want string }{
		{"REEFER_PLUG", "REEFER_PLUG"}, {"HAZMAT", "HAZMAT"}, {"", ""}, {"COLD_STORE", ""}, {"hazmat", ""},
	} {
		must(t, e.Yard.ApplySlot(ctx, fmt.Sprintf("ev-%d", i), "WH1-X1", "WH1-yard", "Yard", "", c.in))
		got, ok, err := e.Store.Locations().Get(ctx, "WH1-X1")
		if err != nil || !ok || string(got.SpotKind) != c.want || got.Kind != shared.Spot {
			t.Errorf("%q: %+v ok=%v err=%v, want kind %q", c.in, got, ok, err, c.want)
		}
	}
	must(t, e.Yard.ApplySlot(ctx, "ev-door", "WH1-DX", "WH1-dock", "Dock", "Both", "HAZMAT"))
	if got, _, _ := e.Store.Locations().Get(ctx, "WH1-DX"); got.SpotKind != shared.SpotKindUnknown {
		t.Errorf("a door has no spot kind: %+v", got)
	}
}

func TestAHazmatTrailerIsOfferedOnlyTheSpotsItsRuleAllows(t *testing.T) {
	e := bigYard()
	ci := checkIn(t, e, hazmat("3"))
	if got := ci.Moves[0].To; got != spot("WH1-Z99") {
		t.Fatalf("the hazmat trailer is sent to %v, want the HAZMAT spot", got)
	}
	if got := ci.Trailers[0]; got.HazmatClass == nil || *got.HazmatClass != "3" || got.UNNumber != nil || got.RequiresTemperatureControl {
		t.Fatalf("view %+v", got)
	}
	plain := checkIn(t, e, func(in *usecases.CheckInInput) { in.TractorPlate, in.Trailers[0].UnitNumber = "OTHER", "T2" })
	if got := plain.Moves[0].To; got != spot("WH1-P01") {
		t.Fatalf("a plain trailer keeps the first free spot: %v", got)
	}
	// The HAZMAT spot is reserved by the first trailer: the next hazmat trailer finds none free.
	_, err := e.Yard.CheckInVisit(e.Ctx, checkInput(hazmat("8"), func(in *usecases.CheckInInput) { in.TractorPlate, in.Trailers[0].UnitNumber = "THIRD", "T3" }))
	is(t, err, usecases.ErrNoFreeSpot)
}

func TestAHazmatTrailerAtASiteWithNoHazmatSpotIsRefusedUnlessThePolicyAllowsIt(t *testing.T) {
	e := yardEnv()
	_, err := e.Yard.CheckInVisit(e.Ctx, checkInput(hazmat("3")))
	is(t, err, trailer.ErrPlacementRuleViolated)
	if len(e.Store.EventNames()) != 0 {
		t.Fatalf("a refused check-in publishes nothing: %v", e.Store.EventNames())
	}
	declare(t, e, func(in *policyInput) { in.AllowHazmatOnUnclassifiedSpots = true })
	ci := checkIn(t, e, hazmat("3"))
	if ci.Moves[0].To.Kind != shared.Spot {
		t.Fatalf("move %+v", ci.Moves[0])
	}
	// The policy admits spots with no kind only: with a PARKING spot first by code, the trailer skips it.
	e2 := yardEnv()
	e2.SpotKind("WH1", "WH1-H1", shared.SpotKindParking)
	declare2 := e2.Yard
	_, err = declare2.DeclareYardPolicy(e2.Ctx, usecases.DeclarePolicyInput{SiteID: "WH1", Policy: policyInput{LeaseSeconds: 300, SealMismatchAction: "FLAG", AllowHazmatOnUnclassifiedSpots: true}})
	must(t, err)
	ci = checkIn(t, e2, hazmat("3"))
	if got := ci.Moves[0].To; got != spot("WH1-P1") {
		t.Fatalf("sent to %v, want the first spot with no kind", got)
	}
}

func TestAReeferIsSentToAPlugWhereTheSiteHasOne(t *testing.T) {
	e := yardEnv()
	if got := checkIn(t, e, reefer).Moves[0].To; got != spot("WH1-H1") {
		t.Fatalf("no plug at the site: the reefer takes the first spot, got %v", got)
	}
	e2 := yardEnv()
	e2.SpotKind("WH1", "WH1-P2", shared.SpotKindReeferPlug)
	if got := checkIn(t, e2, reefer).Moves[0].To; got != spot("WH1-P2") {
		t.Fatalf("the reefer is sent to the plug, got %v", got)
	}
}

func TestRequestMoveJudgesTheSpotKind(t *testing.T) {
	e := yardEnv()
	e.SpotKind("WH1", "WH1-P2", shared.SpotKindHazmat)
	_, parkedTr := arrived(t, e)
	_, err := e.Yard.ClassifyTrailer(e.Ctx, parkedTr.TrailerID, "6.1", "UN2810", false)
	must(t, err)
	_, err = e.Yard.RequestMove(e.Ctx, usecases.RequestMoveInput{TrailerID: parkedTr.TrailerID, To: spot("WH1-P1")})
	is(t, err, trailer.ErrPlacementRuleViolated)
	mv, err := e.Yard.RequestMove(e.Ctx, usecases.RequestMoveInput{TrailerID: parkedTr.TrailerID, To: spot("WH1-P2")})
	must(t, err)
	if mv.To != spot("WH1-P2") {
		t.Fatalf("move %+v", mv)
	}
}

func TestClassifyTrailer(t *testing.T) {
	e := yardEnv()
	_, tr := arrived(t, e)
	got, err := e.Yard.ClassifyTrailer(e.Ctx, tr.TrailerID, "3", "UN1203", true)
	must(t, err)
	if *got.HazmatClass != "3" || *got.UNNumber != "UN1203" || !got.RequiresTemperatureControl || got.Version != tr.Version+1 {
		t.Fatalf("view %+v", got)
	}
	again, err := e.Yard.ClassifyTrailer(e.Ctx, tr.TrailerID, "3", "UN1203", true)
	must(t, err)
	if again.Version != got.Version {
		t.Fatalf("the same classification changes nothing: version %d -> %d", got.Version, again.Version)
	}
	cleared, err := e.Yard.ClassifyTrailer(e.Ctx, tr.TrailerID, "", "", false)
	must(t, err)
	if cleared.HazmatClass != nil || cleared.UNNumber != nil || cleared.RequiresTemperatureControl {
		t.Fatalf("cleared %+v", cleared)
	}
	_, err = e.Yard.ClassifyTrailer(e.Ctx, tr.TrailerID, "11", "", false)
	is(t, err, shared.ErrInvalidHazmatClass)
	_, err = e.Yard.ClassifyTrailer(e.Ctx, tr.TrailerID, "3", "UN1", false)
	is(t, err, shared.ErrInvalidUNNumber)
	_, err = e.Yard.ClassifyTrailer(e.Ctx, "trl-404", "3", "", false)
	is(t, err, usecases.ErrTrailerNotFound)
}

func TestArrivalJudgesTheActualLocation(t *testing.T) {
	e := yardEnv()
	e.SpotKind("WH1", "WH1-P2", shared.SpotKindHazmat)
	ci := checkIn(t, e, hazmat("3"))
	if ci.Moves[0].To != spot("WH1-P2") {
		t.Fatalf("move %+v", ci.Moves[0])
	}
	_, err := e.Yard.CompleteMove(e.Ctx, usecases.CompleteMoveInput{MoveID: ci.Moves[0].MoveID, Actual: spot("WH1-P1"), Note: "blocked"})
	is(t, err, trailer.ErrPlacementRuleViolated)
}

func TestAWalkInHazmatTrailerSkipsTheHoldingSpotsForAHazmatSpot(t *testing.T) {
	e := yardEnv() // H1 is the holding spot of the policy below
	e.SpotKind("WH1", "WH1-P2", shared.SpotKindHazmat)
	declare(t, e, func(in *policyInput) { in.HoldingSpotCodes = []string{"WH1-H1"} })
	plain := checkIn(t, e, func(in *usecases.CheckInInput) { in.TractorPlate, in.Trailers[0].UnitNumber = "PLAIN", "T0" })
	if mv := plain.Moves[0]; mv.Reason != "TO_HOLDING" || mv.To != spot("WH1-H1") {
		t.Fatalf("a walk-in with nothing declared still goes to holding: %+v", mv)
	}
	haz := checkIn(t, e, hazmat("3"))
	if mv := haz.Moves[0]; mv.To != spot("WH1-P2") {
		t.Fatalf("a hazmat walk-in is sent to the HAZMAT spot: %+v", mv)
	}
}

func TestClearDoorAfterWorkStandsDownWhenTheHazardClassHasNoSpotAtTheSite(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	_, docked := dockedFor(t, e, "appt-1", func(in *usecases.CheckInInput) { in.Purpose = "DROP"; in.Trailers[0].HazmatClass = "3" })
	startAndFinish(t, e, docked.TrailerID)
	page, err := e.Yard.ListMoves(e.Ctx, views.MoveFilter{State: "REQUESTED"}, nil, 10)
	must(t, err)
	if len(page.Items) != 0 {
		t.Fatalf("no spot may take the trailer: completing its door work must not fail, the supervisor decides: %+v", page.Items)
	}
}
