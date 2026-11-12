package kafka_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/adapters/kafka/cloudevents"
	outboundkafka "github.com/claudioed/yard-management/internal/adapters/outbound/kafka"
	"github.com/claudioed/yard-management/internal/application/outbox"
	"github.com/claudioed/yard-management/internal/domain/inspection"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardcheck"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

// One golden case per published type (ADR 0004, contracts-delta): the payload
// is built by the SAME production mapping the Postgres publisher uses
// (outbox.Payload), so these tests pin the full chain domain fact -> outbox
// payload -> exact CloudEvents JSON, including the Kafka key, the topics and
// the content-type header. The expected `data` is written out by hand,
// byte for byte, from the contract - it is not generated from the code under
// test.

func at(h, m int) time.Time { return time.Date(2026, 11, 5, h, m, 0, 0, time.UTC) }

func stamp(h, m int) string { return shared.Stamp(at(h, m)) }

func hdr(entity, name, subject string, t time.Time) shared.Header {
	return shared.NewHeader(entity, name, subject, t)
}

func loc(kind shared.LocationKind, code string) shared.Location {
	return shared.Location{Kind: kind, Code: code}
}

type goldenCase struct {
	name        string
	event       shared.Event
	integration bool   // events + analytics when true, analytics only otherwise
	subject     string // the aggregate id = CloudEvents subject = Kafka key
	data        string // the expected `data`, byte for byte
}

