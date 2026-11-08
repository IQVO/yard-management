//go:build integration

package main

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/yard-management/internal/testing/kafkatest"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
)

// The producers' topics and CloudEvents types, byte-identical to their
// AsyncAPI (ADR 0003). The fixtures below are published on the REAL topics and
// reach the service only through its Kafka consumers.
const (
	topicDockScheduling   = "warehouse.dock-scheduling.events"
	topicFacility         = "warehouse.facility.events"
	topicInboundReceiving = "warehouse.inbound-receiving.events"

	typeZoneRegistered   = "com.warehouse.wms.facility-layout.zone.ZoneRegistered"
	typeSlotRegistered   = "com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered"
	typeApptBooked       = "com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentBooked"
	typeApptNoShow       = "com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentNoShow"
	typeASNRegistered    = "com.warehouse.wms.inbound-receiving.asn.ASNRegistered"
	typeReceiptOpened    = "com.warehouse.wms.inbound-receiving.receipt.ReceiptOpened"
	typeReceiptClosed    = "com.warehouse.wms.inbound-receiving.receipt.ReceiptClosed"
	sourceFacility       = "/warehouse/facility-layout"
	sourceDockScheduling = "/warehouse/dock-scheduling"
	sourceInbound        = "/warehouse/inbound-receiving"
)

var fixtureSeq atomic.Int64

// publish writes one structured-mode CloudEvent (the producer's envelope) on
// topic under key and waits for the broker's acknowledgement.
func publish(t *testing.T, topic, source, typ, key string, data map[string]any) {
	t.Helper()
	name := typ[lastDot(typ)+1:]
	raw, err := json.Marshal(map[string]any{
		"specversion": "1.0", "id": fmt.Sprintf("itest-fixture-%d", fixtureSeq.Add(1)), "source": source, "type": typ, "subject": key,
		"time": time.Now().UTC().Format(time.RFC3339), "datacontenttype": "application/json",
		"dataschema": fmt.Sprintf("urn:warehouse:%s:events:%s:v1", source[len("/warehouse/"):], name), "data": data,
	})
	if err != nil {
		t.Fatal(err)
	}
	kafkatest.Produce(t, topic, kafkatest.Message{Key: key, Value: raw})
}

func lastDot(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}

func rfc(ts time.Time) string { return ts.UTC().Truncate(time.Second).Format(time.RFC3339) }

// eventually polls cond until it holds or the bound expires.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// waitRows waits until `SELECT count(*) FROM <from>` equals want.
func waitRows(t *testing.T, pool *pgxpool.Pool, from string, want int) {
	t.Helper()
	eventually(t, fmt.Sprintf("%d rows of %s", want, from), func() bool { return pgtest.Count(t, pool, from) == want })
}

// publishSite sends the facility facts of site WH1 the way facility-layout
// does (camelCase data): two zones, eight spots (role Yard) and five doors
// (role Dock) and waits until the locations copy holds all of them.
func publishSite(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, zone := range []string{"WH1-yard", "WH1-dock"} {
		publish(t, topicFacility, sourceFacility, typeZoneRegistered, zone, map[string]any{"zoneId": zone, "siteCode": "WH1", "areaCode": "YARD"})
	}
	slot := func(code, zone, role, flow string) {
		data := map[string]any{"locationCode": code, "zoneId": zone, "role": role, "locationType": "Slot"}
		if flow != "" {
			data["dockFlow"] = flow
		}
		publish(t, topicFacility, sourceFacility, typeSlotRegistered, code, data)
	}
	for i := 1; i <= 8; i++ {
		slot(fmt.Sprintf("WH1-Y%02d", i), "WH1-yard", "Yard", "")
	}
	for code, flow := range map[string]string{"WH1-D01": "Inbound", "WH1-D02": "Both", "WH1-D03": "Both", "WH1-D04": "Inbound", "WH1-D05": "Inbound"} {
		slot(code, "WH1-dock", "Dock", flow)
	}
	waitRows(t, pool, "zones", 2)
	waitRows(t, pool, "locations WHERE site_code = 'WH1' AND active", 13)
}

// bookAppointment sends DockAppointmentBooked (dock-scheduling's payload) and
// waits until the appointment copy has the row.
func bookAppointment(t *testing.T, pool *pgxpool.Pool, id, door string, windowStart time.Time, asns []string) {
	t.Helper()
	publish(t, topicDockScheduling, sourceDockScheduling, typeApptBooked, id, map[string]any{
		"appointment_id": id, "site_id": "WH1", "direction": "INBOUND", "door_code": door, "carrier": "Acme Freight", "carrier_scac": "ACME",
		"window_start": rfc(windowStart), "window_end": rfc(windowStart.Add(time.Hour)), "asn_numbers": asns, "cpt_id": nil, "shipment_refs": []string{},
		"expected_seal_number": nil, "arrival_tolerance_minutes": 15, "provenance": "native", "version": 1,
	})
	waitRows(t, pool, fmt.Sprintf("appointments WHERE appointment_id = '%s' AND status = 'BOOKED'", id), 1)
}
