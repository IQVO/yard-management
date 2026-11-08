//go:build integration

package kafka_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
)

// dockStream is the topic of TestDockSchedulingConsumerEndToEnd, in order. Its
// comments give the expected fate of each message.
func dockStream(t *testing.T, startA, startB time.Time) (msgs [][]byte, poison []byte) {
	t.Helper()
	booked := bookedEvent(t, "ev-b", "appt-B", "WH1-DOCK-IN-01", startB, []string{"ASN-B"}, 1)
	poison = envelope(t, "ev-poison", dockSchedulingSource, typeDSBooked, "appt-P", "events", `{"appointment_id":"appt-P","door_code":"WH1-DOCK-IN-01","version":1}`)
	msgs = [][]byte{
		cancelledEvent(t, "ev-a-cancel", "appt-A", "WH1-DOCK-IN-01", 2),                       // 0: arrives BEFORE its Booked
		bookedEvent(t, "ev-a-book", "appt-A", "WH1-DOCK-IN-01", startA, []string{"ASN-A"}, 1), // 1: older: must not resurrect
		booked, // 2
		booked, // 3: redelivery
		rescheduledEvent(t, "ev-b-r3", "appt-B", "WH1-DOCK-IN-02", "WH1-DOCK-IN-01", startB.Add(2*time.Hour), 3),          // 4
		rescheduledEvent(t, "ev-b-r2", "appt-B", "WH1-DOCK-IN-03", "WH1-DOCK-IN-02", startB.Add(time.Hour), 2),            // 5: older: ignored
		closureLiftedEvent(t, "ev-c-lift", "clo-C", "WH1-DOCK-IN-01"),                                                     // 6: lifted BEFORE scheduled
		closureScheduledEvent(t, "ev-c-sched", "clo-C", "WH1-DOCK-IN-01", in(-time.Hour), in(time.Hour)),                  // 7
		closureScheduledEvent(t, "ev-d-sched", "clo-D", "WH1-DOCK-IN-02", in(-time.Hour), in(time.Hour)),                  // 8
		envelope(t, "ev-ci", dockSchedulingSource, typeDSCheckedIn, "appt-B", "analytics", `{"appointment_id":"appt-B"}`), // 9: analytics only: ignored
		poison, // 10: no window: dead-lettered
	}
	return msgs, poison
}

func assertAppointmentCopies(t *testing.T, w *world, startB time.Time) {
	t.Helper()
	ctx := context.Background()
	a, ok, err := w.appts.Get(ctx, "appt-A")
	if err != nil || !ok || a.Status != views.ApptCancelled || a.Version != 2 {
		t.Fatalf("appt-A %+v ok=%v err=%v: a Booked of a lower version never resurrects a cancelled appointment", a, ok, err)
	}
	b, ok, err := w.appts.Get(ctx, "appt-B")
	if err != nil || !ok || b.Status != views.ApptBooked || b.Version != 3 || b.DoorCode != "WH1-DOCK-IN-02" ||
		!b.WindowStart.Equal(startB.Add(2*time.Hour)) || !reflect.DeepEqual(b.ASNNumbers, []string{"ASN-B"}) || b.SiteID != "WH1" || b.ToleranceMinutes != 15 {
		t.Fatalf("appt-B %+v ok=%v err=%v: the newest reschedule wins, the booking's ASN coverage survives", b, ok, err)
	}
}

func assertClosureCopies(t *testing.T, w *world) {
	t.Helper()
	ctx := context.Background()
	if closed, _ := w.closures.Closed(ctx, "WH1-DOCK-IN-01", time.Now()); closed {
		t.Fatal("a closure lifted before it was scheduled still closes its door")
	}
	if closed, _ := w.closures.Closed(ctx, "WH1-DOCK-IN-02", time.Now()); !closed {
		t.Fatal("a scheduled, unlifted closure closes its door")
	}
}

func TestDockSchedulingConsumerEndToEnd(t *testing.T) {
	w := newWorld(t)
	topic, dlq := topics(t, "warehouse.dock-scheduling.events")
	group := theGroup("dock-scheduling")
	startA, startB := in(24*time.Hour), in(26*time.Hour)
	msgs, poison := dockStream(t, startA, startB)
	produce(t, topic, "k", msgs...)

	startConsumer(t, w.dockConsumer(t, topic, group))
	committedPast(t, group, topic, int64(len(msgs)))

	assertAppointmentCopies(t, w, startB)
	assertClosureCopies(t, w)
	// claims: 0,1,2,4,5,6,7,8 (the redelivery of 2, the analytics type and the poison claim nothing)
	if n := w.count(t, "processed_events"); n != 8 {
		t.Fatalf("processed_events = %d, want 8", n)
	}
	assertDeadLettered(t, dlq, topic, poison, "window")
}

// --- NoShowFreesHolding (Y-13) ---

type holdingCase struct {
	id, door string
	trailer  string
}

