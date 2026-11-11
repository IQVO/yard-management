package usecases_test

import (
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/testing/yardtest"
)

func TestFacilityFactsFeedTheLocationCopy(t *testing.T) {
	e := yardtest.New()
	ctx := e.Ctx
	must(t, e.Yard.ApplySlot(ctx, "ev-1", "WH9-S1", "z-yard", "Yard", "", ""))
	must(t, e.Yard.ApplySlot(ctx, "ev-2", "WH9-D1", "z-dock", "Dock", "Inbound", ""))
	must(t, e.Yard.ApplySlot(ctx, "ev-3", "WH9-B1", "z-yard", "Storage", "", ""))
	info, ok, err := e.Store.Locations().Get(ctx, "WH9-S1")
	must(t, err)
	if !ok || info.SiteCode != "" || info.Kind != shared.Spot {
		t.Fatalf("a slot before its zone has no site yet (X5): %+v", info)
	}
	if _, ok, _ := e.Store.Locations().Get(ctx, "WH9-B1"); ok {
		t.Fatal("a Storage slot is not the yard's business")
	}
	must(t, e.Yard.ApplyZone(ctx, "ev-4", "z-yard", "WH9"))
	must(t, e.Yard.ApplyZone(ctx, "ev-5", "z-dock", "WH9"))
	info, _, _ = e.Store.Locations().Get(ctx, "WH9-S1")
	door, _, _ := e.Store.Locations().Get(ctx, "WH9-D1")
	if info.SiteCode != "WH9" || door.SiteCode != "WH9" || door.Kind != shared.Door || door.DockFlow != "Inbound" {
		t.Fatalf("the zone resolves the site: %+v %+v", info, door)
	}
	must(t, e.Yard.DecommissionSlot(ctx, "ev-6", "WH9-S1"))
	if info, _, _ = e.Store.Locations().Get(ctx, "WH9-S1"); info.Active {
		t.Fatalf("decommissioned: %+v", info)
	}
}

func TestAConsumedEventIsAppliedOnce(t *testing.T) {
	e := yardtest.New()
	must(t, e.Yard.ApplyZone(e.Ctx, "ev-1", "z1", "WH1"))
	must(t, e.Yard.ApplyZone(e.Ctx, "ev-1", "z1", "OTHER")) // the same CloudEvents id: skipped
	must(t, e.Yard.ApplySlot(e.Ctx, "ev-2", "WH1-S1", "z1", "Yard", "", ""))
	if info, _, _ := e.Store.Locations().Get(e.Ctx, "WH1-S1"); info.SiteCode != "WH1" {
		t.Fatalf("the duplicate did not overwrite the zone: %+v", info)
	}
}

func TestClosuresFeedTheDoorCopy(t *testing.T) {
	e := yardEnv()
	c := views.Closure{ID: "clo-1", SiteID: "WH1", DoorCode: "WH1-D1", From: yardtest.T0, To: yardtest.T0.Add(time.Hour)}
	must(t, e.Yard.ScheduleClosure(e.Ctx, "ev-1", c))
	if closed, _ := e.Store.Closures().Closed(e.Ctx, "WH1-D1", yardtest.T0); !closed {
		t.Fatal("the door is closed during the closure")
	}
	must(t, e.Yard.LiftClosure(e.Ctx, "ev-2", "clo-1"))
	if closed, _ := e.Store.Closures().Closed(e.Ctx, "WH1-D1", yardtest.T0); closed {
		t.Fatal("a lifted closure no longer closes the door")
	}
}

func TestAsnFactsFilterWhatATrailerCarries(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0, func(a *views.Appointment) { a.ASNNumbers = []string{"ASN-1", "ASN-2"} }))
	must(t, e.Yard.RegisterAsn(e.Ctx, "ev-1", "ASN-1"))
	must(t, e.Yard.CancelAsn(e.Ctx, "ev-2", "ASN-2"))
	ci := checkIn(t, e, withAppointment("appt-1"))
	if got := ci.Trailers[0].ASNNumbers; len(got) != 1 || got[0] != "ASN-1" {
		t.Fatalf("a cancelled ASN is not carried: %v", got)
	}
}

