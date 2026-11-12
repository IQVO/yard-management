package kafka_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/claudioed/yard-management/internal/adapters/inbound/kafka"
	"github.com/claudioed/yard-management/internal/application/views"
)

// The carrier registry events (ADR 0016): the producer's own AsyncAPI examples,
// keyed by SCAC, reach the use cases with the CloudEvents id and time; a null
// reason or timestamp arrives as the zero value (the tolerant-reader rule).

func TestDockSchedulingCarrierExamplesMapToTheCarrierCopy(t *testing.T) {
	tests := []struct {
		name string
		msg  []byte
		want []string
	}{
		{"registered", dsCarrierRegistered(t), []string{
			"carrier-registered 00001100-1111-4222-8333-444455556666 ACMF 2026-11-05T12:00:00Z"}},
		{"suspended", dsCarrierSuspended(t), []string{
			"carrier-suspended 00001102-1111-4222-8333-444455556666 ACMF insurance lapsed 2 2026-11-05T12:00:00Z"}},
		{"reinstated", dsCarrierReinstated(t), []string{
			"carrier-reinstated 00001104-1111-4222-8333-444455556666 ACMF 3 2026-11-05T12:00:00Z"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, uc, fk := newConsumerFor(t, "dock-scheduling", tc.msg)
			drain(t, c)
			if got := uc.log(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("calls\n got %v\nwant %v", got, tc.want)
			}
			if got := fk.reader.committed(); !reflect.DeepEqual(got, []int64{0}) {
				t.Fatalf("committed %v", got)
			}
		})
	}
	// The whole registered snapshot, with the null reason and suspended_at of
	// the example arrived as the zero values.
	c, uc, _ := newConsumerFor(t, "dock-scheduling", dsCarrierRegistered(t))
	drain(t, c)
	want := views.Carrier{
		SCAC: exampleSCAC, Name: "ACME Freight", USDOT: "1234567", MC: "MC654321",
		Status: views.CarrierActive, Version: 1,
	}
	if len(uc.carrs) != 1 || !reflect.DeepEqual(uc.carrs[0], want) {
		t.Fatalf("carrier\n got %+v\nwant %+v", uc.carrs, want)
	}
}

func TestCarrierPoisonPayloadsAreDeadLetteredAtOnceAndCommittedPast(t *testing.T) {
	tests := []struct {
		name  string
		msg   []byte
		cause string
	}{
		{"registered without scac", envelope(t, "p", dockSchedulingSource, typeCarrierRegistered, exampleSCAC, "events",
			`{"name":"ACME Freight","status":"ACTIVE","version":1}`), "scac"},
		{"registered with a blank scac", envelope(t, "p", dockSchedulingSource, typeCarrierRegistered, exampleSCAC, "events",
			`{"scac":"   ","status":"ACTIVE","version":1}`), "scac"},
		{"registered with version 0", envelope(t, "p", dockSchedulingSource, typeCarrierRegistered, exampleSCAC, "events",
			`{"scac":"ACMF","status":"ACTIVE","version":0}`), "version"},
		{"suspended without scac", envelope(t, "p", dockSchedulingSource, typeCarrierSuspended, exampleSCAC, "events",
			`{"reason":"insurance lapsed","version":2}`), "scac"},
		{"suspended with version 0", envelope(t, "p", dockSchedulingSource, typeCarrierSuspended, exampleSCAC, "events",
			`{"scac":"ACMF","version":0}`), "version"},
		{"reinstated without scac", envelope(t, "p", dockSchedulingSource, typeCarrierReinstated, exampleSCAC, "events",
			`{"reinstated_at":"2026-11-10T16:00:00Z","version":3}`), "scac"},
		{"reinstated with version 0", envelope(t, "p", dockSchedulingSource, typeCarrierReinstated, exampleSCAC, "events",
			`{"scac":"ACMF","version":0}`), "version"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, uc, fk := newConsumerFor(t, "dock-scheduling", tc.msg)
			drain(t, c)
			letters := fk.dlq.letters()
			if len(letters) != 1 || string(letters[0].value) != string(tc.msg) || letters[0].sourceTopic != kafka.DockSchedulingTopic ||
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