func goldenCases() []goldenCase {
	return []goldenCase{
		{
			name: "VisitCheckedIn", integration: true, subject: "visit-1",
			event: yardvisit.VisitCheckedIn{
				Header: hdr("yardvisit", "VisitCheckedIn", "visit-1", at(14, 0)), VisitID: "visit-1", SiteID: "WH1", Carrier: "Acme Freight",
				CarrierSCAC: shared.Str("ACME"), Purpose: shared.PurposeLiveUnload, AppointmentID: shared.Str("appt-1"),
				Trailers: []yardvisit.VisitTrailer{
					{TrailerID: "trl-1", UnitNumber: "T100", CarrierSCAC: shared.Str("ACME"), Kind: shared.KindDryVan, LoadState: shared.LoadLoaded, RequiresTemperatureControl: true},
					{TrailerID: "trl-2", UnitNumber: "CSQU3054383", Kind: shared.KindContainer, LoadState: shared.LoadEmpty, UnitNumberScheme: shared.Str("ISO6346"), SealClass: shared.Str("H"), HazmatClass: shared.Str("3"), UNNumber: shared.Str("UN1203")},
				},
				ArrivedAt: stamp(14, 0), Version: 1,
			},
			data: `{"visit_id":"visit-1","site_id":"WH1","carrier":"Acme Freight","carrier_scac":"ACME","purpose":"LIVE_UNLOAD","appointment_id":"appt-1",` +
				`"trailers":[{"trailer_id":"trl-1","unit_number":"T100","carrier_scac":"ACME","kind":"DRY_VAN","load_state":"LOADED","unit_number_scheme":null,"seal_class":null,"hazmat_class":null,"un_number":null,"requires_temperature_control":true},` +
				`{"trailer_id":"trl-2","unit_number":"CSQU3054383","carrier_scac":null,"kind":"CONTAINER","load_state":"EMPTY","unit_number_scheme":"ISO6346","seal_class":"H","hazmat_class":"3","un_number":"UN1203","requires_temperature_control":false}],` +
				`"arrived_at":"2026-11-05T14:00:00Z","version":1,"carrier_flag":null}`,
		},
		{
			name: "VisitRejected", integration: true, subject: "visit-2",
			event: yardvisit.VisitRejected{
				Header: hdr("yardvisit", "VisitRejected", "visit-2", at(14, 5)), VisitID: "visit-2", SiteID: "WH1",
				AppointmentID: shared.Str("appt-9"), Reason: yardvisit.ReasonUnknownAppointment, RejectedAt: stamp(14, 5),
			},
			data: `{"visit_id":"visit-2","site_id":"WH1","carrier_scac":null,"appointment_id":"appt-9","reason":"UNKNOWN_APPOINTMENT","rejected_at":"2026-11-05T14:05:00Z"}`,
		},
		{
			name: "VisitCheckedOut", subject: "visit-1",
			event: yardvisit.VisitCheckedOut{
				Header: hdr("yardvisit", "VisitCheckedOut", "visit-1", at(15, 35)), VisitID: "visit-1", SiteID: "WH1",
				TrailersOut: []string{"trl-1"}, DepartedAt: stamp(15, 35), OnSiteMinutes: 95, Version: 2,
			},
			data: `{"visit_id":"visit-1","site_id":"WH1","trailers_out":["trl-1"],"departed_at":"2026-11-05T15:35:00Z","on_site_minutes":95,"version":2}`,
		},
		{
			name: "TrailerDocked", integration: true, subject: "trl-1",
			event: trailer.TrailerDocked{
				Header: hdr("trailer", "TrailerDocked", "trl-1", at(14, 20)), TrailerID: "trl-1", SiteID: "WH1", DoorCode: "WH1-D1",
				AppointmentID: shared.Str("appt-1"), ASNNumbers: []string{"ASN-1", "ASN-2"}, VisitID: shared.Str("visit-1"),
				DockedAt: stamp(14, 20), Version: 3, SealState: shared.Str("VERIFIED"), SealClass: shared.Str("H"), HazmatClass: shared.Str("2.1"), UNNumber: shared.Str("UN1075"),
			},
			data: `{"trailer_id":"trl-1","site_id":"WH1","door_code":"WH1-D1","appointment_id":"appt-1","asn_numbers":["ASN-1","ASN-2"],"visit_id":"visit-1","docked_at":"2026-11-05T14:20:00Z","version":3,"seal_state":"VERIFIED","seal_class":"H","hazmat_class":"2.1","un_number":"UN1075","requires_temperature_control":false}`,
		},
		{
			name: "TrailerDockedWithoutAppointment", integration: true, subject: "trl-3",
			event: trailer.TrailerDocked{
				Header: hdr("trailer", "TrailerDocked", "trl-3", at(14, 21)), TrailerID: "trl-3", SiteID: "WH1", DoorCode: "WH1-D2",
				ASNNumbers: []string{}, DockedAt: stamp(14, 21), Version: 2, RequiresTemperatureControl: true,
			},
			data: `{"trailer_id":"trl-3","site_id":"WH1","door_code":"WH1-D2","appointment_id":null,"asn_numbers":[],"visit_id":null,"docked_at":"2026-11-05T14:21:00Z","version":2,"seal_state":null,"seal_class":null,"hazmat_class":null,"un_number":null,"requires_temperature_control":true}`,
		},
		{
			name: "TrailerUndocked", integration: true, subject: "trl-1",
			event: trailer.TrailerUndocked{
				Header: hdr("trailer", "TrailerUndocked", "trl-1", at(15, 10)), TrailerID: "trl-1", SiteID: "WH1", DoorCode: "WH1-D1",
				UndockedAt: stamp(15, 10), Version: 5,
			},
			data: `{"trailer_id":"trl-1","site_id":"WH1","door_code":"WH1-D1","appointment_id":null,"undocked_at":"2026-11-05T15:10:00Z","version":5}`,
		},
		{
			name: "TrailerParked", subject: "trl-2",
			event: trailer.TrailerParked{
				Header: hdr("trailer", "TrailerParked", "trl-2", at(14, 10)), TrailerID: "trl-2", SiteID: "WH1", SpotCode: "WH1-P1", Version: 2,
			},
			data: `{"trailer_id":"trl-2","site_id":"WH1","spot_code":"WH1-P1","version":2}`,
		},
		{
			name: "DoorWorkStarted", subject: "trl-1",
			event: trailer.DoorWorkStarted{
				Header: hdr("trailer", "DoorWorkStarted", "trl-1", at(14, 25)), TrailerID: "trl-1", SiteID: "WH1", DoorCode: "WH1-D1",
				AppointmentID: shared.Str("appt-1"), StartedAt: stamp(14, 25), Version: 4,
			},
			data: `{"trailer_id":"trl-1","site_id":"WH1","door_code":"WH1-D1","appointment_id":"appt-1","started_at":"2026-11-05T14:25:00Z","version":4}`,
		},
		{
			name: "DoorWorkCompleted", integration: true, subject: "trl-1",
			event: trailer.DoorWorkCompleted{
				Header: hdr("trailer", "DoorWorkCompleted", "trl-1", at(15, 0)), TrailerID: "trl-1", SiteID: "WH1", DoorCode: "WH1-D1",
				AppointmentID: shared.Str("appt-1"), CompletedAt: stamp(15, 0), LoadState: shared.LoadEmpty, Version: 5,
			},
			data: `{"trailer_id":"trl-1","site_id":"WH1","door_code":"WH1-D1","appointment_id":"appt-1","completed_at":"2026-11-05T15:00:00Z","load_state":"EMPTY","version":5}`,
		},
		{
			name: "TrailerDeparted", integration: true, subject: "trl-1",
			event: trailer.TrailerDeparted{
				Header: hdr("trailer", "TrailerDeparted", "trl-1", at(15, 35)), TrailerID: "trl-1", SiteID: "WH1", VisitID: shared.Str("visit-1"),
				DepartedAt: stamp(15, 35), DwellMinutes: 95, LoadState: shared.LoadEmpty, SealNumber: shared.Str("SL-1"), Version: 7, SealState: shared.Str("INTACT"),
			},
			data: `{"trailer_id":"trl-1","site_id":"WH1","visit_id":"visit-1","departed_at":"2026-11-05T15:35:00Z","dwell_minutes":95,"load_state":"EMPTY","seal_number":"SL-1","version":7,"seal_state":"INTACT","seal_class":null}`,
		},
		{
			name: "SealExceptionRaised", integration: true, subject: "trl-1",
			event: trailer.SealExceptionRaised{
				Header: hdr("trailer", "SealExceptionRaised", "trl-1", at(14, 2)), TrailerID: shared.Str("trl-1"), SiteID: "WH1",
				AppointmentID: shared.Str("appt-1"), ExpectedSealNumber: "SL-1", FoundSealNumber: "SL-9", RaisedAt: stamp(14, 2),
			},
			data: `{"trailer_id":"trl-1","site_id":"WH1","appointment_id":"appt-1","expected_seal_number":"SL-1","found_seal_number":"SL-9","raised_at":"2026-11-05T14:02:00Z"}`,
		},
		{
			name: "SealExceptionRaisedForARejectedVisit", integration: true, subject: "visit-2",
			event: trailer.NewSealException("visit-2", "WH1", "appt-9", "SL-1", "SL-9", at(14, 5)),
			data:  `{"trailer_id":null,"site_id":"WH1","appointment_id":"appt-9","expected_seal_number":"SL-1","found_seal_number":"SL-9","raised_at":"2026-11-05T14:05:00Z"}`,
		},
		{
			name: "TrailerHeld", subject: "trl-1",
			event: trailer.TrailerHeld{
				Header: hdr("trailer", "TrailerHeld", "trl-1", at(14, 30)), TrailerID: "trl-1", SiteID: "WH1", Reason: trailer.HoldQuality, Version: 5,
			},
			data: `{"trailer_id":"trl-1","site_id":"WH1","reason":"QUALITY","version":5}`,
		},
		{
			name: "TrailerHoldReleased", subject: "trl-1",
			event: trailer.TrailerHoldReleased{
				Header: hdr("trailer", "TrailerHoldReleased", "trl-1", at(14, 45)), TrailerID: "trl-1", SiteID: "WH1", Version: 6,
			},
			data: `{"trailer_id":"trl-1","site_id":"WH1","version":6}`,
		},
		{
			name: "TrailerSecured", integration: true, subject: "trl-1",
			event: trailer.TrailerSecured{
				Header: hdr("trailer", "TrailerSecured", "trl-1", at(14, 22)), TrailerID: "trl-1", SiteID: "WH1", DoorCode: "WH1-D1",
				Method: trailer.SecuredByWheelChock, SecuredAt: stamp(14, 22), Version: 4,
			},
			data: `{"trailer_id":"trl-1","site_id":"WH1","door_code":"WH1-D1","method":"WHEEL_CHOCKS","secured_at":"2026-11-05T14:22:00Z","version":4}`,
		},
		{
			name: "TrailerSecuringReleased", integration: true, subject: "trl-1",
			event: trailer.TrailerSecuringReleased{
				Header: hdr("trailer", "TrailerSecuringReleased", "trl-1", at(15, 8)), TrailerID: "trl-1", SiteID: "WH1", DoorCode: "WH1-D1",
				ReleasedAt: stamp(15, 8), Version: 7,
			},
			data: `{"trailer_id":"trl-1","site_id":"WH1","door_code":"WH1-D1","released_at":"2026-11-05T15:08:00Z","version":7}`,
		},
		{
			name: "TrailerDwellExceeded", integration: true, subject: "trl-2",
			event: trailer.TrailerDwellExceeded{
				Header: hdr("trailer", "TrailerDwellExceeded", "trl-2", at(18, 10)), TrailerID: "trl-2", SiteID: "WH1", Kind: shared.KindDryVan,
				DwellMinutes: 300, ThresholdMinutes: 240, LocationCode: shared.Str("WH1-P1"),
			},
			data: `{"trailer_id":"trl-2","site_id":"WH1","kind":"DRY_VAN","dwell_minutes":300,"threshold_minutes":240,"location_code":"WH1-P1"}`,
		},
		{
			name: "YardMoveRequested", subject: "mv-1",
			event: yardmove.YardMoveRequested{
				Header: hdr("yardmove", "YardMoveRequested", "mv-1", at(14, 12)), MoveID: "mv-1", SiteID: "WH1", TrailerID: "trl-1",
				Performer: yardmove.Hostler, FromLocation: &shared.Location{Kind: shared.Spot, Code: "WH1-P1"}, ToLocation: loc(shared.Door, "WH1-D1"),
				Reason: yardmove.ToDoor, AppointmentID: shared.Str("appt-1"), DueBy: shared.StampPtr(at(14, 30)), Version: 1,
			},
			data: `{"move_id":"mv-1","site_id":"WH1","trailer_id":"trl-1","performer":"HOSTLER","from_location":{"kind":"SPOT","code":"WH1-P1"},` +
				`"to_location":{"kind":"DOOR","code":"WH1-D1"},"reason":"TO_DOOR","appointment_id":"appt-1","due_by":"2026-11-05T14:30:00Z","version":1}`,
		},
		{
			name: "YardMoveRequestedAtTheGate", subject: "mv-0",
			event: yardmove.YardMoveRequested{
				Header: hdr("yardmove", "YardMoveRequested", "mv-0", at(14, 0)), MoveID: "mv-0", SiteID: "WH1", TrailerID: "trl-1",
				Performer: yardmove.VisitingDriver, ToLocation: loc(shared.Spot, "WH1-H1"), Reason: yardmove.ToHolding, Version: 1,
			},
			data: `{"move_id":"mv-0","site_id":"WH1","trailer_id":"trl-1","performer":"VISITING_DRIVER","from_location":null,` +
				`"to_location":{"kind":"SPOT","code":"WH1-H1"},"reason":"TO_HOLDING","appointment_id":null,"due_by":null,"version":1}`,
		},
		{
			name: "YardMoveClaimed", subject: "mv-1",
			event: yardmove.YardMoveClaimed{
				Header: hdr("yardmove", "YardMoveClaimed", "mv-1", at(14, 13)), MoveID: "mv-1", SiteID: "WH1", HostlerRef: "h-7", ClaimedAt: stamp(14, 13), Version: 2,
			},
			data: `{"move_id":"mv-1","site_id":"WH1","hostler_ref":"h-7","claimed_at":"2026-11-05T14:13:00Z","version":2}`,
		},
		{
			name: "YardMoveLeaseExpired", subject: "mv-1",
			event: yardmove.YardMoveLeaseExpired{
				Header: hdr("yardmove", "YardMoveLeaseExpired", "mv-1", at(14, 18)), MoveID: "mv-1", SiteID: "WH1", HostlerRef: "h-7", Version: 3,
			},
			data: `{"move_id":"mv-1","site_id":"WH1","hostler_ref":"h-7","version":3}`,
		},
		{
			name: "YardMoveCompleted", subject: "mv-1",
			event: yardmove.YardMoveCompleted{
				Header: hdr("yardmove", "YardMoveCompleted", "mv-1", at(14, 19)), MoveID: "mv-1", SiteID: "WH1", TrailerID: "trl-1",
				Performer: yardmove.Hostler, HostlerRef: shared.Str("h-7"), FromLocation: &shared.Location{Kind: shared.Spot, Code: "WH1-P1"},
				ToLocation: loc(shared.Door, "WH1-D1"), ActualLocation: loc(shared.Door, "WH1-D1"), Reason: yardmove.ToDoor,
				CompletedAt: stamp(14, 19), DurationSeconds: 420, Version: 3,
			},
			data: `{"move_id":"mv-1","site_id":"WH1","trailer_id":"trl-1","performer":"HOSTLER","hostler_ref":"h-7","from_location":{"kind":"SPOT","code":"WH1-P1"},` +
				`"to_location":{"kind":"DOOR","code":"WH1-D1"},"actual_location":{"kind":"DOOR","code":"WH1-D1"},"reason":"TO_DOOR",` +
				`"completed_at":"2026-11-05T14:19:00Z","duration_seconds":420,"version":3}`,
		},
		{
			name: "YardMoveCompletedByTheVisitingDriver", subject: "mv-0",
			event: yardmove.YardMoveCompleted{
				Header: hdr("yardmove", "YardMoveCompleted", "mv-0", at(14, 6)), MoveID: "mv-0", SiteID: "WH1", TrailerID: "trl-1",
				Performer: yardmove.VisitingDriver, ToLocation: loc(shared.Spot, "WH1-H1"), ActualLocation: loc(shared.Spot, "WH1-H1"),
				Reason: yardmove.ToHolding, CompletedAt: stamp(14, 6), DurationSeconds: 360, Version: 2,
			},
			data: `{"move_id":"mv-0","site_id":"WH1","trailer_id":"trl-1","performer":"VISITING_DRIVER","hostler_ref":null,"from_location":null,` +
				`"to_location":{"kind":"SPOT","code":"WH1-H1"},"actual_location":{"kind":"SPOT","code":"WH1-H1"},"reason":"TO_HOLDING",` +
				`"completed_at":"2026-11-05T14:06:00Z","duration_seconds":360,"version":2}`,
		},
		{
			name: "YardMoveCancelled", subject: "mv-2",
			event: yardmove.YardMoveCancelled{
				Header: hdr("yardmove", "YardMoveCancelled", "mv-2", at(14, 40)), MoveID: "mv-2", SiteID: "WH1", Reason: "appointment cancelled", Version: 2,
			},
			data: `{"move_id":"mv-2","site_id":"WH1","reason":"appointment cancelled","version":2}`,
		},
		{
			name: "YardCheckClosed", integration: true, subject: "yck-1",
			event: yardcheck.YardCheckClosed{
				Header: hdr("yardcheck", "YardCheckClosed", "yck-1", at(17, 0)), YardCheckID: "yck-1", SiteID: "WH1", ClosedAt: stamp(17, 0),
				Discrepancies: []yardcheck.ClosedDiscrepancy{
					{Kind: yardcheck.Missing, LocationCode: "WH1-P1", TrailerID: shared.Str("trl-1"), UnitNumber: shared.Str("T100")},
					{Kind: yardcheck.Unexpected, LocationCode: "WH1-P2", UnitNumber: shared.Str("ROGUE")},
				},
			},
			data: `{"yard_check_id":"yck-1","site_id":"WH1","closed_at":"2026-11-05T17:00:00Z","discrepancies":[` +
				`{"kind":"MISSING","location_code":"WH1-P1","trailer_id":"trl-1","unit_number":"T100"},` +
				`{"kind":"UNEXPECTED","location_code":"WH1-P2","trailer_id":null,"unit_number":"ROGUE"}]}`,
		},
		{
			name: "InspectionCompleted", integration: true, subject: "insp-1",
			event: inspection.InspectionCompleted{
				Header: hdr("inspection", "InspectionCompleted", "insp-1", at(18, 0)), InspectionID: "insp-1", SiteID: "WH1", TrailerID: "trl-1",
				VisitID: shared.Str("visit-1"), TemplateID: "tractor-trailer-17-point", TemplateVersion: 1, Gate: "PRE_DOCK", Outcome: "FAILED",
				ConditionGrade: shared.Str("DAMAGED"), CompletedAt: stamp(18, 0), FailedItemCodes: []string{"fifth-wheel", "pest-free"},
			},
			data: `{"inspection_id":"insp-1","site_id":"WH1","trailer_id":"trl-1","visit_id":"visit-1","template_id":"tractor-trailer-17-point","template_version":1,` +
				`"gate":"PRE_DOCK","outcome":"FAILED","condition_grade":"DAMAGED","completed_at":"2026-11-05T18:00:00Z","failed_item_codes":["fifth-wheel","pest-free"]}`,
		},
		{
			name: "InspectionCompletedPassedBare", integration: true, subject: "insp-2",
			event: inspection.InspectionCompleted{
				Header: hdr("inspection", "InspectionCompleted", "insp-2", at(18, 5)), InspectionID: "insp-2", SiteID: "WH1", TrailerID: "trl-2",
				TemplateID: "yard-entry", TemplateVersion: 3, Gate: "ON_DEMAND", Outcome: "PASSED", CompletedAt: stamp(18, 5), FailedItemCodes: []string{},
			},
			data: `{"inspection_id":"insp-2","site_id":"WH1","trailer_id":"trl-2","visit_id":null,"template_id":"yard-entry","template_version":3,` +
				`"gate":"ON_DEMAND","outcome":"PASSED","condition_grade":null,"completed_at":"2026-11-05T18:05:00Z","failed_item_codes":[]}`,
		},
		{
			name: "InspectionFailed", integration: true, subject: "insp-1",
			event: inspection.InspectionFailed{
				Header: hdr("inspection", "InspectionFailed", "insp-1", at(18, 0)), InspectionID: "insp-1", SiteID: "WH1", TrailerID: "trl-1",
				TemplateID: "tractor-trailer-17-point", Gate: "PRE_DOCK", FailedAt: stamp(18, 0), HoldPlaced: true,
				FailedItems: []inspection.FailedItem{
					{ItemCode: "fifth-wheel", Note: shared.Str("cracked weld"), Required: true},
					{ItemCode: "pest-free", Required: true},
				},
			},
			data: `{"inspection_id":"insp-1","site_id":"WH1","trailer_id":"trl-1","template_id":"tractor-trailer-17-point","gate":"PRE_DOCK",` +
				`"failed_items":[{"item_code":"fifth-wheel","note":"cracked weld","required":true},{"item_code":"pest-free","note":null,"required":true}],` +
				`"failed_at":"2026-11-05T18:00:00Z","hold_placed":true}`,
		},
		{
			name: "YardCheckClosedClean", integration: true, subject: "yck-2",
			event: yardcheck.YardCheckClosed{
				Header: hdr("yardcheck", "YardCheckClosed", "yck-2", at(17, 30)), YardCheckID: "yck-2", SiteID: "WH2", ClosedAt: stamp(17, 30),
				Discrepancies: []yardcheck.ClosedDiscrepancy{},
			},
			data: `{"yard_check_id":"yck-2","site_id":"WH2","closed_at":"2026-11-05T17:30:00Z","discrepancies":[]}`,
		},
	}
}

