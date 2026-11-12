//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/adapters/outbound/postgres"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
)

func carrierAt(s string) time.Time {
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return v.UTC()
}

func carrierOf(t *testing.T, feed *postgres.Carriers, scac string) views.Carrier {
	t.Helper()
	c, ok, err := feed.Get(context.Background(), scac)
	if err != nil || !ok {
		t.Fatalf("carrier %s: ok=%v err=%v", scac, ok, err)
	}
	c.UpdatedAt = c.UpdatedAt.UTC() // pgx scans timestamptz in the session's zone
	return c
}

// The CarrierView copy (ADR 0016) against a real Postgres: Register replaces
// the whole snapshot, only a NEWER version ever takes effect, Suspend keeps
// the name and identifiers, and Reinstate clears the suspension reason.
func TestCarrierCopyFollowsRegisterSuspendReinstate(t *testing.T) {
	feed := postgres.NewCarriers(pgtest.NewPool(t))
	ctx := context.Background()

	// An unknown SCAC misses.
	if _, ok, err := feed.Get(ctx, "ACMF"); err != nil || ok {
		t.Fatalf("unknown carrier: ok=%v err=%v", ok, err)
	}

	// Register: the whole snapshot, nulls stored as ''.
	reg := views.Carrier{
		SCAC: "ACMF", Name: "ACME Freight", USDOT: "1234567", MC: "MC654321",
		Status: views.CarrierActive, Version: 1, UpdatedAt: carrierAt("2026-11-10T15:00:00Z"),
	}
	if err := feed.Register(ctx, reg); err != nil {
		t.Fatal(err)
	}
	if got := carrierOf(t, feed, "ACMF"); got != reg {
		t.Fatalf("after Register\n got %+v\nwant %+v", got, reg)
	}

	// A NEWER registration replaces the snapshot; a stale one changes nothing.
	newer := reg
	newer.Name, newer.Version = "ACME Freight Ltd", 4
	if err := feed.Register(ctx, newer); err != nil {
		t.Fatal(err)
	}
	stale := reg
	stale.Name = "ACME Freight SE"
	if err := feed.Register(ctx, stale); err != nil {
		t.Fatal(err)
	}
	got := carrierOf(t, feed, "ACMF")
	if got.Name != "ACME Freight Ltd" || got.Version != 4 {
		t.Fatalf("a stale registration overwrote the newer one: %+v", got)
	}

	// Suspend: the standing, the reason and the bookkeeping change; the name
	// and identifiers are kept.
	if err := feed.Suspend(ctx, "ACMF", "insurance lapsed", 5, carrierAt("2026-11-10T15:30:00Z")); err != nil {
		t.Fatal(err)
	}
	got = carrierOf(t, feed, "ACMF")
	if got.Status != views.CarrierSuspended || got.Reason != "insurance lapsed" || got.Version != 5 ||
		got.Name != "ACME Freight Ltd" || got.USDOT != "1234567" || got.MC != "MC654321" || !got.UpdatedAt.Equal(carrierAt("2026-11-10T15:30:00Z")) {
		t.Fatalf("after Suspend %+v", got)
	}

	// A stale suspension changes nothing.
	if err := feed.Suspend(ctx, "ACMF", "an older reason", 3, carrierAt("2026-11-10T16:00:00Z")); err != nil {
		t.Fatal(err)
	}
	if got := carrierOf(t, feed, "ACMF"); got.Reason != "insurance lapsed" || got.Version != 5 {
		t.Fatalf("a stale suspension took effect: %+v", got)
	}

	// Reinstate: ACTIVE again, the reason cleared, the snapshot kept.
	if err := feed.Reinstate(ctx, "ACMF", 6, carrierAt("2026-11-10T16:00:00Z")); err != nil {
		t.Fatal(err)
	}
	got = carrierOf(t, feed, "ACMF")
	if got.Status != views.CarrierActive || got.Reason != "" || got.Version != 6 || got.Name != "ACME Freight Ltd" {
		t.Fatalf("after Reinstate %+v", got)
	}

	// A stale registration after all that still changes nothing.
	oldest := reg
	oldest.Name = "the very first snapshot"
	if err := feed.Register(ctx, oldest); err != nil {
		t.Fatal(err)
	}
	if got := carrierOf(t, feed, "ACMF"); got.Version != 6 || got.Name != "ACME Freight Ltd" {
		t.Fatalf("a stale registration overwrote the reinstated carrier: %+v", got)
	}
}

// A carrier the registry has never seen can be suspended or reinstated first:
// the copy inserts it with no name, so the facts that follow fill it in.
func TestSuspendAndReinstateOfAnUnseenCarrierInsertItWithNoName(t *testing.T) {
	feed := postgres.NewCarriers(pgtest.NewPool(t))
	ctx := context.Background()

	if err := feed.Suspend(ctx, "SWFT", "insurance lapsed", 2, carrierAt("2026-11-10T15:30:00Z")); err != nil {
		t.Fatal(err)
	}
	want := views.Carrier{
		SCAC: "SWFT", Status: views.CarrierSuspended, Reason: "insurance lapsed",
		Version: 2, UpdatedAt: carrierAt("2026-11-10T15:30:00Z"),
	}
	if got := carrierOf(t, feed, "SWFT"); got != want {
		t.Fatalf("an unseen carrier is suspended with no name\n got %+v\nwant %+v", got, want)
	}

	if err := feed.Reinstate(ctx, "OTHR", 1, carrierAt("2026-11-10T16:00:00Z")); err != nil {
		t.Fatal(err)
	}
	want = views.Carrier{
		SCAC: "OTHR", Status: views.CarrierActive, Version: 1, UpdatedAt: carrierAt("2026-11-10T16:00:00Z"),
	}
	if got := carrierOf(t, feed, "OTHR"); got != want {
		t.Fatalf("an unseen carrier is reinstated ACTIVE with no name\n got %+v\nwant %+v", got, want)
	}

	// The registration that arrives after a first-seen suspension replaces the
	// nameless row when its version is newer.
	if err := feed.Register(ctx, views.Carrier{SCAC: "SWFT", Name: "Swift Haulage", Status: views.CarrierActive, Version: 3}); err != nil {
		t.Fatal(err)
	}
	if got := carrierOf(t, feed, "SWFT"); got.Name != "Swift Haulage" || got.Status != views.CarrierActive {
		t.Fatalf("the late registration did not fill the nameless carrier: %+v", got)
	}
}
