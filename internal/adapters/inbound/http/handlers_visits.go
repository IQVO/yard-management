package http

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/yard-management/internal/application/views"
)

// pathParam returns a path segment, unescaped; a segment that cannot be
// unescaped names nothing.
func pathParam(r *http.Request, name string) string {
	raw := chi.URLParam(r, name)
	id, err := url.PathUnescape(raw)
	if err != nil {
		return raw
	}
	return id
}

// respond answers err as its problem, or v with the status.
func respond[T any](w http.ResponseWriter, r *http.Request, status int, v T, err error) {
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, status, v)
}

// respondVersioned answers err as its problem, or v with its weak ETag.
func respondVersioned[T any](w http.ResponseWriter, r *http.Request, v T, version int64, err error) {
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeVersioned(w, v, version)
}

// handleCheckIn backs POST /visits: 201 with the visit, its trailers and the
// moves DirectOnCheckIn created. A refused check-in persists nothing.
func (s *Server) handleCheckIn(w http.ResponseWriter, r *http.Request) {
	var req checkInRequest
	if err := decodeRequiredJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.Yard.CheckInVisit(r.Context(), req.input())
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Location", visitLocation(out.Visit.VisitID))
	writeJSON(w, http.StatusCreated, out)
}

// handleRejectAtGate backs POST /visit-rejections.
func (s *Server) handleRejectAtGate(w http.ResponseWriter, r *http.Request) {
	var req rejectRequest
	if err := decodeRequiredJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.Yard.RejectAtGate(r.Context(), req.input())
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Location", visitLocation(v.VisitID))
	writeJSON(w, http.StatusCreated, v)
}

func visitLocation(id string) string { return "/visits/" + url.PathEscape(id) }

// handleCheckOut backs POST /visits/{visitId}/check-outs.
func (s *Server) handleCheckOut(w http.ResponseWriter, r *http.Request) {
	var req checkOutRequest
	if err := decodeRequiredJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.TrailersOut == nil {
		writeError(w, r, fmt.Errorf("%w: trailersOut is required", errMalformedRequest))
		return
	}
	ids := make([]string, 0, len(*req.TrailersOut))
	for _, t := range *req.TrailersOut {
		ids = append(ids, t.TrailerID)
	}
	v, err := s.Yard.CheckOutVisit(r.Context(), pathParam(r, "visitId"), ids)
	respond(w, r, http.StatusOK, v, err)
}

// handleGetVisit backs GET /visits/{visitId}.
func (s *Server) handleGetVisit(w http.ResponseWriter, r *http.Request) {
	v, err := s.Yard.GetVisit(r.Context(), pathParam(r, "visitId"))
	respondVersioned(w, r, v, v.Version, err)
}

// handleListVisits backs GET /visits.
func (s *Server) handleListVisits(w http.ResponseWriter, r *http.Request) {
	limit, after, err := listParams(r, "siteId", "state", "tractorPlate")
	if err == nil {
		err = oneOf(r.URL.Query().Get("state"), "CHECKED_IN", "CHECKED_OUT", "REJECTED")
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	q := r.URL.Query()
	p, err := s.Yard.ListVisits(r.Context(), views.VisitFilter{SiteID: q.Get("siteId"), State: q.Get("state"), TractorPlate: q.Get("tractorPlate")}, after, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pageOf(p))
}
