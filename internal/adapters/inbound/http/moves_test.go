package http_test

import (
	"net/http"
	"testing"
)

// parked checks a DROP visit in and has the visiting driver park the trailer;
// it returns the trailer id.
func (g *rig) parked(plate, unit string) string {
	g.t.Helper()
	in := g.mustCode(g.dropVisit(plate, unit), http.StatusCreated).JSON()
	move := in["moves"].([]any)[0].(map[string]any)["moveId"].(string)
	g.mustCode(g.post("/moves/"+move+"/completions", nil), http.StatusOK)
	return in["trailers"].([]any)[0].(map[string]any)["trailerId"].(string)
}

func (g *rig) requestMove(trailerID, kind, code string) reply {
	return g.post("/moves", map[string]any{"trailerId": trailerID, "to": map[string]string{"kind": kind, "code": code}})
}

func TestSupervisorMoveIsClaimedByAHostlerAndCompleted(t *testing.T) {
	g := newRig(t)
	g.seedSite()
	trailerID := g.parked("ABC1D23", "TR-1")

	req := g.mustCode(g.requestMove(trailerID, "DOOR", "WH1-D02"), http.StatusCreated).JSON()
	if req["state"] != "REQUESTED" || req["performer"] != "HOSTLER" || req["reason"] != "TO_DOOR" {
		t.Fatalf("move %v", req)
	}
	moveID := req["moveId"].(string)

	claim := g.mustCode(g.post("/moves/claims", map[string]any{"siteId": "WH1", "hostlerRef": "h1"}), http.StatusCreated).JSON()
	if claim["moveId"] != moveID || claim["hostlerRef"] != "h1" || claim["leaseExpiresAt"] == nil {
		t.Fatalf("claim %v", claim)
	}
	if busy := g.post("/moves/claims", map[string]any{"siteId": "WH1", "hostlerRef": "h1"}); busy.Code != http.StatusConflict || busy.Slug() != "hostler-busy" {
		t.Fatalf("a hostler holds one move: %d %s", busy.Code, busy.Body)
	}
	if mine := g.get("/moves?hostlerRef=h1&state=CLAIMED").JSON()["items"].([]any); len(mine) != 1 {
		t.Fatalf("hostlerRef filter found %d moves", len(mine))
	}
	if mine := g.get("/moves?hostlerRef=h2").JSON()["items"].([]any); len(mine) != 0 {
		t.Fatalf("hostlerRef filter of another hostler found %d moves", len(mine))
	}
	completeAsHostler(t, g, moveID)
}

func completeAsHostler(t *testing.T, g *rig, moveID string) {
	t.Helper()
	if wrong := g.post("/moves/"+moveID+"/completions", map[string]any{"hostlerRef": "h2"}); wrong.Code != http.StatusConflict || wrong.Slug() != "not-lease-holder" {
		t.Fatalf("someone else completes: %d %s", wrong.Code, wrong.Body)
	}
	elsewhere := map[string]any{"hostlerRef": "h1", "actualLocation": map[string]string{"kind": "SPOT", "code": "WH1-Y03"}}
	if bare := g.post("/moves/"+moveID+"/completions", elsewhere); bare.Code != http.StatusUnprocessableEntity || bare.Slug() != "unexplained-destination" {
		t.Fatalf("an unexplained deviation: %d %s", bare.Code, bare.Body)
	}
	elsewhere["deviationNote"] = "door was blocked"
	done := g.mustCode(g.post("/moves/"+moveID+"/completions", elsewhere), http.StatusOK).JSON()
	if done["state"] != "COMPLETED" || done["actualLocation"].(map[string]any)["code"] != "WH1-Y03" {
		t.Fatalf("move %v", done)
	}
}

func TestTwoMovesToOneDoorOnlyOneIsAccepted(t *testing.T) {
	g := newRig(t)
	g.seedSite()
	a := g.parked("A1", "TR-A")
	b := g.parked("B1", "TR-B")
	g.mustCode(g.requestMove(a, "DOOR", "WH1-D02"), http.StatusCreated)
	r := g.requestMove(b, "DOOR", "WH1-D02")
	if r.Code != http.StatusConflict || r.Slug() != "destination-reserved" {
		t.Fatalf("the door is reserved: %d %s", r.Code, r.Body)
	}
	if r := g.requestMove(a, "SPOT", "WH1-Y03"); r.Code != http.StatusConflict || r.Slug() != "trailer-has-open-move" {
		t.Fatalf("one open move per trailer: %d %s", r.Code, r.Body)
	}
	if r := g.requestMove(b, "DOOR", "WH1-NOPE"); r.Code != http.StatusUnprocessableEntity || r.Slug() != "unknown-location" {
		t.Fatalf("an unknown door: %d %s", r.Code, r.Body)
	}
}

