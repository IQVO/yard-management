package kafka_test

import (
	"testing"

	"github.com/claudioed/yard-management/internal/analytics/report"
)

// The five KPI standard payloads (ADR 0011), as apis/asyncapi.yaml writes them.
const (
	aCheckedOutData = `{"visit_id":"visit-1","site_id":"WH1","trailers_out":["trl-1"],"departed_at":"2026-11-05T15:40:00Z","on_site_minutes":98,"version":2}`
	aRejectedData   = `{"visit_id":"visit-9","site_id":"WH1","carrier_scac":"ACME","appointment_id":null,"reason":"APPOINTMENT_REQUIRED","rejected_at":"2026-11-05T14:05:00Z"}`
	aSealData       = `{"trailer_id":"trl-1","site_id":"WH1","appointment_id":"appt-1","expected_seal_number":"SL-778","found_seal_number":"SL-779","raised_at":"2026-11-05T14:03:00Z"}`
	aSealGateData   = `{"trailer_id":null,"site_id":"WH1","appointment_id":"appt-1","expected_seal_number":"SL-778","found_seal_number":"SL-779","raised_at":"2026-11-05T14:04:00Z"}`
	aHeldData       = `{"trailer_id":"trl-1","site_id":"WH1","reason":"DAMAGED","version":4}`
	aYardCheckData  = `{"yard_check_id":"yck-1","site_id":"WH1","closed_at":"2026-11-05T16:00:00Z","discrepancies":[` +
		`{"kind":"MISSING","location_code":"WH1-YARD-P014","trailer_id":"trl-1","unit_number":"TR-1042"},` +
		`{"kind":"UNEXPECTED","location_code":"WH1-YARD-P015","trailer_id":null,"unit_number":null}]}`
	aYardCheckCleanData = `{"yard_check_id":"yck-2","site_id":"WH1","closed_at":"2026-11-05T17:00:00Z","discrepancies":[]}`
)

func TestAnalyticsConsumerProjectsTheFiveKPIKindsFromTheContractPayloads(t *testing.T) {
	k := newFakeKafka(
		yardEvent(t, "k1", "yardvisit", "VisitCheckedOut", "visit-1", aCheckedOutData),
		yardEvent(t, "k2", "yardvisit", "VisitRejected", "visit-9", aRejectedData),
		yardEvent(t, "k3", "trailer", "SealExceptionRaised", "trl-1", aSealData),
		yardEvent(t, "k4", "trailer", "SealExceptionRaised", "visit-9", aSealGateData),
		yardEvent(t, "k5", "trailer", "TrailerHeld", "trl-1", aHeldData),
		yardEvent(t, "k6", "yardcheck", "YardCheckClosed", "yck-1", aYardCheckData),
		yardEvent(t, "k7", "yardcheck", "YardCheckClosed", "yck-2", aYardCheckCleanData),
		yardEvent(t, "k8", "yardvisit", "VisitCheckedIn", "visit-1", aVisitData),
	)
	p := &fakeProjection{}
	drain(t, analyticsConsumer(t, k, p))
	got := p.applied()
	if len(got) != 8 || len(k.dlq.letters()) != 0 || len(k.reader.committed()) != 8 {
		t.Fatalf("%d events applied, %d dead letters, %d commits, want 8, 0, 8", len(got), len(k.dlq.letters()), len(k.reader.committed()))
	}
	assertVisitAndGateFacts(t, got)
	assertSealFacts(t, got)
	assertHoldAndAuditFacts(t, got)
}

func assertVisitAndGateFacts(t *testing.T, got []report.Event) {
	t.Helper()
	if o := got[0]; o.Kind != report.KindVisitCheckedOut || o.VisitID != "visit-1" || o.OnSiteMinutes != 98 || !o.DepartedAt.Equal(ymAt(15, 40)) || o.EventID != "k1" {
		t.Errorf("VisitCheckedOut mapped to %+v", o)
	}
	if r := got[1]; r.Kind != report.KindVisitRejected || r.VisitID != "visit-9" || r.Reason != "APPOINTMENT_REQUIRED" || !r.RejectedAt.Equal(ymAt(14, 5)) {
		t.Errorf("VisitRejected mapped to %+v", r)
	}
}

func assertSealFacts(t *testing.T, got []report.Event) {
	t.Helper()
	if s := got[2]; s.Kind != report.KindSealExceptionRaised || s.TrailerID != "trl-1" || s.Subject() != "trl-1" || !s.RaisedAt.Equal(ymAt(14, 3)) {
		t.Errorf("SealExceptionRaised mapped to %+v", s)
	}
	if s := got[3]; s.TrailerID != "" || s.VisitID != "visit-9" || s.Subject() != "visit-9" || !s.RaisedAt.Equal(ymAt(14, 4)) {
		t.Errorf("a seal exception at the gate (null trailer) mapped to %+v, want the visit as subject", s)
	}
}

