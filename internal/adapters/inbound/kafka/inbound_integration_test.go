//go:build integration

package kafka_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
)

const unloadDoor = "WH1-DOCK-IN-01"

// dockedTrailerFor books appt-W (two ASNs, a window that is open now) through
// the dock-scheduling consumer, checks a trailer in for it and docks it. It
// returns the trailer id.
func dockedTrailerFor(t *testing.T, w *world) string {
	t.Helper()
	ctx := context.Background()
	topic, _ := topics(t, "warehouse.dock-scheduling.events")
	group := theGroup("dock")
	produce(t, topic, "k", bookedEvent(t, "book-w", "appt-W", unloadDoor, in(-5*time.Minute), []string{"ASN-A", "ASN-B"}, 1))
	startConsumer(t, w.dockConsumer(t, topic, group))
	committedPast(t, group, topic, 1)

	ci, err := w.yard.CheckInVisit(ctx, usecases.CheckInInput{
		SiteID: "WH1", Carrier: "ACME Freight", CarrierSCAC: "ACMF", TractorPlate: "PLATE-W", DriverRef: "drv",
		Purpose: "LIVE_UNLOAD", AppointmentID: "appt-W",
		Trailers: []usecases.TrailerInput{{UnitNumber: "T-W", Kind: "DRY_VAN", LoadState: "LOADED"}},
	})
	if err != nil || ci.Moves[0].Reason != "TO_DOOR" {
		t.Fatalf("check in: %+v %v", ci.Moves, err)
	}
	if _, err := w.yard.CompleteMove(ctx, usecases.CompleteMoveInput{MoveID: ci.Moves[0].MoveID}); err != nil {
		t.Fatalf("dock the trailer: %v", err)
	}
	trailerID := ci.Trailers[0].TrailerID
	docked, err := w.yard.GetTrailer(ctx, trailerID)
	if err != nil || docked.State != "DOCKED" || docked.DoorWork == nil || *docked.DoorWork != "WAITING" || !reflect.DeepEqual(docked.ASNNumbers, []string{"ASN-A", "ASN-B"}) {
		t.Fatalf("docked trailer %+v err %v", docked, err)
	}
	return trailerID
}

// doorWorkOf is the door-work state and load state of the trailer.
func doorWorkOf(t *testing.T, w *world, trailerID string) (string, shared.LoadState) {
	t.Helper()
	tr, err := w.yard.GetTrailer(context.Background(), trailerID)
	if err != nil || tr.DoorWork == nil {
		t.Fatalf("trailer %+v err %v", tr, err)
	}
	return *tr.DoorWork, tr.LoadState
}

func assertDoorWorkWaits(t *testing.T, w *world, trailerID, why string) {
	t.Helper()
	if dw, _ := doorWorkOf(t, w, trailerID); dw != "WAITING" {
		t.Fatalf("door work %s: %s", dw, why)
	}
}

// AutoCompleteUnload (Y-7): ReceiptClosed joins to the trailer through the
// receipt's appointment, and door work completes only when EVERY ASN on the
// trailer has a closed receipt.
func TestAutoCompleteUnloadWaitsForEveryAsnOfTheTrailer(t *testing.T) {
	w := newWorld(t)
	w.seedSite(t)
	trailerID := dockedTrailerFor(t, w)
	irTopic, _ := topics(t, "warehouse.inbound-receiving.events")
	irGroup := theGroup("inbound")

	produce(t, irTopic, "k",
		asnEvent(t, "ir-asn-a", typeIRASNRegistered, "ASN-A"),
		asnEvent(t, "ir-asn-b", typeIRASNRegistered, "ASN-B"),
		receiptOpenedEvent(t, "ir-open-a", "rcpt-A", "ASN-A", "appt-W", unloadDoor),
		receiptOpenedEvent(t, "ir-open-b", "rcpt-B", "ASN-B", "appt-W", unloadDoor),
		envelope(t, "ir-legacy", inboundReceivingSrc, typeIRLegacyBooked, "appt-W", "events", dsBookedData), // legacy importer type: ignored
		receiptClosedEvent(t, "ir-close-a", "rcpt-A", "ASN-A"),
	)
	startConsumer(t, w.inboundConsumer(t, irTopic, irGroup))
	committedPast(t, irGroup, irTopic, 6)
	assertDoorWorkWaits(t, w, trailerID, "one of two receipts closed: it must wait for every ASN on the trailer")

	produce(t, irTopic, "k",
		receiptClosedEvent(t, "ir-close-a", "rcpt-A", "ASN-A"),         // redelivery of the same event
		receiptClosedEvent(t, "ir-close-ghost", "rcpt-GHOST", "ASN-X"), // a receipt that was never opened here: nothing to join
	)
	committedPast(t, irGroup, irTopic, 8)
	assertDoorWorkWaits(t, w, trailerID, "a redelivery and an unjoinable receipt complete nothing")

	produce(t, irTopic, "k", receiptClosedEvent(t, "ir-close-b", "rcpt-B", "ASN-B"))
	committedPast(t, irGroup, irTopic, 9)
	if dw, load := doorWorkOf(t, w, trailerID); dw != "DONE" || load != shared.LoadEmpty {
		t.Fatalf("door work %s load %s: every ASN has a closed receipt, the unload is complete and the trailer EMPTY", dw, load)
	}
	var completed int
	pgtest.Scan(t, w.pool, `SELECT count(*) FROM outbox WHERE payload->>'type' = 'DoorWorkCompleted'`, nil, &completed)
	if completed != 1 {
		t.Fatalf("DoorWorkCompleted outbox rows = %d, want exactly 1", completed)
	}
	if n := w.count(t, "receipts"); n != 3 {
		t.Fatalf("receipts = %d, want 3 (A, B and the closed marker of the receipt never opened here)", n)
	}
}

func TestAsnCancellationFiltersTheTrailerContentsAndSurvivesReordering(t *testing.T) {
	w := newWorld(t)
	topic, _ := topics(t, "warehouse.inbound-receiving.events")
	group := theGroup("asn")
	produce(t, topic, "k",
		asnEvent(t, "e-1", typeIRASNCancelled, "ASN-2"),  // cancelled BEFORE registered
		asnEvent(t, "e-2", typeIRASNRegistered, "ASN-2"), // must stay cancelled
		asnEvent(t, "e-3", typeIRASNRegistered, "ASN-1"),
	)
	startConsumer(t, w.inboundConsumer(t, topic, group))
	committedPast(t, group, topic, 3)
	active, err := w.asns.FilterActive(context.Background(), []string{"ASN-1", "ASN-2"})
	if err != nil || !reflect.DeepEqual(active, []string{"ASN-1"}) {
		t.Fatalf("active %v err %v", active, err)
	}
}