func TestAppointmentFactsKeepTheCopyAndFreeTheHolding(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	ci := checkIn(t, e, withAppointment("appt-1"))
	mv := ci.Moves[0]
	if mv.Reason != "TO_DOOR" {
		t.Fatalf("move %+v", mv)
	}
	// a hostler TO_DOOR move for an appointment is cancelled when the appointment is
	e.Book(appointment("appt-2", "WH1-D2", yardtest.T0.Add(3*time.Hour), func(a *views.Appointment) { a.ASNNumbers = nil; a.Direction = "OUTBOUND" }))
	_, held := arrived(t, e, withAppointment("appt-2"), secondUnit("PLATE2", "T2"))
	next := request(t, e, held.TrailerID, door("WH1-D2"), 0, time.Time{})
	if next.AppointmentID == nil || *next.AppointmentID != "appt-2" {
		t.Fatalf("move %+v", next)
	}
	cancelled := views.Appointment{ID: "appt-2", Version: 2, Status: views.ApptCancelled}
	must(t, e.Yard.ApplyAppointment(e.Ctx, "ev-1", usecases.AppointmentFact{Appointment: cancelled}))
	page, err := e.Yard.ListMoves(e.Ctx, views.MoveFilter{TrailerID: held.TrailerID, State: "CANCELLED"}, nil, 10)
	must(t, err)
	if len(page.Items) != 1 {
		t.Fatalf("NoShowFreesHolding cancels the open TO_DOOR move: %+v", page.Items)
	}
	if got := trailerOf(t, e, held.TrailerID); got.State != "PARKED" {
		t.Fatalf("a trailer already on site stays where it is: %+v", got)
	}
	a, _, _ := e.Store.Appointments().Get(e.Ctx, "appt-2")
	if a.Status != views.ApptCancelled {
		t.Fatalf("appointment %+v", a)
	}
}

func TestARescheduleToAnotherDoorFreesTheClaimedHostlerMove(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0.Add(3*time.Hour)))
	_, held := arrived(t, e, withAppointment("appt-1"))
	request(t, e, held.TrailerID, door("WH1-D1"), 0, time.Time{})
	claim(t, e, "hostler-a")
	moved := views.Appointment{ID: "appt-1", DoorCode: "WH1-D3", Version: 2}
	must(t, e.Yard.ApplyAppointment(e.Ctx, "ev-1", usecases.AppointmentFact{Appointment: moved, PreviousDoorCode: "WH1-D1"}))
	if got := trailerOf(t, e, held.TrailerID); got.State != "PARKED" || *got.Location != spot("WH1-H1") {
		t.Fatalf("the claimed move is cancelled and the trailer returns: %+v", got)
	}
	// a reschedule on the same door frees nothing
	e.Book(appointment("appt-3", "WH1-D2", yardtest.T0.Add(3*time.Hour), func(a *views.Appointment) { a.ASNNumbers = nil; a.Direction = "OUTBOUND" }))
	same := views.Appointment{ID: "appt-3", DoorCode: "WH1-D2", Version: 2}
	must(t, e.Yard.ApplyAppointment(e.Ctx, "ev-2", usecases.AppointmentFact{Appointment: same, PreviousDoorCode: "WH1-D2"}))
}

func TestReceiptClosedCompletesTheUnloadWhenEveryAsnIsClosed(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0, func(a *views.Appointment) { a.ASNNumbers = []string{"ASN-1", "ASN-2"} }))
	_, docked := dockedFor(t, e, "appt-1")
	for i, asn := range []string{"ASN-1", "ASN-2"} {
		id := "rcp-" + asn
		must(t, e.Yard.OpenReceipt(e.Ctx, "evo-"+asn, views.Receipt{ID: id, ASNNumber: asn, AppointmentID: "appt-1", DoorCode: "WH1-D1"}))
		must(t, e.Yard.CloseReceipt(e.Ctx, "evc-"+asn, id, asn, yardtest.T0.Add(time.Minute)))
		got := trailerOf(t, e, docked.TrailerID)
		done := *got.DoorWork == "DONE"
		if done != (i == 1) {
			t.Fatalf("after %s: door work %s", asn, *got.DoorWork)
		}
		if i == 1 && got.LoadState != shared.LoadEmpty {
			t.Fatalf("an unload leaves the trailer EMPTY: %+v", got)
		}
	}
	if !contains(e.Store.EventNames(), "DoorWorkCompleted") {
		t.Fatalf("events %v", e.Store.EventNames())
	}
}

