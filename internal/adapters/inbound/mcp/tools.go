package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
)

// Deps is everything the MCP tools need, injected by the composition root. The
// use cases are the SAME ones the REST adapter calls; the adapter never
// constructs an outbound adapter itself.
type Deps struct {
	// Yard is the application service: every tool is one of its methods.
	Yard *usecases.Yard
	// Reports is the read side of the analytics reports (ADR 0006). Nil until
	// the projector's read side is wired: get_yard_dwell_report then answers
	// no rows, exactly like GET /reports/yard-dwell.
	Reports ports.ReportReader
	// Idempotency guards the write tools: the key is required and replays the
	// first answer, over the same store the REST Idempotency-Key uses.
	Idempotency IdempotencyDeps
}

// maxReportDays bounds a report window (the REST bound).
const maxReportDays = 92

// replayMeta marks the result of a write tool answered from an earlier call
// with the same idempotencyKey (the REST Idempotency-Replayed header).
var replayMeta = mcp.Meta{"com.warehouse/idempotency-replayed": true}

// --- read handlers ------------------------------------------------------------

func (d Deps) getYardBoard(ctx context.Context, in siteInput) (views.YardBoard, error) {
	b, err := d.Yard.GetYardBoard(ctx, in.SiteID)
	return b, orMapped(err)
}

func (d Deps) listTrailers(ctx context.Context, in listTrailersInput) (pageOut[views.Trailer], error) {
	limit, after, err := pageParams(in.Limit, in.Cursor)
	if err == nil {
		err = oneOf(in.State, "PARKED", "IN_MOVE", "DOCKED", "DEPARTED")
	}
	if err == nil {
		err = oneOf(in.Kind, "DRY_VAN", "REEFER", "FLATBED", "CONTAINER")
	}
	if err != nil {
		return pageOut[views.Trailer]{}, mapError(err)
	}
	f := views.TrailerFilter{
		SiteID: in.SiteID, State: in.State, Kind: in.Kind, LocationCode: in.LocationCode, UnitNumber: in.UnitNumber, VisitID: in.VisitID,
	}
	p, err := d.Yard.ListTrailers(ctx, f, after, limit)
	if err != nil {
		return pageOut[views.Trailer]{}, mapError(err)
	}
	return pageOf(p), nil
}

func (d Deps) getTrailer(ctx context.Context, in trailerIDInput) (views.Trailer, error) {
	t, err := d.Yard.GetTrailer(ctx, in.TrailerID)
	return t, orMapped(err)
}

func (d Deps) listMoves(ctx context.Context, in listMovesInput) (pageOut[views.Move], error) {
	limit, after, err := pageParams(in.Limit, in.Cursor)
	if err == nil {
		err = oneOf(in.State, "REQUESTED", "CLAIMED", "COMPLETED", "CANCELLED")
	}
	if err != nil {
		return pageOut[views.Move]{}, mapError(err)
	}
	f := views.MoveFilter{SiteID: in.SiteID, State: in.State, TrailerID: in.TrailerID, HostlerRef: in.HostlerRef}
	p, err := d.Yard.ListMoves(ctx, f, after, limit)
	if err != nil {
		return pageOut[views.Move]{}, mapError(err)
	}
	return pageOf(p), nil
}

func (d Deps) listVisits(ctx context.Context, in listVisitsInput) (pageOut[views.Visit], error) {
	limit, after, err := pageParams(in.Limit, in.Cursor)
	if err == nil {
		err = oneOf(in.State, "CHECKED_IN", "CHECKED_OUT", "REJECTED")
	}
	if err != nil {
		return pageOut[views.Visit]{}, mapError(err)
	}
	f := views.VisitFilter{SiteID: in.SiteID, State: in.State, TractorPlate: in.TractorPlate}
	p, err := d.Yard.ListVisits(ctx, f, after, limit)
	if err != nil {
		return pageOut[views.Visit]{}, mapError(err)
	}
	return pageOf(p), nil
}

func (d Deps) getVisit(ctx context.Context, in visitIDInput) (views.Visit, error) {
	v, err := d.Yard.GetVisit(ctx, in.VisitID)
	return v, orMapped(err)
}

func (d Deps) getYardPolicy(ctx context.Context, in siteInput) (views.YardPolicy, error) {
	p, err := d.Yard.GetYardPolicy(ctx, in.SiteID)
	return p, orMapped(err)
}

// dwellReport is the answer of get_yard_dwell_report (the REST report body
// plus the freshness of the read side).
type dwellReport struct {
	From        time.Time        `json:"from"`
	To          time.Time        `json:"to"`
	SiteID      *string          `json:"siteId"`
	Rows        []views.DwellRow `json:"rows"`
	LastEventAt *time.Time       `json:"lastEventAt"`
}

