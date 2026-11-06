package http

import (
	"net/http"

	"github.com/claudioed/yard-management/internal/application/usecases"
)

// handleOpenYardCheck backs POST /yard-checks.
func (s *Server) handleOpenYardCheck(w http.ResponseWriter, r *http.Request) {
	var req openYardCheckRequest
	if err := decodeRequiredJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	c, err := s.Yard.OpenYardCheck(r.Context(), req.SiteID)
	respond(w, r, http.StatusCreated, c, err)
}

// handleRecordObservation backs POST /yard-checks/{yardCheckId}/observations.
func (s *Server) handleRecordObservation(w http.ResponseWriter, r *http.Request) {
	var req observationRequest
	if err := decodeRequiredJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	c, err := s.Yard.RecordObservation(r.Context(), pathParam(r, "yardCheckId"), req.LocationCode, req.UnitNumber)
	respond(w, r, http.StatusOK, c, err)
}

// handleCloseYardCheck backs POST /yard-checks/{yardCheckId}/closures.
func (s *Server) handleCloseYardCheck(w http.ResponseWriter, r *http.Request) {
	c, err := s.Yard.CloseYardCheck(r.Context(), pathParam(r, "yardCheckId"))
	respond(w, r, http.StatusOK, c, err)
}

// handleYardBoard backs GET /sites/{siteId}/yard-board.
func (s *Server) handleYardBoard(w http.ResponseWriter, r *http.Request) {
	b, err := s.Yard.GetYardBoard(r.Context(), pathParam(r, "siteId"))
	respond(w, r, http.StatusOK, b, err)
}

// handleGetPolicy backs GET /sites/{siteId}/yard-policy; the ETag carries the
// policy version a later PUT names in If-Match.
func (s *Server) handleGetPolicy(w http.ResponseWriter, r *http.Request) {
	p, err := s.Yard.GetYardPolicy(r.Context(), pathParam(r, "siteId"))
	respondVersioned(w, r, p, p.Version, err)
}

// handleDeclarePolicy backs PUT /sites/{siteId}/yard-policy. A stale
// If-Match is 412 precondition-failed.
func (s *Server) handleDeclarePolicy(w http.ResponseWriter, r *http.Request) {
	ifMatch, err := versionFromETag(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req declarePolicyRequest
	if err := decodeRequiredJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	p, err := s.Yard.DeclareYardPolicy(r.Context(), usecases.DeclarePolicyInput{SiteID: pathParam(r, "siteId"), IfMatch: ifMatch, Policy: req.input()})
	respondVersioned(w, r, p, p.Version, err)
}

// handleDwellSweep backs POST /dwell-sweeps (the body, {siteId}, is optional).
func (s *Server) handleDwellSweep(w http.ResponseWriter, r *http.Request) {
	var req dwellSweepRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	sw, err := s.Yard.SweepDwell(r.Context(), req.SiteID)
	respond(w, r, http.StatusOK, sw, err)
}

// handleLeaseSweep backs POST /move-lease-sweeps.
func (s *Server) handleLeaseSweep(w http.ResponseWriter, r *http.Request) {
	sw, err := s.Yard.SweepMoveLeases(r.Context())
	respond(w, r, http.StatusOK, sw, err)
}
