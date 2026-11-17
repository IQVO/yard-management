package kafka_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/adapters/inbound/kafka"
	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
)

func at(s string) time.Time {
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return v.UTC()
}

// consumerOf builds one consumer over the recorder and the fake kafka.
type consumerBuilder func(f kafka.Factory, group string, uc *recorder) (*kafka.Consumer, error)

var builders = map[string]struct {
	build consumerBuilder
	env   string
	topic string
}{
	"dock-scheduling": {
		env: "DOCK_SCHEDULING_CONSUMER_GROUP", topic: kafka.DockSchedulingTopic,
		build: func(f kafka.Factory, g string, uc *recorder) (*kafka.Consumer, error) {
			return kafka.NewDockSchedulingConsumer(f, g, uc, quietLogger())
		},
	},
	"facility": {
		env: "FACILITY_CONSUMER_GROUP", topic: kafka.FacilityTopic,
		build: func(f kafka.Factory, g string, uc *recorder) (*kafka.Consumer, error) {
			return kafka.NewFacilityConsumer(f, g, uc, quietLogger())
		},
	},
	"inbound-receiving": {
		env: "INBOUND_RECEIVING_CONSUMER_GROUP", topic: kafka.InboundReceivingTopic,
		build: func(f kafka.Factory, g string, uc *recorder) (*kafka.Consumer, error) {
			return kafka.NewInboundReceivingConsumer(f, g, uc, quietLogger())
		},
	},
}

func newConsumerFor(t *testing.T, which string, values ...[]byte) (*kafka.Consumer, *recorder, *fakeKafka) {
	t.Helper()
	fk := newFakeKafka(values...)
	uc := &recorder{}
	c, err := builders[which].build(fk.factory(), "group-"+which, uc)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	c.Retry = fastRetry
	return c, uc, fk
}

func TestConstructorsRequireTheConsumerGroupNamingTheVariable(t *testing.T) {
	for which, b := range builders {
		for _, group := range []string{"", "   "} {
			fk := newFakeKafka()
			c, err := b.build(fk.factory(), group, &recorder{})
			if c != nil || !errors.Is(err, kafka.ErrGroupRequired) || !strings.Contains(err.Error(), b.env) {
				t.Fatalf("%s group %q: consumer %v err %v, want ErrGroupRequired naming %s", which, group, c, err, b.env)
			}
			if fk.readerTopic != "" {
				t.Fatalf("%s: a reader was built although the group is blank", which)
			}
		}
	}
}

func TestConstructorsReadTheirTopicUnderTheGivenGroupAndCloseWhatTheyOpened(t *testing.T) {
	for which, b := range builders {
		c, _, fk := newConsumerFor(t, which)
		if fk.readerTopic != b.topic || fk.readerGroup != "group-"+which || fk.dlqTopic != b.topic {
			t.Fatalf("%s: reader %q/%q dlq %q, want topic %q", which, fk.readerTopic, fk.readerGroup, fk.dlqTopic, b.topic)
		}
		if err := c.Close(); err != nil || !fk.reader.closed || !fk.dlq.closed {
			t.Fatalf("%s: Close = %v, reader closed %v, dlq closed %v", which, err, fk.reader.closed, fk.dlq.closed)
		}
	}
	if got := kafka.DeadLetterTopic(kafka.FacilityTopic); got != "warehouse.facility.events.dlq" {
		t.Fatalf("dead letter topic %q", got)
	}
}

