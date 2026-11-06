package repocontract

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

func seedSite(t *testing.T, s Store) {
	t.Helper()
	must(t, s.LocationFeed.ApplyZone(bg, "z-yard", "WH1"))
	must(t, s.LocationFeed.ApplyZone(bg, "z-dock", "WH1"))
	for _, code := range []string{"WH1-P1", "WH1-P2", "WH1-P3"} {
		must(t, s.LocationFeed.ApplySlot(bg, code, "z-yard", shared.Spot, ""))
	}
	must(t, s.LocationFeed.ApplySlot(bg, "WH1-D1", "z-dock", shared.Door, "Inbound"))
}

// Locations runs the LocationView contract.
func Locations(t *testing.T, newStore NewStore) {
	t.Helper()
	run(t, newStore, map[string]func(t *testing.T, s Store){
		"an unknown location is not found":                           locationsCaseAnUnknownLocationIsNotFound,
		"a slot before its zone has no site until the zone arrives":  locationsCaseASlotBeforeItsZoneHasNoSiteUntilTheZoneArrives,
		"slots list by site, active only, by code":                   locationsCaseSlotsListBySiteActiveOnlyByCode,
		"free spots skip trailers, open-move reservations and doors": locationsCaseFreeSpotsSkipTrailersOpenMoveReservationsAndDoors,
	})
}

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

// Closures runs the DoorClosureView contract.
func Closures(t *testing.T, newStore NewStore) {
	t.Helper()
	run(t, newStore, map[string]func(t *testing.T, s Store){
		"a door with no closure is open":                             closuresCaseADoorWithNoClosureIsOpen,
		"a closure closes the door from its start to before its end": closuresCaseAClosureClosesTheDoorFromItsStartToBeforeItsEnd,
		"a lifted closure no longer closes the door":                 closuresCaseALiftedClosureNoLongerClosesTheDoor,
		"lifting an unseen closure leaves a marker":                  closuresCaseLiftingAnUnseenClosureLeavesAMarker,
	})
}

// Asns runs the AsnContentView contract.
func Asns(t *testing.T, newStore NewStore) {
	t.Helper()
	run(t, newStore, map[string]func(t *testing.T, s Store){
		"every unknown asn counts as active, in order":    asnsCaseEveryUnknownAsnCountsAsActiveInOrder,
		"a cancelled asn is filtered and stays cancelled": asnsCaseACancelledAsnIsFilteredAndStaysCancelled,
	})
}

// Receipts runs the ReceiptAtDoorView contract.
func Receipts(t *testing.T, newStore NewStore) {
	t.Helper()
	run(t, newStore, map[string]func(t *testing.T, s Store){
		"an unknown receipt is not found": receiptsCaseAnUnknownReceiptIsNotFound,
		"open then close":                 receiptsCaseOpenThenClose,
		"closing an unseen receipt leaves a marker the open completes": receiptsCaseClosingAnUnseenReceiptLeavesAMarkerTheOpenCompletes,
		"closed asns of an appointment are sorted":                     receiptsCaseClosedAsnsOfAnAppointmentAreSorted,
	})
}

// ProcessedEvents runs the inbox contract.
func ProcessedEvents(t *testing.T, newStore NewStore) {
	t.Helper()
	run(t, newStore, map[string]func(t *testing.T, s Store){
		"the first claim wins":                     processedEventsCaseTheFirstClaimWins,
		"a claim rolls back with its unit of work": processedEventsCaseAClaimRollsBackWithItsUnitOfWork,
	})
}

// IdempotencyStore runs the Idempotency-Key store contract.
func IdempotencyStore(t *testing.T, newStore NewStore) {
	t.Helper()
	run(t, newStore, map[string]func(t *testing.T, s Store){
		"an unknown key is a miss":                idempotencyStoreCaseAnUnknownKeyIsAMiss,
		"save then lookup round trips the answer": idempotencyStoreCaseSaveThenLookupRoundTripsTheAnswer,
		"the first writer of a key wins":          idempotencyStoreCaseTheFirstWriterOfAKeyWins,
		"an empty body round trips as empty":      idempotencyStoreCaseAnEmptyBodyRoundTripsAsEmpty,
	})
}