func eventID(i int) string { return fmt.Sprintf("0c9a1f00-7d41-4b6e-8c2f-%012d", i+1) }

func rowOf(t *testing.T, i int, c goldenCase) outbox.Row {
	t.Helper()
	payload, err := outbox.Payload(c.event)
	if err != nil {
		t.Fatalf("%s: payload: %v", c.name, err)
	}
	return outbox.Row{
		ID: int64(i + 1), EventID: eventID(i), AggregType: c.event.Entity(), AggregID: c.event.AggregateID(),
		Payload: payload, OccurredAt: c.event.OccurredAt(),
	}
}

// wantEnvelope is the whole CloudEvents structured-mode JSON of one message,
// written out from the contract (ADR 0004): the data is the hand-written one.
func wantEnvelope(c goldenCase, id, stream string) string {
	return fmt.Sprintf(`{"specversion":"1.0","id":%q,"source":"/warehouse/yard-management","type":"com.warehouse.wms.yard-management.%s.%s",`+
		`"datacontenttype":"application/json","dataschema":"urn:warehouse:yard-management:%s:%s:v1","subject":%q,"time":%q,"data":%s}`,
		id, c.event.Entity(), c.event.EventName(), stream, c.event.EventName(), c.subject, shared.Stamp(c.event.OccurredAt()), c.data)
}

