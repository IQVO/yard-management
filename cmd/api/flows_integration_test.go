//go:build integration

package main

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// trailerView reads GET /trailers/{id}.
func (a *api) trailerView(id string) map[string]any {
	a.t.Helper()
	r := a.call(http.MethodGet, "/trailers/"+id, nil, "")
	if r.Code != http.StatusOK {
		a.t.Fatalf("GET /trailers/%s = %d %s", id, r.Code, r.Body)
	}
	return r.JSON()
}

// movesOf lists GET /moves for a trailer in one state.
func (a *api) movesOf(trailerID, state string) []any {
	a.t.Helper()
	r := a.call(http.MethodGet, "/moves?trailerId="+trailerID+"&state="+state, nil, "")
	if r.Code != http.StatusOK {
		a.t.Fatalf("GET /moves = %d %s", r.Code, r.Body)
	}
	return r.JSON()["items"].([]any)
}

func location(m map[string]any, field string) string {
	loc, _ := m[field].(map[string]any)
	return fmt.Sprintf("%v:%v", loc["kind"], loc["code"])
}

// appointmentToDoorAndReceipts: an on-time trailer of a booked inbound
// appointment (two ASNs) is sent to the appointment's door at check-in, docks
// there, and its door work is completed by ReceiptClosed only once BOTH ASNs
// have a closed receipt. Everything inbound arrives through Kafka.
func appointmentToDoorAndReceipts(t *testing.T, a *api, dsn string, pool *pgxpool.Pool) {
	t.Helper()
	const appt, door = "appt-itest-door", "WH1-D04"
	for _, asn := range []string{"ASN-A1", "ASN-A2"} {
		publish(t, topicInboundReceiving, sourceInbound, typeASNRegistered, asn, map[string]any{
			"asn_number": asn, "supplier_ref": "ACME", "expected_arrival": rfc(time.Now()), "lines": []map[string]any{{"line_no": 1, "sku": "SKU-1", "expected_qty": 10}},
		})
	}
	waitRows(t, pool, "asns", 2)
	bookAppointment(t, pool, appt, door, time.Now().Add(-5*time.Minute), []string{"ASN-A1", "ASN-A2"})

	in := a.mustPost("/visits", map[string]any{
		"siteId": "WH1", "carrier": "Acme Freight", "carrierScac": "ACME", "tractorPlate": "APPT-1", "purpose": "LIVE_UNLOAD", "appointmentId": appt,
		"trailers": []map[string]any{{"unitNumber": "TR-A1", "kind": "DRY_VAN", "loadState": "LOADED"}},
	}, http.StatusCreated)
	move := in["moves"].([]any)[0].(map[string]any)
	if move["reason"] != "TO_DOOR" || move["performer"] != "VISITING_DRIVER" || location(move, "to") != "DOOR:"+door {
		t.Fatalf("check-in must direct the trailer to its door %s: %v", door, move)
	}
	trailerID := in["trailers"].([]any)[0].(map[string]any)["trailerId"].(string)
	// X4: the service marks its own copy of the appointment as used.
	waitRows(t, pool, "appointments WHERE appointment_id = '"+appt+"' AND status = 'CHECKED_IN'", 1)

	a.mustPost("/moves/"+move["moveId"].(string)+"/completions", nil, http.StatusOK)
	docked := a.trailerView(trailerID)
	if docked["state"] != "DOCKED" || location(docked, "location") != "DOOR:"+door || docked["doorWork"] != "WAITING" {
		t.Fatalf("the trailer must be docked at its door, waiting: %v", docked)
	}

	for i, asn := range []string{"ASN-A1", "ASN-A2"} {
		publish(t, topicInboundReceiving, sourceInbound, typeReceiptOpened, fmt.Sprintf("rcpt-itest-%d", i+1), map[string]any{
			"receipt_id": fmt.Sprintf("rcpt-itest-%d", i+1), "asn_number": asn, "appointment_id": appt, "door_code": door, "opened_at": rfc(time.Now()),
		})
	}
	waitRows(t, pool, "receipts WHERE appointment_id = '"+appt+"'", 2)

	closeReceipt := func(n int, asn string) {
		publish(t, topicInboundReceiving, sourceInbound, typeReceiptClosed, fmt.Sprintf("rcpt-itest-%d", n), map[string]any{
			"receipt_id": fmt.Sprintf("rcpt-itest-%d", n), "asn_number": asn, "closed_at": rfc(time.Now()), "discrepancies": []any{},
		})
		waitRows(t, pool, fmt.Sprintf("receipts WHERE receipt_id = 'rcpt-itest-%d' AND closed_at IS NOT NULL", n), 1)
	}
	closeReceipt(1, "ASN-A1")
	// The receipt row and the policy commit together, so the row being closed
	// means AutoCompleteUnload already ran and found ASN-A2 still open.
	if tr := a.trailerView(trailerID); tr["doorWork"] != "WAITING" || tr["loadState"] != "LOADED" {
		t.Fatalf("one of two receipts closed must NOT complete door work: %v", tr)
	}
	closeReceipt(2, "ASN-A2")
	if tr := a.trailerView(trailerID); tr["state"] != "DOCKED" || tr["doorWork"] != "DONE" || tr["loadState"] != "EMPTY" {
		t.Fatalf("every ASN closed must complete door work and leave the trailer empty: %v", tr)
	}

	// The live visit departs from the door: the dwell, the docking and the door
	// work the reports are built from end here.
	visitID := in["visit"].(map[string]any)["visitId"].(string)
	// The door-utilisation report measures docked_at -> undocked_at, and both
	// are stamped by RFC 3339 SECONDS (shared.Stamp): a checkout in the SAME
	// wall-clock second as the docking folds the interval to exactly 0
	// (utilisation 0) on a fast runner. Wait until the docking's second has
	// fully passed so the door was measurably occupied.
	if wait := time.Now().UTC().Truncate(time.Second).Add(2 * time.Second).Sub(time.Now()); wait > 0 {
		time.Sleep(wait)
	}
	a.mustPost("/visits/"+visitID+"/check-outs", map[string]any{"trailersOut": []map[string]string{{"trailerId": trailerID}}}, http.StatusOK)
	if tr := a.trailerView(trailerID); tr["state"] != "DEPARTED" {
		t.Fatalf("the trailer left the site: %v", tr)
	}
}

