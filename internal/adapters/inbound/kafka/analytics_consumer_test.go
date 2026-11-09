package kafka_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/adapters/inbound/kafka"
	"github.com/claudioed/yard-management/internal/analytics/report"
)

const yardSource = "/warehouse/yard-management"

// The seven original analytics payloads the projection reads, as the outbound encoder
// writes them (internal/adapters/outbound/kafka goldens).
const (
	aVisitData = `{"visit_id":"visit-1","site_id":"WH1","carrier":"Acme Freight","carrier_scac":"ACME","purpose":"LIVE_UNLOAD","appointment_id":"appt-1",` +
		`"trailers":[{"trailer_id":"trl-1","unit_number":"T100","carrier_scac":"ACME","kind":"DRY_VAN","load_state":"LOADED"},` +
		`{"trailer_id":"trl-2","unit_number":"R200","carrier_scac":null,"kind":"REEFER","load_state":"EMPTY"}],"arrived_at":"2026-11-05T14:00:00Z","version":1}`
	aDockedData     = `{"trailer_id":"trl-1","site_id":"WH1","door_code":"WH1-D1","appointment_id":"appt-1","asn_numbers":["ASN-1"],"visit_id":"visit-1","docked_at":"2026-11-05T14:20:00Z","version":3}`
	aUndockedData   = `{"trailer_id":"trl-1","site_id":"WH1","door_code":"WH1-D1","appointment_id":null,"undocked_at":"2026-11-05T15:10:00Z","version":5}`
	aStartedData    = `{"trailer_id":"trl-1","site_id":"WH1","door_code":"WH1-D1","appointment_id":"appt-1","started_at":"2026-11-05T14:25:00Z","version":4}`
	aCompletedData  = `{"trailer_id":"trl-1","site_id":"WH1","door_code":"WH1-D1","appointment_id":"appt-1","completed_at":"2026-11-05T15:00:00Z","load_state":"EMPTY","version":5}`
	aDepartedData   = `{"trailer_id":"trl-1","site_id":"WH1","visit_id":"visit-1","departed_at":"2026-11-05T15:35:00Z","dwell_minutes":95,"load_state":"EMPTY","seal_number":"SL-1","version":7}`
	aMoveData       = `{"move_id":"mv-1","site_id":"WH1","trailer_id":"trl-1","performer":"HOSTLER","hostler_ref":"h-7","from_location":{"kind":"SPOT","code":"WH1-P1"},"to_location":{"kind":"DOOR","code":"WH1-D1"},"actual_location":{"kind":"DOOR","code":"WH1-D1"},"reason":"TO_DOOR","completed_at":"2026-11-05T14:19:00Z","duration_seconds":420,"version":3}`
	aDriverMoveData = `{"move_id":"mv-0","site_id":"WH1","trailer_id":"trl-1","performer":"VISITING_DRIVER","hostler_ref":null,"from_location":null,"to_location":{"kind":"SPOT","code":"WH1-H1"},"actual_location":{"kind":"SPOT","code":"WH1-H1"},"reason":"TO_HOLDING","completed_at":"2026-11-05T14:06:00Z","duration_seconds":360,"version":2}`
)

func yardType(entity, name string) string {
	return "com.warehouse.wms.yard-management." + entity + "." + name
}

func yardEvent(t *testing.T, id, entity, name, subject, data string) []byte {
	t.Helper()
	return envelope(t, id, yardSource, yardType(entity, name), subject, "analytics", data)
}

// fakeProjection records the events it was given and returns err for every one
// of them when set.
type fakeProjection struct {
	mu     sync.Mutex
	events []report.Event
	err    error
}

func (p *fakeProjection) Apply(_ context.Context, e report.Event) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return false, p.err
	}
	p.events = append(p.events, e)
	return true, nil
}

func (p *fakeProjection) applied() []report.Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]report.Event(nil), p.events...)
}

