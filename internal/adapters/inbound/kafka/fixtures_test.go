package kafka_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Fixture events. The dock-scheduling and inbound-receiving ones are the
// producers' own AsyncAPI examples (dock-scheduling origin/feature/service,
// inbound-receiving origin/develop), byte for byte except the envelope being
// folded through envelope(). facility-layout's AsyncAPI carries no example, so
// its fixtures are built from the message schemas (camelCase data, the base
// fields eventName / eventType / occurredAt included as the producer sends
// them).

const (
	dockSchedulingSource  = "/warehouse/dock-scheduling"
	inboundReceivingSrc   = "/warehouse/inbound-receiving"
	facilityLayoutSource  = "/warehouse/facility-layout"
	exampleAppointmentID  = "appt-123e4567-e89b-12d3-a456-426614174000"
	exampleClosureID      = "clo-123e4567-e89b-12d3-a456-426614174001"
	exampleReceiptID      = "rcpt-223e4567-e89b-12d3-a456-426614174000"
	typeDSBooked          = "com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentBooked"
	typeDSRescheduled     = "com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentRescheduled"
	typeDSCancelled       = "com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentCancelled"
	typeDSNoShow          = "com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentNoShow"
	typeDSCheckedIn       = "com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentCheckedIn"
	typeDSClosureSched    = "com.warehouse.wms.dock-scheduling.dockcalendar.DoorClosureScheduled"
	typeDSClosureLifted   = "com.warehouse.wms.dock-scheduling.dockcalendar.DoorClosureLifted"
	typeIRASNRegistered   = "com.warehouse.wms.inbound-receiving.asn.ASNRegistered"
	typeIRASNCancelled    = "com.warehouse.wms.inbound-receiving.asn.ASNCancelled"
	typeIRReceiptOpened   = "com.warehouse.wms.inbound-receiving.receipt.ReceiptOpened"
	typeIRReceiptClosed   = "com.warehouse.wms.inbound-receiving.receipt.ReceiptClosed"
	typeIRLegacyBooked    = "com.warehouse.wms.inbound-receiving.dockappointment.DockAppointmentBooked"
	typeFLZoneRegistered  = "com.warehouse.wms.facility-layout.zone.ZoneRegistered"
	typeFLSlotRegistered  = "com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered"
	typeFLSlotDecommisnd  = "com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned"
	typeFLSiteRegistered  = "com.warehouse.wms.facility-layout.site.SiteRegistered"
	typeFLSlotGeometryUpd = "com.warehouse.wms.facility-layout.locationslot.LocationGeometryUpdated"
)

// envelope builds the structured-mode JSON of one producer occurrence.
func envelope(t testing.TB, id, source, typ, subject, stream, data string) []byte {
	t.Helper()
	name := typ[lastDot(typ)+1:]
	ctx := source[len("/warehouse/"):]
	raw, err := json.Marshal(map[string]any{
		"specversion": "1.0", "id": id, "source": source, "type": typ, "subject": subject,
		"time": "2026-11-05T12:00:00Z", "datacontenttype": "application/json",
		"dataschema": fmt.Sprintf("urn:warehouse:%s:%s:%s:v1", ctx, stream, name),
		"data":       json.RawMessage(data),
	})
	if err != nil {
		t.Fatalf("envelope %s: %v", typ, err)
	}
	return raw
}

func lastDot(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}

// --- dock-scheduling examples (data verbatim from the AsyncAPI) ---