func decodeJSON(t *testing.T, what string, raw []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s is not JSON: %v\n%s", what, err, raw)
	}
	return v
}

// dataOf extracts the raw bytes of the `data` member of an envelope.
func dataOf(t *testing.T, raw []byte) string {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	return string(env.Data)
}

func TestEncoderGoldenEnvelopes(t *testing.T) {
	enc := outboundkafka.NewEncoder()
	for i, c := range goldenCases() {
		t.Run(c.name, func(t *testing.T) {
			row := rowOf(t, i, c)
			if row.AggregID != c.subject {
				t.Fatalf("aggregate id = %q, want %q", row.AggregID, c.subject)
			}
			msgs, cause, ok := enc.Encode(row)
			if !ok {
				t.Fatalf("Encode refused the row: %s", cause)
			}
			topics, streams := []string{outboundkafka.TopicAnalytics}, []string{cloudevents.StreamAnalytics}
			if c.integration {
				topics = []string{outboundkafka.TopicEvents, outboundkafka.TopicAnalytics}
				streams = []string{cloudevents.StreamEvents, cloudevents.StreamAnalytics}
			}
			if len(msgs) != len(topics) {
				t.Fatalf("%d messages, want %d (%v)", len(msgs), len(topics), topics)
			}
			for j, m := range msgs {
				assertMessage(t, j, m, c, eventID(i), topics[j], streams[j])
			}
			if len(msgs) == 2 {
				assertTwin(t, msgs[0], msgs[1])
			}
		})
	}
}