func analyticsConsumer(t *testing.T, k *fakeKafka, p report.Projection) *kafka.Consumer {
	t.Helper()
	c, err := kafka.NewAnalyticsConsumer(k.factory(), "yard-projector", p, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	c.Retry = fastRetry
	return c
}

func ymAt(h, m int) time.Time { return time.Date(2026, 11, 5, h, m, 0, 0, time.UTC) }

func TestAnalyticsConsumerReadsItsOwnTopicUnderTheGivenGroup(t *testing.T) {
	k := newFakeKafka()
	analyticsConsumer(t, k, &fakeProjection{})
	if k.readerTopic != "warehouse.yard-management.analytics" || k.readerGroup != "yard-projector" || k.dlqTopic != "warehouse.yard-management.analytics" {
		t.Fatalf("topic %q group %q dlq source %q", k.readerTopic, k.readerGroup, k.dlqTopic)
	}
}

func TestAnalyticsConsumerRefusesABlankGroupNamingTheVariable(t *testing.T) {
	_, err := kafka.NewAnalyticsConsumer(newFakeKafka().factory(), " ", &fakeProjection{}, nil)
	if !errors.Is(err, kafka.ErrGroupRequired) || !strings.Contains(err.Error(), "ANALYTICS_CONSUMER_GROUP") {
		t.Fatalf("err = %v", err)
	}
}

func TestAnalyticsConsumerProjectsTheOriginalSevenKindsFromTheEncodersPayloads(t *testing.T) {
	k := newFakeKafka(
		yardEvent(t, "e1", "yardvisit", "VisitCheckedIn", "visit-1", aVisitData),
		yardEvent(t, "e2", "trailer", "TrailerDocked", "trl-1", aDockedData),
		yardEvent(t, "e3", "trailer", "DoorWorkStarted", "trl-1", aStartedData),
		yardEvent(t, "e4", "trailer", "DoorWorkCompleted", "trl-1", aCompletedData),
		yardEvent(t, "e5", "trailer", "TrailerUndocked", "trl-1", aUndockedData),
		yardEvent(t, "e6", "trailer", "TrailerDeparted", "trl-1", aDepartedData),
		yardEvent(t, "e7", "yardmove", "YardMoveCompleted", "mv-1", aMoveData),
		yardEvent(t, "e8", "yardmove", "YardMoveCompleted", "mv-0", aDriverMoveData),
	)
	p := &fakeProjection{}
	drain(t, analyticsConsumer(t, k, p))
	got := p.applied()
	if len(got) != 8 {
		t.Fatalf("%d events applied, want 8", len(got))
	}
	assertVisitAndDoorFacts(t, got)
	assertDoorFacts(t, got)
	assertDepartureAndMoveFacts(t, got)
	if c := k.reader.committed(); len(c) != 8 {
		t.Errorf("commits %v, want all eight", c)
	}
}

func assertVisitAndDoorFacts(t *testing.T, got []report.Event) {
	t.Helper()
	visit := got[0]
	if visit.Kind != report.KindVisitCheckedIn || visit.VisitID != "visit-1" || visit.SiteID != "WH1" || !visit.ArrivedAt.Equal(ymAt(14, 0)) || len(visit.Arrivals) != 2 ||
		visit.Arrivals[1] != (report.Arrival{TrailerID: "trl-2", UnitNumber: "R200", Kind: "REEFER"}) {
		t.Errorf("VisitCheckedIn mapped to %+v", visit)
	}
	if d := got[1]; d.Kind != report.KindTrailerDocked || d.TrailerID != "trl-1" || d.DoorCode != "WH1-D1" || !d.DockedAt.Equal(ymAt(14, 20)) || d.EventID != "e2" || d.At.IsZero() {
		t.Errorf("TrailerDocked mapped to %+v", d)
	}
}

func assertDoorFacts(t *testing.T, got []report.Event) {
	t.Helper()
	if s := got[2]; s.Kind != report.KindDoorWorkStarted || !s.StartedAt.Equal(ymAt(14, 25)) {
		t.Errorf("DoorWorkStarted mapped to %+v", s)
	}
	if c := got[3]; c.Kind != report.KindDoorWorkCompleted || !c.CompletedAt.Equal(ymAt(15, 0)) {
		t.Errorf("DoorWorkCompleted mapped to %+v", c)
	}
}

func assertDepartureAndMoveFacts(t *testing.T, got []report.Event) {
	t.Helper()
	if u := got[4]; u.Kind != report.KindTrailerUndocked || !u.UndockedAt.Equal(ymAt(15, 10)) {
		t.Errorf("TrailerUndocked mapped to %+v", u)
	}
	if d := got[5]; d.Kind != report.KindTrailerDeparted || d.DwellMinutes != 95 || !d.DepartedAt.Equal(ymAt(15, 35)) {
		t.Errorf("TrailerDeparted mapped to %+v", d)
	}
	if m := got[6]; !m.IsHostlerMove() || m.MoveID != "mv-1" || m.HostlerRef != "h-7" || m.DurationSeconds != 420 || !m.CompletedAt.Equal(ymAt(14, 19)) {
		t.Errorf("hostler YardMoveCompleted mapped to %+v", m)
	}
	if m := got[7]; m.IsHostlerMove() || m.Performer != "VISITING_DRIVER" || m.HostlerRef != "" {
		t.Errorf("visiting driver YardMoveCompleted mapped to %+v", m)
	}
}

func TestAnalyticsConsumerIgnoresOtherTypesAndSkipsNonCloudEvents(t *testing.T) {
	k := newFakeKafka(
		yardEvent(t, "e1", "trailer", "TrailerParked", "trl-1", `{"trailer_id":"trl-1","site_id":"WH1","spot_code":"WH1-P1","version":2}`),
		yardEvent(t, "e2", "yardmove", "YardMoveRequested", "mv-1", `{"move_id":"mv-1"}`),
		[]byte(`{"not":"a cloud event"}`),
		[]byte(``),
	)
	p := &fakeProjection{}
	drain(t, analyticsConsumer(t, k, p))
	if len(p.applied()) != 0 || len(k.dlq.letters()) != 0 || len(k.reader.committed()) != 4 {
		t.Fatalf("applied %v dead letters %v commits %v: ignored and skipped messages are committed past, never dead-lettered", p.applied(), k.dlq.letters(), k.reader.committed())
	}
}

func TestAnalyticsConsumerDeadLettersEveryClassOfPoison(t *testing.T) {
	cases := map[string][]byte{
		"data that is not an object":   yardEvent(t, "p1", "trailer", "TrailerDocked", "trl-1", `"nope"`),
		"an unparseable time":          yardEvent(t, "p2", "trailer", "TrailerDocked", "trl-1", `{"trailer_id":"trl-1","site_id":"WH1","door_code":"D","docked_at":"yesterday"}`),
		"no site":                      yardEvent(t, "p3", "trailer", "TrailerDocked", "trl-1", `{"trailer_id":"trl-1","door_code":"D","docked_at":"2026-11-05T14:20:00Z"}`),
		"no door":                      yardEvent(t, "p4", "trailer", "TrailerDocked", "trl-1", `{"trailer_id":"trl-1","site_id":"WH1","docked_at":"2026-11-05T14:20:00Z"}`),
		"a subject that is not the id": yardEvent(t, "p5", "trailer", "TrailerDocked", "someone-else", aDockedData),
		"a kind that does not exist":   yardEvent(t, "p6", "yardvisit", "VisitCheckedIn", "visit-1", `{"visit_id":"visit-1","site_id":"WH1","trailers":[{"trailer_id":"t","kind":"TANKER"}],"arrived_at":"2026-11-05T14:00:00Z"}`),
		"a negative dwell":             yardEvent(t, "p7", "trailer", "TrailerDeparted", "trl-1", `{"trailer_id":"trl-1","site_id":"WH1","departed_at":"2026-11-05T15:35:00Z","dwell_minutes":-3}`),
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			k := newFakeKafka(value)
			p := &fakeProjection{}
			drain(t, analyticsConsumer(t, k, p))
			if len(p.applied()) != 0 || len(k.dlq.letters()) != 1 || len(k.reader.committed()) != 1 {
				t.Fatalf("applied %v letters %v commits %v: poison goes to the DLQ and is committed past", p.applied(), k.dlq.letters(), k.reader.committed())
			}
			if l := k.dlq.letters()[0]; l.sourceTopic != "warehouse.yard-management.analytics" || string(l.value) != string(value) || l.cause == "" {
				t.Errorf("dead letter %+v", l)
			}
		})
	}
}

