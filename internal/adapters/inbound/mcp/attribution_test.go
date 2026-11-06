package mcp_test

import (
	"strings"
	"testing"
)

// by is the initiated_by object of an event's data (nil when absent).
func by(t *testing.T, data map[string]any) map[string]any {
	t.Helper()
	v, ok := data["initiated_by"]
	if !ok {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("initiated_by = %v, want an object", v)
	}
	return m
}

// An MCP write stamps initiated_by (via mcp plus the supplied ids) into the
// data of EVERY event it raises.
func TestWritesStampInitiatedBy(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	trailerID := r.parkedTrailer("PLATE1", "T-100")
	setup := len(r.env.Store.Events())

	r.ok("request_move", map[string]any{
		"idempotencyKey": "k1", "trailerId": trailerID, "to": door("WH1-D02"),
		"proposalId": "prop-9", "approvedBy": "sup-alice", "agentId": "ops-agent-1",
	})
	moves := r.eventData("YardMoveRequested")
	got := by(t, moves[len(moves)-1])
	if got["via"] != "mcp" || got["proposal_id"] != "prop-9" || got["approved_by"] != "sup-alice" || got["agent_id"] != "ops-agent-1" || len(got) != 4 {
		t.Fatalf("initiated_by = %v", got)
	}

	// The original event data is untouched.
	last := moves[len(moves)-1]
	if last["trailer_id"] != trailerID || last["reason"] != "TO_DOOR" {
		t.Fatalf("event data lost its fields: %v", last)
	}

	// Only what the caller gave is present; via is always set by the adapter.
	r.ok("place_hold", map[string]any{"idempotencyKey": "k2", "trailerId": trailerID, "reason": "QUALITY"})
	held := r.eventData("TrailerHeld")
	if got := by(t, held[len(held)-1]); got["via"] != "mcp" || len(got) != 1 {
		t.Fatalf("initiated_by without ids = %v", got)
	}

	// Blank and padded ids are trimmed to nothing.
	r.ok("release_hold", map[string]any{"idempotencyKey": "k3", "trailerId": trailerID, "agentId": "  ", "approvedBy": " sup-bob "})
	released := r.eventData("TrailerHoldReleased")
	if got := by(t, released[len(released)-1]); got["approved_by"] != "sup-bob" || got["agent_id"] != nil {
		t.Fatalf("initiated_by trimming = %v", got)
	}
	if len(r.env.Store.Events()) == setup {
		t.Fatal("the calls raised no events")
	}
}

// Every event a single call raises is stamped, not just the first: a seal
// BROKEN raises the hold as well as the seal exception.
func TestEveryEventOfACallIsStamped(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	trailerID := r.parkedTrailer("PLATE1", "T-100")
	r.ok("record_seal", map[string]any{"idempotencyKey": "k0", "trailerId": trailerID, "state": "INTACT", "sealNumber": "SEAL-1"})
	before := len(r.env.Store.Events())

	r.ok("record_seal", map[string]any{"idempotencyKey": "k1", "trailerId": trailerID, "state": "BROKEN", "agentId": "a1"})
	events := r.published(before)
	if len(events) < 1 {
		t.Fatal("record_seal BROKEN raised no events")
	}
	for _, e := range events {
		if got := by(t, e.Data); got["via"] != "mcp" || got["agent_id"] != "a1" {
			t.Fatalf("%s is not attributed: %v", e.Name, e.Data)
		}
	}
	held := r.eventData("TrailerHeld")
	if held[len(held)-1]["reason"] != "SEAL_BROKEN" {
		t.Fatalf("TrailerHeld = %v", held[len(held)-1])
	}
}

// The seeding (REST-path) calls carry no initiated_by: REST writes are never
// attributed.
func TestRestWritesCarryNoInitiatedBy(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	r.parkedTrailer("PLATE1", "T-100")
	for _, name := range []string{"VisitCheckedIn", "TrailerParked", "YardMoveRequested", "YardMoveCompleted"} {
		for _, data := range r.eventData(name) {
			if data["initiated_by"] != nil {
				t.Fatalf("%s from a direct use-case call carries initiated_by: %v", name, data)
			}
		}
	}
}

func TestOverlongAttributionIsRefused(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	long := strings.Repeat("x", 129)
	for _, field := range []string{"proposalId", "approvedBy", "agentId"} {
		t.Run(field, func(t *testing.T) {
			res := r.write("open_yard_check", map[string]any{"siteId": "WH1", field: long})
			wantError(t, res, "validation")
			if !strings.Contains(res.Text, field) {
				t.Fatalf("error %q does not name %s", res.Text, field)
			}
		})
	}
	t.Run("NUL byte", func(t *testing.T) {
		wantError(t, r.write("open_yard_check", map[string]any{"siteId": "WH1", "agentId": "a\x00b"}), "validation")
	})
	if n := len(r.env.Store.Events()); n != 0 {
		t.Fatalf("%d events after refused calls", n)
	}
}
