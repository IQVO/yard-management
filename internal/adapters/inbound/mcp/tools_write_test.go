package mcp_test

import (
	"testing"

	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/domain/shared"
)

func door(code string) map[string]any { return map[string]any{"kind": "DOOR", "code": code} }
func spot(code string) map[string]any { return map[string]any{"kind": "SPOT", "code": code} }

func TestRequestMove(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	trailerID := r.parkedTrailer("PLATE1", "T-100")

	mv := r.ok("request_move", map[string]any{
		"idempotencyKey": "k1", "trailerId": trailerID, "to": door("WH1-D02"), "priorityBand": 3, "dueBy": "2026-11-05T16:00:00Z",
	}).Data
	if mv["state"] != "REQUESTED" || mv["performer"] != "HOSTLER" || mv["reason"] != "TO_DOOR" || mv["trailerId"] != trailerID {
		t.Fatalf("request_move = %v", mv)
	}
	if p := mv["priority"].(map[string]any); p["band"] != 3.0 || p["dueBy"] != "2026-11-05T16:00:00Z" {
		t.Fatalf("priority = %v", p)
	}
	if got := r.env.Store.EventNames(); got[len(got)-1] != "YardMoveRequested" {
		t.Fatalf("events = %v, want YardMoveRequested last", got)
	}
}

func TestRequestMoveRefusals(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	first := r.parkedTrailer("PLATE1", "T-100")
	second := r.parkedTrailer("PLATE2", "T-200")
	r.ok("request_move", map[string]any{"idempotencyKey": "k1", "trailerId": first, "to": door("WH1-D02")})

	cases := []struct {
		name string
		args map[string]any
		slug string
	}{
		{"open move", map[string]any{"trailerId": first, "to": door("WH1-D01")}, "trailer-has-open-move"},
		{"reserved destination", map[string]any{"trailerId": second, "to": door("WH1-D02")}, "destination-reserved"},
		{"unknown trailer", map[string]any{"trailerId": "trl-nope", "to": door("WH1-D01")}, "trailer-not-found"},
		{"unknown location", map[string]any{"trailerId": second, "to": spot("WH1-NOPE")}, "unknown-location"},
		{"bad kind", map[string]any{"trailerId": second, "to": map[string]any{"kind": "GATE", "code": "G1"}}, "unknown-location"},
		{"bad reason", map[string]any{"trailerId": second, "to": door("WH1-D01"), "reason": "WHIM"}, "validation"},
		{"bad band", map[string]any{"trailerId": second, "to": door("WH1-D01"), "priorityBand": 12}, "validation"},
		{"bad dueBy", map[string]any{"trailerId": second, "to": door("WH1-D01"), "dueBy": "soon"}, "validation"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { wantError(t, r.write("request_move", tc.args), tc.slug) })
	}
}

func TestCancelMove(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	trailerID := r.parkedTrailer("PLATE1", "T-100")
	mv := r.ok("request_move", map[string]any{"idempotencyKey": "k1", "trailerId": trailerID, "to": door("WH1-D02")}).Data

	got := r.ok("cancel_move", map[string]any{"idempotencyKey": "k2", "moveId": str(mv, "moveId"), "reason": "door needed elsewhere"}).Data
	if got["state"] != "CANCELLED" {
		t.Fatalf("cancel_move = %v", got)
	}
	wantError(t, r.write("cancel_move", map[string]any{"moveId": str(mv, "moveId")}), "illegal-transition")
	wantError(t, r.write("cancel_move", map[string]any{"moveId": "mv-nope"}), "move-not-found")
}

func TestHoldLifecycle(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	trailerID := r.parkedTrailer("PLATE1", "T-100")

	held := r.ok("place_hold", map[string]any{"idempotencyKey": "k1", "trailerId": trailerID, "reason": "DAMAGED"}).Data
	if held["hold"] != "DAMAGED" {
		t.Fatalf("place_hold = %v", held)
	}
	wantError(t, r.write("request_move", map[string]any{"trailerId": trailerID, "to": door("WH1-D02")}), "trailer-on-hold")
	wantError(t, r.write("place_hold", map[string]any{"trailerId": trailerID, "reason": "SPITE"}), "validation")

	released := r.ok("release_hold", map[string]any{"idempotencyKey": "k2", "trailerId": trailerID}).Data
	if released["hold"] != nil {
		t.Fatalf("release_hold = %v", released)
	}
	wantError(t, r.write("release_hold", map[string]any{"trailerId": trailerID}), "trailer-not-on-hold")
	wantError(t, r.write("place_hold", map[string]any{"trailerId": "trl-nope", "reason": "DAMAGED"}), "trailer-not-found")
}

