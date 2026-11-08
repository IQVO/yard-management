package repocontract

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/views"
)

func appt(id string, version int64, mod ...func(*views.Appointment)) views.Appointment {
	a := views.Appointment{
		ID: id, SiteID: "WH1", Direction: "INBOUND", DoorCode: "WH1-D1", CarrierSCAC: "ACME", WindowStart: T0, WindowEnd: T0.Add(time.Hour),
		ASNNumbers: []string{"ASN-1"}, ShipmentRefs: []string{}, ExpectedSeal: "SL-1", ToleranceMinutes: 15, Version: version,
	}
	for _, m := range mod {
		m(&a)
	}
	return a
}

func apptStatus(t *testing.T, s Store, id string) views.Appointment {
	t.Helper()
	got, ok, err := s.Appointments.Get(bg, id)
	must(t, err)
	if !ok {
		t.Fatalf("appointment %s unknown", id)
	}
	return got
}

// Appointments runs the AppointmentView contract.
func Appointments(t *testing.T, newStore NewStore) {
	t.Helper()
	run(t, newStore, map[string]func(t *testing.T, s Store){
		"an unknown appointment is not found":                             appointmentsCaseAnUnknownAppointmentIsNotFound,
		"an unseen appointment is inserted BOOKED from the event":         appointmentsCaseAnUnseenAppointmentIsInsertedBookedFromTheEvent,
		"only a newer version takes effect":                               appointmentsCaseOnlyANewerVersionTakesEffect,
		"a reschedule overlays only the fields it carries":                appointmentsCaseARescheduleOverlaysOnlyTheFieldsItCarries,
		"local statuses survive a reschedule and terminal ones are final": appointmentsCaseLocalStatusesSurviveARescheduleAndTerminalOnesAreFinal,
		"a cancellation for an unseen appointment creates the row":        appointmentsCaseACancellationForAnUnseenAppointmentCreatesTheRow,
		"list by door is by window start, filtered by status":             appointmentsCaseListByDoorIsByWindowStartFilteredByStatus,
	})
}

func appointmentEqual(a, b views.Appointment) bool {
	return a.ID == b.ID && a.SiteID == b.SiteID && a.Direction == b.Direction && a.DoorCode == b.DoorCode && a.CarrierSCAC == b.CarrierSCAC &&
		a.WindowStart.Equal(b.WindowStart) && a.WindowEnd.Equal(b.WindowEnd) && len(a.ASNNumbers) == len(b.ASNNumbers) &&
		len(a.ShipmentRefs) == len(b.ShipmentRefs) && a.ExpectedSeal == b.ExpectedSeal && a.ToleranceMinutes == b.ToleranceMinutes &&
		a.Status == b.Status && a.Version == b.Version
}

// appointmentsCaseAnUnknownAppointmentIsNotFound: an unknown appointment is not found.
func appointmentsCaseAnUnknownAppointmentIsNotFound(t *testing.T, s Store) {
	_, ok, err := s.Appointments.Get(bg, "appt-404")
	must(t, err)
	if ok {
		t.Fatal("found")
	}
}

// appointmentsCaseAnUnseenAppointmentIsInsertedBookedFromTheEvent: an unseen appointment is inserted BOOKED from the event.
func appointmentsCaseAnUnseenAppointmentIsInsertedBookedFromTheEvent(t *testing.T, s Store) {
	in := appt("appt-1", 1)
	must(t, s.ApptFeed.Apply(bg, in))
	got := apptStatus(t, s, "appt-1")
	in.Status = views.ApptBooked
	if !appointmentEqual(got, in) {
		t.Fatalf("got %+v want %+v", got, in)
	}
}

// appointmentsCaseOnlyANewerVersionTakesEffect: only a newer version takes effect.
func appointmentsCaseOnlyANewerVersionTakesEffect(t *testing.T, s Store) {
	must(t, s.ApptFeed.Apply(bg, appt("appt-1", 3)))
	must(t, s.ApptFeed.Apply(bg, appt("appt-1", 3, func(a *views.Appointment) { a.DoorCode = "WH1-D2" })))
	must(t, s.ApptFeed.Apply(bg, appt("appt-1", 2, func(a *views.Appointment) { a.DoorCode = "WH1-D2" })))
	if got := apptStatus(t, s, "appt-1"); got.DoorCode != "WH1-D1" || got.Version != 3 {
		t.Fatalf("a replay must not overwrite: %+v", got)
	}
	must(t, s.ApptFeed.Apply(bg, appt("appt-1", 4, func(a *views.Appointment) { a.DoorCode = "WH1-D2" })))
	if got := apptStatus(t, s, "appt-1"); got.DoorCode != "WH1-D2" || got.Version != 4 {
		t.Fatalf("a newer fact wins: %+v", got)
	}
}

// appointmentsCaseARescheduleOverlaysOnlyTheFieldsItCarries: a reschedule overlays only the fields it carries.
func appointmentsCaseARescheduleOverlaysOnlyTheFieldsItCarries(t *testing.T, s Store) {
	must(t, s.ApptFeed.Apply(bg, appt("appt-1", 1)))
	resched := views.Appointment{ID: "appt-1", Version: 2, DoorCode: "WH1-D3", WindowStart: T0.Add(time.Hour), WindowEnd: T0.Add(2 * time.Hour)}
	must(t, s.ApptFeed.Apply(bg, resched))
	got := apptStatus(t, s, "appt-1")
	if got.DoorCode != "WH1-D3" || !got.WindowStart.Equal(T0.Add(time.Hour)) || !got.WindowEnd.Equal(T0.Add(2*time.Hour)) ||
		got.ExpectedSeal != "SL-1" || got.ToleranceMinutes != 15 || !reflect.DeepEqual(got.ASNNumbers, []string{"ASN-1"}) || got.Status != views.ApptBooked {
		t.Fatalf("got %+v", got)
	}
}

