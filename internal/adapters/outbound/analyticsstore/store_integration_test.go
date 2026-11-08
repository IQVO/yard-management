//go:build integration

package analyticsstore_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"

	// The database comes from testcontainers through pgtest; the blank import
	// keeps the fleet rule visible from this package.
	_ "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/claudioed/yard-management/internal/adapters/outbound/analyticsstore"
	"github.com/claudioed/yard-management/internal/adapters/outbound/postgres"
	"github.com/claudioed/yard-management/internal/analytics/report"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	m.Run()
	pgtest.Shutdown()
}

// store is a migrated analytical model on a fresh database, with its writer and
// its reader.
type store struct {
	dsn    string
	proj   *analyticsstore.Projection
	reader *analyticsstore.Reader
	// day is the start of a UTC day two days back: every fixture lies in the
	// past, so a docking left open is clipped to the window, not to now.
	day time.Time
}

func newStore(t *testing.T) *store {
	t.Helper()
	dsn := pgtest.EmptyURL(t)
	ctx := context.Background()
	if err := analyticsstore.RunMigrations(ctx, dsn); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	writer, err := analyticsstore.NewPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Close)
	readerPool, err := analyticsstore.NewReadOnlyPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(readerPool.Close)
	return &store{dsn: dsn, proj: analyticsstore.NewProjection(writer), reader: analyticsstore.NewReader(readerPool),
		day: time.Now().UTC().Truncate(24 * time.Hour).Add(-48 * time.Hour)}
}

// at is the instant h:m of the fixture day.
func (s *store) at(h, m int) time.Time {
	return s.day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
}

func (s *store) window(site string) views.ReportWindow {
	return views.ReportWindow{From: s.day, To: s.day.Add(24 * time.Hour), SiteID: site}
}

var eventSeq int

// apply folds e under a fresh event id (and a CloudEvents time of its own
// instant), failing the test on error.
func (s *store) apply(t *testing.T, e report.Event) {
	t.Helper()
	if applied, err := s.tryApply(e); err != nil || !applied {
		t.Fatalf("apply %s: applied=%v err=%v", e.Kind, applied, err)
	}
}

func (s *store) tryApply(e report.Event) (bool, error) {
	eventSeq++
	e.EventID = fmt.Sprintf("evt-%s-%d", e.Kind, eventSeq)
	if e.At.IsZero() {
		e.At = s.day
	}
	if e.SiteID == "" {
		e.SiteID = "WH1"
	}
	return s.proj.Apply(context.Background(), e)
}

func (s *store) arrive(t *testing.T, site, visit string, at time.Time, trailers ...report.Arrival) {
	t.Helper()
	s.apply(t, report.Event{Kind: report.KindVisitCheckedIn, SiteID: site, VisitID: visit, Arrivals: trailers, ArrivedAt: at, At: at})
}

// trailerCycle plays one trailer at a door: docked, door work, undocked.
func (s *store) trailerCycle(t *testing.T, id, door string, docked, started, completed, undocked time.Time) {
	t.Helper()
	s.apply(t, report.Event{Kind: report.KindTrailerDocked, TrailerID: id, DoorCode: door, DockedAt: docked, At: docked})
	s.apply(t, report.Event{Kind: report.KindDoorWorkStarted, TrailerID: id, DoorCode: door, StartedAt: started, At: started})
	s.apply(t, report.Event{Kind: report.KindDoorWorkCompleted, TrailerID: id, DoorCode: door, CompletedAt: completed, At: completed})
	s.apply(t, report.Event{Kind: report.KindTrailerUndocked, TrailerID: id, DoorCode: door, UndockedAt: undocked, At: undocked})
}

func (s *store) depart(t *testing.T, site, id string, at time.Time, dwell int64) {
	t.Helper()
	s.apply(t, report.Event{Kind: report.KindTrailerDeparted, SiteID: site, TrailerID: id, DepartedAt: at, DwellMinutes: dwell, At: at})
}

