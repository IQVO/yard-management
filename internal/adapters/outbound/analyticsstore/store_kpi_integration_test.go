//go:build integration

package analyticsstore_test

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/claudioed/yard-management/analytics"
	"github.com/claudioed/yard-management/internal/analytics/report"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
)

// visit plays one visit through the gate: check-in at in, check-out at out.
func (s *store) visit(t *testing.T, site, id, purpose string, in, out time.Time) {
	t.Helper()
	s.apply(t, report.Event{Kind: report.KindVisitCheckedIn, SiteID: site, VisitID: id, Purpose: purpose, ArrivedAt: in, At: in})
	s.checkOut(t, site, id, out, int64(out.Sub(in)/time.Minute))
}

func (s *store) checkOut(t *testing.T, site, id string, out time.Time, onSite int64) {
	t.Helper()
	s.apply(t, report.Event{Kind: report.KindVisitCheckedOut, SiteID: site, VisitID: id, DepartedAt: out, OnSiteMinutes: onSite, At: out})
}

func near(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

func TestTurnTimeIsGateToGateByPurposeWithPercentiles(t *testing.T) {
	s := newStore(t)
	// DROP: 30, 40, 50, 60 minutes -> p50 45, p90 57 (linear interpolation).
	for i, m := range []int{30, 40, 50, 60} {
		id := string(rune('a' + i))
		s.visit(t, "WH1", "drop-"+id, "DROP", s.at(8+i, 0), s.at(8+i, 0).Add(time.Duration(m)*time.Minute))
	}
	s.visit(t, "WH1", "unload-1", "LIVE_UNLOAD", s.at(9, 0), s.at(11, 0)) // a single sample: 120
	for i := range 3 {                                                    // ties: 10, 10, 10
		s.visit(t, "WH1", "pickup-"+string(rune('a'+i)), "PICKUP", s.at(12+i, 0), s.at(12+i, 10))
	}
	// Left out: checked out after the window, another site, no seen check-in (no purpose).
	s.visit(t, "WH1", "late", "DROP", s.at(23, 30), s.at(23, 30).Add(2*time.Hour))
	s.visit(t, "WH2", "other-site", "DROP", s.at(8, 0), s.at(9, 0))
	s.checkOut(t, "WH1", "no-checkin", s.at(10, 0), 500)

	rows, err := s.reader.TurnTime(context.Background(), s.window("WH1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows %+v, want DROP, LIVE_UNLOAD, PICKUP", rows)
	}
	drop, unload, pickup := rows[0], rows[1], rows[2]
	if drop.Purpose != "DROP" || drop.Visits != 4 || !near(drop.AvgMinutes, 45) || !near(drop.P50Minutes, 45) || !near(drop.P90Minutes, 57) || drop.MaxMinutes != 60 {
		t.Errorf("DROP %+v, want 4 visits, avg 45, p50 45, p90 57, max 60", drop)
	}
	if unload.Purpose != "LIVE_UNLOAD" || unload.Visits != 1 || !near(unload.AvgMinutes, 120) || !near(unload.P50Minutes, 120) || !near(unload.P90Minutes, 120) || unload.MaxMinutes != 120 {
		t.Errorf("LIVE_UNLOAD %+v, a single sample is every percentile", unload)
	}
	if pickup.Purpose != "PICKUP" || pickup.Visits != 3 || !near(pickup.AvgMinutes, 10) || !near(pickup.P50Minutes, 10) || !near(pickup.P90Minutes, 10) {
		t.Errorf("PICKUP %+v, ties give the tied value", pickup)
	}
	all, err := s.reader.TurnTime(context.Background(), s.window(""))
	if err != nil || len(all) != 3 || all[0].Visits != 5 {
		t.Fatalf("every site: %+v (%v), want the DROP of WH2 counted too", all, err)
	}
}

func TestTurnTimeIsTheSameWhenTheCheckOutOvertakesTheCheckIn(t *testing.T) {
	s := newStore(t)
	// Different keys on different partitions: the departure may be folded first.
	s.checkOut(t, "WH1", "v1", s.at(10, 0), 75)
	s.apply(t, report.Event{Kind: report.KindVisitCheckedIn, SiteID: "WH1", VisitID: "v1", Purpose: "BOBTAIL", ArrivedAt: s.at(8, 45), At: s.at(8, 45)})
	rows, err := s.reader.TurnTime(context.Background(), s.window("WH1"))
	if err != nil || len(rows) != 1 || rows[0].Purpose != "BOBTAIL" || rows[0].MaxMinutes != 75 {
		t.Fatalf("rows %+v err %v, want one BOBTAIL visit of 75 minutes", rows, err)
	}
}

func TestDetentionExposureCountsDwellOverTheThresholdPerKind(t *testing.T) {
	s := newStore(t)
	for i, k := range []struct {
		kind  string
		dwell int64
	}{{"DRY_VAN", 60}, {"DRY_VAN", 120}, {"DRY_VAN", 121}, {"DRY_VAN", 300}, {"REEFER", 500}, {"FLATBED", 30}} {
		id := string(rune('a' + i))
		s.arrive(t, "WH1", "visit-"+id, s.at(1, 0), report.Arrival{TrailerID: "trl-" + id, UnitNumber: id, Kind: k.kind})
		s.depart(t, "WH1", "trl-"+id, s.at(12, 0), k.dwell)
	}
	s.depart(t, "WH1", "trl-nokind", s.at(12, 0), 900) // no seen arrival: no kind, left out

	rows, err := s.reader.DetentionExposure(context.Background(), s.window("WH1"), 120)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows %+v, want DRY_VAN, FLATBED, REEFER", rows)
	}
	dry, flat, reefer := rows[0], rows[1], rows[2]
	// 120 is not OVER 120: only 121 and 300 exceed, by 1 + 180 minutes.
	if dry.Kind != "DRY_VAN" || dry.Trailers != 4 || dry.Exceeding != 2 || dry.Share == nil || *dry.Share != 0.5 || dry.ExcessMinutes != 181 {
		t.Errorf("DRY_VAN %+v, want 4 trailers, 2 exceeding, share 0.5, excess 181", dry)
	}
	if flat.Kind != "FLATBED" || flat.Trailers != 1 || flat.Exceeding != 0 || flat.Share == nil || *flat.Share != 0 || flat.ExcessMinutes != 0 {
		t.Errorf("FLATBED %+v, want one trailer, none exceeding, share 0", flat)
	}
	if reefer.Kind != "REEFER" || reefer.Trailers != 1 || reefer.Exceeding != 1 || reefer.Share == nil || *reefer.Share != 1 || reefer.ExcessMinutes != 380 {
		t.Errorf("REEFER %+v, a single exceeding sample: share 1, excess 380", reefer)
	}

	strict, err := s.reader.DetentionExposure(context.Background(), s.window("WH1"), 1)
	if err != nil || strict[0].Exceeding != 4 || strict[0].ExcessMinutes != 59+119+120+299 {
		t.Fatalf("threshold 1: %+v (%v), want every DRY_VAN over it", strict, err)
	}
	lax, err := s.reader.DetentionExposure(context.Background(), s.window("WH1"), 10080)
	if err != nil || lax[0].Exceeding != 0 || lax[0].ExcessMinutes != 0 {
		t.Fatalf("threshold 10080: %+v (%v), want none over a week", lax, err)
	}
	none, err := s.reader.DetentionExposure(context.Background(), s.window("WH2"), 120)
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("an empty window is an empty report: %+v (%v)", none, err)
	}
}

