package http

import (
	"net/http"

	"github.com/claudioed/yard-management/internal/application/views"
)

// handleGetTrailer backs GET /trailers/{trailerId}.
func (s *Server) handleGetTrailer(w http.ResponseWriter, r *http.Request) {
	t, err := s.Yard.GetTrailer(r.Context(), pathParam(r, "trailerId"))
	respondVersioned(w, r, t, t.Version, err)
}

// handleListTrailers backs GET /trailers.
func (s *Server) handleListTrailers(w http.ResponseWriter, r *http.Request) {
	limit, after, err := listParams(r, "siteId", "state", "kind", "locationCode", "unitNumber", "visitId")
	q := r.URL.Query()
	if err == nil {
		err = oneOf(q.Get("state"), "PARKED", "IN_MOVE", "DOCKED", "DEPARTED")
	}
	if err == nil {
		err = oneOf(q.Get("kind"), "DRY_VAN", "REEFER", "FLATBED", "CONTAINER")
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	f := views.TrailerFilter{
		SiteID: q.Get("siteId"), State: q.Get("state"), Kind: q.Get("kind"), LocationCode: q.Get("locationCode"),
		UnitNumber: q.Get("unitNumber"), VisitID: q.Get("visitId"),
	}
	p, err := s.Yard.ListTrailers(r.Context(), f, after, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pageOf(p))
}

// handleStartDoorWork backs POST /trailers/{trailerId}/door-work-starts.
func (s *Server) handleStartDoorWork(w http.ResponseWriter, r *http.Request) {
	t, err := s.Yard.StartDoorWork(r.Context(), pathParam(r, "trailerId"))
	respond(w, r, http.StatusOK, t, err)
}

// handleCompleteDoorWork backs POST /trailers/{trailerId}/door-work-completions.
func (s *Server) handleCompleteDoorWork(w http.ResponseWriter, r *http.Request) {
	var req doorWorkCompletionRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	t, err := s.Yard.CompleteDoorWork(r.Context(), pathParam(r, "trailerId"), loadStateOf(req.LoadState))
	respond(w, r, http.StatusOK, t, err)
}

// handlePlaceHold backs POST /trailers/{trailerId}/holds.
func (s *Server) handlePlaceHold(w http.ResponseWriter, r *http.Request) {
	var req holdRequest
	if err := decodeRequiredJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	t, err := s.Yard.PlaceHold(r.Context(), pathParam(r, "trailerId"), req.reason())
	respond(w, r, http.StatusOK, t, err)
}

// handleReleaseHold backs POST /trailers/{trailerId}/hold-releases.
func (s *Server) handleReleaseHold(w http.ResponseWriter, r *http.Request) {
	t, err := s.Yard.ReleaseHold(r.Context(), pathParam(r, "trailerId"))
	respond(w, r, http.StatusOK, t, err)
}

// handleRecordSeal backs POST /trailers/{trailerId}/seals (INTACT or APPLIED
// records the number and the optional class, BROKEN marks it broken and holds
// the trailer).
func (s *Server) handleRecordSeal(w http.ResponseWriter, r *http.Request) {
	var req sealRequest
	if err := decodeRequiredJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	t, err := s.Yard.RecordSeal(r.Context(), pathParam(r, "trailerId"), req.State, req.SealNumber, req.SealClass)
	respond(w, r, http.StatusOK, t, err)
}

// handleSecureTrailer backs POST /trailers/{trailerId}/securings (the dock
// safety interlock, ADR 0013).
func (s *Server) handleSecureTrailer(w http.ResponseWriter, r *http.Request) {
	var req securingRequest
	if err := decodeRequiredJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	t, err := s.Yard.SecureTrailer(r.Context(), pathParam(r, "trailerId"), req.Method, req.SecurerRef)
	respond(w, r, http.StatusOK, t, err)
}

// handleReleaseTrailerSecuring backs POST /trailers/{trailerId}/securing-releases.
func (s *Server) handleReleaseTrailerSecuring(w http.ResponseWriter, r *http.Request) {
	t, err := s.Yard.ReleaseTrailerSecuring(r.Context(), pathParam(r, "trailerId"))
	respond(w, r, http.StatusOK, t, err)
}
