package mcp_test

import (
	"testing"
)

// openInspection opens a PRE_DOCK inspection of a built-in template over the tool.
func (r *rig) openInspection(trailerID, template, gate string) map[string]any {
	r.t.Helper()
	return r.ok("open_inspection", map[string]any{
		"idempotencyKey": r.key(), "trailerId": trailerID, "templateId": template, "gate": gate, "performerRef": "emp-7",
		"proposalId": "prop-3", "approvedBy": "qa-alice", "agentId": "ops-agent-1",
	}).Data
}

// passEveryItem records PASS on every item of the template, and NOT_APPLICABLE
// with a note and evidence on the fan housing of a dry container.
func (r *rig) passEveryItem(id string, items []any) {
	for _, it := range items {
		code := str(it.(map[string]any), "itemCode")
		args := map[string]any{"idempotencyKey": r.key(), "inspectionId": id, "itemCode": code, "result": "PASS"}
		if code == "fan-housing" {
			args["result"], args["note"], args["evidenceRefs"] = "NOT_APPLICABLE", "dry container", []string{"photo:1"}
		}
		r.ok("record_inspection_item", args)
	}
}

// completeClean completes the inspection CLEAN and checks the outcome, the
// InspectionCompleted event with its MCP attribution, and that nothing failed.
func (r *rig) completeClean(t *testing.T, id string) {
	t.Helper()
	done := r.ok("complete_inspection", map[string]any{"idempotencyKey": r.key(), "inspectionId": id, "conditionGrade": "CLEAN", "approvedBy": "qa-alice"}).Data
	if done["status"] != "PASSED" || done["conditionGrade"] != "CLEAN" || done["completedAt"] == nil {
		t.Fatalf("complete_inspection = %v", done)
	}
	completed := r.eventData("InspectionCompleted")
	if len(completed) != 1 || completed[0]["outcome"] != "PASSED" || by(t, completed[0])["approved_by"] != "qa-alice" || by(t, completed[0])["via"] != "mcp" {
		t.Fatalf("InspectionCompleted = %v", completed)
	}
	if len(r.eventData("InspectionFailed")) != 0 || len(r.eventData("TrailerHeld")) != 0 {
		t.Fatal("a pass raises no failure and no hold")
	}
}

func TestInspectionToolsRunAChecklistToAPass(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	trailerID := r.parkedTrailer("PLATE1", "T-100")

	opened := r.openInspection(trailerID, "empty-container-7-point", "PRE_DOCK")
	id := str(opened, "inspectionId")
	items := opened["items"].([]any)
	if opened["status"] != "OPEN" || opened["templateVersion"] != 1.0 || opened["gate"] != "PRE_DOCK" || len(items) != 10 || items[0].(map[string]any)["result"] != nil {
		t.Fatalf("open_inspection = %v", opened)
	}
	r.passEveryItem(id, items)
	got := r.ok("get_inspection", map[string]any{"inspectionId": id}).Data
	if got["status"] != "OPEN" || got["version"] != 11.0 || got["items"].([]any)[7].(map[string]any)["result"] != "NOT_APPLICABLE" ||
		got["items"].([]any)[7].(map[string]any)["evidenceRefs"].([]any)[0] != "photo:1" {
		t.Fatalf("get_inspection = %v", got)
	}

	r.completeClean(t, id)

	list := r.ok("list_trailer_inspections", map[string]any{"trailerId": trailerID}).Data
	if rows := list["items"].([]any); len(rows) != 1 || rows[0].(map[string]any)["inspectionId"] != id {
		t.Fatalf("list_trailer_inspections = %v", list)
	}
}