func TestDockSchedulingExamplesMapToTheAppointmentCopy(t *testing.T) {
	tests := []struct {
		name string
		msg  []byte
		want usecases.AppointmentFact
	}{
		{"booked", dsBooked(t), usecases.AppointmentFact{Appointment: views.Appointment{
			ID: exampleAppointmentID, SiteID: "WH1", Direction: "INBOUND", DoorCode: "WH1-DOCK-IN-01", CarrierSCAC: "ACMF",
			WindowStart: at("2026-11-06T08:00:00Z"), WindowEnd: at("2026-11-06T10:00:00Z"), ASNNumbers: []string{"ASN-1001"},
			ShipmentRefs: []string{}, ExpectedSeal: "SEAL-77120", ToleranceMinutes: 15, Status: views.ApptBooked, Version: 1}}},
		{"rescheduled", dsRescheduled(t), usecases.AppointmentFact{PreviousDoorCode: "WH1-DOCK-IN-01", Appointment: views.Appointment{
			ID: exampleAppointmentID, SiteID: "WH1", DoorCode: "WH1-DOCK-IN-02",
			WindowStart: at("2026-11-06T09:00:00Z"), WindowEnd: at("2026-11-06T11:00:00Z"), ToleranceMinutes: 15, Version: 2}}},
		{"cancelled", dsCancelled(t), usecases.AppointmentFact{Appointment: views.Appointment{
			ID: exampleAppointmentID, SiteID: "WH1", DoorCode: "WH1-DOCK-IN-01", Status: views.ApptCancelled, Version: 2}}},
		{"no-show", dsNoShow(t), usecases.AppointmentFact{Appointment: views.Appointment{
			ID: exampleAppointmentID, SiteID: "WH1", Direction: "INBOUND", DoorCode: "WH1-DOCK-IN-01", CarrierSCAC: "ACMF",
			WindowEnd: at("2026-11-06T10:00:00Z"), Status: views.ApptNoShow, Version: 2}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, uc, fk := newConsumerFor(t, "dock-scheduling", tc.msg)
			drain(t, c)
			if len(uc.facts) != 1 || !reflect.DeepEqual(uc.facts[0], tc.want) {
				t.Fatalf("fact\n got %+v\nwant %+v", uc.facts, tc.want)
			}
			if got := fk.reader.committed(); !reflect.DeepEqual(got, []int64{0}) {
				t.Fatalf("committed %v", got)
			}
		})
	}
}

func TestDockSchedulingClosureExamplesMapToTheClosureCopy(t *testing.T) {
	c, uc, _ := newConsumerFor(t, "dock-scheduling", dsClosureScheduled(t), dsClosureLifted(t))
	drain(t, c)
	want := views.Closure{ID: exampleClosureID, SiteID: "WH1", DoorCode: "WH1-DOCK-IN-01", From: at("2026-11-10T00:00:00Z"), To: at("2026-11-11T00:00:00Z")}
	if len(uc.clos) != 1 || uc.clos[0] != want {
		t.Fatalf("closure %+v, want %+v", uc.clos, want)
	}
	wantCalls := []string{
		"schedule 0000100c-1111-4222-8333-444455556666 " + exampleClosureID,
		"lift 0000100e-1111-4222-8333-444455556666 " + exampleClosureID,
	}
	if got := uc.log(); !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("calls %v, want %v", got, wantCalls)
	}
}

func TestFacilityExamplesMapToTheLocationCopy(t *testing.T) {
	c, uc, _ := newConsumerFor(t, "facility",
		flZone(t, "f-1", "WH1-YARD", "WH1"),
		flSlot(t, "f-2", "WH1-SPOT-01", "WH1-YARD", "Yard", ""),
		flSlot(t, "f-3", "WH1-DOCK-IN-01", "WH1-DOCK", "Dock", "Inbound"),
		flDecommissioned(t, "f-4", "WH1-SPOT-01"))
	drain(t, c)
	want := []string{
		"zone f-1 WH1-YARD WH1",
		"slot f-2 WH1-SPOT-01 WH1-YARD Yard ",
		"slot f-3 WH1-DOCK-IN-01 WH1-DOCK Dock Inbound",
		"decommission f-4 WH1-SPOT-01",
	}
	if got := uc.log(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls %v, want %v", got, want)
	}
}

