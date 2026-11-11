package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/yard-management/internal/application/views"
)

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
			"thresholds per trailer kind, the lease seconds, the seal mismatch action (FLAG or REJECT), the minimum ISO 17712 seal class (I, S or H; null = none) and the free time. A site that " +
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