// UnitOfWork runs the UnitOfWork + outbox contract: the aggregates a use case
// saves and the events it publishes commit or roll back together.
func UnitOfWork(t *testing.T, newStore NewStore) {
	t.Helper()
	run(t, newStore, map[string]func(t *testing.T, s Store){
		"a committed unit of work saves the aggregate and its events": unitOfWorkCaseACommittedUnitOfWorkSavesTheAggregateAndItsEvents,
		"a failed unit of work saves nothing and publishes nothing":   unitOfWorkCaseAFailedUnitOfWorkSavesNothingAndPublishesNothing,
		"a nested unit of work joins the outer one":                   unitOfWorkCaseANestedUnitOfWorkJoinsTheOuterOne,
		"events are published in order with their payloads' names":    unitOfWorkCaseEventsArePublishedInOrderWithTheirPayloadsNames,
		"the constraint backstops fail the unit of work":              unitOfWorkCaseTheConstraintBackstopsFailTheUnitOfWork,
	})
}

// locationsCaseAnUnknownLocationIsNotFound: an unknown location is not found.
func locationsCaseAnUnknownLocationIsNotFound(t *testing.T, s Store) {
	_, ok, err := s.Locations.Get(bg, "NOPE")
	must(t, err)
	if ok {
		t.Fatal("unknown location found")
	}
	list, err := s.Locations.ListBySite(bg, "WH1")
	must(t, err)
	if len(list) != 0 {
		t.Fatalf("list = %v", list)
	}
}

// locationsCaseASlotBeforeItsZoneHasNoSiteUntilTheZoneArrives: a slot before its zone has no site until the zone arrives.
func locationsCaseASlotBeforeItsZoneHasNoSiteUntilTheZoneArrives(t *testing.T, s Store) {
	must(t, s.LocationFeed.ApplySlot(bg, "WH1-P1", "z-yard", shared.Spot, ""))
	info, ok, err := s.Locations.Get(bg, "WH1-P1")
	must(t, err)
	if !ok || info.SiteCode != "" || !info.Active || info.Kind != shared.Spot || info.ZoneID != "z-yard" {
		t.Fatalf("slot %+v", info)
	}
	list, _ := s.Locations.ListBySite(bg, "WH1")
	if len(list) != 0 {
		t.Fatalf("an unresolved slot belongs to no site: %v", list)
	}
	must(t, s.LocationFeed.ApplyZone(bg, "z-yard", "WH1"))
	info, _, _ = s.Locations.Get(bg, "WH1-P1")
	if info.SiteCode != "WH1" {
		t.Fatalf("the zone resolves the slot: %+v", info)
	}
	must(t, s.LocationFeed.ApplyZone(bg, "z-yard", "WH9")) // the zone moving does not move the slot
	info, _, _ = s.Locations.Get(bg, "WH1-P1")
	if info.SiteCode != "WH1" {
		t.Fatalf("slot %+v", info)
	}
}

