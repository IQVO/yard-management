package kafka_test

import (
	"reflect"
	"testing"

	"github.com/claudioed/yard-management/internal/adapters/inbound/kafka"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/testing/yardtest"
)

// These tests drive the consumers over the REAL application service (the
// in-memory store) so the adapter is proven against the use cases it will be
// wired to; the Postgres and Kafka proof is in the integration tests.

func realConsumer(t *testing.T, which string, e *yardtest.Env, values ...[]byte) (*kafka.Consumer, *fakeKafka) {
	t.Helper()
	fk := newFakeKafka(values...)
	var c *kafka.Consumer
	var err error
	switch which {
	case "dock-scheduling":
		c, err = kafka.NewDockSchedulingConsumer(fk.factory(), "g", e.Yard, quietLogger())
	case "facility":
		c, err = kafka.NewFacilityConsumer(fk.factory(), "g", e.Yard, quietLogger())
	default:
		c, err = kafka.NewInboundReceivingConsumer(fk.factory(), "g", e.Yard, quietLogger())
	}
	if err != nil {
		t.Fatal(err)
	}
	c.Retry = fastRetry
	return c, fk
}

func appointmentOf(t *testing.T, e *yardtest.Env, id string) views.Appointment {
	t.Helper()
	a, ok, err := e.Store.Appointments().Get(e.Ctx, id)
	if err != nil || !ok {
		t.Fatalf("appointment %s: ok=%v err=%v", id, ok, err)
	}
	return a
}

func TestTheAppointmentCopyFollowsBookedRescheduledNoShow(t *testing.T) {
	e := yardtest.New()
	c, _ := realConsumer(t, "dock-scheduling", e, dsBooked(t), dsRescheduled(t), dsNoShowV(t, "ns-3", 3))
	drain(t, c)
	got := appointmentOf(t, e, exampleAppointmentID)
	want := views.Appointment{
		ID: exampleAppointmentID, SiteID: "WH1", Direction: "INBOUND", DoorCode: "WH1-DOCK-IN-02", CarrierSCAC: "ACMF",
		WindowStart: at("2026-11-06T09:00:00Z"), WindowEnd: at("2026-11-06T11:00:00Z"), ASNNumbers: []string{"ASN-1001"},
		ShipmentRefs: []string{}, ExpectedSeal: "SEAL-77120", ToleranceMinutes: 15, Status: views.ApptNoShow, Version: 3,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("copy\n got %+v\nwant %+v", got, want)
	}
}

// Version guard and reordering: a Booked (v1) that arrives after the
// Cancelled (v2) never resurrects the appointment, and an older reschedule
// never overwrites a newer one.
func TestAnOlderAppointmentFactNeverOverridesANewerOne(t *testing.T) {
	e := yardtest.New()
	c, _ := realConsumer(t, "dock-scheduling", e, dsCancelled(t), dsBooked(t))
	drain(t, c)
	if got := appointmentOf(t, e, exampleAppointmentID); got.Status != views.ApptCancelled || got.Version != 2 {
		t.Fatalf("a late Booked resurrected the appointment: %+v", got)
	}

	e = yardtest.New()
	newer := envelope(t, "r-3", dockSchedulingSource, typeDSRescheduled, exampleAppointmentID, "events",
		`{"appointment_id":"`+exampleAppointmentID+`","site_id":"WH1","door_code":"WH1-DOCK-IN-03","previous_door_code":"WH1-DOCK-IN-02","window_start":"2026-11-06T12:00:00Z","window_end":"2026-11-06T13:00:00Z","arrival_tolerance_minutes":10,"version":3}`)
	c, _ = realConsumer(t, "dock-scheduling", e, dsBooked(t), newer, dsRescheduled(t))
	drain(t, c)
	got := appointmentOf(t, e, exampleAppointmentID)
	if got.Version != 3 || got.DoorCode != "WH1-DOCK-IN-03" || got.WindowStart != at("2026-11-06T12:00:00Z") || got.ToleranceMinutes != 10 {
		t.Fatalf("an older reschedule overwrote the newer one: %+v", got)
	}
}