// ADR 0014: the optional spotKind (and the gln, which the yard ignores) of a
// facility-layout slot reaches the use case; absent or null is "no kind", an
// unknown kind is passed on for the use case to read as unknown.
func TestFacilitySpotKindIsConsumedAndTolerated(t *testing.T) {
	slot := func(id, code, extra string) []byte {
		data := fmt.Sprintf(`{"eventName": "LocationSlotRegistered", "occurredAt": "2026-11-05T11:00:00Z", "locationCode": %q, "aisleId": "A1", "zoneId": "WH1-YARD", "locationType": "Slot", "role": "Yard"%s}`, code, extra)
		return envelope(t, id, facilityLayoutSource, typeFLSlotRegistered, code, "events", data)
	}
	c, uc, fk := newConsumerFor(t, "facility",
		slot("f-1", "S1", `, "spotKind": "HAZMAT", "gln": "0614141000012"`),
		slot("f-2", "S2", `, "spotKind": null`),
		slot("f-3", "S3", ``),
		slot("f-4", "S4", `, "spotKind": "COLD_STORE"`))
	drain(t, c)
	want := []string{
		"slot f-1 S1 WH1-YARD Yard  kind=HAZMAT",
		"slot f-2 S2 WH1-YARD Yard ",
		"slot f-3 S3 WH1-YARD Yard ",
		"slot f-4 S4 WH1-YARD Yard  kind=COLD_STORE",
	}
	if got := uc.log(); !reflect.DeepEqual(got, want) || len(fk.dlq.letters()) != 0 {
		t.Fatalf("calls %v, want %v; dead letters %v", got, want, fk.dlq.letters())
	}
}

func TestFacilitySlotsOfOtherRolesAreNotTheYardsBusiness(t *testing.T) {
	c, uc, fk := newConsumerFor(t, "facility",
		flSlot(t, "f-1", "WH1-STOR-1", "WH1-STOR", "Storage", ""),
		flSlot(t, "f-2", "WH1-OLD-1", "WH1-STOR", "", ""), // absent role = Storage
		flSlot(t, "f-3", "WH1-QC-1", "WH1-QC", "QC", ""),
		flSlot(t, "f-4", "WH1-SPOT-01", "WH1-YARD", "Yard", ""))
	drain(t, c)
	if got, want := uc.log(), []string{"slot f-4 WH1-SPOT-01 WH1-YARD Yard "}; !reflect.DeepEqual(got, want) {
		t.Fatalf("calls %v, want %v", got, want)
	}
	if got := fk.reader.committed(); !reflect.DeepEqual(got, []int64{0, 1, 2, 3}) || len(fk.dlq.letters()) != 0 {
		t.Fatalf("committed %v, dead letters %v: ignored slots are committed past, never dead-lettered", got, fk.dlq.letters())
	}
}

func TestInboundReceivingExamplesMapToTheAsnAndReceiptCopies(t *testing.T) {
	c, uc, _ := newConsumerFor(t, "inbound-receiving", irASNRegistered(t), irASNCancelled(t), irReceiptOpened(t), irReceiptClosed(t))
	drain(t, c)
	want := []string{
		"asn-registered 7c1f3a52-0d4e-4b8a-9e61-2a5d8f0c3b14 ASN-1001",
		"asn-cancelled 1e9b4d70-3c2a-4f5d-8b17-6a0c9d2e7f31 ASN-1001",
		"receipt-opened d4e6f8a0-3b5c-4d7e-9f1a-2b3c4d5e6f70 " + exampleReceiptID,
		"receipt-closed e5f7a9b1-4c6d-4e8f-a02b-3c4d5e6f7a81 " + exampleReceiptID + " ASN-1001 2026-10-09T09:30:00Z",
	}
	if got := uc.log(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls %v, want %v", got, want)
	}
	wantReceipt := views.Receipt{ID: exampleReceiptID, ASNNumber: "ASN-1001", AppointmentID: exampleAppointmentID, DoorCode: "WH1-DOCK-IN-01"}
	if len(uc.rcpts) != 1 || uc.rcpts[0] != wantReceipt {
		t.Fatalf("receipt %+v, want %+v", uc.rcpts, wantReceipt)
	}
}

