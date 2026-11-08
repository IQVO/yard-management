package repocontract

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/views"
)

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