func TestDockedTrailerDoorWorkHoldsAndSeals(t *testing.T) {
	g := newRig(t)
	g.seedSite()
	trailerID := g.parked("ABC1D23", "TR-1")
	g.mustCode(g.requestMove(trailerID, "DOOR", "WH1-D02"), http.StatusCreated)
	g.mustCode(g.post("/moves/claims", map[string]any{"siteId": "WH1", "hostlerRef": "h1"}), http.StatusCreated)
	moves := g.get("/moves?state=CLAIMED").JSON()["items"].([]any)
	g.mustCode(g.post("/moves/"+moves[0].(map[string]any)["moveId"].(string)+"/completions", map[string]any{"hostlerRef": "h1"}), http.StatusOK)

	if tr := g.get("/trailers/" + trailerID).JSON(); tr["state"] != "DOCKED" || tr["doorWork"] != "WAITING" {
		t.Fatalf("trailer %v", tr)
	}
	if r := g.post("/trailers/"+trailerID+"/hold-releases", nil); r.Code != http.StatusConflict || r.Slug() != "trailer-not-on-hold" {
		t.Fatalf("nothing to release: %d %s", r.Code, r.Body)
	}
	g.mustCode(g.post("/trailers/"+trailerID+"/holds", map[string]any{"reason": "QUALITY"}), http.StatusOK)
	if r := g.post("/trailers/"+trailerID+"/door-work-starts", nil); r.Code != http.StatusConflict || r.Slug() != "trailer-on-hold" {
		t.Fatalf("a held trailer: %d %s", r.Code, r.Body)
	}
	g.mustCode(g.post("/trailers/"+trailerID+"/hold-releases", nil), http.StatusOK)
	if r := g.mustCode(g.post("/trailers/"+trailerID+"/door-work-starts", nil), http.StatusOK).JSON(); r["doorWork"] != "IN_PROGRESS" {
		t.Fatalf("trailer %v", r)
	}
	done := g.mustCode(g.post("/trailers/"+trailerID+"/door-work-completions", map[string]any{"loadState": "EMPTY"}), http.StatusOK).JSON()
	if done["doorWork"] != "DONE" || done["loadState"] != "EMPTY" {
		t.Fatalf("trailer %v", done)
	}

	seal := g.mustCode(g.post("/trailers/"+trailerID+"/seals", map[string]any{"state": "INTACT", "sealNumber": "SL-1"}), http.StatusOK).JSON()
	if seal["seal"].(map[string]any)["number"] != "SL-1" {
		t.Fatalf("seal %v", seal)
	}
	broken := g.mustCode(g.post("/trailers/"+trailerID+"/seals", map[string]any{"state": "BROKEN"}), http.StatusOK).JSON()
	if broken["hold"] != "SEAL_BROKEN" || broken["seal"].(map[string]any)["state"] != "BROKEN" {
		t.Fatalf("a broken seal holds the trailer: %v", broken)
	}
	if r := g.post("/trailers/"+trailerID+"/seals", map[string]any{"state": "TORN"}); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown seal state: %d %s", r.Code, r.Body)
	}
	if r := g.post("/trailers/"+trailerID+"/holds", map[string]any{"reason": "BORED"}); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown hold reason: %d %s", r.Code, r.Body)
	}
}

func TestCancelAMoveReturnsTheTrailer(t *testing.T) {
	g := newRig(t)
	g.seedSite()
	trailerID := g.parked("ABC1D23", "TR-1")
	req := g.mustCode(g.requestMove(trailerID, "SPOT", "WH1-Y03"), http.StatusCreated).JSON()
	cancelled := g.mustCode(g.post("/moves/"+req["moveId"].(string)+"/cancellations", map[string]any{"reason": "changed my mind"}), http.StatusOK).JSON()
	if cancelled["state"] != "CANCELLED" {
		t.Fatalf("move %v", cancelled)
	}
	if r := g.post("/moves/"+req["moveId"].(string)+"/cancellations", nil); r.Code != http.StatusConflict || r.Slug() != "illegal-transition" {
		t.Fatalf("a second cancel: %d %s", r.Code, r.Body)
	}
}

func TestYardChecksReportWhatIsMissing(t *testing.T) {
	g := newRig(t)
	g.seedSite()
	g.parked("ABC1D23", "TR-1")

	chk := g.mustCode(g.post("/yard-checks", map[string]any{"siteId": "WH1"}), http.StatusCreated).JSON()
	id := chk["yardCheckId"].(string)
	if r := g.post("/yard-checks", map[string]any{"siteId": "WH1"}); r.Code != http.StatusConflict || r.Slug() != "yard-check-open" {
		t.Fatalf("one open check: %d %s", r.Code, r.Body)
	}
	g.mustCode(g.post("/yard-checks/"+id+"/observations", map[string]any{"locationCode": "WH1-Y02"}), http.StatusOK)
	if r := g.post("/yard-checks/"+id+"/observations", map[string]any{"locationCode": "WH1-Y02"}); r.Code != http.StatusConflict || r.Slug() != "location-already-observed" {
		t.Fatalf("observed twice: %d %s", r.Code, r.Body)
	}
	closed := g.mustCode(g.post("/yard-checks/"+id+"/closures", nil), http.StatusOK).JSON()
	if closed["state"] != "CLOSED" || len(closed["discrepancies"].([]any)) == 0 {
		t.Fatalf("a parked trailer nobody observed is missing: %v", closed)
	}
}