func TestReceiptClosedCompletesInProgressDoorWork(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	_, docked := dockedFor(t, e, "appt-1")
	_, err := e.Yard.StartDoorWork(e.Ctx, docked.TrailerID)
	must(t, err)
	must(t, e.Yard.OpenReceipt(e.Ctx, "ev-o", views.Receipt{ID: "rcp-1", ASNNumber: "ASN-1", AppointmentID: "appt-1", DoorCode: "WH1-D1"}))
	must(t, e.Yard.CloseReceipt(e.Ctx, "ev-c", "rcp-1", "ASN-1", yardtest.T0))
	if got := trailerOf(t, e, docked.TrailerID); *got.DoorWork != "DONE" {
		t.Fatalf("trailer %+v", got)
	}
}

func TestReceiptClosedLeavesAHeldTrailerAlone(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	_, docked := dockedFor(t, e, "appt-1")
	_, err := e.Yard.PlaceHold(e.Ctx, docked.TrailerID, "QUALITY")
	must(t, err)
	must(t, e.Yard.OpenReceipt(e.Ctx, "ev-o", views.Receipt{ID: "rcp-1", ASNNumber: "ASN-1", AppointmentID: "appt-1", DoorCode: "WH1-D1"}))
	must(t, e.Yard.CloseReceipt(e.Ctx, "ev-c", "rcp-1", "ASN-1", yardtest.T0))
	if got := trailerOf(t, e, docked.TrailerID); *got.DoorWork != "WAITING" {
		t.Fatalf("a held trailer's door work waits for the release: %+v", got)
	}
}

func TestReceiptClosedIgnoresWhatItCannotJoin(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	_, docked := dockedFor(t, e, "appt-1")
	// closed before it was opened, and for an unknown appointment: nothing to complete
	must(t, e.Yard.CloseReceipt(e.Ctx, "ev-1", "rcp-9", "ASN-1", yardtest.T0))
	must(t, e.Yard.OpenReceipt(e.Ctx, "ev-2", views.Receipt{ID: "rcp-8", ASNNumber: "ASN-1", AppointmentID: "appt-404", DoorCode: "WH1-D1"}))
	must(t, e.Yard.CloseReceipt(e.Ctx, "ev-3", "rcp-8", "ASN-1", yardtest.T0))
	// a receipt without an appointment, and a trailer that is not at a door
	must(t, e.Yard.OpenReceipt(e.Ctx, "ev-4", views.Receipt{ID: "rcp-7", ASNNumber: "ASN-1", DoorCode: "WH1-D1"}))
	must(t, e.Yard.CloseReceipt(e.Ctx, "ev-5", "rcp-7", "ASN-1", yardtest.T0))
	if got := trailerOf(t, e, docked.TrailerID); *got.DoorWork != "WAITING" {
		t.Fatalf("trailer %+v", got)
	}
	e.Book(appointment("appt-2", "WH1-D3", yardtest.T0.Add(5*time.Hour), func(a *views.Appointment) { a.ASNNumbers = []string{"ASN-2"} }))
	_, early := arrived(t, e, withAppointment("appt-2"), secondUnit("PLATE2", "T2"))
	must(t, e.Yard.OpenReceipt(e.Ctx, "ev-6", views.Receipt{ID: "rcp-6", ASNNumber: "ASN-2", AppointmentID: "appt-2", DoorCode: "WH1-D3"}))
	must(t, e.Yard.CloseReceipt(e.Ctx, "ev-7", "rcp-6", "ASN-2", yardtest.T0))
	if got := trailerOf(t, e, early.TrailerID); got.State != "PARKED" || got.DoorWork != nil {
		t.Fatalf("trailer %+v", got)
	}
}