// assertMessage pins one message: topic, key, subject, id, type, dataschema,
// headers, the hand-written data byte for byte and the whole envelope.
func assertMessage(t *testing.T, j int, m outbox.Message, c goldenCase, id, topic, stream string) {
	t.Helper()
	if m.Topic != topic {
		t.Errorf("message %d: topic %q, want %q", j, m.Topic, topic)
	}
	if string(m.Key) != c.subject || m.Subject != c.subject || m.EventID != id {
		t.Errorf("message %d: key/subject/id = %q/%q/%q", j, m.Key, m.Subject, m.EventID)
	}
	if want := "com.warehouse.wms.yard-management." + c.event.Entity() + "." + c.event.EventName(); m.EventType != want {
		t.Errorf("message %d: type %q, want %q", j, m.EventType, want)
	}
	if want := "urn:warehouse:yard-management:" + stream + ":" + c.event.EventName() + ":v1"; m.DataSchema != want {
		t.Errorf("message %d: dataschema %q, want %q", j, m.DataSchema, want)
	}
	if len(m.Headers) != 1 || m.Headers[0].Key != "content-type" || m.Headers[0].Value != cloudevents.MediaType {
		t.Errorf("message %d: headers %v, want exactly the CloudEvents content-type", j, m.Headers)
	}
	if got := dataOf(t, m.Value); got != c.data {
		t.Errorf("message %d: data differs\n got: %s\nwant: %s", j, got, c.data)
	}
	want := wantEnvelope(c, id, stream)
	if got := decodeJSON(t, "value", m.Value); !reflect.DeepEqual(got, decodeJSON(t, "want", []byte(want))) {
		t.Errorf("message %d: envelope differs\n got: %s\nwant: %s", j, m.Value, want)
	}
	if _, err := cloudevents.Decode(m.Value); err != nil {
		t.Errorf("message %d: not a valid CloudEvent: %v", j, err)
	}
}

