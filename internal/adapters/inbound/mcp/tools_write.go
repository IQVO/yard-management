package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
)

// --- write handlers -----------------------------------------------------------
//
// A write handler returns the RAW error of the use case: the idempotency guard
// tells a refusal (stored, replayed) from a failure (rolled back, retryable)
// by the catalogue, and the wrapper maps the error for the model afterwards.

func (d Deps) requestMove(ctx context.Context, in requestMoveInput) (views.Move, error) {
	var dueBy time.Time
	if in.DueBy != "" {
		t, err := time.Parse(time.RFC3339, in.DueBy)
		if err != nil {
			return views.Move{}, shared.Invalid("dueBy must be an RFC 3339 instant")
		}
		dueBy = t.UTC()
	}
	return d.Yard.RequestMove(ctx, usecases.RequestMoveInput{
		TrailerID: in.TrailerID,
		To:        shared.Location{Kind: shared.LocationKind(in.To.Kind), Code: in.To.Code},
		Reason:    yardmove.Reason(in.Reason),
		Band:      in.PriorityBand,
		DueBy:     dueBy,
	})
}

func (d Deps) cancelMove(ctx context.Context, in cancelMoveInput) (views.Move, error) {
	return d.Yard.CancelMove(ctx, in.MoveID, in.Reason)
}

func (d Deps) placeHold(ctx context.Context, in placeHoldInput) (views.Trailer, error) {
	return d.Yard.PlaceHold(ctx, in.TrailerID, trailer.HoldReason(in.Reason))
}

func (d Deps) releaseHold(ctx context.Context, in releaseHoldInput) (views.Trailer, error) {
	return d.Yard.ReleaseHold(ctx, in.TrailerID)
}

func (d Deps) recordSeal(ctx context.Context, in recordSealInput) (views.Trailer, error) {
	return d.Yard.RecordSeal(ctx, in.TrailerID, in.State, in.SealNumber, in.SealClass)
}

func (d Deps) secureTrailer(ctx context.Context, in secureTrailerInput) (views.Trailer, error) {
	return d.Yard.SecureTrailer(ctx, in.TrailerID, in.Method, in.SecurerRef)
}

func (d Deps) releaseSecuring(ctx context.Context, in releaseSecuringInput) (views.Trailer, error) {
	return d.Yard.ReleaseTrailerSecuring(ctx, in.TrailerID)
}

func (d Deps) openYardCheck(ctx context.Context, in openYardCheckInput) (views.YardCheck, error) {
	return d.Yard.OpenYardCheck(ctx, in.SiteID)
}

// registerWriteTools adds the eight write tools (L3); the inspection tools are in tools_inspections.go.
func (d Deps) registerWriteTools(server *mcp.Server) {
	// The shared hint values; the annotations literals themselves stay inline.
	closedWorld, notDestructive, destructive := false, false, true

	addWrite(server, d, &mcp.Tool{
		Name: "request_move",
		Description: "Ask for a trailer to be moved to a free spot or door of its site. The destination is validated now (known active " +
			"slot, door open and flow-compatible, trailer not on hold; a trailer with a hazmat class or a temperature-control need only to a spot its kind allows) and RESERVED by the open move; a hostler then claims it from the " +
			"floor client. Changes state (L3): call it only for a move a human approved, and pass proposalId, approvedBy and agentId so " +
			"the move is attributable. idempotencyKey is required. Fails with trailer-not-found, trailer-has-open-move, " +
			"location-occupied, destination-reserved, door-closed, door-flow-mismatch, trailer-on-hold, trailer-still-secured, placement-rule-violated, inspection-required or inspection-failed (a door move of a trailer without the passed PRE_DOCK inspection the site policy requires), unknown-location or validation.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &notDestructive, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL3),
	}, d.requestMove)

	addWrite(server, d, &mcp.Tool{
		Name: "cancel_move",
		Description: "Cancel an open (REQUESTED or CLAIMED) move. Cancelling a claimed hostler move returns the trailer to where it was when " +
			"it can. Changes state (L3): call it only when a human approved, and pass proposalId, approvedBy and agentId. idempotencyKey is " +
			"required. Fails with move-not-found or illegal-transition (the move already ended).",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &destructive, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL3),
	}, d.cancelMove)

	addWrite(server, d, &mcp.Tool{
		Name: "place_hold",
		Description: "Put a hold (DAMAGED, SEAL_BROKEN, NOT_FOUND, QUALITY or SECURITY) on a trailer. A held trailer is not docked or moved to " +
			"a door; a hold on an already docked trailer blocks its door work until released. Changes state (L3): call it only when a human " +
			"approved, and pass proposalId, approvedBy and agentId. idempotencyKey is required. Fails with trailer-not-found, " +
			"illegal-transition or validation.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &notDestructive, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL3),
	}, d.placeHold)

	addWrite(server, d, &mcp.Tool{
		Name: "release_hold",
		Description: "Release the hold of a trailer. Changes state (L3): call it only when a human approved, and pass proposalId, approvedBy " +
			"and agentId. idempotencyKey is required. Fails with trailer-not-found or trailer-not-on-hold.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &notDestructive, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL3),
	}, d.releaseHold)

	addWrite(server, d, &mcp.Tool{
		Name: "record_seal",
		Description: "Record the seal of a trailer: state INTACT or APPLIED with the sealNumber and an optional ISO 17712 sealClass (I, S or H), or BROKEN (which places a SEAL_BROKEN hold). Changes " +
			"state (L3): call it only when a human approved, and pass proposalId, approvedBy and agentId. idempotencyKey is required. " +
			"Fails with trailer-not-found, illegal-transition, validation, invalid-seal-class or seal-class-too-low.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &notDestructive, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL3),
	}, d.recordSeal)

	addWrite(server, d, &mcp.Tool{
		Name: "secure_trailer",
		Description: "Record that a DOCKED trailer is secured, by a vehicle restraint (RESTRAINT) or wheel chocks (WHEEL_CHOCKS): the dock safety interlock " +
			"(OSHA 29 CFR 1910.178(k)(1), ANSI MH30.3). Where the site's yard policy sets requireSecuringBeforeDoorWork, door work does not start until this is recorded. " +
			"The same method again changes nothing. Changes state (L3): call it only when a human approved, and pass proposalId, approvedBy and agentId. " +
			"idempotencyKey is required. Fails with trailer-not-found, illegal-transition (not docked, or already secured another way), trailer-has-open-move or validation.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &notDestructive, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL3),
	}, d.secureTrailer)

	addWrite(server, d, &mcp.Tool{
		Name: "release_securing",
		Description: "Record that the restraint of a docked trailer was released or its chocks removed. Required before the trailer is moved away from its door or leaves " +
			"the site; refused while door work is in progress. Changes state (L3): call it only when a human approved, and pass proposalId, approvedBy and agentId. " +
			"idempotencyKey is required. Fails with trailer-not-found, trailer-not-secured or door-work-open.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &notDestructive, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL3),
	}, d.releaseSecuring)

	addWrite(server, d, &mcp.Tool{
		Name: "open_yard_check",
		Description: "Open a yard check (an audit) of a site. The walk itself (observations) and the closing reconciliation are supervisor " +
			"actions of the floor client. Changes state (L3): call it only when a human approved, and pass proposalId, approvedBy and " +
			"agentId. idempotencyKey is required. Fails with yard-check-open when one is already open for the site, or validation.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &notDestructive, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL3),
	}, d.openYardCheck)
}