func TestRecordSeal(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	trailerID := r.parkedTrailer("PLATE1", "T-100")

	intact := r.ok("record_seal", map[string]any{"idempotencyKey": "k1", "trailerId": trailerID, "state": "INTACT", "sealNumber": "SEAL-77"}).Data
	if seal := intact["seal"].(map[string]any); seal["state"] != "INTACT" || seal["number"] != "SEAL-77" {
		t.Fatalf("record_seal INTACT = %v", intact)
	}
	broken := r.ok("record_seal", map[string]any{"idempotencyKey": "k2", "trailerId": trailerID, "state": "BROKEN"}).Data
	if broken["hold"] != "SEAL_BROKEN" || broken["seal"].(map[string]any)["state"] != "BROKEN" {
		t.Fatalf("record_seal BROKEN = %v", broken)
	}
	wantError(t, r.write("record_seal", map[string]any{"trailerId": trailerID, "state": "LOOSE"}), "validation")
	wantError(t, r.write("record_seal", map[string]any{"trailerId": trailerID, "state": "INTACT"}), "validation")
}

func TestSecureAndReleaseSecuring(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	trailerID := r.parkedTrailer("PLATE1", "T-100")
	wantError(t, r.write("secure_trailer", map[string]any{"trailerId": trailerID, "method": "RESTRAINT"}), "illegal-transition")

	y, ctx := r.env.Yard, r.env.Ctx
	if _, err := y.RequestMove(ctx, usecases.RequestMoveInput{TrailerID: trailerID, To: shared.Location{Kind: shared.Door, Code: "WH1-D02"}}); err != nil {
		t.Fatal(err)
	}
	mv, err := y.ClaimNextMove(ctx, "WH1", "h1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := y.CompleteMove(ctx, usecases.CompleteMoveInput{MoveID: mv.MoveID, HostlerRef: "h1", Actual: mv.To}); err != nil {
		t.Fatal(err)
	}

	got := r.ok("secure_trailer", map[string]any{"idempotencyKey": "k1", "trailerId": trailerID, "method": "WHEEL_CHOCKS", "securerRef": "op-3", "agentId": "agent-9"}).Data
	if got["securedBy"] != "WHEEL_CHOCKS" || got["securerRef"] != "op-3" || got["securedAt"] == nil {
		t.Fatalf("secure_trailer = %v", got)
	}
	wantError(t, r.write("secure_trailer", map[string]any{"trailerId": trailerID, "method": "RESTRAINT"}), "illegal-transition")
	wantError(t, r.write("secure_trailer", map[string]any{"trailerId": trailerID, "method": "LOCKS"}), "validation")
	wantError(t, r.write("secure_trailer", map[string]any{"trailerId": "trl-nope", "method": "RESTRAINT"}), "trailer-not-found")
	wantError(t, r.write("request_move", map[string]any{"trailerId": trailerID, "to": map[string]any{"kind": "SPOT", "code": "WH1-Y02"}}), "trailer-still-secured")

	released := r.ok("release_securing", map[string]any{"idempotencyKey": "k2", "trailerId": trailerID}).Data
	if released["securedBy"] != nil || released["securedAt"] != nil {
		t.Fatalf("release_securing = %v", released)
	}
	wantError(t, r.write("release_securing", map[string]any{"trailerId": trailerID}), "trailer-not-secured")
	if events := r.eventData("TrailerSecured"); len(events) != 1 || events[0]["initiated_by"] == nil || events[0]["method"] != "WHEEL_CHOCKS" {
		t.Errorf("TrailerSecured events %v: want one, attributed by the MCP write", events)
	}
	if events := r.eventData("TrailerSecuringReleased"); len(events) != 1 {
		t.Errorf("TrailerSecuringReleased events %v: want one", events)
	}
}

func TestOpenYardCheck(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	check := r.ok("open_yard_check", map[string]any{"idempotencyKey": "k1", "siteId": "WH1"}).Data
	if check["state"] != "OPEN" || check["siteId"] != "WH1" {
		t.Fatalf("open_yard_check = %v", check)
	}
	wantError(t, r.write("open_yard_check", map[string]any{"siteId": "WH1"}), "yard-check-open")
}

// The write tools validate their arguments at the schema: a missing key or
// required argument never reaches a use case.
func TestWriteInputsAreValidatedBySchema(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	cases := map[string]struct {
		tool string
		args map[string]any
	}{
		"no idempotencyKey":     {"open_yard_check", map[string]any{"siteId": "WH1"}},
		"no siteId":             {"open_yard_check", map[string]any{"idempotencyKey": "k"}},
		"unknown argument":      {"open_yard_check", map[string]any{"idempotencyKey": "k", "siteId": "WH1", "dryRun": true}},
		"no destination":        {"request_move", map[string]any{"idempotencyKey": "k", "trailerId": "trl-1"}},
		"band is not an int":    {"request_move", map[string]any{"idempotencyKey": "k", "trailerId": "trl-1", "to": door("D"), "priorityBand": "high"}},
		"hold without a reason": {"place_hold", map[string]any{"idempotencyKey": "k", "trailerId": "trl-1"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res := r.call(tc.tool, tc.args)
			if !res.IsError {
				t.Fatalf("%s accepted %v", tc.tool, tc.args)
			}
		})
	}
	if n := len(r.env.Store.Events()); n != 0 {
		t.Fatalf("%d events after refused calls", n)
	}
}