// assertTwin: the analytics twin has the same id, type, subject and key and
// byte-identical data; only the topic and the dataschema differ.
func assertTwin(t *testing.T, a, b outbox.Message) {
	t.Helper()
	if a.EventID != b.EventID || a.EventType != b.EventType || a.Subject != b.Subject || string(a.Key) != string(b.Key) ||
		dataOf(t, a.Value) != dataOf(t, b.Value) {
		t.Errorf("the analytics twin must differ only in topic and dataschema:\n%+v\n%+v", a, b)
	}
}

func TestGoldenCasesCoverTheWholeCatalogue(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range goldenCases() {
		name := c.event.Entity() + "." + c.event.EventName()
		covered[name] = true
		integration, ok := outboundkafka.IsIntegration(c.event.Entity(), c.event.EventName())
		if !ok {
			t.Errorf("%s is not in the encoder catalogue", name)
		}
		if integration != c.integration {
			t.Errorf("%s: golden says integration=%v, catalogue says %v", name, c.integration, integration)
		}
	}
	total, integration := outboundkafka.PublishedCount()
	if total != 23 || integration != 13 {
		t.Fatalf("the catalogue holds %d types (%d integration), ADR 0004 + 0010 + 0013 + 0015 say 23 (13)", total, integration)
	}
	if len(covered) != total {
		t.Errorf("golden cases cover %d of %d published types", len(covered), total)
	}
	wantIntegration := []string{
		"yardvisit.VisitCheckedIn", "yardvisit.VisitRejected", "trailer.TrailerDocked", "trailer.TrailerUndocked",
		"trailer.SealExceptionRaised", "trailer.TrailerDwellExceeded", "yardcheck.YardCheckClosed",
		"trailer.DoorWorkCompleted", "trailer.TrailerDeparted", "trailer.TrailerSecured", "trailer.TrailerSecuringReleased",
		"inspection.InspectionCompleted", "inspection.InspectionFailed",
	}
	for _, name := range wantIntegration {
		parts := strings.SplitN(name, ".", 2)
		if integ, ok := outboundkafka.IsIntegration(parts[0], parts[1]); !ok || !integ {
			t.Errorf("%s must be an integration event", name)
		}
	}
}