func TestAWalkInReceiptCarriesNoAppointment(t *testing.T) {
	data := `{"receipt_id": "rcpt-9", "asn_number": "ASN-9", "opened_at": "2026-10-09T08:05:00Z"}`
	c, uc, _ := newConsumerFor(t, "inbound-receiving", envelope(t, "ir-9", inboundReceivingSrc, typeIRReceiptOpened, "rcpt-9", "events", data))
	drain(t, c)
	if len(uc.rcpts) != 1 || uc.rcpts[0].AppointmentID != "" || uc.rcpts[0].DoorCode != "" {
		t.Fatalf("receipt %+v: the omitted appointment and door default to empty", uc.rcpts)
	}
}

func TestUnknownTypesAndNonCloudEventsAreCommittedPastWithoutEffect(t *testing.T) {
	cases := map[string][][]byte{
		"dock-scheduling": {
			envelope(t, "x-1", dockSchedulingSource, typeDSCheckedIn, exampleAppointmentID, "analytics", `{"appointment_id":"a"}`),
			envelope(t, "x-2", dockSchedulingSource, "com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentBooked.v2", "a", "events", dsBookedData),
		},
		"facility": {
			envelope(t, "x-1", facilityLayoutSource, typeFLSiteRegistered, "WH1", "events", `{"siteCode":"WH1","siteName":"Main","siteRole":"UNSPECIFIED"}`),
			envelope(t, "x-2", facilityLayoutSource, typeFLSlotGeometryUpd, "WH1-SPOT-01", "events", `{"locationCode":"WH1-SPOT-01"}`),
		},
		"inbound-receiving": {
			envelope(t, "x-1", inboundReceivingSrc, typeIRLegacyBooked, exampleAppointmentID, "events", dsBookedData),
			envelope(t, "x-2", inboundReceivingSrc, "com.warehouse.wms.inbound-receiving.receipt.ReceiptLineReceived", "r", "events", `{}`),
		},
	}
	junk := [][]byte{
		[]byte("not json at all"),
		[]byte(`{}`),
		[]byte(`{"eventName":"ZoneRegistered","eventType":"ZoneRegistered","zoneId":"z","siteCode":"WH1"}`), // the retired flat envelope
		[]byte(`{"specversion":"0.3","id":"1","source":"/s","type":"t","subject":"s"}`),
		nil,
	}
	for which, unknown := range cases {
		t.Run(which, func(t *testing.T) {
			msgs := append(append([][]byte{}, unknown...), junk...)
			c, uc, fk := newConsumerFor(t, which, msgs...)
			drain(t, c)
			if got := uc.log(); len(got) != 0 {
				t.Fatalf("unexpected effects %v", got)
			}
			if got := fk.reader.committed(); len(got) != len(msgs) {
				t.Fatalf("committed %v, want all %d messages", got, len(msgs))
			}
			if got := fk.dlq.letters(); len(got) != 0 {
				t.Fatalf("dead letters %v: neither unknown types nor non-CloudEvents are dead-lettered", got)
			}
		})
	}
}