// locationsCaseSlotsListBySiteActiveOnlyByCode: slots list by site, active only, by code.
func locationsCaseSlotsListBySiteActiveOnlyByCode(t *testing.T, s Store) {
	seedSite(t, s)
	must(t, s.LocationFeed.ApplySlot(bg, "WH2-P1", "z-other", shared.Spot, ""))
	must(t, s.LocationFeed.Decommission(bg, "WH1-P2"))
	must(t, s.LocationFeed.Decommission(bg, "NOPE")) // unknown: nothing happens
	list, err := s.Locations.ListBySite(bg, "WH1")
	must(t, err)
	codes := []string{}
	for _, l := range list {
		codes = append(codes, l.Code)
	}
	if !reflect.DeepEqual(codes, []string{"WH1-D1", "WH1-P1", "WH1-P3"}) {
		t.Fatalf("list = %v", codes)
	}
	door, _, _ := s.Locations.Get(bg, "WH1-D1")
	if door.Kind != shared.Door || door.DockFlow != "Inbound" {
		t.Fatalf("door %+v", door)
	}
	gone, _, _ := s.Locations.Get(bg, "WH1-P2")
	if gone.Active {
		t.Fatalf("decommissioned %+v", gone)
	}
	must(t, s.LocationFeed.ApplySlot(bg, "WH1-P2", "z-yard", shared.Spot, "")) // registering again reactivates
	again, _, _ := s.Locations.Get(bg, "WH1-P2")
	if !again.Active {
		t.Fatalf("re-registered %+v", again)
	}
}

// locationsCaseFreeSpotsSkipTrailersOpenMoveReservationsAndDoors: free spots skip trailers, open-move reservations and doors.
func locationsCaseFreeSpotsSkipTrailersOpenMoveReservationsAndDoors(t *testing.T, s Store) {
	seedSite(t, s)
	tr := Arrived(t, "trl-1", "WH1", "T1", 0)
	must(t, s.Trailers.Save(bg, tr))
	ArriveAt(t, tr, spot("WH1-P1"), time.Minute)
	must(t, s.Trailers.Save(bg, tr))
	must(t, s.Moves.Save(bg, NewMove(t, "mv-1", "trl-2", spot("WH1-P2"), 5, time.Time{}, 0)))
	free, err := s.Locations.FreeSpots(bg, "WH1", nil, 10)
	must(t, err)
	if !reflect.DeepEqual(free, []string{"WH1-P3"}) {
		t.Fatalf("free = %v", free)
	}
	must(t, tr.StartMoving(T0.Add(2*time.Minute)))
	must(t, s.Trailers.Save(bg, tr))
	free, _ = s.Locations.FreeSpots(bg, "WH1", nil, 1)
	if !reflect.DeepEqual(free, []string{"WH1-P1"}) {
		t.Fatalf("a trailer in a move holds no spot, limit 1: %v", free)
	}
	none, _ := s.Locations.FreeSpots(bg, "WH1", []string{"z-nowhere"}, 10)
	if len(none) != 0 {
		t.Fatalf("zones restrict the spots: %v", none)
	}
	zoned, _ := s.Locations.FreeSpots(bg, "WH1", []string{"z-yard"}, 10)
	if !reflect.DeepEqual(zoned, []string{"WH1-P1", "WH1-P3"}) {
		t.Fatalf("zoned = %v", zoned)
	}
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

// closuresCaseADoorWithNoClosureIsOpen: a door with no closure is open.
func closuresCaseADoorWithNoClosureIsOpen(t *testing.T, s Store) {
	if closedAt(t, s, T0) {
		t.Fatal("closed")
	}
}

// closuresCaseAClosureClosesTheDoorFromItsStartToBeforeItsEnd: a closure closes the door from its start to before its end.
func closuresCaseAClosureClosesTheDoorFromItsStartToBeforeItsEnd(t *testing.T, s Store) {
	must(t, s.ClosureFeed.Schedule(bg, closure))
	if closedAt(t, s, T0.Add(-time.Second)) || !closedAt(t, s, T0) || !closedAt(t, s, T0.Add(59*time.Minute)) || closedAt(t, s, T0.Add(time.Hour)) {
		t.Fatal("closure window")
	}
	other, err := s.Closures.Closed(bg, "WH1-D2", T0.Add(time.Minute))
	must(t, err)
	if other {
		t.Fatal("another door is not closed")
	}
}

// closuresCaseALiftedClosureNoLongerClosesTheDoor: a lifted closure no longer closes the door.
func closuresCaseALiftedClosureNoLongerClosesTheDoor(t *testing.T, s Store) {
	must(t, s.ClosureFeed.Schedule(bg, closure))
	must(t, s.ClosureFeed.Lift(bg, "clo-1"))
	if closedAt(t, s, T0.Add(time.Minute)) {
		t.Fatal("lifted")
	}
	must(t, s.ClosureFeed.Schedule(bg, closure)) // a replay of Scheduled does not reclose it
	if closedAt(t, s, T0.Add(time.Minute)) {
		t.Fatal("a replay reclosed the door")
	}
}

// closuresCaseLiftingAnUnseenClosureLeavesAMarker: lifting an unseen closure leaves a marker.
func closuresCaseLiftingAnUnseenClosureLeavesAMarker(t *testing.T, s Store) {
	must(t, s.ClosureFeed.Lift(bg, "clo-1"))
	must(t, s.ClosureFeed.Schedule(bg, closure))
	if closedAt(t, s, T0.Add(time.Minute)) {
		t.Fatal("the late Scheduled must not close a lifted closure")
	}
}

// asnsCaseEveryUnknownAsnCountsAsActiveInOrder: every unknown asn counts as active, in order.
func asnsCaseEveryUnknownAsnCountsAsActiveInOrder(t *testing.T, s Store) {
	got, err := s.Asns.FilterActive(bg, []string{"ASN-2", "ASN-1"})
	must(t, err)
	if !reflect.DeepEqual(got, []string{"ASN-2", "ASN-1"}) {
		t.Fatalf("got %v", got)
	}
	empty, err := s.Asns.FilterActive(bg, nil)
	must(t, err)
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty = %#v", empty)
	}
}

