package http_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestCheckInDropParksTheTrailerWhenTheDriverCompletesTheMove(t *testing.T) {
	g := newRig(t)
	g.seedSite()

	in := g.mustCode(g.dropVisit("ABC1D23", "TR-1"), http.StatusCreated)
	body := in.JSON()
	visit := body["visit"].(map[string]any)
	visitID := visit["visitId"].(string)
	if loc := in.Header.Get("Location"); loc != "/visits/"+visitID {
		t.Fatalf("Location %q", loc)
	}
	move := body["moves"].([]any)[0].(map[string]any)
	if move["performer"] != "VISITING_DRIVER" || move["state"] != "CLAIMED" || move["reason"] != "TO_HOLDING" {
		t.Fatalf("the first move is claimed by the visiting driver to a holding spot: %v", move)
	}

	got := g.mustCode(g.get("/visits/"+visitID), http.StatusOK)
	if got.Header.Get("ETag") != `W/"1"` {
		t.Fatalf("ETag %q", got.Header.Get("ETag"))
	}

	done := g.mustCode(g.post("/moves/"+move["moveId"].(string)+"/completions", nil), http.StatusOK).JSON()
	if done["state"] != "COMPLETED" {
		t.Fatalf("move %v", done)
	}
	trailerID := body["trailers"].([]any)[0].(map[string]any)["trailerId"].(string)
	tr := g.mustCode(g.get("/trailers/"+trailerID), http.StatusOK)
	if tr.JSON()["state"] != "PARKED" || tr.Header.Get("ETag") == "" {
		t.Fatalf("trailer %s", tr.Body)
	}

	byVisit := g.mustCode(g.get("/trailers?visitId="+visitID), http.StatusOK).JSON()["items"].([]any)
	if len(byVisit) != 1 {
		t.Fatalf("visitId filter found %d trailers", len(byVisit))
	}
	other := g.mustCode(g.get("/trailers?visitId=visit-nope"), http.StatusOK).JSON()["items"].([]any)
	if len(other) != 0 {
		t.Fatalf("visitId filter of another visit found %d trailers", len(other))
	}
}

func TestCheckInRefusalsPersistNothing(t *testing.T) {
	g := newRig(t)
	g.seedSite()
	g.mustCode(g.dropVisit("ABC1D23", "TR-1"), http.StatusCreated)

	again := g.dropVisit("ABC1D23", "TR-2")
	if again.Code != http.StatusConflict || again.Slug() != "visit-already-open" {
		t.Fatalf("second visit on a plate: %d %s", again.Code, again.Body)
	}
	dup := g.dropVisit("XYZ9", "TR-1")
	if dup.Code != http.StatusConflict || dup.Slug() != "trailer-already-on-site" {
		t.Fatalf("same unit twice: %d %s", dup.Code, dup.Body)
	}
	if n := len(g.get("/visits").JSON()["items"].([]any)); n != 1 {
		t.Fatalf("%d visits after two refused check-ins, want 1", n)
	}
	bad := g.post("/visits", map[string]any{"siteId": "WH1", "carrier": "", "tractorPlate": "P1", "purpose": "BOBTAIL"})
	if bad.Code != http.StatusUnprocessableEntity || bad.Slug() != "invalid-carrier" {
		t.Fatalf("blank carrier: %d %s", bad.Code, bad.Body)
	}
}

func TestTrailersAreOptionalOnCheckInButAnAppointmentMeansExactlyOne(t *testing.T) {
	g := newRig(t)
	g.seedSite()

	g.mustCode(g.post("/visits", map[string]any{"siteId": "WH1", "carrier": "Acme", "tractorPlate": "B1", "purpose": "BOBTAIL"}), http.StatusCreated)
	g.mustCode(g.post("/visits", map[string]any{"siteId": "WH1", "carrier": "Acme", "tractorPlate": "P1", "purpose": "PICKUP"}), http.StatusCreated)

	named := g.post("/visits", map[string]any{"siteId": "WH1", "carrier": "Acme", "tractorPlate": "P2", "purpose": "PICKUP", "appointmentId": "appt-1"})
	if named.Code != http.StatusUnprocessableEntity || named.Slug() != "unknown-appointment" {
		t.Fatalf("an appointment without exactly one trailer: %d %s", named.Code, named.Body)
	}
	drop := g.post("/visits", map[string]any{"siteId": "WH1", "carrier": "Acme", "tractorPlate": "D1", "purpose": "DROP"})
	if drop.Code != http.StatusUnprocessableEntity || drop.Slug() != "validation" {
		t.Fatalf("a DROP brings a trailer: %d %s", drop.Code, drop.Body)
	}
}

func TestRejectAtGateRecordsARejectedVisitWithTheDriverRef(t *testing.T) {
	g := newRig(t)
	g.seedSite()
	rej := g.mustCode(g.post("/visit-rejections", map[string]any{
		"siteId": "WH1", "carrier": "Acme", "tractorPlate": "R1", "driverRef": "drv-9", "reason": "SITE_REQUEST",
	}), http.StatusCreated)
	v := rej.JSON()
	if v["state"] != "REJECTED" || v["driverRef"] != "drv-9" || v["rejectionReason"] != "SITE_REQUEST" {
		t.Fatalf("visit %v", v)
	}
	if rej.Header.Get("Location") != "/visits/"+v["visitId"].(string) {
		t.Fatalf("Location %q", rej.Header.Get("Location"))
	}
	bad := g.post("/visit-rejections", map[string]any{"siteId": "WH1", "carrier": "Acme", "tractorPlate": "R2", "reason": "BECAUSE"})
	if bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown reason: %d %s", bad.Code, bad.Body)
	}
	if n := len(g.get("/visits?state=REJECTED").JSON()["items"].([]any)); n != 1 {
		t.Fatalf("%d rejected visits", n)
	}
}