func TestCompleteInspectionFailingHoldsTheTrailer(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	trailerID := r.parkedTrailer("PLATE1", "T-100")
	id := str(r.openInspection(trailerID, "reefer-pre-cool-and-sanitation", "PRE_LOAD"), "inspectionId")
	for _, code := range []string{"physical-condition", "sanitary-condition-washout", "no-previous-cargo-residue", "no-pest-infestation", "pre-cooled-to-set-temperature", "receiver-temperature-abuse-check"} {
		result := "PASS"
		if code == "no-previous-cargo-residue" {
			result = "FAIL"
		}
		r.ok("record_inspection_item", map[string]any{"idempotencyKey": r.key(), "inspectionId": id, "itemCode": code, "result": result, "note": "fish smell"})
	}
	done := r.ok("complete_inspection", map[string]any{"idempotencyKey": r.key(), "inspectionId": id}).Data
	if done["status"] != "FAILED" {
		t.Fatalf("complete_inspection = %v", done)
	}
	trailer := r.ok("get_trailer", map[string]any{"trailerId": trailerID}).Data
	if trailer["hold"] != "QUALITY" {
		t.Fatalf("the trailer is held for %v, want QUALITY", trailer["hold"])
	}
	failed := r.eventData("InspectionFailed")
	if len(failed) != 1 || failed[0]["hold_placed"] != true || by(t, failed[0])["agent_id"] != nil {
		t.Fatalf("InspectionFailed = %v", failed)
	}
	if held := r.eventData("TrailerHeld"); len(held) != 1 || by(t, held[0])["via"] != "mcp" {
		t.Fatalf("TrailerHeld = %v, want one attributed to mcp", held)
	}
}

func TestInspectionToolRefusals(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	trailerID := r.parkedTrailer("PLATE1", "T-100")
	opened := r.openInspection(trailerID, "tractor-trailer-17-point", "PRE_DOCK")
	id := str(opened, "inspectionId")

	wantError(t, r.write("open_inspection", map[string]any{"trailerId": trailerID, "templateId": "tractor-trailer-17-point", "gate": "PRE_DOCK"}), "inspection-already-open")
	wantError(t, r.write("open_inspection", map[string]any{"trailerId": "trl-nope", "templateId": "tractor-trailer-17-point", "gate": "PRE_DOCK"}), "trailer-not-found")
	wantError(t, r.write("open_inspection", map[string]any{"trailerId": trailerID, "templateId": "ghost", "gate": "PRE_DOCK"}), "unknown-template")
	wantError(t, r.write("open_inspection", map[string]any{"trailerId": trailerID, "templateId": "empty-container-7-point", "gate": "SOMEDAY"}), "validation")
	wantError(t, r.write("record_inspection_item", map[string]any{"inspectionId": "insp-nope", "itemCode": "engine", "result": "PASS"}), "inspection-not-found")
	wantError(t, r.write("record_inspection_item", map[string]any{"inspectionId": id, "itemCode": "wipers", "result": "PASS"}), "unknown-item")
	wantError(t, r.write("record_inspection_item", map[string]any{"inspectionId": id, "itemCode": "engine", "result": "NOT_APPLICABLE"}), "not-applicable-not-allowed")
	wantError(t, r.write("complete_inspection", map[string]any{"inspectionId": id}), "required-item-pending")
	wantError(t, r.call("get_inspection", map[string]any{"inspectionId": "insp-nope"}), "inspection-not-found")
	wantError(t, r.call("list_trailer_inspections", map[string]any{"trailerId": "trl-nope"}), "trailer-not-found")
	wantError(t, r.call("list_trailer_inspections", map[string]any{"trailerId": trailerID, "limit": 501}), "invalid-limit")
	wantError(t, r.call("list_trailer_inspections", map[string]any{"trailerId": trailerID, "cursor": "%%%"}), "invalid-cursor")
}

func TestListTrailerInspectionsPages(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	trailerID := r.parkedTrailer("PLATE1", "T-100")
	for _, gate := range []string{"PRE_DOCK", "PRE_LOAD", "PRE_DEPARTURE"} {
		r.openInspection(trailerID, "empty-container-7-point", gate)
		r.env.Store.Advance(60e9)
	}
	first := r.ok("list_trailer_inspections", map[string]any{"trailerId": trailerID, "limit": 2}).Data
	rows := first["items"].([]any)
	if len(rows) != 2 || rows[0].(map[string]any)["gate"] != "PRE_DEPARTURE" || first["nextCursor"] == nil {
		t.Fatalf("first page = %v", first)
	}
	second := r.ok("list_trailer_inspections", map[string]any{"trailerId": trailerID, "limit": 2, "cursor": first["nextCursor"]}).Data
	if rows := second["items"].([]any); len(rows) != 1 || rows[0].(map[string]any)["gate"] != "PRE_DOCK" || second["nextCursor"] != nil {
		t.Fatalf("second page = %v", second)
	}
}
