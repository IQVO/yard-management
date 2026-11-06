//go:build integration

package kafka_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/testing/kafkatest"
)

// facilityStream is the topic of TestFacilityConsumerEndToEnd, in order. Its
// comments give the expected fate of each message.
func facilityStream(t *testing.T) (msgs [][]byte, poison []byte) {
	t.Helper()
	spotSlot := flSlot(t, "ev-spot", "WH1-SPOT-01", "WH1-YARD", "Yard", "")
	poison = envelope(t, "ev-poison", facilityLayoutSource, typeFLZoneRegistered, "WH1-BAD", "events", `{"zoneId":"WH1-BAD"}`)
	msgs = [][]byte{
		flSlot(t, "ev-door", "WH1-DOCK-IN-01", "WH1-DOCK", "Dock", "Inbound"), // 0: a door BEFORE its zone: pending (X5)
		spotSlot, // 1
		flSlot(t, "ev-stor", "WH1-STOR-01", "WH1-STOR", "Storage", ""), // 2: not the yard's business
		spotSlot, // 3: redelivery of 1 (same CloudEvents id)
		flZone(t, "ev-zone-dock", "WH1-DOCK", "WH1"), // 4
		flZone(t, "ev-zone-yard", "WH1-YARD", "WH1"), // 5
		envelope(t, "ev-site", facilityLayoutSource, typeFLSiteRegistered, "WH1", "events", `{"siteCode":"WH1","siteName":"Main","siteRole":"UNSPECIFIED"}`), // 6: unknown type
		[]byte("this is not a cloud event"), // 7: skipped
		poison,                              // 8: dead-lettered
		flDecommissioned(t, "ev-decom", "WH1-SPOT-01"), // 9
	}
	return msgs, poison
}

func assertFacilityCopies(t *testing.T, w *world) {
	t.Helper()
	ctx := context.Background()
	door, ok, err := w.locations.Get(ctx, "WH1-DOCK-IN-01")
	if err != nil || !ok || door.SiteCode != "WH1" || door.Kind != shared.Door || door.DockFlow != "Inbound" || !door.Active {
		t.Fatalf("door %+v ok=%v err=%v: the zone resolves the pending door's site", door, ok, err)
	}
	spot, ok, err := w.locations.Get(ctx, "WH1-SPOT-01")
	if err != nil || !ok || spot.SiteCode != "WH1" || spot.Kind != shared.Spot || spot.Active {
		t.Fatalf("spot %+v ok=%v err=%v: resolved and decommissioned", spot, ok, err)
	}
	if _, ok, _ := w.locations.Get(ctx, "WH1-STOR-01"); ok {
		t.Fatal("a Storage slot entered the location copy")
	}
	// claims: door, spot, zone-dock, zone-yard, decommission (the redelivery, the
	// Storage slot, the unknown type, the junk and the poison claim nothing)
	if n := w.count(t, "processed_events"); n != 5 {
		t.Fatalf("processed_events = %d, want 5", n)
	}
}

// assertDeadLettered reads the first message of the dead letter topic and
// checks it is the original, untouched, with its source topic and cause.
func assertDeadLettered(t *testing.T, dlq, sourceTopic string, original []byte, cause string) {
	t.Helper()
	letter := kafkatest.ReadN(t, dlq, 1, 30*time.Second)[0]
	if string(letter.Value) != string(original) || letter.Headers["x-dlq-source-topic"] != sourceTopic ||
		!strings.Contains(letter.Headers["x-dlq-cause"], cause) || letter.Key != "k" {
		t.Fatalf("dead letter %+v: want the original untouched, with source topic %s and a cause containing %q", letter, sourceTopic, cause)
	}
}

func TestFacilityConsumerEndToEnd(t *testing.T) {
	w := newWorld(t)
	topic, dlq := topics(t, "warehouse.facility.events")
	group := theGroup("facility")
	msgs, poison := facilityStream(t)
	produce(t, topic, "k", msgs...)

	startConsumer(t, w.facilityConsumer(t, topic, group))
	committedPast(t, group, topic, int64(len(msgs)))
	assertFacilityCopies(t, w)
	assertDeadLettered(t, dlq, topic, poison, "siteCode")

	// Replay: a new group re-reads the whole topic; the inbox makes it a no-op
	// (a replayed Registered must not reactivate the decommissioned spot).
	replay := theGroup("facility-replay")
	startConsumer(t, w.facilityConsumer(t, topic, replay))
	committedPast(t, replay, topic, int64(len(msgs)))
	assertFacilityCopies(t, w)
}
