//go:build integration

package kafka_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// --- producer-side event builders (dynamic windows, ids and versions) ---

func jsonList(items []string) string {
	raw, _ := json.Marshal(items)
	return string(raw)
}

func bookedEvent(t *testing.T, evID, apptID, door string, start time.Time, asns []string, version int) []byte {
	data := fmt.Sprintf(`{"appointment_id": %q, "site_id": "WH1", "direction": "INBOUND", "door_code": %q, "carrier": "ACME Freight", "carrier_scac": "ACMF", "window_start": %q, "window_end": %q, "asn_numbers": %s, "cpt_id": null, "shipment_refs": [], "expected_seal_number": null, "arrival_tolerance_minutes": 15, "provenance": "native", "version": %d}`,
		apptID, door, rfc(start), rfc(start.Add(time.Hour)), jsonList(asns), version)
	return envelope(t, evID, dockSchedulingSource, typeDSBooked, apptID, "events", data)
}

func rescheduledEvent(t *testing.T, evID, apptID, door, prevDoor string, start time.Time, version int) []byte {
	data := fmt.Sprintf(`{"appointment_id": %q, "site_id": "WH1", "door_code": %q, "previous_door_code": %q, "window_start": %q, "window_end": %q, "previous_window_start": %q, "previous_window_end": %q, "arrival_tolerance_minutes": 15, "version": %d}`,
		apptID, door, prevDoor, rfc(start), rfc(start.Add(time.Hour)), rfc(start), rfc(start.Add(time.Hour)), version)
	return envelope(t, evID, dockSchedulingSource, typeDSRescheduled, apptID, "events", data)
}

func cancelledEvent(t *testing.T, evID, apptID, door string, version int) []byte {
	data := fmt.Sprintf(`{"appointment_id": %q, "site_id": "WH1", "door_code": %q, "reason": "CARRIER_REQUEST", "version": %d}`, apptID, door, version)
	return envelope(t, evID, dockSchedulingSource, typeDSCancelled, apptID, "events", data)
}

func noShowEvent(t *testing.T, evID, apptID, door string, end time.Time, version int) []byte {
	data := fmt.Sprintf(`{"appointment_id": %q, "site_id": "WH1", "door_code": %q, "direction": "INBOUND", "carrier_scac": "ACMF", "window_end": %q, "version": %d}`, apptID, door, rfc(end), version)
	return envelope(t, evID, dockSchedulingSource, typeDSNoShow, apptID, "events", data)
}

func closureScheduledEvent(t *testing.T, evID, closureID, door string, from, to time.Time) []byte {
	data := fmt.Sprintf(`{"site_id": "WH1", "closure_id": %q, "door_code": %q, "from": %q, "to": %q, "reason": "maintenance", "version": 3}`, closureID, door, rfc(from), rfc(to))
	return envelope(t, evID, dockSchedulingSource, typeDSClosureSched, "WH1", "events", data)
}

func closureLiftedEvent(t *testing.T, evID, closureID, door string) []byte {
	data := fmt.Sprintf(`{"site_id": "WH1", "closure_id": %q, "door_code": %q, "version": 4}`, closureID, door)
	return envelope(t, evID, dockSchedulingSource, typeDSClosureLifted, "WH1", "events", data)
}

func receiptOpenedEvent(t *testing.T, evID, receiptID, asn, apptID, door string) []byte {
	data := fmt.Sprintf(`{"receipt_id": %q, "asn_number": %q, "appointment_id": %q, "door_code": %q, "opened_at": %q}`, receiptID, asn, apptID, door, rfc(in(0)))
	return envelope(t, evID, inboundReceivingSrc, typeIRReceiptOpened, receiptID, "events", data)
}

func receiptClosedEvent(t *testing.T, evID, receiptID, asn string) []byte {
	data := fmt.Sprintf(`{"receipt_id": %q, "asn_number": %q, "closed_at": %q, "discrepancies": []}`, receiptID, asn, rfc(in(0)))
	return envelope(t, evID, inboundReceivingSrc, typeIRReceiptClosed, receiptID, "events", data)
}

func asnEvent(t *testing.T, evID, typ, asn string) []byte {
	data := fmt.Sprintf(`{"asn_number": %q}`, asn)
	return envelope(t, evID, inboundReceivingSrc, typ, asn, "events", data)
}

// --- delivery guarantees ---

// A transient failure after the effect rolls the claim back with it: the
// redelivery is processed (not skipped as a duplicate) and the effect lands once.
func TestARolledBackMessageIsRedeliveredAndAppliedOnce(t *testing.T) {
	w := newWorld(t)
	topic, dlq := topics(t, "warehouse.facility.events")
	group := theGroup("flaky")
	w.uow.failures.Store(2)
	produce(t, topic, "k", flZone(t, "ev-1", "WH1-YARD", "WH1"), flSlot(t, "ev-2", "WH1-SPOT-01", "WH1-YARD", "Yard", ""))

	startConsumer(t, w.facilityConsumer(t, topic, group))
	committedPast(t, group, topic, 2)

	spot, ok, err := w.locations.Get(context.Background(), "WH1-SPOT-01")
	if err != nil || !ok || spot.SiteCode != "WH1" {
		t.Fatalf("spot %+v ok=%v err=%v", spot, ok, err)
	}
	if n := w.count(t, "processed_events"); n != 2 {
		t.Fatalf("processed_events = %d, want exactly 2", n)
	}
	if calls := w.uow.calls.Load(); calls != 4 {
		t.Fatalf("units of work = %d, want 4 (the first message rolled back twice, then applied; the second applied)", calls)
	}
	// nothing was dead-lettered: a transient failure is retried
	if end := endOffset(t, dlq); end != 0 {
		t.Fatalf("%d dead letters, want none", end)
	}
}

// A restart under the same group resumes from the committed offset: the
// messages already handled are not read again.
func TestARestartResumesFromTheCommittedOffset(t *testing.T) {
	w := newWorld(t)
	topic, _ := topics(t, "warehouse.facility.events")
	group := theGroup("resume")
	produce(t, topic, "k",
		flZone(t, "ev-1", "WH1-YARD", "WH1"), flZone(t, "ev-2", "WH1-DOCK", "WH1"),
		flSlot(t, "ev-3", "WH1-SPOT-01", "WH1-YARD", "Yard", ""))
	stop := startConsumer(t, w.facilityConsumer(t, topic, group))
	committedPast(t, group, topic, 3)
	stop()
	if calls := w.uow.calls.Load(); calls != 3 {
		t.Fatalf("units of work = %d, want 3", calls)
	}

	produce(t, topic, "k",
		flSlot(t, "ev-4", "WH1-DOCK-IN-01", "WH1-DOCK", "Dock", "Both"),
		flDecommissioned(t, "ev-5", "WH1-SPOT-01"))
	startConsumer(t, w.facilityConsumer(t, topic, group))
	committedPast(t, group, topic, 5)

	if calls := w.uow.calls.Load(); calls != 5 {
		t.Fatalf("units of work = %d, want 5: only the two new messages were handled after the restart", calls)
	}
	if n := w.count(t, "processed_events"); n != 5 {
		t.Fatalf("processed_events = %d, want 5", n)
	}
}