func TestYardDwellAddsPercentilesAndProcessingTime(t *testing.T) {
	s := newStore(t)
	for i, dwell := range []int64{60, 90, 120, 600} {
		id := string(rune('a' + i))
		s.arrive(t, "WH1", "visit-"+id, s.at(1, 0), report.Arrival{TrailerID: "trl-" + id, UnitNumber: id, Kind: "DRY_VAN"})
		s.depart(t, "WH1", "trl-"+id, s.at(12, 0), dwell)
	}
	// Door work: trl-a 30 and 20 minutes, trl-b 40 minutes, trl-c an unfinished session (not counted).
	s.trailerCycle(t, "trl-a", "WH1-D1", s.at(2, 0), s.at(2, 5), s.at(2, 35), s.at(2, 40))
	s.trailerCycle(t, "trl-a", "WH1-D1", s.at(4, 0), s.at(4, 5), s.at(4, 25), s.at(4, 40))
	s.trailerCycle(t, "trl-b", "WH1-D2", s.at(3, 0), s.at(3, 5), s.at(3, 45), s.at(3, 50))
	s.apply(t, report.Event{Kind: report.KindDoorWorkStarted, TrailerID: "trl-c", DoorCode: "WH1-D3", StartedAt: s.at(5, 0), At: s.at(5, 0)})
	s.arrive(t, "WH1", "visit-r", s.at(1, 0), report.Arrival{TrailerID: "trl-r", UnitNumber: "r", Kind: "REEFER"})
	s.depart(t, "WH1", "trl-r", s.at(12, 0), 45)

	rows, err := s.reader.YardDwell(context.Background(), s.window("WH1"))
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows %+v err %v, want DRY_VAN and REEFER", rows, err)
	}
	dry, reefer := rows[0], rows[1]
	// dwell 60, 90, 120, 600: p50 = 105, p90 = 120 + 0.7*480 = 456.
	if dry.P50DwellMinutes == nil || !near(*dry.P50DwellMinutes, 105) || dry.P90DwellMinutes == nil || !near(*dry.P90DwellMinutes, 456) {
		t.Errorf("DRY_VAN percentiles %v %v, want 105 and 456 (the mean %v hides the tail)", dry.P50DwellMinutes, dry.P90DwellMinutes, dry.AvgDwellMinutes)
	}
	if dry.AvgProcessingMinutes == nil || !near(*dry.AvgProcessingMinutes, 30) {
		t.Errorf("DRY_VAN processing %v, want the mean of 30, 20 and 40 = 30", dry.AvgProcessingMinutes)
	}
	if reefer.AvgProcessingMinutes != nil || reefer.P50DwellMinutes == nil || !near(*reefer.P50DwellMinutes, 45) || !near(*reefer.P90DwellMinutes, 45) {
		t.Errorf("REEFER %+v, want no processing sample (null) and a single sample 45 for both percentiles", reefer)
	}
}

