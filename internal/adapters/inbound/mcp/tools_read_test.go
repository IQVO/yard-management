package mcp_test

import (
	"strings"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/views"
)

func items(t *testing.T, res result) []any {
	t.Helper()
	list, ok := res.Data["items"].([]any)
	if !ok {
		t.Fatalf("answer has no items array: %v", res.Data)
	}
	return list
}

func TestGetYardBoard(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	trailerID := r.parkedTrailer("PLATE1", "T-100")

	board := r.ok("get_yard_board", map[string]any{"siteId": "WH1"}).Data
	if board["siteId"] != "WH1" {
		t.Fatalf("board = %v", board)
	}
	if got := len(board["spots"].([]any)); got != 3 {
		t.Fatalf("spots = %d, want 3", got)
	}
	if got := len(board["doors"].([]any)); got != 2 {
		t.Fatalf("doors = %d, want 2", got)
	}
	var seen string
	for _, s := range board["spots"].([]any) {
		if tr, ok := s.(map[string]any)["trailer"].(map[string]any); ok {
			seen = str(tr, "trailerId")
		}
	}
	if seen != trailerID {
		t.Fatalf("the parked trailer %s is not on the board (saw %q)", trailerID, seen)
	}
}

func TestGetYardBoardUnknownSite(t *testing.T) {
	r := newRig(t)
	wantError(t, r.call("get_yard_board", map[string]any{"siteId": "NOPE"}), "site-not-found")
}

func TestTrailerReads(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	first := r.parkedTrailer("PLATE1", "T-100")
	r.parkedTrailer("PLATE2", "T-200")

	got := r.ok("get_trailer", map[string]any{"trailerId": first}).Data
	if got["trailerId"] != first || got["unitNumber"] != "T-100" || got["state"] != "PARKED" || got["loadState"] != "LOADED" {
		t.Fatalf("get_trailer = %v", got)
	}
	if loc := got["location"].(map[string]any); loc["kind"] != "SPOT" {
		t.Fatalf("location = %v", loc)
	}
	wantError(t, r.call("get_trailer", map[string]any{"trailerId": "trl-nope"}), "trailer-not-found")

	all := items(t, r.ok("list_trailers", map[string]any{"siteId": "WH1"}))
	if len(all) != 2 {
		t.Fatalf("list_trailers = %d items, want 2", len(all))
	}
	one := items(t, r.ok("list_trailers", map[string]any{"unitNumber": "T-200", "state": "PARKED", "kind": "DRY_VAN"}))
	if len(one) != 1 || one[0].(map[string]any)["unitNumber"] != "T-200" {
		t.Fatalf("filtered list_trailers = %v", one)
	}
	if none := items(t, r.ok("list_trailers", map[string]any{"state": "DOCKED"})); len(none) != 0 {
		t.Fatalf("DOCKED trailers = %v, want none", none)
	}
}

func TestListPaging(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	r.parkedTrailer("PLATE1", "T-100")
	r.parkedTrailer("PLATE2", "T-200")
	r.parkedTrailer("PLATE3", "T-300")

	seen := map[string]bool{}
	cursor := ""
	for pages := 0; pages < 5; pages++ {
		args := map[string]any{"limit": 2}
		if cursor != "" {
			args["cursor"] = cursor
		}
		page := r.ok("list_trailers", args)
		for _, it := range items(t, page) {
			seen[str(it.(map[string]any), "trailerId")] = true
		}
		cursor = str(page.Data, "nextCursor")
		if cursor == "" {
			break
		}
	}
	if len(seen) != 3 {
		t.Fatalf("paging saw %d distinct trailers, want 3", len(seen))
	}
}

func TestListBadInputs(t *testing.T) {
	r := newRig(t)
	cases := []struct {
		tool string
		args map[string]any
		slug string
	}{
		{"list_trailers", map[string]any{"limit": 501}, "invalid-limit"},
		{"list_trailers", map[string]any{"limit": -1}, "invalid-limit"},
		{"list_trailers", map[string]any{"cursor": "!!not-base64!!"}, "invalid-cursor"},
		{"list_trailers", map[string]any{"cursor": "e30"}, "invalid-cursor"},
		{"list_trailers", map[string]any{"state": "GONE"}, "invalid-state"},
		{"list_trailers", map[string]any{"kind": "SLED"}, "invalid-state"},
		{"list_moves", map[string]any{"state": "DONE"}, "invalid-state"},
		{"list_moves", map[string]any{"limit": 999}, "invalid-limit"},
		{"list_visits", map[string]any{"state": "INSIDE"}, "invalid-state"},
		{"list_visits", map[string]any{"cursor": "bm90LWpzb24"}, "invalid-cursor"},
	}
	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.slug, func(t *testing.T) { wantError(t, r.call(tc.tool, tc.args), tc.slug) })
	}
	t.Run("a cursor whose instant is not RFC 3339", func(t *testing.T) {
		// {"s":"yesterday","i":"x"}
		wantError(t, r.call("list_moves", map[string]any{"cursor": "eyJzIjoieWVzdGVyZGF5IiwiaSI6IngifQ"}), "invalid-cursor")
	})
}

