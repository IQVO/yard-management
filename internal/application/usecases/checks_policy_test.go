package usecases_test

import (
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/yardcheck"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
	"github.com/claudioed/yard-management/internal/testing/yardtest"
)

func TestYardCheckReportsWhatTheAuditFoundDifferent(t *testing.T) {
	e := yardEnv()
	parked(t, e, "PLATE1", "T1") // H1
	parked(t, e, "PLATE2", "T2") // P1
	parked(t, e, "PLATE3", "T3") // P2
	check, err := e.Yard.OpenYardCheck(e.Ctx, "WH1")
	must(t, err)
	if check.State != "OPEN" || check.Version != 1 {
		t.Fatalf("check %+v", check)
	}
	_, err = e.Yard.OpenYardCheck(e.Ctx, "WH1")
	is(t, err, yardcheck.ErrYardCheckOpen)
	for _, o := range [][2]string{{"WH1-H1", "T1"}, {"WH1-P1", "ROGUE"}, {"WH1-D1", "T3"}} {
		_, err = e.Yard.RecordObservation(e.Ctx, check.YardCheckID, o[0], o[1])
		must(t, err)
	}
	_, err = e.Yard.RecordObservation(e.Ctx, check.YardCheckID, "WH1-H1", "T1")
	is(t, err, yardcheck.ErrLocationAlreadyObserved)
	closed, err := e.Yard.CloseYardCheck(e.Ctx, check.YardCheckID)
	must(t, err)
	got := map[string]string{}
	for _, d := range closed.Discrepancies {
		got[d.Kind+" "+d.LocationCode] = ""
		if d.UnitNumber != nil {
			got[d.Kind+" "+d.LocationCode] = *d.UnitNumber
		}
	}
	want := map[string]string{"UNEXPECTED WH1-P1": "ROGUE", "WRONG_LOCATION WH1-D1": "T3", "MISSING WH1-P1": "T2"}
	if closed.State != "CLOSED" || len(got) != len(want) {
		t.Fatalf("check %+v", closed)
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Fatalf("discrepancies %v, want %v", got, want)
		}
	}
	if !contains(e.Store.EventNames(), "YardCheckClosed") {
		t.Fatalf("events %v", e.Store.EventNames())
	}
	_, err = e.Yard.CloseYardCheck(e.Ctx, check.YardCheckID)
	is(t, err, shared.ErrIllegalTransition)
	_, err = e.Yard.OpenYardCheck(e.Ctx, "WH1") // closed: a new audit may open
	must(t, err)
	_, err = e.Yard.RecordObservation(e.Ctx, "yck-404", "WH1-H1", "")
	is(t, err, usecases.ErrYardCheckNotFound)
	_, err = e.Yard.CloseYardCheck(e.Ctx, "yck-404")
	is(t, err, usecases.ErrYardCheckNotFound)
}

func TestYardPolicyDefaults(t *testing.T) {
	e := yardEnv()
	def, err := e.Yard.GetYardPolicy(e.Ctx, "WH1")
	must(t, err)
	if def.Declared || def.RequireAppointment || def.LeaseSeconds != 300 || def.SealMismatchAction != "FLAG" || def.FreeTimeMinutes != nil || def.Version != 1 {
		t.Fatalf("default policy %+v", def)
	}
}

func TestYardPolicyDeclaration(t *testing.T) {
	e := yardEnv()
	p := declare(t, e, func(in *policyInput) {
		in.RequireAppointment, in.HoldingSpotCodes, in.FreeTimeMinutes = true, []string{"WH1-H1"}, 120
		in.DwellThresholdMinutes = map[shared.TrailerKind]int64{shared.KindDryVan: 90}
	})
	if !p.Declared || !p.RequireAppointment || p.Version != 2 || p.FreeTimeMinutes == nil || *p.FreeTimeMinutes != 120 || p.DwellThresholdMinutes["DRY_VAN"] != 90 {
		t.Fatalf("declared policy %+v", p)
	}
	again, err := e.Yard.DeclareYardPolicy(e.Ctx, usecases.DeclarePolicyInput{SiteID: "WH1", IfMatch: 2, Policy: yardpolicy.DeclareInput{LeaseSeconds: 60, SealMismatchAction: yardpolicy.Reject}})
	must(t, err)
	if again.Version != 3 || again.SealMismatchAction != "REJECT" || again.LeaseSeconds != 60 {
		t.Fatalf("policy %+v", again)
	}
	_, err = e.Yard.DeclareYardPolicy(e.Ctx, usecases.DeclarePolicyInput{SiteID: "WH1", IfMatch: 2, Policy: yardpolicy.DeclareInput{LeaseSeconds: 60, SealMismatchAction: yardpolicy.Flag}})
	is(t, err, usecases.ErrStalePolicy)
}