func TestExceptionRatesPerSite(t *testing.T) {
	s := newStore(t)
	for i := range 3 {
		s.apply(t, report.Event{Kind: report.KindVisitCheckedIn, SiteID: "WH1", VisitID: "ok-" + string(rune('a'+i)), Purpose: "DROP", ArrivedAt: s.at(8+i, 0), At: s.at(8+i, 0)})
	}
	s.apply(t, report.Event{Kind: report.KindVisitRejected, SiteID: "WH1", VisitID: "no-1", Reason: "APPOINTMENT_REQUIRED", RejectedAt: s.at(9, 30), At: s.at(9, 30)})
	s.apply(t, report.Event{Kind: report.KindSealExceptionRaised, SiteID: "WH1", TrailerID: "trl-1", RaisedAt: s.at(10, 0), At: s.at(10, 0)})
	s.apply(t, report.Event{Kind: report.KindSealExceptionRaised, SiteID: "WH1", VisitID: "no-1", RaisedAt: s.at(9, 29), At: s.at(9, 29)}) // at the gate, no trailer
	for i, reason := range []string{"DAMAGED", "DAMAGED", "SECURITY"} {
		s.apply(t, report.Event{Kind: report.KindTrailerHeld, SiteID: "WH1", TrailerID: "trl-" + string(rune('a'+i)), Reason: reason, Version: int64(2 + i), At: s.at(11+i, 0)})
	}
	s.apply(t, report.Event{Kind: report.KindYardCheckClosed, SiteID: "WH1", YardCheckID: "yck-1", ClosedAt: s.at(16, 0), Discrepancies: 3, At: s.at(16, 0)})
	s.apply(t, report.Event{Kind: report.KindYardCheckClosed, SiteID: "WH1", YardCheckID: "yck-2", ClosedAt: s.at(17, 0), Discrepancies: 0, At: s.at(17, 0)})
	// Only rejections at another site, and an event outside the window.
	s.apply(t, report.Event{Kind: report.KindVisitRejected, SiteID: "WH2", VisitID: "no-2", Reason: "SITE_REQUEST", RejectedAt: s.at(9, 0), At: s.at(9, 0)})
	s.apply(t, report.Event{Kind: report.KindTrailerHeld, SiteID: "WH1", TrailerID: "trl-old", Reason: "QUALITY", Version: 2, At: s.day.Add(-time.Hour)})

	rows, err := s.reader.Exceptions(context.Background(), s.window("WH1"))
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows %+v err %v, want WH1 only", rows, err)
	}
	r := rows[0]
	if r.SiteID != "WH1" || r.VisitsCheckedIn != 3 || r.VisitsRejected != 1 || r.RejectionRate == nil || *r.RejectionRate != 0.25 {
		t.Errorf("gate %+v, want 3 checked in, 1 rejected, rate 1/4", r)
	}
	if r.SealExceptions != 2 || r.SealExceptionRate == nil || !near(*r.SealExceptionRate, 2.0/3) {
		t.Errorf("seal %+v, want 2 exceptions, 2/3 per check-in", r)
	}
	want := map[string]int{"DAMAGED": 2, "SEAL_BROKEN": 0, "NOT_FOUND": 0, "QUALITY": 0, "SECURITY": 1}
	if len(r.HoldsByReason) != 5 {
		t.Errorf("holds %v, want every reason listed", r.HoldsByReason)
	}
	for reason, n := range want {
		if r.HoldsByReason[reason] != n {
			t.Errorf("holds %v, want %v", r.HoldsByReason, want)
			break
		}
	}
	if r.YardChecksClosed != 2 || r.Discrepancies != 3 {
		t.Errorf("yard checks %+v, want 2 closed with 3 discrepancies", r)
	}

	all, err := s.reader.Exceptions(context.Background(), s.window(""))
	if err != nil || len(all) != 2 || all[1].SiteID != "WH2" || all[1].VisitsRejected != 1 || all[1].RejectionRate == nil || *all[1].RejectionRate != 1 || all[1].SealExceptionRate != nil {
		t.Fatalf("every site: %+v (%v), want WH2 with one rejection, rate 1 and a null seal rate", all, err)
	}
	empty, err := s.reader.Exceptions(context.Background(), views.ReportWindow{From: s.day.Add(-72 * time.Hour), To: s.day.Add(-48 * time.Hour)})
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("an empty window is an empty report: %+v (%v)", empty, err)
	}
}