// Replay and redelivery: the same CloudEvents id twice (a redelivery) and a
// whole second pass over the topic (a replay) change nothing.
func TestReplayAndRedeliveryAreIdempotentOnTheEventId(t *testing.T) {
	e := yardtest.New()
	first := flZone(t, "same-id", "WH1-YARD", "WH1")
	conflicting := flZone(t, "same-id", "WH1-YARD", "OTHER") // the same id must be skipped, not re-applied
	c, _ := realConsumer(t, "facility", e, first, conflicting,
		flSlot(t, "f-2", "WH1-SPOT-01", "WH1-YARD", "Yard", ""),
		flDecommissioned(t, "f-3", "WH1-SPOT-01"))
	drain(t, c)
	replay, _ := realConsumer(t, "facility", e, first, flSlot(t, "f-2", "WH1-SPOT-01", "WH1-YARD", "Yard", ""),
		flDecommissioned(t, "f-3", "WH1-SPOT-01"))
	drain(t, replay)
	info, ok, err := e.Store.Locations().Get(e.Ctx, "WH1-SPOT-01")
	if err != nil || !ok || info.SiteCode != "WH1" || info.Active || info.Kind != shared.Spot {
		t.Fatalf("location %+v ok=%v err=%v: the replayed Registered must not reactivate a decommissioned slot", info, ok, err)
	}
}

// X5: a slot that arrives before its zone is held pending, and the zone
// resolves its site.
func TestASlotBeforeItsZoneIsPendingUntilTheZoneArrives(t *testing.T) {
	e := yardtest.New()
	c, _ := realConsumer(t, "facility", e,
		flSlot(t, "f-1", "WH1-DOCK-IN-01", "WH1-DOCK", "Dock", "Inbound"),
		flSlot(t, "f-2", "WH1-SPOT-01", "WH1-YARD", "Yard", ""))
	drain(t, c)
	door, ok, _ := e.Store.Locations().Get(e.Ctx, "WH1-DOCK-IN-01")
	if !ok || door.SiteCode != "" || door.Kind != shared.Door || door.DockFlow != "Inbound" {
		t.Fatalf("door before its zone: %+v ok=%v", door, ok)
	}
	c, _ = realConsumer(t, "facility", e, flZone(t, "f-3", "WH1-DOCK", "WH1"), flZone(t, "f-4", "WH1-YARD", "WH1"))
	drain(t, c)
	door, _, _ = e.Store.Locations().Get(e.Ctx, "WH1-DOCK-IN-01")
	spot, _, _ := e.Store.Locations().Get(e.Ctx, "WH1-SPOT-01")
	if door.SiteCode != "WH1" || spot.SiteCode != "WH1" {
		t.Fatalf("the zones resolve the sites: %+v %+v", door, spot)
	}
}

func TestClosuresFollowScheduledThenLiftedInAnyOrder(t *testing.T) {
	for name, order := range map[string][][]byte{
		"scheduled then lifted": {dsClosureScheduled(t), dsClosureLifted(t)},
		"lifted then scheduled": {dsClosureLifted(t), dsClosureScheduled(t)},
	} {
		t.Run(name, func(t *testing.T) {
			e := yardtest.New()
			c, _ := realConsumer(t, "dock-scheduling", e, order...)
			drain(t, c)
			closed, err := e.Store.Closures().Closed(e.Ctx, "WH1-DOCK-IN-01", at("2026-11-10T12:00:00Z"))
			if err != nil || closed {
				t.Fatalf("closed=%v err=%v: a lifted closure leaves the door open whatever the order", closed, err)
			}
		})
	}
	e := yardtest.New()
	c, _ := realConsumer(t, "dock-scheduling", e, dsClosureScheduled(t))
	drain(t, c)
	if closed, _ := e.Store.Closures().Closed(e.Ctx, "WH1-DOCK-IN-01", at("2026-11-10T12:00:00Z")); !closed {
		t.Fatal("a scheduled, unlifted closure closes the door within its span")
	}
}

func TestAsnAndReceiptCopiesFollowTheInboundReceivingExamples(t *testing.T) {
	e := yardtest.New()
	c, _ := realConsumer(t, "inbound-receiving", e, irASNCancelled(t), irASNRegistered(t), irReceiptClosed(t), irReceiptOpened(t))
	drain(t, c)
	active, err := e.Store.Asns().FilterActive(e.Ctx, []string{"ASN-1001", "ASN-2002"})
	if err != nil || !reflect.DeepEqual(active, []string{"ASN-2002"}) {
		t.Fatalf("active %v err %v: a cancelled ASN stays cancelled whichever event came first", active, err)
	}
	r, ok, err := e.Store.Receipts().Get(e.Ctx, exampleReceiptID)
	if err != nil || !ok || r.AppointmentID != exampleAppointmentID || r.DoorCode != "WH1-DOCK-IN-01" || r.ClosedAt.IsZero() {
		t.Fatalf("receipt %+v ok=%v err=%v: a close before the open stays closed and the open fills the join", r, ok, err)
	}
	closedASNs, _ := e.Store.Receipts().ClosedASNs(e.Ctx, exampleAppointmentID)
	if !reflect.DeepEqual(closedASNs, []string{"ASN-1001"}) {
		t.Fatalf("closed ASNs of the appointment %v", closedASNs)
	}
}