func TestPoisonPayloadsAreDeadLetteredAtOnceAndCommittedPast(t *testing.T) {
	notAnObject := `"just a string"`
	tests := []struct {
		name  string
		which string
		msg   []byte
		cause string
	}{
		{"booked data is not an object", "dock-scheduling", envelope(t, "p", dockSchedulingSource, typeDSBooked, "a", "events", notAnObject), "does not decode"},
		{"booked without appointment_id", "dock-scheduling", envelope(t, "p", dockSchedulingSource, typeDSBooked, "a", "events", `{"door_code":"D","window_start":"2026-11-06T08:00:00Z","window_end":"2026-11-06T10:00:00Z","version":1}`), "appointment_id"},
		{"booked without version", "dock-scheduling", envelope(t, "p", dockSchedulingSource, typeDSBooked, "a", "events", `{"appointment_id":"a","door_code":"D","window_start":"2026-11-06T08:00:00Z","window_end":"2026-11-06T10:00:00Z"}`), "version"},
		{"booked without a window", "dock-scheduling", envelope(t, "p", dockSchedulingSource, typeDSBooked, "a", "events", `{"appointment_id":"a","door_code":"D","version":1}`), "window"},
		{"booked with an unparseable time", "dock-scheduling", envelope(t, "p", dockSchedulingSource, typeDSBooked, "a", "events", `{"appointment_id":"a","door_code":"D","window_start":"tomorrow","window_end":"later","version":1}`), "does not decode"},
		{"booked without door_code", "dock-scheduling", envelope(t, "p", dockSchedulingSource, typeDSBooked, "a", "events", `{"appointment_id":"a","window_start":"2026-11-06T08:00:00Z","window_end":"2026-11-06T10:00:00Z","version":1}`), "door_code"},
		{"rescheduled without a window", "dock-scheduling", envelope(t, "p", dockSchedulingSource, typeDSRescheduled, "a", "events", `{"appointment_id":"a","door_code":"D","version":2}`), "window"},
		{"cancelled with a blank appointment_id", "dock-scheduling", envelope(t, "p", dockSchedulingSource, typeDSCancelled, "a", "events", `{"appointment_id":"  ","version":2}`), "appointment_id"},
		{"no-show without version", "dock-scheduling", envelope(t, "p", dockSchedulingSource, typeDSNoShow, "a", "events", `{"appointment_id":"a"}`), "version"},
		{"closure scheduled without closure_id", "dock-scheduling", envelope(t, "p", dockSchedulingSource, typeDSClosureSched, "WH1", "events", `{"door_code":"D","from":"2026-11-10T00:00:00Z","to":"2026-11-11T00:00:00Z"}`), "closure_id"},
		{"closure scheduled without a span", "dock-scheduling", envelope(t, "p", dockSchedulingSource, typeDSClosureSched, "WH1", "events", `{"closure_id":"c","door_code":"D"}`), "span"},
		{"closure lifted without closure_id", "dock-scheduling", envelope(t, "p", dockSchedulingSource, typeDSClosureLifted, "WH1", "events", `{"door_code":"D"}`), "closure_id"},
		{"zone data is an array", "facility", envelope(t, "p", facilityLayoutSource, typeFLZoneRegistered, "z", "events", `[1,2]`), "does not decode"},
		{"zone without siteCode", "facility", envelope(t, "p", facilityLayoutSource, typeFLZoneRegistered, "z", "events", `{"zoneId":"z"}`), "siteCode"},
		{"slot without zoneId", "facility", envelope(t, "p", facilityLayoutSource, typeFLSlotRegistered, "s", "events", `{"locationCode":"S","role":"Yard"}`), "zoneId"},
		{"slot without locationCode", "facility", envelope(t, "p", facilityLayoutSource, typeFLSlotRegistered, "s", "events", `{"zoneId":"z","role":"Yard"}`), "locationCode"},
		{"decommission without locationCode", "facility", envelope(t, "p", facilityLayoutSource, typeFLSlotDecommisnd, "s", "events", `{}`), "locationCode"},
		{"asn registered without asn_number", "inbound-receiving", envelope(t, "p", inboundReceivingSrc, typeIRASNRegistered, "a", "events", `{"lines":[]}`), "asn_number"},
		{"asn cancelled with data null", "inbound-receiving", envelope(t, "p", inboundReceivingSrc, typeIRASNCancelled, "a", "events", `null`), "asn_number"},
		{"receipt opened without receipt_id", "inbound-receiving", envelope(t, "p", inboundReceivingSrc, typeIRReceiptOpened, "r", "events", `{"asn_number":"A"}`), "receipt_id"},
		{"receipt closed without asn_number", "inbound-receiving", envelope(t, "p", inboundReceivingSrc, typeIRReceiptClosed, "r", "events", `{"receipt_id":"r","closed_at":"2026-10-09T09:30:00Z"}`), "asn_number"},
		{"receipt closed without closed_at", "inbound-receiving", envelope(t, "p", inboundReceivingSrc, typeIRReceiptClosed, "r", "events", `{"receipt_id":"r","asn_number":"A"}`), "closed_at"},
		{"receipt closed with an unparseable time", "inbound-receiving", envelope(t, "p", inboundReceivingSrc, typeIRReceiptClosed, "r", "events", `{"receipt_id":"r","asn_number":"A","closed_at":"yesterday"}`), "does not decode"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, uc, fk := newConsumerFor(t, tc.which, tc.msg)
			drain(t, c)
			letters := fk.dlq.letters()
			if len(letters) != 1 || string(letters[0].value) != string(tc.msg) || letters[0].sourceTopic != builders[tc.which].topic ||
				!strings.Contains(letters[0].cause, tc.cause) {
				t.Fatalf("dead letters %+v, want the message with a cause containing %q", letters, tc.cause)
			}
			if got := uc.log(); len(got) != 0 {
				t.Fatalf("a poison message had effects: %v", got)
			}
			if got := fk.reader.committed(); !reflect.DeepEqual(got, []int64{0}) {
				t.Fatalf("committed %v: the offset must move past the dead-lettered message", got)
			}
		})
	}
}