func TestYardBoardListsEverySpotAndDoor(t *testing.T) {
	g := newRig(t)
	g.seedSite()
	g.parked("ABC1D23", "TR-1")
	board := g.mustCode(g.get("/sites/WH1/yard-board"), http.StatusOK).JSON()
	if len(board["spots"].([]any)) != 3 || len(board["doors"].([]any)) != 2 {
		t.Fatalf("board %v", board)
	}
}

func TestYardPolicyIsVersionedByETag(t *testing.T) {
	g := newRig(t)
	g.seedSite()
	def := g.mustCode(g.get("/sites/WH1/yard-policy"), http.StatusOK)
	if def.JSON()["declared"] != false || def.Header.Get("ETag") != `W/"1"` {
		t.Fatalf("default policy %s %q", def.Body, def.Header.Get("ETag"))
	}
	declare := map[string]any{"requireAppointment": true, "sealMismatchAction": "REJECT", "leaseSeconds": 120, "dwellThresholdMinutes": map[string]int{"DRY_VAN": 60}}
	if r := g.do("PUT", "/sites/WH1/yard-policy", declare, "Idempotency-Key", g.freshKey(), "If-Match", `W/"7"`); r.Code != http.StatusPreconditionFailed || r.Slug() != "precondition-failed" {
		t.Fatalf("a stale If-Match: %d %s", r.Code, r.Body)
	}
	if r := g.do("PUT", "/sites/WH1/yard-policy", declare, "Idempotency-Key", g.freshKey(), "If-Match", "soon"); r.Code != http.StatusBadRequest || r.Slug() != "invalid-if-match" {
		t.Fatalf("a bad If-Match: %d %s", r.Code, r.Body)
	}
	put := g.mustCode(g.do("PUT", "/sites/WH1/yard-policy", declare, "Idempotency-Key", g.freshKey(), "If-Match", `W/"1"`), http.StatusOK)
	if put.Header.Get("ETag") != `W/"2"` || put.JSON()["declared"] != true || put.JSON()["leaseSeconds"] != float64(120) {
		t.Fatalf("declared policy %s %q", put.Body, put.Header.Get("ETag"))
	}
	declare["leaseSeconds"] = 0
	if r := g.do("PUT", "/sites/WH1/yard-policy", declare, "Idempotency-Key", g.freshKey()); r.Code != http.StatusUnprocessableEntity || r.Slug() != "validation" {
		t.Fatalf("a zero lease: %d %s", r.Code, r.Body)
	}
}

func TestSweepsNeedNoKeyAndReportsKeepTheirShape(t *testing.T) {
	g := newRig(t)
	g.seedSite()
	for _, path := range []string{"/dwell-sweeps", "/move-lease-sweeps"} {
		r := g.mustCode(g.do("POST", path, nil), http.StatusOK).JSON()
		if r["count"] != float64(0) || r["sweptAt"] == nil {
			t.Fatalf("%s: %v", path, r)
		}
	}
	g.mustCode(g.do("POST", "/dwell-sweeps", `{"siteId":"WH1"}`), http.StatusOK)

	win := "?from=2026-11-01T00:00:00Z&to=2026-11-02T00:00:00Z&siteId=WH1"
	for _, path := range []string{"/reports/yard-dwell", "/reports/door-utilisation", "/reports/hostler-productivity"} {
		r := g.mustCode(g.get(path+win), http.StatusOK).JSON()
		if r["siteId"] != "WH1" || len(r["rows"].([]any)) != 0 || r["from"] != "2026-11-01T00:00:00Z" {
			t.Fatalf("%s: %v", path, r)
		}
		if bad := g.get(path + "?from=2026-11-02T00:00:00Z&to=2026-11-01T00:00:00Z"); bad.Code != http.StatusBadRequest || bad.Slug() != "invalid-report-range" {
			t.Fatalf("%s reversed window: %d %s", path, bad.Code, bad.Body)
		}
		if bad := g.get(path + "?from=2026-01-01T00:00:00Z&to=2026-11-01T00:00:00Z"); bad.Code != http.StatusBadRequest {
			t.Fatalf("%s a window over 92 days: %d", path, bad.Code)
		}
	}
	if r := g.get("/reports/yard-dwell"); r.Code != http.StatusBadRequest {
		t.Fatalf("no window: %d", r.Code)
	}
	fresh := g.mustCode(g.get("/reports/freshness"), http.StatusOK).JSON()
	if fresh["lastEventAt"] != nil || fresh["asOf"] == nil {
		t.Fatalf("freshness %v", fresh)
	}
}