// kpiEvents is a day of the yard in the natural order of each key.
func (s *store) kpiEvents() []report.Event {
	var ev []report.Event
	add := func(e report.Event) { ev = append(ev, e) }
	add(report.Event{Kind: report.KindVisitCheckedIn, SiteID: "WH1", VisitID: "v1", Purpose: "DROP", ArrivedAt: s.at(8, 0), At: s.at(8, 0),
		Arrivals: []report.Arrival{{TrailerID: "t1", UnitNumber: "T1", Kind: "DRY_VAN"}}})
	add(report.Event{Kind: report.KindDoorWorkStarted, SiteID: "WH1", TrailerID: "t1", DoorCode: "WH1-D1", StartedAt: s.at(8, 30), At: s.at(8, 30)})
	add(report.Event{Kind: report.KindTrailerDeparted, SiteID: "WH1", TrailerID: "t1", DepartedAt: s.at(11, 0), DwellMinutes: 180, At: s.at(11, 0)})
	add(report.Event{Kind: report.KindVisitCheckedOut, SiteID: "WH1", VisitID: "v1", DepartedAt: s.at(11, 5), OnSiteMinutes: 185, At: s.at(11, 5)})
	add(report.Event{Kind: report.KindVisitCheckedIn, SiteID: "WH1", VisitID: "v2", Purpose: "LIVE_LOAD", ArrivedAt: s.at(9, 0), At: s.at(9, 0),
		Arrivals: []report.Arrival{{TrailerID: "t2", UnitNumber: "T2", Kind: "REEFER"}}})
	add(report.Event{Kind: report.KindTrailerDeparted, SiteID: "WH1", TrailerID: "t2", DepartedAt: s.at(10, 0), DwellMinutes: 60, At: s.at(10, 0)})
	add(report.Event{Kind: report.KindVisitCheckedOut, SiteID: "WH1", VisitID: "v2", DepartedAt: s.at(10, 5), OnSiteMinutes: 65, At: s.at(10, 5)})
	add(report.Event{Kind: report.KindVisitRejected, SiteID: "WH1", VisitID: "v3", Reason: "SEAL_MISMATCH", RejectedAt: s.at(12, 0), At: s.at(12, 0)})
	add(report.Event{Kind: report.KindSealExceptionRaised, SiteID: "WH1", VisitID: "v3", RaisedAt: s.at(11, 59), At: s.at(11, 59)})
	add(report.Event{Kind: report.KindTrailerHeld, SiteID: "WH1", TrailerID: "t1", Reason: "SEAL_BROKEN", Version: 3, At: s.at(8, 40)})
	add(report.Event{Kind: report.KindTrailerHeld, SiteID: "WH1", TrailerID: "t1", Reason: "QUALITY", Version: 6, At: s.at(9, 40)})
	add(report.Event{Kind: report.KindYardCheckClosed, SiteID: "WH1", YardCheckID: "y1", ClosedAt: s.at(15, 0), Discrepancies: 2, At: s.at(15, 0)})
	return ev
}