func TestMoveAndVisitReads(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	trailerID := r.parkedTrailer("PLATE1", "T-100")
	mv := r.ok("request_move", map[string]any{"idempotencyKey": "k-move", "trailerId": trailerID, "to": map[string]any{"kind": "DOOR", "code": "WH1-D02"}}).Data
	moveID := str(mv, "moveId")

	open := items(t, r.ok("list_moves", map[string]any{"siteId": "WH1", "state": "REQUESTED", "trailerId": trailerID}))
	if len(open) != 1 || open[0].(map[string]any)["moveId"] != moveID {
		t.Fatalf("list_moves = %v", open)
	}
	if none := items(t, r.ok("list_moves", map[string]any{"hostlerRef": "nobody"})); len(none) != 0 {
		t.Fatalf("moves of nobody = %v", none)
	}

	visits := items(t, r.ok("list_visits", map[string]any{"siteId": "WH1", "state": "CHECKED_IN", "tractorPlate": "PLATE1"}))
	if len(visits) != 1 {
		t.Fatalf("list_visits = %v", visits)
	}
	visitID := str(visits[0].(map[string]any), "visitId")
	v := r.ok("get_visit", map[string]any{"visitId": visitID}).Data
	if v["visitId"] != visitID || v["purpose"] != "DROP" || v["state"] != "CHECKED_IN" {
		t.Fatalf("get_visit = %v", v)
	}
	wantError(t, r.call("get_visit", map[string]any{"visitId": "visit-nope"}), "visit-not-found")
}

func TestGetYardPolicy(t *testing.T) {
	r := newRig(t)
	p := r.ok("get_yard_policy", map[string]any{"siteId": "WH1"}).Data
	if p["siteId"] != "WH1" || p["declared"] != false || p["leaseSeconds"] != 300.0 || p["sealMismatchAction"] != "FLAG" {
		t.Fatalf("default policy = %v", p)
	}
}

func TestGetYardDwellReport(t *testing.T) {
	r := newRig(t)
	from := "2026-11-01T00:00:00Z"
	to := "2026-11-05T00:00:00Z"

	empty := r.ok("get_yard_dwell_report", map[string]any{"from": from, "to": to})
	if rows := empty.Data["rows"].([]any); len(rows) != 0 || empty.Data["siteId"] != nil || empty.Data["lastEventAt"] != nil {
		t.Fatalf("empty report = %v", empty.Data)
	}

	wait := 12.5
	r.reports.rows = []views.DwellRow{{Kind: "DRY_VAN", Trailers: 4, AvgDwellMinutes: 90.5, MaxDwellMinutes: 240, AvgDriverWaitMinutes: &wait}}
	r.reports.last, r.reports.hasLast = time.Date(2026, 11, 4, 23, 59, 0, 0, time.UTC), true
	got := r.ok("get_yard_dwell_report", map[string]any{"from": from, "to": to, "siteId": "WH1"})
	rows := got.Data["rows"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["kind"] != "DRY_VAN" || rows[0].(map[string]any)["avgDriverWaitMinutes"] != 12.5 {
		t.Fatalf("report rows = %v", got.Data)
	}
	if got.Data["siteId"] != "WH1" || got.Data["lastEventAt"] != "2026-11-04T23:59:00Z" {
		t.Fatalf("report header = %v", got.Data)
	}
	if r.reports.window.SiteID != "WH1" || !r.reports.window.From.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("window handed to the read side = %+v", r.reports.window)
	}
}

func TestGetYardDwellReportRefusals(t *testing.T) {
	r := newRig(t)
	cases := map[string]map[string]any{
		"unparsable from": {"from": "yesterday", "to": "2026-11-05T00:00:00Z"},
		"unparsable to":   {"from": "2026-11-01T00:00:00Z", "to": "tomorrow"},
		"to before from":  {"from": "2026-11-05T00:00:00Z", "to": "2026-11-01T00:00:00Z"},
		"over 92 days":    {"from": "2026-01-01T00:00:00Z", "to": "2026-11-01T00:00:00Z"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) { wantError(t, r.call("get_yard_dwell_report", args), "invalid-report-range") })
	}
	t.Run("the read side fails", func(t *testing.T) {
		r.reports.err = errBoom
		res := r.call("get_yard_dwell_report", map[string]any{"from": "2026-11-01T00:00:00Z", "to": "2026-11-05T00:00:00Z"})
		wantError(t, res, "internal-error")
		if strings.Contains(res.Text, "boom") {
			t.Fatalf("an infrastructure error leaked to the model: %q", res.Text)
		}
	})
}