func TestAPoisonMessageDoesNotBlockTheOnesAfterIt(t *testing.T) {
	poison := envelope(t, "p", facilityLayoutSource, typeFLZoneRegistered, "z", "events", `{"zoneId":"z"}`)
	c, uc, fk := newConsumerFor(t, "facility", poison, flZone(t, "f-2", "WH1-YARD", "WH1"))
	drain(t, c)
	if got, want := uc.log(), []string{"zone f-2 WH1-YARD WH1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("calls %v, want %v", got, want)
	}
	if got := fk.reader.committed(); !reflect.DeepEqual(got, []int64{0, 1}) || len(fk.dlq.letters()) != 1 {
		t.Fatalf("committed %v, dead letters %d", got, len(fk.dlq.letters()))
	}
}

func TestAPoisonMessageWithoutADeadLetterWriterIsStillCommittedPast(t *testing.T) {
	poison := envelope(t, "p", facilityLayoutSource, typeFLZoneRegistered, "z", "events", `{"zoneId":"z"}`)
	c, _, fk := newConsumerFor(t, "facility", poison)
	c.DLQ = nil
	drain(t, c)
	if got := fk.reader.committed(); !reflect.DeepEqual(got, []int64{0}) {
		t.Fatalf("committed %v", got)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close without a dead-letter writer: %v", err)
	}
}

func TestATransientFailureRetriesTheSameMessageAndNeverDeadLettersIt(t *testing.T) {
	c, uc, fk := newConsumerFor(t, "facility", flZone(t, "f-1", "WH1-YARD", "WH1"), flZone(t, "f-2", "WH1-DOCK", "WH1"))
	uc.failNext = 3
	drain(t, c)
	want := []string{"zone f-1 WH1-YARD WH1", "zone f-2 WH1-DOCK WH1"}
	if got := uc.log(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls %v, want %v (the failed message applied once, in order)", got, want)
	}
	if got := fk.reader.committed(); !reflect.DeepEqual(got, []int64{0, 1}) || len(fk.dlq.letters()) != 0 {
		t.Fatalf("committed %v, dead letters %v: a transient failure is retried, not dead-lettered", got, fk.dlq.letters())
	}
}
