package trailer_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
)

var t0 = time.Date(2026, 11, 5, 14, 0, 0, 0, time.UTC)

func at(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }

func arrival() trailer.ArrivalInput {
	return trailer.ArrivalInput{
		ID: "trl-1", SiteID: "WH1", SCAC: "ACME", Number: "TR-1042", Kind: shared.KindDryVan, Load: shared.LoadLoaded,
		ASNNumbers: []string{"ASN-1"}, Purpose: shared.PurposeLiveUnload, VisitID: "visit-1", AppointmentID: "appt-1", At: t0,
	}
}

func newTrailer(t *testing.T, mod ...func(*trailer.ArrivalInput)) *trailer.Trailer {
	t.Helper()
	in := arrival()
	for _, m := range mod {
		m(&in)
	}
	tr, err := trailer.RecordArrival(in)
	if err != nil {
		t.Fatalf("RecordArrival: %v", err)
	}
	return tr
}

var (
	spot1 = shared.Location{Kind: shared.Spot, Code: "WH1-P1"}
	spot2 = shared.Location{Kind: shared.Spot, Code: "WH1-P2"}
	door1 = shared.Location{Kind: shared.Door, Code: "WH1-D1"}
)

func slotOf(loc shared.Location) trailer.Slot {
	return trailer.Slot{Location: loc, Site: "WH1", Active: true, DockFlow: trailer.FlowBoth}
}

func placeAt(loc shared.Location) trailer.Placement {
	return trailer.Placement{Target: loc, Slot: slotOf(loc)}
}

// parked returns a trailer arrived and parked on spot1.
func parked(t *testing.T, mod ...func(*trailer.ArrivalInput)) *trailer.Trailer {
	t.Helper()
	tr := newTrailer(t, mod...)
	if err := tr.ArriveAt(placeAt(spot1), at(1)); err != nil {
		t.Fatalf("ArriveAt spot: %v", err)
	}
	tr.PullEvents()
	return tr
}

// docked returns a trailer arrived and docked at door1 (version 2, door work WAITING).
func docked(t *testing.T, mod ...func(*trailer.ArrivalInput)) *trailer.Trailer {
	t.Helper()
	tr := newTrailer(t, mod...)
	if err := tr.ArriveAt(placeAt(door1), at(1)); err != nil {
		t.Fatalf("ArriveAt door: %v", err)
	}
	tr.PullEvents()
	return tr
}

// worked returns a docked trailer whose door work is DONE.
func worked(t *testing.T) *trailer.Trailer {
	t.Helper()
	tr := docked(t)
	must(t, tr.StartDoorWork(at(2)))
	must(t, tr.CompleteDoorWork(shared.LoadEmpty, at(3)))
	tr.PullEvents()
	return tr
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// data marshals an event the way the outbox does and returns it as a map.
func data(t *testing.T, e shared.Event) map[string]any {
	t.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// only returns the single pending event of tr.
func only(t *testing.T, tr *trailer.Trailer) shared.Event {
	t.Helper()
	ev := tr.PullEvents()
	if len(ev) != 1 {
		t.Fatalf("want exactly 1 event, got %d: %+v", len(ev), ev)
	}
	return ev[0]
}

// eq compares an event's data with the exact expected map (key set included).
func eq(t *testing.T, got, want map[string]any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("data differs:\n got %v\nwant %v", got, want)
	}
}

// header asserts the identity of an event.
func header(t *testing.T, e shared.Event, name string, when time.Time) {
	t.Helper()
	if e.EventName() != name || e.Entity() != "trailer" || e.AggregateID() != "trl-1" || !e.OccurredAt().Equal(when) {
		t.Fatalf("header %s/%s/%s/%v, want %s at %v", e.EventName(), e.Entity(), e.AggregateID(), e.OccurredAt(), name, when)
	}
}