// parkWithADoorMove checks a trailer in for the appointment (an early arrival:
// it goes to a holding spot), completes the gate move and requests the hostler
// TO_DOOR move for the appointment's door.
func parkWithADoorMove(t *testing.T, w *world, i int, c *holdingCase) {
	t.Helper()
	ctx := context.Background()
	ci, err := w.yard.CheckInVisit(ctx, usecases.CheckInInput{
		SiteID: "WH1", Carrier: "ACME Freight", CarrierSCAC: "ACMF", TractorPlate: fmt.Sprintf("PLATE-%d", i), DriverRef: "drv",
		Purpose: "DROP", AppointmentID: c.id,
		Trailers: []usecases.TrailerInput{{UnitNumber: fmt.Sprintf("T-%d", i), Kind: "DRY_VAN", LoadState: "LOADED"}},
	})
	if err != nil {
		t.Fatalf("check in %s: %v", c.id, err)
	}
	if _, err := w.yard.CompleteMove(ctx, usecases.CompleteMoveInput{MoveID: ci.Moves[0].MoveID}); err != nil {
		t.Fatalf("complete the gate move of %s: %v", c.id, err)
	}
	mv, err := w.yard.RequestMove(ctx, usecases.RequestMoveInput{TrailerID: ci.Trailers[0].TrailerID, To: shared.Location{Kind: shared.Door, Code: c.door}})
	if err != nil || mv.State != "REQUESTED" || mv.AppointmentID == nil || *mv.AppointmentID != c.id {
		t.Fatalf("request the door move of %s: %+v %v", c.id, mv, err)
	}
	c.trailer = ci.Trailers[0].TrailerID
}

// toDoorMoves counts the TO_DOOR moves of the trailer in the given state.
func toDoorMoves(t *testing.T, w *world, trailerID, state string) int {
	t.Helper()
	page, err := w.yard.ListMoves(context.Background(), views.MoveFilter{TrailerID: trailerID, State: state}, nil, 10)
	if err != nil {
		t.Fatalf("list moves of %s: %v", trailerID, err)
	}
	n := 0
	for _, m := range page.Items {
		if m.Reason == "TO_DOOR" {
			n++
		}
	}
	return n
}

func TestNoShowFreesHoldingThroughKafka(t *testing.T) {
	w := newWorld(t)
	w.seedSite(t)
	topic, _ := topics(t, "warehouse.dock-scheduling.events")
	group := theGroup("dock-noshow")
	start := in(3 * time.Hour) // far enough ahead to be an early arrival: the trailer goes to a holding spot
	cases := []*holdingCase{
		{id: "appt-noshow", door: "WH1-DOCK-IN-01"}, {id: "appt-moved", door: "WH1-DOCK-IN-02"},
		{id: "appt-same", door: "WH1-DOCK-IN-03"}, {id: "appt-cancel", door: "WH1-DOCK-IN-04"},
	}
	for _, c := range cases {
		produce(t, topic, "k", bookedEvent(t, "book-"+c.id, c.id, c.door, start, []string{"ASN-" + c.id}, 1))
	}
	startConsumer(t, w.dockConsumer(t, topic, group))
	committedPast(t, group, topic, int64(len(cases)))
	for i, c := range cases {
		parkWithADoorMove(t, w, i, c)
	}

	produce(t, topic, "k",
		noShowEvent(t, "ev-noshow", "appt-noshow", "WH1-DOCK-IN-01", start.Add(time.Hour), 2),
		rescheduledEvent(t, "ev-moved", "appt-moved", "WH1-DOCK-IN-05", "WH1-DOCK-IN-02", start, 2),
		rescheduledEvent(t, "ev-same", "appt-same", "WH1-DOCK-IN-03", "WH1-DOCK-IN-03", start.Add(time.Hour), 2),
		cancelledEvent(t, "ev-cancel", "appt-cancel", "WH1-DOCK-IN-04", 2),
	)
	committedPast(t, group, topic, int64(2*len(cases)))

	wantCancelled := map[string]int{"appt-noshow": 1, "appt-moved": 1, "appt-cancel": 1, "appt-same": 0}
	for _, c := range cases {
		cancelled, requested := toDoorMoves(t, w, c.trailer, "CANCELLED"), toDoorMoves(t, w, c.trailer, "REQUESTED")
		if cancelled != wantCancelled[c.id] || requested != 1-wantCancelled[c.id] {
			t.Fatalf("%s: TO_DOOR moves cancelled=%d requested=%d, want cancelled=%d", c.id, cancelled, requested, wantCancelled[c.id])
		}
		tr, err := w.yard.GetTrailer(context.Background(), c.trailer)
		if err != nil || tr.State != "PARKED" {
			t.Fatalf("%s: trailer %+v err %v: a trailer already on site stays where it is", c.id, tr, err)
		}
	}
	if a, _, _ := w.appts.Get(context.Background(), "appt-noshow"); a.Status != views.ApptNoShow {
		t.Fatalf("appt-noshow copy %+v", a)
	}
}