// snapshot is every report of the window as JSON, to compare two stores.
func (s *store) snapshot(t *testing.T) string {
	t.Helper()
	ctx, w := context.Background(), s.window("")
	dwell, e1 := s.reader.YardDwell(ctx, w)
	turn, e2 := s.reader.TurnTime(ctx, w)
	det, e3 := s.reader.DetentionExposure(ctx, w, 120)
	exc, e4 := s.reader.Exceptions(ctx, w)
	for _, err := range []error{e1, e2, e3, e4} {
		if err != nil {
			t.Fatal(err)
		}
	}
	b, err := json.Marshal([]any{dwell, turn, det, exc})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (s *store) applyAll(t *testing.T, events []report.Event) {
	t.Helper()
	for _, e := range events {
		s.apply(t, e)
	}
}

func TestReplayingTheSameEventsIsIdempotent(t *testing.T) {
	s := newStore(t)
	events := s.kpiEvents()
	s.applyAll(t, events)
	first := s.snapshot(t)
	if first == "" || first == `[[],[],[],[]]` {
		t.Fatalf("the fixture folded nothing: %s", first)
	}
	// The same events under NEW CloudEvents ids (a rebuilt consumer group after
	// the dedupe set was dropped) change nothing: every row is keyed by its natural key.
	s.applyAll(t, events)
	if again := s.snapshot(t); again != first {
		t.Fatalf("replay under new ids changed the reports:\n%s\n%s", first, again)
	}
	// The same ids are skipped by the dedupe set.
	e := events[0]
	e.EventID = "evt-fixed"
	if applied, err := s.proj.Apply(context.Background(), e); err != nil || !applied {
		t.Fatalf("first delivery: %v %v", applied, err)
	}
	if applied, err := s.proj.Apply(context.Background(), e); err != nil || applied {
		t.Fatalf("redelivery: applied=%v err=%v, want skipped", applied, err)
	}
	if again := s.snapshot(t); again != first {
		t.Fatalf("redelivery changed the reports")
	}
}

func TestEventsOfDifferentKeysConvergeWhateverTheArrivalOrder(t *testing.T) {
	forward := newStore(t)
	forward.applyAll(t, forward.kpiEvents())
	want := forward.snapshot(t)

	reversed := newStore(t)
	events := reversed.kpiEvents()
	for i := len(events) - 1; i >= 0; i-- {
		reversed.apply(t, events[i])
	}
	if got := reversed.snapshot(t); got != want {
		t.Fatalf("reverse arrival order gives other reports:\n%s\n%s", want, got)
	}

	// Every rotation of the stream converges as well.
	for shift := 1; shift < len(events); shift++ {
		rotated := newStore(t)
		evs := rotated.kpiEvents()
		rotated.applyAll(t, append(append([]report.Event(nil), evs[shift:]...), evs[:shift]...))
		if got := rotated.snapshot(t); got != want {
			t.Fatalf("rotation by %d gives other reports:\n%s\n%s", shift, want, got)
		}
	}
}

func TestADepartureReplayedWithOtherValuesKeepsTheEarliestAndItsMinutes(t *testing.T) {
	s := newStore(t)
	s.apply(t, report.Event{Kind: report.KindVisitCheckedIn, SiteID: "WH1", VisitID: "v1", Purpose: "DROP", ArrivedAt: s.at(8, 0), At: s.at(8, 0)})
	s.checkOut(t, "WH1", "v1", s.at(10, 0), 120)
	s.checkOut(t, "WH1", "v1", s.at(12, 0), 240) // a later duplicate with other values never wins
	s.checkOut(t, "WH1", "v1", s.at(9, 0), 60)   // an earlier one does, with its own minutes
	rows, err := s.reader.TurnTime(context.Background(), s.window("WH1"))
	if err != nil || len(rows) != 1 || rows[0].Visits != 1 || rows[0].MaxMinutes != 60 {
		t.Fatalf("rows %+v err %v, want one visit of 60 minutes", rows, err)
	}
}

func TestMigration0002GoesDownLeavingTheFirstOneIntact(t *testing.T) {
	s := newStore(t)
	down, err := analytics.Migrations.ReadFile("migrations/0002_kpi_standard.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	pool := pgtest.Open(t, s.dsn)
	if err := pgtest.Exec(pool, string(down)); err != nil {
		t.Fatalf("down: %v", err)
	}
	var gone, kept int
	pgtest.Scan(t, pool, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'analytics'
		AND table_name IN ('visit_facts','gate_exceptions','trailer_holds','yard_check_results')`, nil, &gone)
	pgtest.Scan(t, pool, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'analytics'
		AND table_name IN ('processed_events','trailer_facts','door_dockings','door_work','hostler_moves')`, nil, &kept)
	if gone != 0 || kept != 5 {
		t.Fatalf("after down: %d KPI tables left, %d of the 5 first-migration tables kept", gone, kept)
	}
}