const (
	dsBookedData = `{"appointment_id": "appt-123e4567-e89b-12d3-a456-426614174000", "site_id": "WH1", "direction": "INBOUND", "door_code": "WH1-DOCK-IN-01", "carrier": "ACME Freight", "carrier_scac": "ACMF", "window_start": "2026-11-06T08:00:00Z", "window_end": "2026-11-06T10:00:00Z", "asn_numbers": ["ASN-1001"], "cpt_id": null, "shipment_refs": [], "expected_seal_number": "SEAL-77120", "arrival_tolerance_minutes": 15, "provenance": "native", "version": 1}`

	dsRescheduledData = `{"appointment_id": "appt-123e4567-e89b-12d3-a456-426614174000", "site_id": "WH1", "door_code": "WH1-DOCK-IN-02", "previous_door_code": "WH1-DOCK-IN-01", "window_start": "2026-11-06T09:00:00Z", "window_end": "2026-11-06T11:00:00Z", "previous_window_start": "2026-11-06T08:00:00Z", "previous_window_end": "2026-11-06T10:00:00Z", "arrival_tolerance_minutes": 15, "version": 2}`

	dsCancelledData = `{"appointment_id": "appt-123e4567-e89b-12d3-a456-426614174000", "site_id": "WH1", "door_code": "WH1-DOCK-IN-01", "reason": "CARRIER_REQUEST", "version": 2}`

	dsNoShowData = `{"appointment_id": "appt-123e4567-e89b-12d3-a456-426614174000", "site_id": "WH1", "door_code": "WH1-DOCK-IN-01", "direction": "INBOUND", "carrier_scac": "ACMF", "window_end": "2026-11-06T10:00:00Z", "version": 2}`

	dsClosureScheduledData = `{"site_id": "WH1", "closure_id": "clo-123e4567-e89b-12d3-a456-426614174001", "door_code": "WH1-DOCK-IN-01", "from": "2026-11-10T00:00:00Z", "to": "2026-11-11T00:00:00Z", "reason": "Leveller maintenance", "version": 3}`

	dsClosureLiftedData = `{"site_id": "WH1", "closure_id": "clo-123e4567-e89b-12d3-a456-426614174001", "door_code": "WH1-DOCK-IN-01", "version": 4}`
)

func dsBooked(t testing.TB) []byte {
	return envelope(t, "00001000-1111-4222-8333-444455556666", dockSchedulingSource, typeDSBooked, exampleAppointmentID, "events", dsBookedData)
}

func dsRescheduled(t testing.TB) []byte {
	return envelope(t, "00001002-1111-4222-8333-444455556666", dockSchedulingSource, typeDSRescheduled, exampleAppointmentID, "events", dsRescheduledData)
}

func dsCancelled(t testing.TB) []byte {
	return envelope(t, "00001004-1111-4222-8333-444455556666", dockSchedulingSource, typeDSCancelled, exampleAppointmentID, "events", dsCancelledData)
}

func dsNoShow(t testing.TB) []byte {
	return envelope(t, "00001006-1111-4222-8333-444455556666", dockSchedulingSource, typeDSNoShow, exampleAppointmentID, "events", dsNoShowData)
}

// dsNoShowV is the NoShow example after the Rescheduled example: a later
// aggregate version, on the door the reschedule moved the appointment to (the
// examples are independent snapshots, v2 each; the producer bumps the version
// on every accepted change).
func dsNoShowV(t testing.TB, id string, version int) []byte {
	data := strings.Replace(dsNoShowData, `"version": 2`, fmt.Sprintf(`"version": %d`, version), 1)
	data = strings.Replace(data, `"door_code": "WH1-DOCK-IN-01"`, `"door_code": "WH1-DOCK-IN-02"`, 1)
	return envelope(t, id, dockSchedulingSource, typeDSNoShow, exampleAppointmentID, "events", data)
}

func dsClosureScheduled(t testing.TB) []byte {
	return envelope(t, "0000100c-1111-4222-8333-444455556666", dockSchedulingSource, typeDSClosureSched, "WH1", "events", dsClosureScheduledData)
}

func dsClosureLifted(t testing.TB) []byte {
	return envelope(t, "0000100e-1111-4222-8333-444455556666", dockSchedulingSource, typeDSClosureLifted, "WH1", "events", dsClosureLiftedData)
}

// --- inbound-receiving examples (data verbatim from the AsyncAPI) ---

