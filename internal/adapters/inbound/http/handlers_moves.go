package http

import (
	"net/http"

	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
)

func loadStateOf(s string) shared.LoadState { return shared.LoadState(s) }

// handleRequestMove backs POST /moves.
func (s *Server) handleRequestMove(w http.ResponseWriter, r *http.Request) {
	var req requestMoveRequest
	if err := decodeRequiredJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	m, err := s.Yard.RequestMove(r.Context(), req.input())
	respond(w, r, http.StatusCreated, m, err)
}

// handleListMoves backs GET /moves.
func (s *Server) handleListMoves(w http.ResponseWriter, r *http.Request) {
	limit, after, err := listParams(r, "siteId", "state", "trailerId", "hostlerRef")
	q := r.URL.Query()
	if err == nil {
		err = oneOf(q.Get("state"), "REQUESTED", "CLAIMED", "COMPLETED", "CANCELLED")
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	f := views.MoveFilter{SiteID: q.Get("siteId"), State: q.Get("state"), TrailerID: q.Get("trailerId"), HostlerRef: q.Get("hostlerRef")}
	p, err := s.Yard.ListMoves(r.Context(), f, after, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pageOf(p))
}

// handleClaimNext backs POST /moves/claims: 201 with the claimed move, 404
// no-claimable-move when nothing waits.
func (s *Server) handleClaimNext(w http.ResponseWriter, r *http.Request) {
	var req claimRequest
	if err := decodeRequiredJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	m, err := s.Yard.ClaimNextMove(r.Context(), req.SiteID, req.HostlerRef)
	respond(w, r, http.StatusCreated, m, err)
}

// handleCompleteMove backs POST /moves/{moveId}/completions.
func (s *Server) handleCompleteMove(w http.ResponseWriter, r *http.Request) {
	var req completeMoveRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	m, err := s.Yard.CompleteMove(r.Context(), req.input(pathParam(r, "moveId")))
	respond(w, r, http.StatusOK, m, err)
}

// handleCancelMove backs POST /moves/{moveId}/cancellations.
func (s *Server) handleCancelMove(w http.ResponseWriter, r *http.Request) {
	var req cancelRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	m, err := s.Yard.CancelMove(r.Context(), pathParam(r, "moveId"), req.Reason)
	respond(w, r, http.StatusOK, m, err)
}
