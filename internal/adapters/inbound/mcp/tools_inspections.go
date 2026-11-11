package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/inspection"
	"github.com/claudioed/yard-management/internal/domain/shared"
)

// The compliance inspection tools (ADR 0015): two reads at L1 and three writes
// at L3. The gate itself (a PASSED inspection before a trailer docks, loads or
// leaves) is a yard policy enforced by the use cases, not a tool.

func (d Deps) getInspection(ctx context.Context, in inspectionIDInput) (views.Inspection, error) {
	i, err := d.Yard.GetInspection(ctx, in.InspectionID)
	return i, orMapped(err)
}

func (d Deps) listTrailerInspections(ctx context.Context, in listTrailerInspectionsInput) (pageOut[views.Inspection], error) {
	limit, after, err := pageParams(in.Limit, in.Cursor)
	if err != nil {
		return pageOut[views.Inspection]{}, mapError(err)
	}
	p, err := d.Yard.ListTrailerInspections(ctx, in.TrailerID, after, limit)
	if err != nil {
		return pageOut[views.Inspection]{}, mapError(err)
	}
	return pageOf(p), nil
}

func (d Deps) openInspection(ctx context.Context, in openInspectionInput) (views.Inspection, error) {
	return d.Yard.OpenInspection(ctx, usecases.OpenInspectionInput{
		TrailerID: in.TrailerID, TemplateID: in.TemplateID, TemplateVersion: in.TemplateVersion, Gate: shared.Gate(in.Gate),
		VisitID: in.VisitID, PerformerRef: in.PerformerRef,
	})
}

func (d Deps) recordInspectionItem(ctx context.Context, in recordInspectionItemInput) (views.Inspection, error) {
	return d.Yard.RecordInspectionItem(ctx, in.InspectionID, in.ItemCode, inspection.ItemRecord{
		Result: inspection.Result(in.Result), Note: in.Note, EvidenceRefs: in.EvidenceRefs,
	})
}

func (d Deps) completeInspection(ctx context.Context, in completeInspectionInput) (views.Inspection, error) {
	return d.Yard.CompleteInspection(ctx, in.InspectionID, inspection.ConditionGrade(in.ConditionGrade))
}

// registerInspectionTools adds the two inspection read tools (L1) and the three
// write tools (L3).
func (d Deps) registerInspectionTools(server *mcp.Server) {
	closedWorld, notDestructive := false, false

	addRead(server, &mcp.Tool{
		Name: "get_inspection",
		Description: "Read one compliance inspection by id: the trailer, the template and version it was opened from (a snapshot), the gate, the status " +
			"(OPEN, PASSED or FAILED), every item with its result, note and evidence references, the condition grade and the instants. " +
			"Read-only; fails with inspection-not-found for an unknown id.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.getInspection)

	addRead(server, &mcp.Tool{
		Name: "list_trailer_inspections",
		Description: "List the compliance inspections of one trailer, newest first, a page at a time (default 100, max 500). Use it to see whether a trailer has " +
			"passed the inspection a gate asks for. Pass the returned nextCursor for the next page. Read-only; fails with trailer-not-found, or with invalid-limit " +
			"or invalid-cursor for a bad page argument.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.listTrailerInspections)

	addWrite(server, d, &mcp.Tool{
		Name: "open_inspection",
		Description: "Open a compliance inspection checklist for a trailer on site: a snapshot of the template (the latest version unless templateVersion is given) with every item " +
			"pending, for a gate (PRE_DOCK, PRE_LOAD, PRE_DEPARTURE or ON_DEMAND). One inspection per trailer, template and gate is open at a time. Changes state (L3): call it " +
			"only when a human approved, and pass proposalId, approvedBy and agentId. idempotencyKey is required. Fails with trailer-not-found, illegal-transition (the trailer left), " +
			"unknown-template, inspection-already-open, visit-not-found or validation.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &notDestructive, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL3),
	}, d.openInspection)

	addWrite(server, d, &mcp.Tool{
		Name: "record_inspection_item",
		Description: "Record what the inspector found at one item of an OPEN inspection: PASS, FAIL or NOT_APPLICABLE (only where the template allows it), with an optional note and " +
			"opaque evidence references. Recording again replaces the result. Changes state (L3): call it only when a human approved, and pass proposalId, approvedBy and agentId. " +
			"idempotencyKey is required. Fails with inspection-not-found, inspection-closed, unknown-item, not-applicable-not-allowed or validation.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &notDestructive, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL3),
	}, d.recordInspectionItem)

	addWrite(server, d, &mcp.Tool{
		Name: "complete_inspection",
		Description: "Complete an inspection. It is FAILED when a required item failed (and the trailer is then placed on hold in the same step, unless it already is held), PASSED otherwise; " +
			"completing is final. Every required item needs a result first. Changes state (L3): call it only when a human approved, and pass proposalId, approvedBy and agentId. " +
			"idempotencyKey is required. Fails with inspection-not-found, inspection-closed, required-item-pending or validation.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &notDestructive, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL3),
	}, d.completeInspection)
}