func (s *store) move(t *testing.T, site, id, performer, hostler string, at time.Time, seconds int64) {
	t.Helper()
	s.apply(t, report.Event{Kind: report.KindYardMoveCompleted, SiteID: site, MoveID: id, TrailerID: "trl-x", Performer: performer, HostlerRef: hostler, CompletedAt: at, DurationSeconds: seconds, At: at})
}

func TestMigrationsAreIdempotentAndLiveInTheirOwnSchemaBesideTheOLTPOnes(t *testing.T) {
	dsn := pgtest.EmptyURL(t)
	if err := postgres.RunMigrations(dsn); err != nil {
		t.Fatalf("oltp migrations: %v", err)
	}
	for range 2 {
		if err := analyticsstore.RunMigrations(context.Background(), dsn); err != nil {
			t.Fatalf("analytics migrations: %v", err)
		}
	}
	pool := pgtest.Open(t, dsn)
	var inAnalytics, inPublic int
	pgtest.Scan(t, pool, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'analytics'
		AND table_name IN ('processed_events','trailer_facts','door_dockings','door_work','hostler_moves','schema_migrations')`, nil, &inAnalytics)
	pgtest.Scan(t, pool, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'
		AND table_name IN ('processed_events','trailer_facts','door_dockings','door_work','hostler_moves')`, nil, &inPublic)
	// The OLTP processed_events (the inbox) is public; the analytical one is not.
	if inAnalytics != 6 || inPublic != 1 {
		t.Fatalf("analytics schema has %d of 6 tables, public has %d of the shared names (want only the OLTP inbox)", inAnalytics, inPublic)
	}
}