// appointmentsCaseLocalStatusesSurviveARescheduleAndTerminalOnesAreFinal: local statuses survive a reschedule and terminal ones are final.
func appointmentsCaseLocalStatusesSurviveARescheduleAndTerminalOnesAreFinal(t *testing.T, s Store) {
	must(t, s.ApptFeed.Apply(bg, appt("appt-1", 1)))
	ok, err := s.ApptFeed.MarkStatus(bg, "appt-1", views.ApptCheckedIn)
	must(t, err)
	if !ok {
		t.Fatal("mark refused")
	}
	must(t, s.ApptFeed.Apply(bg, views.Appointment{ID: "appt-1", Version: 2, DoorCode: "WH1-D2"}))
	if got := apptStatus(t, s, "appt-1"); got.Status != views.ApptCheckedIn || got.DoorCode != "WH1-D2" {
		t.Fatalf("got %+v", got)
	}
	must(t, s.ApptFeed.Apply(bg, views.Appointment{ID: "appt-1", Version: 3, Status: views.ApptCancelled}))
	must(t, s.ApptFeed.Apply(bg, views.Appointment{ID: "appt-1", Version: 4, Status: views.ApptBooked, DoorCode: "WH1-D3"}))
	got := apptStatus(t, s, "appt-1")
	if got.Status != views.ApptCancelled || got.DoorCode != "WH1-D3" {
		t.Fatalf("a cancelled appointment is never BOOKED again: %+v", got)
	}
	ok, err = s.ApptFeed.MarkStatus(bg, "appt-1", views.ApptUsed)
	must(t, err)
	if ok {
		t.Fatal("a terminal appointment cannot be marked")
	}
	ok, err = s.ApptFeed.MarkStatus(bg, "appt-404", views.ApptUsed)
	must(t, err)
	if ok {
		t.Fatal("an unknown appointment cannot be marked")
	}
	must(t, s.ApptFeed.Apply(bg, views.Appointment{ID: "appt-2", Version: 1, Status: views.ApptNoShow}))
	must(t, s.ApptFeed.Apply(bg, views.Appointment{ID: "appt-2", Version: 2, Status: views.ApptCheckedIn}))
	if got := apptStatus(t, s, "appt-2"); got.Status != views.ApptNoShow {
		t.Fatalf("NO_SHOW is terminal: %+v", got)
	}
}

// appointmentsCaseACancellationForAnUnseenAppointmentCreatesTheRow: a cancellation for an unseen appointment creates the row.
func appointmentsCaseACancellationForAnUnseenAppointmentCreatesTheRow(t *testing.T, s Store) {
	must(t, s.ApptFeed.Apply(bg, views.Appointment{ID: "appt-1", Version: 2, Status: views.ApptCancelled, DoorCode: "WH1-D1"}))
	must(t, s.ApptFeed.Apply(bg, appt("appt-1", 1))) // the older Booked arrives late
	if got := apptStatus(t, s, "appt-1"); got.Status != views.ApptCancelled || !got.WindowStart.IsZero() {
		t.Fatalf("got %+v", got)
	}
}

// appointmentsCaseListByDoorIsByWindowStartFilteredByStatus: list by door is by window start, filtered by status.
func appointmentsCaseListByDoorIsByWindowStartFilteredByStatus(t *testing.T, s Store) {
	must(t, s.ApptFeed.Apply(bg, appt("appt-b", 1, func(a *views.Appointment) { a.WindowStart = T0.Add(2 * time.Hour) })))
	must(t, s.ApptFeed.Apply(bg, appt("appt-a", 1, func(a *views.Appointment) { a.WindowStart = T0.Add(time.Hour) })))
	must(t, s.ApptFeed.Apply(bg, appt("appt-c", 1)))
	must(t, s.ApptFeed.Apply(bg, appt("appt-d", 1, func(a *views.Appointment) { a.DoorCode = "WH1-D2" })))
	_, err := s.ApptFeed.MarkStatus(bg, "appt-c", views.ApptCheckedIn)
	must(t, err)
	ids := func(as []views.Appointment, err error) []string {
		must(t, err)
		out := []string{}
		for _, a := range as {
			out = append(out, a.ID)
		}
		return out
	}
	if got := ids(s.Appointments.ListByDoor(bg, "WH1-D1", views.ApptBooked, views.ApptCheckedIn)); !reflect.DeepEqual(got, []string{"appt-c", "appt-a", "appt-b"}) {
		t.Fatalf("list = %v", got)
	}
	if got := ids(s.Appointments.ListByDoor(bg, "WH1-D1", views.ApptCheckedIn)); !reflect.DeepEqual(got, []string{"appt-c"}) {
		t.Fatalf("list = %v", got)
	}
	if got := ids(s.Appointments.ListByDoor(bg, "WH1-D1")); len(got) != 0 {
		t.Fatalf("no status, no rows: %v", got)
	}
}