func assertHoldAndAuditFacts(t *testing.T, got []report.Event) {
	t.Helper()
	if h := got[4]; h.Kind != report.KindTrailerHeld || h.TrailerID != "trl-1" || h.Reason != "DAMAGED" || h.Version != 4 {
		t.Errorf("TrailerHeld mapped to %+v", h)
	}
	if y := got[5]; y.Kind != report.KindYardCheckClosed || y.YardCheckID != "yck-1" || y.Discrepancies != 2 || !y.ClosedAt.Equal(ymAt(16, 0)) {
		t.Errorf("YardCheckClosed mapped to %+v, want the two discrepancies counted", y)
	}
	if y := got[6]; y.YardCheckID != "yck-2" || y.Discrepancies != 0 {
		t.Errorf("a clean audit mapped to %+v", y)
	}
	if v := got[7]; v.Kind != report.KindVisitCheckedIn || v.Purpose != "LIVE_UNLOAD" {
		t.Errorf("VisitCheckedIn must carry the purpose, got %+v", v)
	}
}

func TestAnalyticsConsumerDeadLettersPoisonOfTheKPIKinds(t *testing.T) {
	cases := map[string][]byte{
		"checked out with negative minutes":           yardEvent(t, "q1", "yardvisit", "VisitCheckedOut", "visit-1", `{"visit_id":"visit-1","site_id":"WH1","departed_at":"2026-11-05T15:40:00Z","on_site_minutes":-4}`),
		"checked out without an instant":              yardEvent(t, "q2", "yardvisit", "VisitCheckedOut", "visit-1", `{"visit_id":"visit-1","site_id":"WH1","on_site_minutes":4}`),
		"checked out with another subject":            yardEvent(t, "q3", "yardvisit", "VisitCheckedOut", "visit-2", aCheckedOutData),
		"rejected without a reason":                   yardEvent(t, "q4", "yardvisit", "VisitRejected", "visit-9", `{"visit_id":"visit-9","site_id":"WH1","rejected_at":"2026-11-05T14:05:00Z"}`),
		"a seal exception with an unparseable time":   yardEvent(t, "q5", "trailer", "SealExceptionRaised", "trl-1", `{"trailer_id":"trl-1","site_id":"WH1","raised_at":"soon"}`),
		"a hold with an unknown reason":               yardEvent(t, "q6", "trailer", "TrailerHeld", "trl-1", `{"trailer_id":"trl-1","site_id":"WH1","reason":"BORED","version":4}`),
		"a hold without a version":                    yardEvent(t, "q7", "trailer", "TrailerHeld", "trl-1", `{"trailer_id":"trl-1","site_id":"WH1","reason":"DAMAGED"}`),
		"a yard check whose discrepancies is a count": yardEvent(t, "q8", "yardcheck", "YardCheckClosed", "yck-1", `{"yard_check_id":"yck-1","site_id":"WH1","closed_at":"2026-11-05T16:00:00Z","discrepancies":2}`),
		"a yard check with another subject":           yardEvent(t, "q9", "yardcheck", "YardCheckClosed", "yck-9", aYardCheckData),
		"a yard check without a site":                 yardEvent(t, "q10", "yardcheck", "YardCheckClosed", "yck-1", `{"yard_check_id":"yck-1","closed_at":"2026-11-05T16:00:00Z","discrepancies":[]}`),
		"a visit with a purpose that does not exist":  yardEvent(t, "q11", "yardvisit", "VisitCheckedIn", "visit-1", `{"visit_id":"visit-1","site_id":"WH1","purpose":"SIGHTSEEING","trailers":[],"arrived_at":"2026-11-05T14:00:00Z"}`),
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			k := newFakeKafka(value)
			p := &fakeProjection{}
			drain(t, analyticsConsumer(t, k, p))
			if len(p.applied()) != 0 || len(k.dlq.letters()) != 1 || len(k.reader.committed()) != 1 {
				t.Fatalf("applied %v letters %v commits %v: poison goes to the DLQ and is committed past", p.applied(), k.dlq.letters(), k.reader.committed())
			}
		})
	}
}

func TestAnalyticsConsumerStillIgnoresTheKindsItDoesNotFold(t *testing.T) {
	k := newFakeKafka(
		yardEvent(t, "i1", "trailer", "TrailerHoldReleased", "trl-1", `{"trailer_id":"trl-1","site_id":"WH1","version":5}`),
		yardEvent(t, "i2", "trailer", "TrailerDwellExceeded", "trl-1", `{"trailer_id":"trl-1","site_id":"WH1"}`),
	)
	p := &fakeProjection{}
	drain(t, analyticsConsumer(t, k, p))
	if len(p.applied()) != 0 || len(k.dlq.letters()) != 0 || len(k.reader.committed()) != 2 {
		t.Fatalf("applied %v letters %v commits %v", p.applied(), k.dlq.letters(), k.reader.committed())
	}
}