func (d Deps) getYardDwellReport(ctx context.Context, in dwellReportInput) (dwellReport, error) {
	win, err := reportWindow(in)
	if err != nil {
		return dwellReport{}, mapError(err)
	}
	out := dwellReport{From: win.From, To: win.To, Rows: []views.DwellRow{}}
	if win.SiteID != "" {
		out.SiteID = &win.SiteID
	}
	if d.Reports == nil {
		return out, nil
	}
	rows, err := d.Reports.YardDwell(ctx, win)
	if err != nil {
		return dwellReport{}, mapError(err)
	}
	if rows != nil {
		out.Rows = rows
	}
	at, ok, err := d.Reports.LastEventAt(ctx)
	if err != nil {
		return dwellReport{}, mapError(err)
	}
	if ok {
		u := at.UTC()
		out.LastEventAt = &u
	}
	return out, nil
}

// reportWindow reads and validates from/to/siteId.
func reportWindow(in dwellReportInput) (views.ReportWindow, error) {
	from, ferr := time.Parse(time.RFC3339, in.From)
	to, terr := time.Parse(time.RFC3339, in.To)
	if ferr != nil || terr != nil || !to.After(from) || to.Sub(from) > maxReportDays*24*time.Hour {
		return views.ReportWindow{}, errInvalidReportRange
	}
	return views.ReportWindow{From: from.UTC(), To: to.UTC(), SiteID: in.SiteID}, nil
}

// orMapped maps a non-nil error and keeps nil.
func orMapped(err error) error {
	if err == nil {
		return nil
	}
	return mapError(err)
}

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
	return d.Yard.RecordSeal(ctx, in.TrailerID, in.State, in.SealNumber)
}

func (d Deps) openYardCheck(ctx context.Context, in openYardCheckInput) (views.YardCheck, error) {
	return d.Yard.OpenYardCheck(ctx, in.SiteID)
}

// --- registry -----------------------------------------------------------------

// registerTools adds the eight read tools (L1) and the six write tools (L3).
// Every annotations literal stays inline on its tool so the authority fitness
// sensor can prove ReadOnlyHint (internal/architecture). The gate, hostler
// and dock-operator actions (check-in, check-out, claim, complete, door work)
// are floor-client actions on REST and are deliberately NOT tools
// (docs/adr/0007).
func (d Deps) registerTools(server *mcp.Server) {
	d.registerReadTools(server)
	d.registerWriteTools(server)
}