func TestTheReportsRoleReadsButNeverWrites(t *testing.T) {
	dsn := pgtest.EmptyURL(t)
	admin := pgtest.Open(t, dsn)
	if err := pgtest.Exec(admin, `DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'yard_analytics_reader') THEN
		CREATE ROLE yard_analytics_reader LOGIN PASSWORD 'reader'; END IF; END $$`); err != nil {
		t.Fatal(err)
	}
	if err := analyticsstore.RunMigrations(context.Background(), dsn); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("yard_analytics_reader", "reader")
	pool, err := analyticsstore.NewReadOnlyPool(context.Background(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var n int
	pgtest.Scan(t, pool, `SELECT count(*) FROM analytics.trailer_facts`, nil, &n)
	if err := pgtest.Exec(pool, `INSERT INTO analytics.hostler_moves VALUES ('m','WH1','t','h',now(),1)`); err == nil {
		t.Fatal("the reports role must not write")
	}
	if _, _, err := analyticsstore.NewReader(pool).LastEventAt(context.Background()); err != nil {
		t.Fatalf("the reports role reads freshness: %v", err)
	}
}

func TestYardDwellByKindWithDriverWait(t *testing.T) {
	s := newStore(t)
	s.arrive(t, "WH1", "visit-1", s.at(14, 0),
		report.Arrival{TrailerID: "trl-1", UnitNumber: "T100", Kind: "DRY_VAN"}, report.Arrival{TrailerID: "trl-2", UnitNumber: "R200", Kind: "REEFER"})
	s.arrive(t, "WH1", "visit-2", s.at(10, 0), report.Arrival{TrailerID: "trl-3", UnitNumber: "F300", Kind: "FLATBED"})
	s.arrive(t, "WH1", "visit-3", s.at(8, 0), report.Arrival{TrailerID: "trl-4", UnitNumber: "T101", Kind: "DRY_VAN"})
	s.trailerCycle(t, "trl-1", "WH1-D1", s.at(14, 20), s.at(14, 25), s.at(15, 0), s.at(15, 10))
	s.trailerCycle(t, "trl-2", "WH1-D1", s.at(15, 10), s.at(15, 20), s.at(16, 0), s.at(16, 10))
	s.depart(t, "WH1", "trl-1", s.at(15, 35), 95)
	s.depart(t, "WH1", "trl-2", s.at(16, 30), 150)
	s.depart(t, "WH1", "trl-3", s.at(11, 0), 60) // no door work: no driver wait
	s.depart(t, "WH1", "trl-4", s.at(9, 0), 60)
	// Outside the window, in another site, and with no seen arrival: all left out.
	s.depart(t, "WH1", "trl-late", s.at(23, 0).Add(2*time.Hour), 600)
	s.arrive(t, "WH2", "visit-9", s.at(9, 0), report.Arrival{TrailerID: "trl-w2", UnitNumber: "W2", Kind: "CONTAINER"})
	s.depart(t, "WH2", "trl-w2", s.at(12, 0), 180)
	s.depart(t, "WH1", "trl-nokind", s.at(12, 0), 999)

	rows, err := s.reader.YardDwell(context.Background(), s.window("WH1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows %+v, want DRY_VAN, FLATBED, REEFER", rows)
	}
	dry, flat, reefer := rows[0], rows[1], rows[2]
	if dry.Kind != "DRY_VAN" || dry.Trailers != 2 || dry.AvgDwellMinutes != 77.5 || dry.MaxDwellMinutes != 95 || dry.AvgDriverWaitMinutes == nil || *dry.AvgDriverWaitMinutes != 25 {
		t.Errorf("DRY_VAN %+v, want 2 trailers, mean 77.5, max 95, wait 25 (only trl-1 had door work)", dry)
	}
	if flat.Kind != "FLATBED" || flat.Trailers != 1 || flat.AvgDwellMinutes != 60 || flat.AvgDriverWaitMinutes != nil {
		t.Errorf("FLATBED %+v, want one trailer, 60, wait null", flat)
	}
	if reefer.Kind != "REEFER" || reefer.Trailers != 1 || reefer.MaxDwellMinutes != 150 || reefer.AvgDriverWaitMinutes == nil || *reefer.AvgDriverWaitMinutes != 80 {
		t.Errorf("REEFER %+v, want one trailer, max 150, wait 80", reefer)
	}

	all, err := s.reader.YardDwell(context.Background(), s.window(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 || all[0].Kind != "CONTAINER" || all[0].Trailers != 1 {
		t.Errorf("every site: %+v, want CONTAINER of WH2 too", all)
	}
}

func TestArrivalAndDoorWorkInEitherOrderGiveTheSameDriverWait(t *testing.T) {
	s := newStore(t)
	// DoorWorkStarted (keyed by trailer) overtakes VisitCheckedIn (keyed by visit).
	s.apply(t, report.Event{Kind: report.KindDoorWorkStarted, TrailerID: "trl-5", DoorCode: "WH1-D3", StartedAt: s.at(9, 30), At: s.at(9, 30)})
	s.arrive(t, "WH1", "visit-5", s.at(9, 0), report.Arrival{TrailerID: "trl-5", UnitNumber: "X", Kind: "REEFER"})
	// A later work start never moves the first one.
	s.apply(t, report.Event{Kind: report.KindDoorWorkStarted, TrailerID: "trl-5", DoorCode: "WH1-D3", StartedAt: s.at(11, 0), At: s.at(11, 0)})
	s.depart(t, "WH1", "trl-5", s.at(12, 0), 180)
	rows, err := s.reader.YardDwell(context.Background(), s.window("WH1"))
	if err != nil || len(rows) != 1 || rows[0].AvgDriverWaitMinutes == nil || *rows[0].AvgDriverWaitMinutes != 30 {
		t.Fatalf("rows %+v err %v, want a 30 minute wait", rows, err)
	}
}

func TestDoorUtilisationFromDockedAndDoorWorkTime(t *testing.T) {
	s := newStore(t)
	s.trailerCycle(t, "trl-1", "WH1-D1", s.at(14, 20), s.at(14, 25), s.at(15, 0), s.at(15, 10)) // 50 docked, 35 work
	s.trailerCycle(t, "trl-2", "WH1-D1", s.at(15, 10), s.at(15, 20), s.at(16, 0), s.at(16, 10)) // 60 docked, 40 work
	// A docking that began the evening before and ended at 02:00: 120 minutes inside the window.
	s.trailerCycle(t, "trl-3", "WH1-D2", s.day.Add(-3*time.Hour), s.day.Add(-2*time.Hour), s.day.Add(time.Hour), s.at(2, 0))
	// A docking never closed: clipped to the end of the window (23:00-24:00).
	s.apply(t, report.Event{Kind: report.KindTrailerDocked, TrailerID: "trl-4", DoorCode: "WH1-D3", DockedAt: s.at(23, 0), At: s.at(23, 0)})
	// Another site and a docking wholly outside the window.
	s.apply(t, report.Event{Kind: report.KindTrailerDocked, SiteID: "WH2", TrailerID: "trl-5", DoorCode: "WH2-D1", DockedAt: s.at(1, 0), At: s.at(1, 0)})
	s.trailerCycle(t, "trl-6", "WH1-D1", s.day.Add(-30*time.Hour), s.day.Add(-29*time.Hour), s.day.Add(-28*time.Hour), s.day.Add(-27*time.Hour))

	rows, err := s.reader.DoorUtilisation(context.Background(), s.window("WH1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows %+v, want D1, D2, D3 of WH1", rows)
	}
	want := []views.DoorRow{
		{SiteID: "WH1", DoorCode: "WH1-D1", Dockings: 2, OccupiedMinutes: 110, WorkMinutes: 75, Utilisation: 110.0 / 1440},
		{SiteID: "WH1", DoorCode: "WH1-D2", Dockings: 1, OccupiedMinutes: 120, WorkMinutes: 60, Utilisation: 120.0 / 1440},
		{SiteID: "WH1", DoorCode: "WH1-D3", Dockings: 1, OccupiedMinutes: 60, WorkMinutes: 0, Utilisation: 60.0 / 1440},
	}
	for i, w := range want {
		g := rows[i]
		if g.SiteID != w.SiteID || g.DoorCode != w.DoorCode || g.Dockings != w.Dockings || g.OccupiedMinutes != w.OccupiedMinutes || g.WorkMinutes != w.WorkMinutes || diff(g.Utilisation, w.Utilisation) > 1e-9 {
			t.Errorf("row %d = %+v, want %+v", i, g, w)
		}
	}
	all, err := s.reader.DoorUtilisation(context.Background(), s.window(""))
	if err != nil || len(all) != 4 {
		t.Fatalf("every site: %d rows (%v), want 4 with WH2-D1", len(all), err)
	}
}

func diff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

func TestHostlerProductivityPerBusyHour(t *testing.T) {
	s := newStore(t)
	s.move(t, "WH1", "mv-1", "HOSTLER", "h-7", s.at(14, 19), 420)
	s.move(t, "WH1", "mv-2", "HOSTLER", "h-7", s.at(14, 50), 300)
	s.move(t, "WH1", "mv-3", "HOSTLER", "h-7", s.at(15, 30), 600)
	s.move(t, "WH1", "mv-4", "HOSTLER", "h-9", s.at(9, 0), 60)
	s.move(t, "WH1", "mv-5", "VISITING_DRIVER", "", s.at(9, 5), 999)             // not a yard truck
	s.move(t, "WH1", "mv-6", "HOSTLER", "h-7", s.at(23, 30).Add(time.Hour), 100) // after the window
	s.move(t, "WH2", "mv-7", "HOSTLER", "h-7", s.at(10, 0), 200)

	rows, err := s.reader.HostlerProductivity(context.Background(), s.window("WH1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows %+v, want h-7 and h-9", rows)
	}
	if h := rows[0]; h.HostlerRef != "h-7" || h.Moves != 3 || h.AvgMoveSeconds != 440 || h.MovesPerHour != 1.5 {
		t.Errorf("h-7 = %+v, want 3 moves, 440 s, 1.5 per busy hour (hours 14 and 15)", h)
	}
	if h := rows[1]; h.HostlerRef != "h-9" || h.Moves != 1 || h.AvgMoveSeconds != 60 || h.MovesPerHour != 1 {
		t.Errorf("h-9 = %+v", h)
	}
	all, err := s.reader.HostlerProductivity(context.Background(), s.window(""))
	if err != nil || len(all) != 3 || all[2].SiteID != "WH2" {
		t.Fatalf("every site: %+v (%v), want WH2 too", all, err)
	}
}

func TestAnEmptyWindowIsAnEmptyReportNotAnError(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	d, err1 := s.reader.YardDwell(ctx, s.window(""))
	u, err2 := s.reader.DoorUtilisation(ctx, s.window(""))
	h, err3 := s.reader.HostlerProductivity(ctx, s.window(""))
	if err1 != nil || err2 != nil || err3 != nil || d == nil || u == nil || h == nil || len(d)+len(u)+len(h) != 0 {
		t.Fatalf("empty reports are non-nil empty slices: %v %v %v / %v %v %v", d, u, h, err1, err2, err3)
	}
}

func TestApplyIsIdempotentPerEventIDAndFreshnessFollowsTheNewestEvent(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, ok, err := s.reader.LastEventAt(ctx); err != nil || ok {
		t.Fatalf("before the first event: ok=%v err=%v", ok, err)
	}
	e := report.Event{Kind: report.KindYardMoveCompleted, EventID: "evt-dup", At: s.at(14, 19), SiteID: "WH1", MoveID: "mv-1", TrailerID: "trl-1",
		Performer: "HOSTLER", HostlerRef: "h-7", CompletedAt: s.at(14, 19), DurationSeconds: 420}
	if applied, err := s.proj.Apply(ctx, e); err != nil || !applied {
		t.Fatalf("first apply: %v %v", applied, err)
	}
	if applied, err := s.proj.Apply(ctx, e); err != nil || applied {
		t.Fatalf("a redelivery is skipped: applied=%v err=%v", applied, err)
	}
	s.move(t, "WH1", "mv-2", "HOSTLER", "h-7", s.at(16, 0), 100)
	if rows, _ := s.reader.HostlerProductivity(ctx, s.window("WH1")); len(rows) != 1 || rows[0].Moves != 2 {
		t.Fatalf("rows %+v, want 2 moves (the duplicate did not count)", rows)
	}
	at, ok, err := s.reader.LastEventAt(ctx)
	if err != nil || !ok || !at.Equal(s.at(16, 0)) {
		t.Fatalf("freshness %v ok=%v err=%v, want %v", at, ok, err, s.at(16, 0))
	}
}

func TestADeterministicRejectionIsErrRejectedAndLeavesNoClaim(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	// A NUL byte is Postgres SQLSTATE 22021: it can never be stored.
	bad := report.Event{Kind: report.KindYardMoveCompleted, EventID: "evt-nul", At: s.at(9, 0), SiteID: "WH1", MoveID: "mv-nul", TrailerID: "trl-1",
		Performer: "HOSTLER", HostlerRef: "h\x00-7", CompletedAt: s.at(9, 0), DurationSeconds: 1}
	if _, err := s.proj.Apply(ctx, bad); !errors.Is(err, report.ErrRejected) {
		t.Fatalf("err = %v, want ErrRejected", err)
	}
	// A pure validation failure is refused before any SQL.
	if _, err := s.proj.Apply(ctx, report.Event{Kind: report.KindTrailerDocked, EventID: "evt-x", At: s.at(9, 0), SiteID: "WH1"}); !errors.Is(err, report.ErrRejected) {
		t.Fatalf("err = %v, want ErrRejected", err)
	}
	var claims int
	pool := pgtest.Open(t, s.dsn)
	pgtest.Scan(t, pool, `SELECT count(*) FROM analytics.processed_events`, nil, &claims)
	if claims != 0 {
		t.Fatalf("%d claims left behind: a rejected event's claim rolls back with it", claims)
	}
}

func TestATransientFailureIsNotAnErrRejected(t *testing.T) {
	s := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e := report.Event{Kind: report.KindYardMoveCompleted, EventID: "evt-cancel", At: s.at(9, 0), SiteID: "WH1", MoveID: "mv-1", TrailerID: "trl-1",
		Performer: "HOSTLER", HostlerRef: "h-7", CompletedAt: s.at(9, 0), DurationSeconds: 1}
	if _, err := s.proj.Apply(ctx, e); err == nil || errors.Is(err, report.ErrRejected) {
		t.Fatalf("a cancelled context is transient, got %v", err)
	}
}
