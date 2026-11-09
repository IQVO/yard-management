package http_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/views"
)

// kpiReports is a ReportReader with canned KPI rows that records what it was
// asked.
type kpiReports struct {
	window    views.ReportWindow
	threshold int
	err       error
}

func (k *kpiReports) YardDwell(_ context.Context, w views.ReportWindow) ([]views.DwellRow, error) {
	k.window = w
	p50, p90, proc := 60.0, 95.0, 30.5
	return []views.DwellRow{{Kind: "DRY_VAN", Trailers: 2, AvgDwellMinutes: 77.5, MaxDwellMinutes: 95, P50DwellMinutes: &p50, P90DwellMinutes: &p90, AvgProcessingMinutes: &proc}}, k.err
}

func (k *kpiReports) DoorUtilisation(context.Context, views.ReportWindow) ([]views.DoorRow, error) {
	return nil, nil
}

func (k *kpiReports) HostlerProductivity(context.Context, views.ReportWindow) ([]views.HostlerRow, error) {
	return nil, nil
}

func (k *kpiReports) TurnTime(_ context.Context, w views.ReportWindow) ([]views.TurnTimeRow, error) {
	k.window = w
	return []views.TurnTimeRow{{Purpose: "DROP", Visits: 3, AvgMinutes: 40, P50Minutes: 35, P90Minutes: 58, MaxMinutes: 60}}, k.err
}

func (k *kpiReports) DetentionExposure(_ context.Context, w views.ReportWindow, threshold int) ([]views.DetentionRow, error) {
	k.window, k.threshold = w, threshold
	share := 0.5
	return []views.DetentionRow{{Kind: "REEFER", Trailers: 4, Exceeding: 2, Share: &share, ExcessMinutes: 75}}, k.err
}

func (k *kpiReports) Exceptions(_ context.Context, w views.ReportWindow) ([]views.ExceptionRow, error) {
	k.window = w
	rate := 0.25
	return []views.ExceptionRow{{SiteID: "WH1", VisitsCheckedIn: 3, VisitsRejected: 1, RejectionRate: &rate, SealExceptions: 1,
		HoldsByReason: map[string]int{"DAMAGED": 1, "SEAL_BROKEN": 0, "NOT_FOUND": 0, "QUALITY": 0, "SECURITY": 0}, YardChecksClosed: 1, Discrepancies: 2}}, k.err
}

func (k *kpiReports) LastEventAt(context.Context) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

const kpiWindow = "?from=2026-11-01T00:00:00Z&to=2026-11-02T00:00:00Z&siteId=WH1"

func TestKPIReportsServeTheirRowsAndPassTheWindow(t *testing.T) {
	g := newRig(t)
	fake := &kpiReports{}
	g.server.Reports = fake
	for path, field := range map[string]string{
		"/reports/yard-dwell":         "p90DwellMinutes",
		"/reports/turn-time":          "p90Minutes",
		"/reports/detention-exposure": "excessMinutes",
		"/reports/exceptions":         "holdsByReason",
	} {
		r := g.mustCode(g.get(path+kpiWindow), http.StatusOK).JSON()
		rows, _ := r["rows"].([]any)
		if r["siteId"] != "WH1" || len(rows) != 1 || rows[0].(map[string]any)[field] == nil {
			t.Fatalf("%s: %v", path, r)
		}
		if fake.window.SiteID != "WH1" || fake.window.To.Sub(fake.window.From) != 24*time.Hour {
			t.Fatalf("%s window %+v", path, fake.window)
		}
	}
	dwell := g.mustCode(g.get("/reports/yard-dwell"+kpiWindow), http.StatusOK).JSON()["rows"].([]any)[0].(map[string]any)
	if dwell["p50DwellMinutes"] != 60.0 || dwell["avgProcessingMinutes"] != 30.5 {
		t.Fatalf("dwell row %v", dwell)
	}
}

func TestDetentionExposureTakesAThresholdDefaultingToTwoHours(t *testing.T) {
	g := newRig(t)
	fake := &kpiReports{}
	g.server.Reports = fake
	body := g.mustCode(g.get("/reports/detention-exposure"+kpiWindow), http.StatusOK).JSON()
	if fake.threshold != 120 || body["thresholdMinutes"] != 120.0 {
		t.Fatalf("default threshold %d, body %v", fake.threshold, body)
	}
	row := body["rows"].([]any)[0].(map[string]any)
	if row["kind"] != "REEFER" || row["trailers"] != 4.0 || row["exceeding"] != 2.0 || row["share"] != 0.5 || row["excessMinutes"] != 75.0 {
		t.Fatalf("row %v", row)
	}
	for _, ok := range []string{"1", "45", "10080"} {
		g.mustCode(g.get("/reports/detention-exposure"+kpiWindow+"&thresholdMinutes="+ok), http.StatusOK)
	}
	if fake.threshold != 10080 {
		t.Fatalf("threshold %d, want the last accepted value 10080", fake.threshold)
	}
}

func TestDetentionExposureRejectsABadThresholdWith400(t *testing.T) {
	g := newRig(t)
	g.server.Reports = &kpiReports{}
	for _, bad := range []string{"0", "-5", "10081", "abc", "1.5", "", "120m"} {
		r := g.get("/reports/detention-exposure" + kpiWindow + "&thresholdMinutes=" + bad)
		if r.Code != http.StatusBadRequest || r.Slug() != "invalid-threshold-minutes" {
			t.Errorf("thresholdMinutes=%q: %d %s, want 400 invalid-threshold-minutes", bad, r.Code, r.Body)
		}
	}
}

func TestKPIReportsWithoutAReadSideKeepTheContractShape(t *testing.T) {
	g := newRig(t)
	for _, path := range []string{"/reports/turn-time", "/reports/detention-exposure", "/reports/exceptions"} {
		r := g.mustCode(g.get(path+kpiWindow), http.StatusOK).JSON()
		if r["siteId"] != "WH1" || len(r["rows"].([]any)) != 0 || r["from"] != "2026-11-01T00:00:00Z" {
			t.Fatalf("%s: %v", path, r)
		}
		if bad := g.get(path + "?from=2026-11-02T00:00:00Z&to=2026-11-01T00:00:00Z"); bad.Code != http.StatusBadRequest || bad.Slug() != "invalid-report-range" {
			t.Fatalf("%s reversed window: %d %s", path, bad.Code, bad.Body)
		}
		if bad := g.get(path + "?from=2026-01-01T00:00:00Z&to=2026-11-01T00:00:00Z"); bad.Code != http.StatusBadRequest {
			t.Fatalf("%s a window over 92 days: %d", path, bad.Code)
		}
		if bad := g.get(path); bad.Code != http.StatusBadRequest {
			t.Fatalf("%s no window: %d", path, bad.Code)
		}
	}
	r := g.mustCode(g.get("/reports/detention-exposure"+kpiWindow), http.StatusOK).JSON()
	if r["thresholdMinutes"] != 120.0 {
		t.Fatalf("the threshold is echoed even with no rows: %v", r)
	}
}

func TestKPIReportErrorsAreProblems(t *testing.T) {
	g := newRig(t)
	g.server.Reports = &kpiReports{err: errors.New("boom")}
	for _, path := range []string{"/reports/turn-time", "/reports/detention-exposure", "/reports/exceptions"} {
		if r := g.get(path + kpiWindow); r.Code != http.StatusInternalServerError {
			t.Errorf("%s: %d %s", path, r.Code, r.Body)
		}
	}
}