// reportsFromTheProjection: the visit of appointmentToDoorAndReceipts and the
// hostler move of dropVisitFlow come back through the analytics topic, the
// projector and the reports role, served by the API's GET /reports/*.
func reportsFromTheProjection(t *testing.T, a *api, dsn string, pool *pgxpool.Pool) {
	t.Helper()
	window := fmt.Sprintf("from=%s&to=%s&siteId=WH1", url.QueryEscape(time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)), url.QueryEscape(time.Now().Add(time.Hour).UTC().Format(time.RFC3339)))
	rows := func(report string, want int) []any {
		var items []any
		eventually(t, report+" to have rows", func() bool {
			r := a.call(http.MethodGet, "/reports/"+report+"?"+window, nil, "")
			if r.Code != http.StatusOK {
				t.Fatalf("GET /reports/%s = %d %s", report, r.Code, r.Body)
			}
			items = r.JSON()["rows"].([]any)
			return len(items) >= want
		})
		return items
	}

	dwell := rows("yard-dwell", 1)
	if len(dwell) != 1 {
		t.Fatalf("dwell rows %v, want the single departed DRY_VAN", dwell)
	}
	d := dwell[0].(map[string]any)
	if d["kind"] != "DRY_VAN" || d["trailers"] != float64(1) || d["avgDriverWaitMinutes"] == nil || d["avgDwellMinutes"].(float64) < 0 {
		t.Fatalf("dwell row %v: one DRY_VAN, with a driver wait (door work started after arrival)", d)
	}

	// Two doors saw a docking: the drop visit's WH1-D02 and the appointment's WH1-D04.
	doors := rows("door-utilisation", 2)
	if len(doors) != 2 {
		t.Fatalf("door rows %v, want WH1-D02 and WH1-D04", doors)
	}
	door := doors[1].(map[string]any)
	if doors[0].(map[string]any)["doorCode"] != "WH1-D02" || door["doorCode"] != "WH1-D04" || door["dockings"] != float64(1) || door["siteId"] != "WH1" || door["utilisation"].(float64) <= 0 {
		t.Fatalf("door row %v: the appointment's door, docked once", door)
	}

	hostlers := rows("hostler-productivity", 1)
	h := hostlers[0].(map[string]any)
	if h["hostlerRef"] != "h1" || h["moves"] != float64(1) || h["movesPerHour"] != float64(1) {
		t.Fatalf("hostler row %v: h1 completed one move (the visiting driver's moves do not count)", h)
	}

	f := a.call(http.MethodGet, "/reports/freshness", nil, "").JSON()
	if f["lastEventAt"] == nil {
		t.Fatalf("freshness %v: the projector has folded events", f)
	}
}