func TestYardPolicyRefusals(t *testing.T) {
	e := yardEnv()
	cases := []struct {
		name string
		mod  func(*policyInput)
		want error
	}{
		{"a holding spot that is a door", func(in *policyInput) { in.HoldingSpotCodes = []string{"WH1-D1"} }, shared.ErrUnknownLocation},
		{"an unknown holding spot", func(in *policyInput) { in.HoldingSpotCodes = []string{"NOPE"} }, shared.ErrUnknownLocation},
		{"a non-positive lease", func(in *policyInput) { in.LeaseSeconds = 0 }, shared.ErrValidation},
		{"a bad seal action", func(in *policyInput) { in.SealMismatchAction = "SHRUG" }, shared.ErrValidation},
		{"a non-positive threshold", func(in *policyInput) { in.DwellThresholdMinutes = map[shared.TrailerKind]int64{shared.KindReefer: 0} }, shared.ErrValidation},
	}
	for _, c := range cases {
		in := policyInput{LeaseSeconds: 300, SealMismatchAction: yardpolicy.Flag}
		c.mod(&in)
		_, err := e.Yard.DeclareYardPolicy(e.Ctx, usecases.DeclarePolicyInput{SiteID: "WH1", Policy: in})
		if !isErr(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
}

func TestDwellSweepAlertsOncePerTrailer(t *testing.T) {
	e := yardEnv()
	declare(t, e, func(in *policyInput) {
		in.DwellThresholdMinutes = map[shared.TrailerKind]int64{shared.KindDryVan: 60}
	})
	parked(t, e, "PLATE1", "T1")
	if sweep, err := e.Yard.SweepDwell(e.Ctx, "WH1"); err != nil || sweep.Count != 0 {
		t.Fatalf("under the threshold: %+v %v", sweep, err)
	}
	e.Store.Advance(61 * time.Minute)
	sweep, err := e.Yard.SweepDwell(e.Ctx, "WH1")
	must(t, err)
	if sweep.Count != 1 || !contains(e.Store.EventNames(), "TrailerDwellExceeded") {
		t.Fatalf("sweep %+v events %v", sweep, e.Store.EventNames())
	}
	again, err := e.Yard.SweepDwell(e.Ctx, "")
	must(t, err)
	if again.Count != 0 {
		t.Fatalf("once per trailer: %+v", again)
	}
}

func TestDwellSweepIgnoresKindsWithoutAThreshold(t *testing.T) {
	e := yardEnv()
	declare(t, e, func(in *policyInput) {
		in.DwellThresholdMinutes = map[shared.TrailerKind]int64{shared.KindReefer: 30}
	})
	parked(t, e, "PLATE1", "T1") // a DRY_VAN
	e.Store.Advance(48 * time.Hour)
	sweep, err := e.Yard.SweepDwell(e.Ctx, "WH1")
	must(t, err)
	if sweep.Count != 0 {
		t.Fatalf("sweep %+v", sweep)
	}
}

func TestListsPageByKeyset(t *testing.T) {
	e := yardEnv()
	for i, plate := range []string{"PLATE1", "PLATE2", "PLATE3"} {
		parked(t, e, plate, "T"+string(rune('1'+i)))
		e.Store.Advance(time.Minute)
	}
	first, err := e.Yard.ListVisits(e.Ctx, views.VisitFilter{SiteID: "WH1"}, nil, 2)
	must(t, err)
	if len(first.Items) != 2 || first.Next == nil || first.Items[0].TractorPlate != "PLATE3" {
		t.Fatalf("first page %+v", first)
	}
	rest, err := e.Yard.ListVisits(e.Ctx, views.VisitFilter{SiteID: "WH1"}, first.Next, 2)
	must(t, err)
	if len(rest.Items) != 1 || rest.Next != nil || rest.Items[0].TractorPlate != "PLATE1" {
		t.Fatalf("last page %+v", rest)
	}
	listTrailersAndMovesPage(t, e)
}

func listTrailersAndMovesPage(t *testing.T, e *yardtest.Env) {
	t.Helper()
	trailers, err := e.Yard.ListTrailers(e.Ctx, views.TrailerFilter{Kind: "DRY_VAN", LocationCode: "WH1-P1"}, nil, 0)
	must(t, err)
	if len(trailers.Items) != 1 || trailers.Items[0].UnitNumber != "T2" {
		t.Fatalf("trailers %+v", trailers.Items)
	}
	pages, err := e.Yard.ListTrailers(e.Ctx, views.TrailerFilter{}, nil, 1)
	must(t, err)
	if len(pages.Items) != 1 || pages.Next == nil {
		t.Fatalf("trailer pages %+v", pages)
	}
	moves, err := e.Yard.ListMoves(e.Ctx, views.MoveFilter{State: "COMPLETED"}, nil, 1000)
	must(t, err)
	if len(moves.Items) != 3 || moves.Next != nil {
		t.Fatalf("moves %+v", moves)
	}
	small, err := e.Yard.ListMoves(e.Ctx, views.MoveFilter{SiteID: "WH1"}, nil, 2)
	must(t, err)
	if len(small.Items) != 2 || small.Next == nil {
		t.Fatalf("move pages %+v", small)
	}
}

func TestGetsAnswerOrMiss(t *testing.T) {
	e := yardEnv()
	ci, tr := arrived(t, e)
	if v, err := e.Yard.GetVisit(e.Ctx, ci.Visit.VisitID); err != nil || v.TractorPlate != "PLATE1" {
		t.Fatalf("visit %+v %v", v, err)
	}
	_, err := e.Yard.GetVisit(e.Ctx, "visit-404")
	is(t, err, usecases.ErrVisitNotFound)
	_, err = e.Yard.GetTrailer(e.Ctx, "trl-404")
	is(t, err, usecases.ErrTrailerNotFound)
	if got := trailerOf(t, e, tr.TrailerID); got.UnitNumber != "T1" {
		t.Fatalf("trailer %+v", got)
	}
}

func TestYardBoardShowsSpotsDoorsAndTrailersInMove(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	_, docked := dockedFor(t, e, "appt-1")
	parked(t, e, "PLATE2", "T2")
	moving := checkIn(t, e, secondUnit("PLATE3", "T3")) // still in its first move
	must(t, e.Yard.ScheduleClosure(e.Ctx, "ev-1", views.Closure{ID: "clo-1", SiteID: "WH1", DoorCode: "WH1-D3", From: yardtest.T0, To: yardtest.T0.Add(time.Hour)}))
	board, err := e.Yard.GetYardBoard(e.Ctx, "WH1")
	must(t, err)
	if len(board.Spots) != 3 || len(board.Doors) != 3 || len(board.InMove) != 1 || len(board.OpenMoves) != 1 {
		t.Fatalf("board %+v", board)
	}
	if board.InMove[0].TrailerID != moving.Trailers[0].TrailerID {
		t.Fatalf("in move %+v", board.InMove)
	}
	for _, d := range board.Doors {
		switch d.Code {
		case "WH1-D1":
			if d.Trailer == nil || d.Trailer.TrailerID != docked.TrailerID || d.DockFlow == nil || *d.DockFlow != "Both" {
				t.Fatalf("door %+v", d)
			}
		case "WH1-D3":
			if !d.Closed {
				t.Fatalf("door %+v", d)
			}
		}
	}
	_, err = e.Yard.GetYardBoard(e.Ctx, "NOWHERE")
	is(t, err, usecases.ErrSiteNotFound)
}