const (
	irASNRegisteredData = `{"asn_number": "ASN-1001", "supplier_ref": "ACME", "expected_arrival": "2026-10-09T08:00:00Z", "lines": [{"line_no": 1, "sku": "SKU-1", "expected_qty": 40}, {"line_no": 2, "sku": "SKU-2", "expected_qty": 5}]}`

	irASNCancelledData = `{"asn_number": "ASN-1001", "reason": "Supplier cancelled the shipment"}`

	irReceiptOpenedData = `{"receipt_id": "rcpt-223e4567-e89b-12d3-a456-426614174000", "asn_number": "ASN-1001", "appointment_id": "appt-123e4567-e89b-12d3-a456-426614174000", "door_code": "WH1-DOCK-IN-01", "opened_at": "2026-10-09T08:05:00Z"}`

	irReceiptClosedData = `{"receipt_id": "rcpt-223e4567-e89b-12d3-a456-426614174000", "asn_number": "ASN-1001", "closed_at": "2026-10-09T09:30:00Z", "discrepancies": [{"line_no": 1, "sku": "SKU-1", "kind": "Short", "expected_qty": 40, "received_qty": 34, "damaged_qty": 4}, {"line_no": 2, "sku": "SKU-2", "kind": "Over", "expected_qty": 5, "received_qty": 7, "damaged_qty": 0}]}`
)

func irASNRegistered(t testing.TB) []byte {
	return envelope(t, "7c1f3a52-0d4e-4b8a-9e61-2a5d8f0c3b14", inboundReceivingSrc, typeIRASNRegistered, "ASN-1001", "events", irASNRegisteredData)
}

func irASNCancelled(t testing.TB) []byte {
	return envelope(t, "1e9b4d70-3c2a-4f5d-8b17-6a0c9d2e7f31", inboundReceivingSrc, typeIRASNCancelled, "ASN-1001", "events", irASNCancelledData)
}

func irReceiptOpened(t testing.TB) []byte {
	return envelope(t, "d4e6f8a0-3b5c-4d7e-9f1a-2b3c4d5e6f70", inboundReceivingSrc, typeIRReceiptOpened, exampleReceiptID, "events", irReceiptOpenedData)
}

func irReceiptClosed(t testing.TB) []byte {
	return envelope(t, "e5f7a9b1-4c6d-4e8f-a02b-3c4d5e6f7a81", inboundReceivingSrc, typeIRReceiptClosed, exampleReceiptID, "events", irReceiptClosedData)
}

// --- facility-layout fixtures (from the message schemas) ---

func flZoneData(zoneID, siteCode string) string {
	return fmt.Sprintf(`{"eventName": "ZoneRegistered", "eventType": %q, "occurredAt": "2026-11-05T11:00:00Z", "zoneId": %q, "siteCode": %q, "areaCode": "YARD", "zoneCode": "Z1", "temperatureClass": "Ambient", "hazmat": false}`,
		typeFLZoneRegistered, zoneID, siteCode)
}

// flSlotData builds a LocationSlotRegistered payload; role "" omits the field
// (events from before ADR-0016, meaning Storage) and dockFlow "" omits dockFlow.
func flSlotData(code, zoneID, role, dockFlow string) string {
	extra := ""
	if role != "" {
		extra += fmt.Sprintf(`, "role": %q`, role)
	}
	if dockFlow != "" {
		extra += fmt.Sprintf(`, "dockFlow": %q`, dockFlow)
	}
	return fmt.Sprintf(`{"eventName": "LocationSlotRegistered", "eventType": %q, "occurredAt": "2026-11-05T11:00:00Z", "locationCode": %q, "aisleId": "AISLE-1", "zoneId": %q, "locationType": "Slot"%s}`,
		typeFLSlotRegistered, code, zoneID, extra)
}

func flDecommissionedData(code string) string {
	return fmt.Sprintf(`{"eventName": "LocationSlotDecommissioned", "eventType": %q, "occurredAt": "2026-11-05T11:30:00Z", "locationCode": %q}`,
		typeFLSlotDecommisnd, code)
}

func flZone(t testing.TB, id, zoneID, siteCode string) []byte {
	return envelope(t, id, facilityLayoutSource, typeFLZoneRegistered, zoneID, "events", flZoneData(zoneID, siteCode))
}

func flSlot(t testing.TB, id, code, zoneID, role, dockFlow string) []byte {
	return envelope(t, id, facilityLayoutSource, typeFLSlotRegistered, code, "events", flSlotData(code, zoneID, role, dockFlow))
}

func flDecommissioned(t testing.TB, id, code string) []byte {
	return envelope(t, id, facilityLayoutSource, typeFLSlotDecommisnd, code, "events", flDecommissionedData(code))
}