// asnsCaseACancelledAsnIsFilteredAndStaysCancelled: a cancelled asn is filtered and stays cancelled.
func asnsCaseACancelledAsnIsFilteredAndStaysCancelled(t *testing.T, s Store) {
	must(t, s.AsnFeed.Register(bg, "ASN-1"))
	must(t, s.AsnFeed.Cancel(bg, "ASN-2"))
	must(t, s.AsnFeed.Register(bg, "ASN-2")) // a late Registered does not revive it
	got, err := s.Asns.FilterActive(bg, []string{"ASN-1", "ASN-2", "ASN-3"})
	must(t, err)
	if !reflect.DeepEqual(got, []string{"ASN-1", "ASN-3"}) {
		t.Fatalf("got %v", got)
	}
}

// receiptsCaseAnUnknownReceiptIsNotFound: an unknown receipt is not found.
func receiptsCaseAnUnknownReceiptIsNotFound(t *testing.T, s Store) {
	_, ok, err := s.Receipts.Get(bg, "rcp-404")
	must(t, err)
	if ok {
		t.Fatal("found")
	}
	closed, err := s.Receipts.ClosedASNs(bg, "appt-1")
	must(t, err)
	if closed == nil || len(closed) != 0 {
		t.Fatalf("closed = %#v", closed)
	}
}

// receiptsCaseOpenThenClose: open then close.
func receiptsCaseOpenThenClose(t *testing.T, s Store) {
	must(t, s.ReceiptFeed.Open(bg, opened))
	got, ok, err := s.Receipts.Get(bg, "rcp-1")
	must(t, err)
	if !ok || !reflect.DeepEqual(got, opened) {
		t.Fatalf("got %+v", got)
	}
	must(t, s.ReceiptFeed.Close(bg, "rcp-1", "ASN-1", T0.Add(time.Hour)))
	got, _, _ = s.Receipts.Get(bg, "rcp-1")
	if !got.ClosedAt.Equal(T0.Add(time.Hour)) || got.AppointmentID != "appt-1" {
		t.Fatalf("got %+v", got)
	}
	must(t, s.ReceiptFeed.Open(bg, opened)) // a replay of Opened keeps it closed
	got, _, _ = s.Receipts.Get(bg, "rcp-1")
	if got.ClosedAt.IsZero() {
		t.Fatal("a closed receipt was reopened")
	}
}

