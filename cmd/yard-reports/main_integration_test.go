//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	// The database comes from testcontainers through pgtest; the blank import
	// keeps the fleet rule visible from this package.
	_ "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/claudioed/yard-management/internal/adapters/outbound/analyticsstore"
	"github.com/claudioed/yard-management/internal/analytics/report"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func getJSON(t *testing.T, raw string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(raw)
	if err != nil {
		return 0, nil
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func waitReady(t *testing.T, raw string) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if code, _ := getJSON(t, raw); code == http.StatusOK {
			return
		}
	}
	t.Fatal("/readyz never answered 200")
}

// row returns the only row of a report body, failing the test when there is
// not exactly one.
func row(t *testing.T, what string, body map[string]any) map[string]any {
	t.Helper()
	rows, _ := body["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("%s: want exactly one row, got %v", what, body["rows"])
	}
	r, _ := rows[0].(map[string]any)
	return r
}

func num(t *testing.T, what string, r map[string]any, key string) float64 {
	t.Helper()
	v, ok := r[key].(float64)
	if !ok {
		t.Fatalf("%s: %q is not a number: %v", what, key, r[key])
	}
	return v
}

// The real entry point over an analytically-migrated database seeded through
// the repo's own analytics writer (the projector owns the migrations, ADR
// 0006 §3; RunMigrations stands in for it here): /readyz answers only once
// the freshness table is readable, the documented reports answer from the
// seeded facts (ADR 0006 §4, ADR 0011 §4), and a cancelled context drains
// cleanly.
func TestReportsServeTheProjectionAndStopCleanly(t *testing.T) {
	dsn := pgtest.EmptyURL(t)
	ctx := context.Background()
	if err := analyticsstore.RunMigrations(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	writer, err := analyticsstore.NewPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	proj := analyticsstore.NewProjection(writer)

	// A UTC day two days back, so every window is stable. The journey: a
	// LIVE_UNLOAD visit brings DRY_VAN trl-r1 at 08:00, it docks at 08:10,
	// door work runs 08:15-08:35, it undocks at 08:40, departs at 09:30
	// after 90 minutes on site and the visit checks out at 09:35; hostler
	// hostler-r1 completes a 300 s move at 08:20.
	day := time.Now().UTC().Truncate(24 * time.Hour).Add(-48 * time.Hour)
	at := func(h, m int) time.Time { return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute) }
	seed := []report.Event{
		{Kind: report.KindVisitCheckedIn, At: at(8, 0), SiteID: "WH1", VisitID: "visit-r1", Purpose: "LIVE_UNLOAD",
			ArrivedAt: at(8, 0), Arrivals: []report.Arrival{{TrailerID: "trl-r1", UnitNumber: "UNIT-R1", Kind: "DRY_VAN"}}},
		{Kind: report.KindTrailerDocked, At: at(8, 10), SiteID: "WH1", TrailerID: "trl-r1", DoorCode: "WH1-D01", DockedAt: at(8, 10)},
		{Kind: report.KindDoorWorkStarted, At: at(8, 15), SiteID: "WH1", TrailerID: "trl-r1", DoorCode: "WH1-D01", StartedAt: at(8, 15)},
		{Kind: report.KindDoorWorkCompleted, At: at(8, 35), SiteID: "WH1", TrailerID: "trl-r1", DoorCode: "WH1-D01", CompletedAt: at(8, 35)},
		{Kind: report.KindTrailerUndocked, At: at(8, 40), SiteID: "WH1", TrailerID: "trl-r1", DoorCode: "WH1-D01", UndockedAt: at(8, 40)},
		{Kind: report.KindTrailerDeparted, At: at(9, 30), SiteID: "WH1", TrailerID: "trl-r1", DepartedAt: at(9, 30), DwellMinutes: 90},
		{Kind: report.KindVisitCheckedOut, At: at(9, 35), SiteID: "WH1", VisitID: "visit-r1", DepartedAt: at(9, 35), OnSiteMinutes: 95},
		{Kind: report.KindYardMoveCompleted, At: at(8, 20), SiteID: "WH1", MoveID: "mv-r1", TrailerID: "trl-x",
			Performer: report.PerformerHostler, HostlerRef: "hostler-r1", CompletedAt: at(8, 20), DurationSeconds: 300},
	}
	for i, e := range seed {
		e.EventID = fmt.Sprintf("reports-itest-%d", i+1)
		if applied, err := proj.Apply(ctx, e); err != nil || !applied {
			t.Fatalf("seed %s: applied=%v err=%v", e.Kind, applied, err)
		}
	}

	addr := freeAddr(t)
	env := map[string]string{
		"ANALYTICS_READER_DATABASE_URL": dsn,
		"HTTP_ADDR":                     addr,
		"SHUTDOWN_DRAIN_DELAY":          "0s",
		"LOG_LEVEL":                     "error",
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(runCtx, func(k string) string { return env[k] }) }()

	waitReady(t, "http://"+addr+"/readyz")
	base := "http://" + addr + "/reports"
	win := "?from=" + url.QueryEscape(day.Format(time.RFC3339)) + "&to=" + url.QueryEscape(day.Add(24*time.Hour).Format(time.RFC3339))

	// yard-dwell (ADR 0006 §4, the additive percentiles of ADR 0011): one
	// DRY_VAN that departed in the window, 90 minutes of dwell, a 15 minute
	// driver wait (08:00 to 08:15) and 20 minutes of processing.
	code, body := getJSON(t, base+"/yard-dwell"+win)
	if code != http.StatusOK {
		t.Fatalf("yard-dwell = %d %v", code, body)
	}
	dwell := row(t, "yard-dwell", body)
	if dwell["kind"] != "DRY_VAN" || num(t, "yard-dwell", dwell, "trailers") != 1 ||
		num(t, "yard-dwell", dwell, "avgDwellMinutes") != 90 || num(t, "yard-dwell", dwell, "maxDwellMinutes") != 90 ||
		num(t, "yard-dwell", dwell, "avgDriverWaitMinutes") != 15 || num(t, "yard-dwell", dwell, "avgProcessingMinutes") != 20 ||
		num(t, "yard-dwell", dwell, "p50DwellMinutes") != 90 || num(t, "yard-dwell", dwell, "p90DwellMinutes") != 90 {
		t.Fatalf("yard-dwell row = %v", dwell)
	}

	// door-utilisation (ADR 0006 §4): one docking, 30 minutes occupied
	// (08:10 to 08:40) of which 20 were worked, utilisation within 0..1.
	code, body = getJSON(t, base+"/door-utilisation"+win)
	if code != http.StatusOK {
		t.Fatalf("door-utilisation = %d %v", code, body)
	}
	door := row(t, "door-utilisation", body)
	if door["siteId"] != "WH1" || door["doorCode"] != "WH1-D01" || num(t, "door-utilisation", door, "dockings") != 1 ||
		num(t, "door-utilisation", door, "occupiedMinutes") != 30 || num(t, "door-utilisation", door, "workMinutes") != 20 {
		t.Fatalf("door-utilisation row = %v", door)
	}
	if u := num(t, "door-utilisation", door, "utilisation"); u <= 0 || u > 1 {
		t.Fatalf("door-utilisation utilisation = %v, want within 0..1", u)
	}

	// hostler-productivity (ADR 0006 §4): one move of 300 s, one per hour
	// actually worked.
	code, body = getJSON(t, base+"/hostler-productivity"+win)
	if code != http.StatusOK {
		t.Fatalf("hostler-productivity = %d %v", code, body)
	}
	hostler := row(t, "hostler-productivity", body)
	if hostler["hostlerRef"] != "hostler-r1" || num(t, "hostler-productivity", hostler, "moves") != 1 ||
		num(t, "hostler-productivity", hostler, "avgMoveSeconds") != 300 || num(t, "hostler-productivity", hostler, "movesPerHour") != 1 {
		t.Fatalf("hostler-productivity row = %v", hostler)
	}

	// turn-time (ADR 0011 §4): one LIVE_UNLOAD visit, 95 minutes
	// gate-to-gate, checked out inside the window.
	code, body = getJSON(t, base+"/turn-time"+win)
	if code != http.StatusOK {
		t.Fatalf("turn-time = %d %v", code, body)
	}
	turn := row(t, "turn-time", body)
	if turn["purpose"] != "LIVE_UNLOAD" || num(t, "turn-time", turn, "visits") != 1 ||
		num(t, "turn-time", turn, "avgMinutes") != 95 || num(t, "turn-time", turn, "maxMinutes") != 95 {
		t.Fatalf("turn-time row = %v", turn)
	}

	// freshness (ADR 0006 §4): the CloudEvents time of the newest seeded
	// event, which is the visit's 09:35 check-out.
	code, body = getJSON(t, base+"/freshness")
	if code != http.StatusOK || body["lastEventAt"] == nil {
		t.Fatalf("freshness = %d %v", code, body)
	}
	last, err := time.Parse(time.RFC3339Nano, body["lastEventAt"].(string))
	if err != nil || !last.Equal(at(9, 35)) {
		t.Fatalf("freshness lastEventAt = %v, want %v", body["lastEventAt"], at(9, 35))
	}

	// The documented window contract (ADR 0006 §4): to must be after from.
	if code, _ = getJSON(t, base+"/yard-dwell?from="+url.QueryEscape(day.Format(time.RFC3339))+"&to="+url.QueryEscape(day.Format(time.RFC3339))); code != http.StatusBadRequest {
		t.Fatalf("an empty window must be a 400, got %d", code)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a cancelled reports server must drain cleanly, got %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the reports server did not stop after the context was cancelled")
	}
}