func TestEncoderRefusesPoisonRows(t *testing.T) {
	enc := outboundkafka.NewEncoder()
	good := outbox.Row{ID: 1, EventID: eventID(0), AggregType: "yardmove", AggregID: "mv-1", OccurredAt: at(14, 0),
		Payload: []byte(`{"type":"YardMoveClaimed","data":{"move_id":"mv-1"}}`)}
	if _, cause, ok := enc.Encode(good); !ok {
		t.Fatalf("the control row must encode: %s", cause)
	}
	cases := []struct {
		name  string
		mod   func(*outbox.Row)
		cause string
	}{
		{"payload that is not JSON", func(r *outbox.Row) { r.Payload = []byte(`not json`) }, "malformed outbox payload"},
		{"an event the service does not publish", func(r *outbox.Row) { r.Payload = []byte(`{"type":"Bogus","data":{}}`) }, "not a published event"},
		{"an event of another aggregate", func(r *outbox.Row) { r.AggregType = "trailer" }, "not a published event"},
		{"an unknown aggregate", func(r *outbox.Row) { r.AggregType = "ghost" }, "not a published event"},
		{"an empty aggregate id", func(r *outbox.Row) { r.AggregID = "" }, "unencodable"},
		{"data that is not JSON", func(r *outbox.Row) { r.Payload = []byte(`{"type":"YardMoveClaimed","data":}`) }, "malformed outbox payload"},
		{"no data", func(r *outbox.Row) { r.Payload = []byte(`{"type":"YardMoveClaimed"}`) }, "has no data"},
		{"null data", func(r *outbox.Row) { r.Payload = []byte(`{"type":"YardMoveClaimed","data":null}`) }, "has no data"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row := good
			c.mod(&row)
			msgs, cause, ok := enc.Encode(row)
			if ok || len(msgs) != 0 {
				t.Fatalf("Encode accepted a poison row: %+v", msgs)
			}
			if !strings.Contains(cause, c.cause) {
				t.Errorf("cause = %q, want it to contain %q", cause, c.cause)
			}
		})
	}
}