// registerReadTools adds the eight read tools (L1).
func (d Deps) registerReadTools(server *mcp.Server) {
	// The shared hint value; the annotations literals themselves stay inline.
	closedWorld := false

	addRead(server, &mcp.Tool{
		Name: "get_yard_board",
		Description: "Read the live map of one site: every active yard spot and dock door (code, kind, dockFlow, closed, and the trailer " +
			"parked or docked there), the trailers in motion (inMove) and the open moves. Use it first to see what is where. " +
			"Read-only; fails with site-not-found for a site with no known location.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.getYardBoard)

	addRead(server, &mcp.Tool{
		Name: "list_trailers",
		Description: "List trailers on site by arrival, a page at a time (default 100, max 500), optionally by siteId, state " +
			"(PARKED, IN_MOVE, DOCKED, DEPARTED; omitted means not DEPARTED), kind, locationCode, unitNumber or visitId. Each trailer " +
			"shows its location, door work, seal, hold, load state and open move. Pass the returned nextCursor for the next page. " +
			"Read-only; a bad limit, cursor or enum fails with invalid-limit, invalid-cursor or invalid-state.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.listTrailers)

	addRead(server, &mcp.Tool{
		Name: "get_trailer",
		Description: "Read one trailer by id: where it is, load state, door work, seal, hold, ASNs, visit, appointment, arrival time " +
			"(the dwell clock start), open move and version. Read-only; fails with trailer-not-found for an unknown id.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.getTrailer)

	addRead(server, &mcp.Tool{
		Name: "list_moves",
		Description: "List yard moves, oldest request first, a page at a time (default 100, max 500), optionally by siteId, state " +
			"(REQUESTED, CLAIMED, COMPLETED, CANCELLED), trailerId or hostlerRef. Each move shows performer, from, to, reason, priority " +
			"band and due time, lease and actual location. Read-only; a bad limit, cursor or state fails with invalid-limit, " +
			"invalid-cursor or invalid-state.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.listMoves)

	addRead(server, &mcp.Tool{
		Name: "list_visits",
		Description: "List gate visits, newest arrival first, a page at a time (default 100, max 500), optionally by siteId, state " +
			"(CHECKED_IN, CHECKED_OUT, REJECTED) or tractorPlate. Read-only; a bad limit, cursor or state fails with invalid-limit, " +
			"invalid-cursor or invalid-state.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.listVisits)

	addRead(server, &mcp.Tool{
		Name: "get_visit",
		Description: "Read one gate visit by id: carrier, tractor plate, purpose, appointment, state, rejection reason, the trailers it " +
			"brought and took, arrival and departure. Read-only; fails with visit-not-found for an unknown id.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.getVisit)

	addRead(server, &mcp.Tool{
		Name: "get_yard_policy",
		Description: "Read the yard policy of a site: whether an appointment is required, the holding spots and parking zones, the dwell " +
			"thresholds per trailer kind, the lease seconds, the seal mismatch action (FLAG or REJECT) and the free time. A site that " +
			"never declared one answers the defaults (declared false). Read-only.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.getYardPolicy)

	addRead(server, &mcp.Tool{
		Name: "get_yard_dwell_report",
		Description: "Read the yard dwell report of a window (from and to, RFC 3339, at most 92 days): per trailer kind the trailers, " +
			"average and maximum dwell minutes and the average driver wait. The rows come from the analytics read side and are empty " +
			"until it is wired; lastEventAt tells how fresh they are (null until the first event). Read-only; a bad window fails " +
			"with invalid-report-range.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.getYardDwellReport)
}

// registerWriteTools adds the six write tools (L3).
func (d Deps) registerWriteTools(server *mcp.Server) {
	// The shared hint values; the annotations literals themselves stay inline.
	closedWorld, notDestructive, destructive := false, false, true

	addWrite(server, d, &mcp.Tool{
		Name: "request_move",
		Description: "Ask for a trailer to be moved to a free spot or door of its site. The destination is validated now (known active " +
			"slot, door open and flow-compatible, trailer not on hold) and RESERVED by the open move; a hostler then claims it from the " +
			"floor client. Changes state (L3): call it only for a move a human approved, and pass proposalId, approvedBy and agentId so " +
			"the move is attributable. idempotencyKey is required. Fails with trailer-not-found, trailer-has-open-move, " +
			"location-occupied, destination-reserved, door-closed, door-flow-mismatch, trailer-on-hold, unknown-location or validation.",
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
		Description: "Record the seal of a trailer: state INTACT with the sealNumber, or BROKEN (which places a SEAL_BROKEN hold). Changes " +
			"state (L3): call it only when a human approved, and pass proposalId, approvedBy and agentId. idempotencyKey is required. " +
			"Fails with trailer-not-found, illegal-transition or validation.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &notDestructive, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL3),
	}, d.recordSeal)

	addWrite(server, d, &mcp.Tool{
		Name: "open_yard_check",
		Description: "Open a yard check (an audit) of a site. The walk itself (observations) and the closing reconciliation are supervisor " +
			"actions of the floor client. Changes state (L3): call it only when a human approved, and pass proposalId, approvedBy and " +
			"agentId. idempotencyKey is required. Fails with yard-check-open when one is already open for the site, or validation.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &notDestructive, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL3),
	}, d.openYardCheck)
}

// addRead registers one read tool. A handler error is returned to the SDK as
// the handler's error, which the SDK turns into an isError tool result (never
// a transport/protocol failure), carrying the error text.
func addRead[In, Out any](server *mcp.Server, tool *mcp.Tool, handle func(context.Context, In) (Out, error)) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := handle(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, out, nil
	})
}

// writeInput is what the write wrapper needs of an input: the arguments every
// write tool embeds.
type writeInput interface{ writeMeta() callArgs }

// addWrite registers one write tool behind the idempotency guard: the caller's
// attribution rides the context so every event the call raises carries
// `initiated_by`, and the key makes a retry return the first answer.
func addWrite[In writeInput, Out any](server *mcp.Server, d Deps, tool *mcp.Tool, handle func(context.Context, In) (Out, error)) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		args := in.writeMeta()
		by, err := newInitiator(args.ProposalID, args.ApprovedBy, args.AgentID)
		if err != nil {
			return nil, zero, mapError(err)
		}
		fp, err := fingerprint(tool.Name, in)
		if err != nil {
			return nil, zero, mapError(err)
		}
		out, replayed, err := keyed(withInitiator(ctx, by), d.Idempotency, args.IdempotencyKey, fp, func(ctx context.Context) (Out, error) {
			return handle(ctx, in)
		})
		if err != nil {
			return nil, zero, mapError(err)
		}
		if replayed {
			return &mcp.CallToolResult{Meta: replayMeta}, out, nil
		}
		return nil, out, nil
	})
}