// receiptsCaseClosingAnUnseenReceiptLeavesAMarkerTheOpenCompletes: closing an unseen receipt leaves a marker the open completes.
func receiptsCaseClosingAnUnseenReceiptLeavesAMarkerTheOpenCompletes(t *testing.T, s Store) {
	must(t, s.ReceiptFeed.Close(bg, "rcp-1", "ASN-1", T0.Add(time.Hour)))
	must(t, s.ReceiptFeed.Open(bg, opened))
	got, _, _ := s.Receipts.Get(bg, "rcp-1")
	if got.AppointmentID != "appt-1" || got.ClosedAt.IsZero() || got.ASNNumber != "ASN-1" {
		t.Fatalf("got %+v", got)
	}
	must(t, s.ReceiptFeed.Close(bg, "rcp-2", "ASN-2", T0)) // no asn yet: the close names it
	second, _, _ := s.Receipts.Get(bg, "rcp-2")
	if second.ASNNumber != "ASN-2" {
		t.Fatalf("got %+v", second)
	}
}

// receiptsCaseClosedAsnsOfAnAppointmentAreSorted: closed asns of an appointment are sorted.
func receiptsCaseClosedAsnsOfAnAppointmentAreSorted(t *testing.T, s Store) {
	for _, r := range []views.Receipt{
		{ID: "rcp-b", ASNNumber: "ASN-B", AppointmentID: "appt-1"}, {ID: "rcp-a", ASNNumber: "ASN-A", AppointmentID: "appt-1"},
		{ID: "rcp-c", ASNNumber: "ASN-C", AppointmentID: "appt-1"}, {ID: "rcp-x", ASNNumber: "ASN-X", AppointmentID: "appt-2"},
	} {
		must(t, s.ReceiptFeed.Open(bg, r))
	}
	for _, id := range []string{"rcp-b", "rcp-a", "rcp-x"} {
		must(t, s.ReceiptFeed.Close(bg, id, "", T0))
	}
	got, err := s.Receipts.ClosedASNs(bg, "appt-1")
	must(t, err)
	if !reflect.DeepEqual(got, []string{"ASN-A", "ASN-B"}) {
		t.Fatalf("got %v", got)
	}
}

// processedEventsCaseTheFirstClaimWins: the first claim wins.
func processedEventsCaseTheFirstClaimWins(t *testing.T, s Store) {
	first, err := s.Processed.Claim(bg, "ev-1")
	must(t, err)
	again, err := s.Processed.Claim(bg, "ev-1")
	must(t, err)
	other, err := s.Processed.Claim(bg, "ev-2")
	must(t, err)
	if !first || again || !other {
		t.Fatalf("first=%v again=%v other=%v", first, again, other)
	}
}

// processedEventsCaseAClaimRollsBackWithItsUnitOfWork: a claim rolls back with its unit of work.
func processedEventsCaseAClaimRollsBackWithItsUnitOfWork(t *testing.T, s Store) {
	boom := ports.ErrNotFound
	err := s.UoW.Do(bg, func(ctx context.Context) error {
		first, err := s.Processed.Claim(ctx, "ev-1")
		must(t, err)
		if !first {
			t.Fatal("not first")
		}
		return boom
	})
	wantErr(t, err, boom)
	first, err := s.Processed.Claim(bg, "ev-1")
	must(t, err)
	if !first {
		t.Fatal("the rolled-back claim must be undone, so the redelivery is processed")
	}
}

// idempotencyStoreCaseAnUnknownKeyIsAMiss: an unknown key is a miss.
func idempotencyStoreCaseAnUnknownKeyIsAMiss(t *testing.T, s Store) {
	_, ok, err := s.Idempotency.Lookup(bg, "k-404")
	must(t, err)
	if ok {
		t.Fatal("hit")
	}
}