// noShowCancelsToDoorMove: a trailer that arrived long before its window waits
// in a holding spot; the supervisor queues a hostler move to its door; the
// appointment's NoShow, arriving through Kafka, cancels that open move and the
// trailer stays where it is.
func noShowCancelsToDoorMove(t *testing.T, a *api, dsn string, pool *pgxpool.Pool) {
	t.Helper()
	const appt, door = "appt-itest-noshow", "WH1-D05"
	bookAppointment(t, pool, appt, door, time.Now().Add(3*time.Hour), []string{"ASN-B1"})

	in := a.mustPost("/visits", map[string]any{
		"siteId": "WH1", "carrier": "Acme Freight", "carrierScac": "ACME", "tractorPlate": "APPT-2", "purpose": "DROP", "appointmentId": appt,
		"trailers": []map[string]any{{"unitNumber": "TR-B1", "kind": "DRY_VAN", "loadState": "LOADED"}},
	}, http.StatusCreated)
	first := in["moves"].([]any)[0].(map[string]any)
	if first["reason"] != "TO_HOLDING" {
		t.Fatalf("an arrival hours before the window goes to holding: %v", first)
	}
	trailerID := in["trailers"].([]any)[0].(map[string]any)["trailerId"].(string)
	a.mustPost("/moves/"+first["moveId"].(string)+"/completions", nil, http.StatusOK)

	req := a.mustPost("/moves", map[string]any{"trailerId": trailerID, "to": map[string]string{"kind": "DOOR", "code": door}}, http.StatusCreated)
	if req["reason"] != "TO_DOOR" || req["state"] != "REQUESTED" || req["performer"] != "HOSTLER" {
		t.Fatalf("the supervisor's move: %v", req)
	}

	publish(t, topicDockScheduling, sourceDockScheduling, typeApptNoShow, appt, map[string]any{
		"appointment_id": appt, "site_id": "WH1", "door_code": door, "direction": "INBOUND", "carrier_scac": "ACME",
		"window_end": rfc(time.Now().Add(4 * time.Hour)), "version": 2,
	})
	waitRows(t, pool, "appointments WHERE appointment_id = '"+appt+"' AND status = 'NO_SHOW'", 1)

	if open := a.movesOf(trailerID, "REQUESTED"); len(open) != 0 {
		t.Fatalf("the open TO_DOOR move must be cancelled: %v", open)
	}
	cancelled := a.movesOf(trailerID, "CANCELLED")
	if len(cancelled) != 1 || cancelled[0].(map[string]any)["moveId"] != req["moveId"] {
		t.Fatalf("cancelled moves %v, want the supervisor's %v", cancelled, req["moveId"])
	}
	if tr := a.trailerView(trailerID); tr["state"] != "PARKED" || tr["openMoveId"] != nil {
		t.Fatalf("the trailer stays where it is, with no open move: %v", tr)
	}
}