func TestCheckOutNeedsTheListAndReleasedTrailers(t *testing.T) {
	g := newRig(t)
	g.seedSite()
	in := g.mustCode(g.dropVisit("ABC1D23", "TR-1"), http.StatusCreated).JSON()
	visitID := in["visit"].(map[string]any)["visitId"].(string)
	trailerID := in["trailers"].([]any)[0].(map[string]any)["trailerId"].(string)

	if r := g.post("/visits/"+visitID+"/check-outs", map[string]any{}); r.Code != http.StatusBadRequest {
		t.Fatalf("no trailersOut: %d %s", r.Code, r.Body)
	}
	held := g.post("/visits/"+visitID+"/check-outs", map[string]any{"trailersOut": []map[string]string{{"trailerId": trailerID}}})
	if held.Code != http.StatusConflict || held.Slug() != "trailer-not-released" {
		t.Fatalf("a trailer in a move is not released: %d %s", held.Code, held.Body)
	}
	out := g.mustCode(g.post("/visits/"+visitID+"/check-outs", map[string]any{"trailersOut": []map[string]string{}}), http.StatusOK).JSON()
	if out["state"] != "CHECKED_OUT" {
		t.Fatalf("visit %v", out)
	}
	if r := g.post("/visits/"+visitID+"/check-outs", map[string]any{"trailersOut": []map[string]string{}}); r.Code != http.StatusConflict || r.Slug() != "illegal-transition" {
		t.Fatalf("a second check-out: %d %s", r.Code, r.Body)
	}
}

func TestNotFoundSlugs(t *testing.T) {
	g := newRig(t)
	g.seedSite()
	for _, c := range []struct{ method, path, body, slug string }{
		{"GET", "/visits/nope", "", "visit-not-found"},
		{"POST", "/visits/nope/check-outs", `{"trailersOut":[]}`, "visit-not-found"},
		{"GET", "/trailers/nope", "", "trailer-not-found"},
		{"POST", "/trailers/nope/holds", `{"reason":"DAMAGED"}`, "trailer-not-found"},
		{"POST", "/trailers/nope/door-work-starts", "", "trailer-not-found"},
		{"POST", "/moves/nope/completions", "", "move-not-found"},
		{"POST", "/moves/nope/cancellations", "", "move-not-found"},
		{"POST", "/yard-checks/nope/observations", `{"locationCode":"WH1-Y01"}`, "yard-check-not-found"},
		{"POST", "/yard-checks/nope/closures", "", "yard-check-not-found"},
		{"GET", "/sites/NOWHERE/yard-board", "", "site-not-found"},
		{"POST", "/moves/claims", `{"siteId":"WH1","hostlerRef":"h1"}`, "no-claimable-move"},
		{"POST", "/moves", `{"trailerId":"nope","to":{"kind":"SPOT","code":"WH1-Y01"}}`, "trailer-not-found"},
	} {
		r := g.do(c.method, c.path, c.body, "Idempotency-Key", g.freshKey())
		if r.Code != http.StatusNotFound || r.Slug() != c.slug {
			t.Errorf("%s %s: %d %s, want 404 %s", c.method, c.path, r.Code, r.Body, c.slug)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/problem+json" {
			t.Errorf("%s %s: content type %q", c.method, c.path, ct)
		}
	}
}

func TestMalformedRequests(t *testing.T) {
	g := newRig(t)
	for name, c := range map[string]struct{ path, body string }{
		"unknown field": {"/visits", `{"siteId":"WH1","bogus":1}`},
		"not json":      {"/visits", `nope`},
		"trailing data": {"/yard-checks", `{"siteId":"WH1"} {}`},
		"empty body":    {"/moves", ``},
	} {
		if r := g.post(c.path, c.body); r.Code != http.StatusBadRequest || r.Slug() != "malformed-request-body" {
			t.Errorf("%s: %d %s", name, r.Code, r.Body)
		}
	}
}

func TestListsPageByKeyset(t *testing.T) {
	g := newRig(t)
	g.seedSite()
	for i, plate := range []string{"P1", "P2", "P3"} {
		g.env.Store.Advance(1e9)
		g.mustCode(g.post("/visits", map[string]any{"siteId": "WH1", "carrier": "Acme", "tractorPlate": plate, "purpose": "BOBTAIL"}), http.StatusCreated)
		_ = i
	}
	first := g.mustCode(g.get("/visits?limit=2"), http.StatusOK).JSON()
	if len(first["items"].([]any)) != 2 || first["nextCursor"] == nil {
		t.Fatalf("first page %v", first)
	}
	second := g.mustCode(g.get("/visits?limit=2&cursor="+first["nextCursor"].(string)), http.StatusOK).JSON()
	if len(second["items"].([]any)) != 1 || second["nextCursor"] != nil {
		t.Fatalf("second page %v", second)
	}
	for _, q := range []string{"limit=0", "limit=501", "limit=x", "cursor=%%", "cursor=e30", "bogus=1", "state=NOPE"} {
		if r := g.get("/visits?" + q); r.Code != http.StatusBadRequest {
			t.Errorf("?%s: %d %s", q, r.Code, r.Body)
		}
	}
	if r := g.get("/moves?hostlerRef=h1&state=CLAIMED"); r.Code != http.StatusOK {
		t.Errorf("moves filters: %d %s", r.Code, r.Body)
	}
	if r := g.get("/moves?kind=x"); r.Code != http.StatusBadRequest || r.Slug() != "unknown-query-parameter" {
		t.Errorf("an undeclared parameter: %d %s", r.Code, r.Body)
	}
	if !strings.Contains(string(g.get("/trailers").Body), `"items":[]`) {
		t.Errorf("an empty page is an empty list")
	}
}
