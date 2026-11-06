package report_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/analytics/report"
)

var t0 = time.Date(2026, 11, 5, 14, 0, 0, 0, time.UTC)

// valid returns a fully populated event of kind k.
func valid(k report.Kind) report.Event {
	return report.Event{
		Kind: k, EventID: "evt-1", At: t0, SiteID: "WH1",
		TrailerID: "trl-1", VisitID: "visit-1", DoorCode: "WH1-D1", MoveID: "mv-1",
		Arrivals:  []report.Arrival{{TrailerID: "trl-1", UnitNumber: "T100", Kind: "DRY_VAN"}},
		ArrivedAt: t0, DockedAt: t0, UndockedAt: t0, StartedAt: t0, CompletedAt: t0, DepartedAt: t0,
		DwellMinutes: 95, Performer: report.PerformerHostler, HostlerRef: "h-7", DurationSeconds: 420,
	}
}

var allKinds = []report.Kind{
	report.KindVisitCheckedIn, report.KindTrailerDocked, report.KindTrailerUndocked, report.KindDoorWorkStarted,
	report.KindDoorWorkCompleted, report.KindTrailerDeparted, report.KindYardMoveCompleted,
}

func TestEveryKindValidatesWhenComplete(t *testing.T) {
	for _, k := range allKinds {
		if err := valid(k).Validate(); err != nil {
			t.Errorf("%s: %v", k, err)
		}
	}
}

func TestSubjectIsTheAggregateIDOfTheKind(t *testing.T) {
	want := map[report.Kind]string{
		report.KindVisitCheckedIn: "visit-1", report.KindYardMoveCompleted: "mv-1", report.KindTrailerDocked: "trl-1",
		report.KindTrailerUndocked: "trl-1", report.KindDoorWorkStarted: "trl-1", report.KindDoorWorkCompleted: "trl-1", report.KindTrailerDeparted: "trl-1",
	}
	for k, subject := range want {
		if got := valid(k).Subject(); got != subject {
			t.Errorf("%s subject %q, want %q", k, got, subject)
		}
	}
}

func TestValidateRejectsWhatCanNeverBeProjected(t *testing.T) {
	cases := []struct {
		name   string
		kind   report.Kind
		mutate func(*report.Event)
		cause  string
	}{
		{"no id", report.KindTrailerDocked, func(e *report.Event) { e.EventID = " " }, "no event id"},
		{"no time", report.KindTrailerDocked, func(e *report.Event) { e.At = time.Time{} }, "no CloudEvents time"},
		{"no site", report.KindTrailerDocked, func(e *report.Event) { e.SiteID = "" }, "no site_id"},
		{"unknown kind", report.Kind("Nope"), func(e *report.Event) {}, "unknown kind"},
		{"visit without id", report.KindVisitCheckedIn, func(e *report.Event) { e.VisitID = "" }, "no visit_id"},
		{"visit without arrival", report.KindVisitCheckedIn, func(e *report.Event) { e.ArrivedAt = time.Time{} }, "no arrived_at"},
		{"arrival without trailer id", report.KindVisitCheckedIn, func(e *report.Event) { e.Arrivals[0].TrailerID = "" }, "no trailer_id"},
		{"arrival with a bad kind", report.KindVisitCheckedIn, func(e *report.Event) { e.Arrivals[0].Kind = "TANKER" }, "not a trailer kind"},
		{"docked without door", report.KindTrailerDocked, func(e *report.Event) { e.DoorCode = "" }, "no door_code"},
		{"docked without instant", report.KindTrailerDocked, func(e *report.Event) { e.DockedAt = time.Time{} }, "no docked_at"},
		{"undocked without trailer", report.KindTrailerUndocked, func(e *report.Event) { e.TrailerID = "" }, "no trailer_id"},
		{"undocked without instant", report.KindTrailerUndocked, func(e *report.Event) { e.UndockedAt = time.Time{} }, "no undocked_at"},
		{"work started without instant", report.KindDoorWorkStarted, func(e *report.Event) { e.StartedAt = time.Time{} }, "no started_at"},
		{"work started without door", report.KindDoorWorkStarted, func(e *report.Event) { e.DoorCode = "" }, "no door_code"},
		{"work completed without instant", report.KindDoorWorkCompleted, func(e *report.Event) { e.CompletedAt = time.Time{} }, "no completed_at"},
		{"departed without instant", report.KindTrailerDeparted, func(e *report.Event) { e.DepartedAt = time.Time{} }, "no departed_at"},
		{"departed with negative dwell", report.KindTrailerDeparted, func(e *report.Event) { e.DwellMinutes = -1 }, "negative"},
		{"move without id", report.KindYardMoveCompleted, func(e *report.Event) { e.MoveID = "" }, "no move_id"},
		{"move without trailer", report.KindYardMoveCompleted, func(e *report.Event) { e.TrailerID = "" }, "no trailer_id"},
		{"move with negative duration", report.KindYardMoveCompleted, func(e *report.Event) { e.DurationSeconds = -5 }, "negative"},
		{"move without instant", report.KindYardMoveCompleted, func(e *report.Event) { e.CompletedAt = time.Time{} }, "no completed_at"},
	}
	for _, c := range cases {
		e := valid(c.kind)
		e.Arrivals = append([]report.Arrival(nil), e.Arrivals...)
		c.mutate(&e)
		err := e.Validate()
		if !errors.Is(err, report.ErrRejected) || !strings.Contains(err.Error(), c.cause) {
			t.Errorf("%s: %v, want ErrRejected containing %q", c.name, err, c.cause)
		}
	}
}

func TestOnlyAHostlerWithAReferenceCountsAsYardTruckProductivity(t *testing.T) {
	e := valid(report.KindYardMoveCompleted)
	if !e.IsHostlerMove() {
		t.Fatal("a hostler move with a reference counts")
	}
	e.HostlerRef = " "
	if e.IsHostlerMove() {
		t.Error("a move with no hostler reference does not count")
	}
	e = valid(report.KindYardMoveCompleted)
	e.Performer = "VISITING_DRIVER"
	if e.IsHostlerMove() {
		t.Error("a visiting driver's move does not count")
	}
	if valid(report.KindTrailerDocked).IsHostlerMove() {
		t.Error("only YardMoveCompleted counts")
	}
}

func TestUtilisationIsTheOccupiedShareOfTheWindowClampedToOne(t *testing.T) {
	cases := []struct {
		occupied, window time.Duration
		want             float64
	}{
		{6 * time.Hour, 24 * time.Hour, 0.25},
		{0, 24 * time.Hour, 0},
		{-time.Hour, 24 * time.Hour, 0},
		{time.Hour, 0, 0},
		{24 * time.Hour, 24 * time.Hour, 1},
		{30 * time.Hour, 24 * time.Hour, 1},
	}
	for _, c := range cases {
		if got := report.Utilisation(c.occupied, c.window); got != c.want {
			t.Errorf("Utilisation(%v, %v) = %v, want %v", c.occupied, c.window, got, c.want)
		}
	}
}

func TestPerBusyHourDividesMovesByTheHoursWorked(t *testing.T) {
	cases := []struct {
		moves, hours int
		want         float64
	}{{12, 4, 3}, {1, 1, 1}, {0, 4, 0}, {5, 0, 0}, {-1, 2, 0}, {5, -1, 0}}
	for _, c := range cases {
		if got := report.PerBusyHour(c.moves, c.hours); got != c.want {
			t.Errorf("PerBusyHour(%d, %d) = %v, want %v", c.moves, c.hours, got, c.want)
		}
	}
}