func TestDeadLetterKeepsThePayloadVerbatim(t *testing.T) {
	enc := &outboundkafka.Encoder{DLQTopic: "dlq.test"}
	row := outbox.Row{EventID: "ev-1", AggregID: "mv-9", Payload: []byte(`{"type":"Bogus","data":{"x":1}}`)}
	m := enc.DeadLetter(row, "because")
	if m.Topic != "dlq.test" || string(m.Key) != "mv-9" || m.Subject != "mv-9" || m.EventID != "ev-1" || string(m.Value) != string(row.Payload) {
		t.Fatalf("dead letter = %+v", m)
	}
	headers := map[string]string{}
	for _, h := range m.Headers {
		headers[h.Key] = h.Value
	}
	if len(headers) != 2 || headers["content-type"] != "application/json" || headers["dlq-reason"] != "because" {
		t.Errorf("headers = %v", headers)
	}
	long := strings.Repeat("x", 2000)
	for _, h := range enc.DeadLetter(row, long).Headers {
		if h.Key == "dlq-reason" && len(h.Value) != 512 {
			t.Errorf("dlq-reason length = %d, want it cut to 512", len(h.Value))
		}
	}
	if got := (&outboundkafka.Encoder{}).DeadLetter(row, "x").Topic; got != outboundkafka.TopicDLQ {
		t.Errorf("zero encoder dlq topic = %q", got)
	}
}

func TestZeroEncoderFallsBackToTheFleetTopics(t *testing.T) {
	c := goldenCases()[0] // an integration type
	msgs, _, ok := (&outboundkafka.Encoder{}).Encode(rowOf(t, 0, c))
	if !ok || len(msgs) != 2 || msgs[0].Topic != outboundkafka.TopicEvents || msgs[1].Topic != outboundkafka.TopicAnalytics {
		t.Fatalf("msgs = %+v", msgs)
	}
	custom := &outboundkafka.Encoder{EventsTopic: "e.test", AnalyticsTopic: "a.test"}
	msgs, _, _ = custom.Encode(rowOf(t, 0, c))
	if msgs[0].Topic != "e.test" || msgs[1].Topic != "a.test" {
		t.Fatalf("custom topics = %q %q", msgs[0].Topic, msgs[1].Topic)
	}
}

func TestTopicsAreTheEventsAnalyticsAndDLQTopics(t *testing.T) {
	want := []string{"warehouse.yard-management.events", "warehouse.yard-management.analytics", "warehouse.yard-management.events.dlq"}
	if got := outboundkafka.Topics(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Topics() = %v, want %v", got, want)
	}
	if outboundkafka.Partitions != 8 {
		t.Fatalf("partitions = %d, want 8", outboundkafka.Partitions)
	}
}

func TestIsIntegrationOutsideTheCatalogue(t *testing.T) {
	if _, ok := outboundkafka.IsIntegration("trailer", "Bogus"); ok {
		t.Fatal("an unknown type is not in the catalogue")
	}
}