// idempotencyStoreCaseSaveThenLookupRoundTripsTheAnswer: save then lookup round trips the answer.
func idempotencyStoreCaseSaveThenLookupRoundTripsTheAnswer(t *testing.T, s Store) {
	must(t, s.Idempotency.Save(bg, rec))
	got, ok, err := s.Idempotency.Lookup(bg, "k-1")
	must(t, err)
	if !ok || !reflect.DeepEqual(got, rec) {
		t.Fatalf("got %+v", got)
	}
}

// idempotencyStoreCaseTheFirstWriterOfAKeyWins: the first writer of a key wins.
func idempotencyStoreCaseTheFirstWriterOfAKeyWins(t *testing.T, s Store) {
	must(t, s.Idempotency.Save(bg, rec))
	second := rec
	second.Status, second.Body, second.Fingerprint = 409, []byte("late"), "fp-2"
	must(t, s.Idempotency.Save(bg, second))
	got, _, _ := s.Idempotency.Lookup(bg, "k-1")
	if got.Status != 201 || got.Fingerprint != "fp-1" {
		t.Fatalf("got %+v", got)
	}
}

// idempotencyStoreCaseAnEmptyBodyRoundTripsAsEmpty: an empty body round trips as empty.
func idempotencyStoreCaseAnEmptyBodyRoundTripsAsEmpty(t *testing.T, s Store) {
	empty := views.IdempotencyRecord{Key: "k-2", Fingerprint: "fp", Status: 204, CreatedAt: T0}
	must(t, s.Idempotency.Save(bg, empty))
	got, ok, err := s.Idempotency.Lookup(bg, "k-2")
	must(t, err)
	if !ok || len(got.Body) != 0 || got.Status != 204 {
		t.Fatalf("got %+v", got)
	}
}

// unitOfWorkCaseACommittedUnitOfWorkSavesTheAggregateAndItsEvents: a committed unit of work saves the aggregate and its events.
func unitOfWorkCaseACommittedUnitOfWorkSavesTheAggregateAndItsEvents(t *testing.T, s Store) {
	v := NewVisit(t, "visit-1", "WH1", "PLATE1", 0, Ref("trl-1", "T1"))
	events := v.PullEvents()
	must(t, s.UoW.Do(bg, func(ctx context.Context) error {
		if err := s.Visits.Save(ctx, v); err != nil {
			return err
		}
		return s.Events.Publish(ctx, events...)
	}))
	if _, err := s.Visits.FindByID(bg, "visit-1"); err != nil {
		t.Fatal(err)
	}
	if got := s.PendingEvents(t); !reflect.DeepEqual(got, []string{"VisitCheckedIn"}) {
		t.Fatalf("events = %v", got)
	}
}

// unitOfWorkCaseAFailedUnitOfWorkSavesNothingAndPublishesNothing: a failed unit of work saves nothing and publishes nothing.
func unitOfWorkCaseAFailedUnitOfWorkSavesNothingAndPublishesNothing(t *testing.T, s Store) {
	v := NewVisit(t, "visit-1", "WH1", "PLATE1", 0, Ref("trl-1", "T1"))
	events := v.PullEvents()
	boom := ports.ErrNotFound
	err := s.UoW.Do(bg, func(ctx context.Context) error {
		if err := s.Visits.Save(ctx, v); err != nil {
			return err
		}
		if err := s.Events.Publish(ctx, events...); err != nil {
			return err
		}
		return boom
	})
	wantErr(t, err, boom)
	if _, err := s.Visits.FindByID(bg, "visit-1"); err == nil {
		t.Fatal("the rolled-back visit is stored")
	}
	if got := s.PendingEvents(t); len(got) != 0 {
		t.Fatalf("the rolled-back events are published: %v", got)
	}
}