func TestAnalyticsConsumerDeadLettersWhatTheStoreRejectsButRetriesWhatIsTransient(t *testing.T) {
	value := yardEvent(t, "e1", "trailer", "TrailerDocked", "trl-1", aDockedData)

	k := newFakeKafka(value)
	drain(t, analyticsConsumer(t, k, &fakeProjection{err: fmt.Errorf("%w: SQLSTATE 22003", report.ErrRejected)}))
	if len(k.dlq.letters()) != 1 {
		t.Fatalf("a store rejection is poison: %v", k.dlq.letters())
	}

	// A transient failure retries the SAME message until the store recovers.
	k = newFakeKafka(value)
	p := &transientProjection{failures: 3}
	drain(t, analyticsConsumer(t, k, p))
	if p.calls != 4 || len(k.dlq.letters()) != 0 || len(k.reader.committed()) != 1 {
		t.Fatalf("calls %d dead letters %v commits %v: three transient failures, then success, never dead-lettered", p.calls, k.dlq.letters(), k.reader.committed())
	}
}

// transientProjection fails with a plain error the first `failures` calls.
type transientProjection struct {
	failures, calls int
}

func (p *transientProjection) Apply(context.Context, report.Event) (bool, error) {
	p.calls++
	if p.calls <= p.failures {
		return false, errors.New("connection reset by peer")
	}
	return true, nil
}