// unitOfWorkCaseANestedUnitOfWorkJoinsTheOuterOne: a nested unit of work joins the outer one.
func unitOfWorkCaseANestedUnitOfWorkJoinsTheOuterOne(t *testing.T, s Store) {
	a := NewVisit(t, "visit-1", "WH1", "PLATE1", 0, Ref("trl-1", "T1"))
	b := NewVisit(t, "visit-2", "WH1", "PLATE2", 0, Ref("trl-2", "T2"))
	boom := ports.ErrNotFound
	err := s.UoW.Do(bg, func(ctx context.Context) error {
		if err := s.UoW.Do(ctx, func(ctx context.Context) error { return s.Visits.Save(ctx, a) }); err != nil {
			return err
		}
		if err := s.Visits.Save(ctx, b); err != nil {
			return err
		}
		return boom
	})
	wantErr(t, err, boom)
	for _, id := range []string{"visit-1", "visit-2"} {
		if _, err := s.Visits.FindByID(bg, id); err == nil {
			t.Fatalf("%s survived the outer rollback", id)
		}
	}
}

// unitOfWorkCaseEventsArePublishedInOrderWithTheirPayloadsNames: events are published in order with their payloads' names.
func unitOfWorkCaseEventsArePublishedInOrderWithTheirPayloadsNames(t *testing.T, s Store) {
	tr := Arrived(t, "trl-1", "WH1", "T1", 0)
	ArriveAt(t, tr, door("WH1-D1"), time.Minute)
	must(t, tr.StartMoving(T0.Add(2*time.Minute)))
	events := tr.PullEvents()
	must(t, s.Events.Publish(bg, events...))
	if got := s.PendingEvents(t); !reflect.DeepEqual(got, []string{"TrailerDocked", "TrailerUndocked"}) {
		t.Fatalf("events = %v", got)
	}
}

// unitOfWorkCaseTheConstraintBackstopsFailTheUnitOfWork: the constraint backstops fail the unit of work.
func unitOfWorkCaseTheConstraintBackstopsFailTheUnitOfWork(t *testing.T, s Store) {
	must(t, s.Visits.Save(bg, NewVisit(t, "visit-1", "WH1", "PLATE1", 0, Ref("trl-1", "T1"))))
	err := s.UoW.Do(bg, func(ctx context.Context) error {
		return s.Visits.Save(ctx, NewVisit(t, "visit-2", "WH1", "PLATE1", 0, Ref("trl-2", "T2")))
	})
	wantErr(t, err, yardvisit.ErrVisitAlreadyOpen)
	occupied := Arrived(t, "trl-1", "WH1", "T1", 0)
	must(t, s.Trailers.Save(bg, occupied))
	ArriveAt(t, occupied, spot("WH1-P1"), time.Minute)
	must(t, s.Trailers.Save(bg, occupied))
	other := Arrived(t, "trl-2", "WH1", "T2", 0)
	must(t, s.Trailers.Save(bg, other))
	ArriveAt(t, other, spot("WH1-P1"), time.Minute)
	wantErr(t, s.UoW.Do(bg, func(ctx context.Context) error { return s.Trailers.Save(ctx, other) }), trailer.ErrLocationOccupied)
}

var (
	closure = views.Closure{ID: "clo-1", SiteID: "WH1", DoorCode: "WH1-D1", From: T0, To: T0.Add(time.Hour)}
	opened  = views.Receipt{ID: "rcp-1", ASNNumber: "ASN-1", AppointmentID: "appt-1", DoorCode: "WH1-D1"}
	rec     = views.IdempotencyRecord{Key: "k-1", Fingerprint: "fp-1", Status: 201, ContentType: "application/json", Body: []byte(`{"ok":true}`), CreatedAt: T0}
)

func closedAt(t *testing.T, s Store, at time.Time) bool {
	t.Helper()
	closed, err := s.Closures.Closed(bg, "WH1-D1", at)
	must(t, err)
	return closed
}
