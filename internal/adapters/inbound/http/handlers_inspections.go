package http

import (
	"net/http"
	"strconv"

	"github.com/claudioed/yard-management/internal/adapters/inbound/http/page"
	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/inspection"
	"github.com/claudioed/yard-management/internal/domain/shared"
)

// handleOpenInspection backs POST /trailers/{trailerId}/inspections: 201 with
// the inspection, every item pending (ADR 0015).
func (s *Server) handleOpenInspection(w http.ResponseWriter, r *http.Request) {
	var req openInspectionRequest
	if err := decodeRequiredJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	i, err := s.Yard.OpenInspection(r.Context(), req.input(pathParam(r, "trailerId")))
	if err == nil {
		w.Header().Set("Location", "/inspections/"+i.InspectionID)
	}
	respond(w, r, http.StatusCreated, i, err)
}

// handleListTrailerInspections backs GET /trailers/{trailerId}/inspections.
func (s *Server) handleListTrailerInspections(w http.ResponseWriter, r *http.Request) {
	limit, after, err := listParams(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	p, err := s.Yard.ListTrailerInspections(r.Context(), pathParam(r, "trailerId"), after, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pageOf(p))
}

// handleGetInspection backs GET /inspections/{inspectionId}.
func (s *Server) handleGetInspection(w http.ResponseWriter, r *http.Request) {
	i, err := s.Yard.GetInspection(r.Context(), pathParam(r, "inspectionId"))
	respondVersioned(w, r, i, i.Version, err)
}

// handleRecordInspectionItem backs PUT /inspections/{inspectionId}/items/{itemCode}.
func (s *Server) handleRecordInspectionItem(w http.ResponseWriter, r *http.Request) {
	var req recordItemRequest
	if err := decodeRequiredJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	i, err := s.Yard.RecordInspectionItem(r.Context(), pathParam(r, "inspectionId"), pathParam(r, "itemCode"), req.record())
	respond(w, r, http.StatusOK, i, err)
}

// handleCompleteInspection backs POST /inspections/{inspectionId}/completions
// (the body, {conditionGrade}, is optional).
func (s *Server) handleCompleteInspection(w http.ResponseWriter, r *http.Request) {
	var req completeInspectionRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	i, err := s.Yard.CompleteInspection(r.Context(), pathParam(r, "inspectionId"), inspection.ConditionGrade(req.ConditionGrade))
	respond(w, r, http.StatusOK, i, err)
}

// handleListTemplates backs GET /inspection-templates: the latest version of
// every template, built-in and custom, by id.
func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	limit, err := page.Limit(r)
	var pos *page.Position
	if err == nil {
		if name := page.Unknown(r); name != "" {
			err = errUnknownQuery(name)
		} else {
			pos, err = page.After(r)
		}
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	after := ""
	if pos != nil {
		after = pos.ID
	}
	p, err := s.Yard.ListInspectionTemplates(r.Context(), after, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := page.Page[views.InspectionTemplate]{Items: p.Items}
	if out.Items == nil {
		out.Items = []views.InspectionTemplate{}
	}
	if p.Next != nil {
		out.NextCursor = page.Encode(page.Position{Sort: "id", ID: p.Next.ID})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetTemplate backs GET /inspection-templates/{templateId} (the latest
// version) and GET /inspection-templates/{templateId}/versions/{version}.
func (s *Server) handleGetTemplate(w http.ResponseWriter, r *http.Request) {
	version := 0
	if raw := pathParam(r, "version"); raw != "" {
		v, err := templateVersion(raw)
		if err != nil {
			writeError(w, r, err)
			return
		}
		version = v
	}
	t, err := s.Yard.GetInspectionTemplate(r.Context(), pathParam(r, "templateId"), version)
	respond(w, r, http.StatusOK, t, err)
}

// handlePublishTemplate backs PUT /inspection-templates/{templateId}/versions/{version}:
// 201 for a new version, 200 for the same content again, 409 for other content
// under a published version or for a built-in id.
func (s *Server) handlePublishTemplate(w http.ResponseWriter, r *http.Request) {
	version, err := templateVersion(pathParam(r, "version"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req publishTemplateRequest
	if err := decodeRequiredJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	t, created, err := s.Yard.PublishInspectionTemplate(r.Context(), usecases.PublishTemplateInput{
		TemplateID: pathParam(r, "templateId"), Version: version, Title: req.Title, Items: req.items(),
	})
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	respond(w, r, status, t, err)
}

// templateVersion reads a template version out of the path.
func templateVersion(raw string) (int, error) {
	v, err := strconv.Atoi(raw)
	if err != nil || v < 1 {
		return 0, shared.Invalid("template version must be a positive integer")
	}
	return v, nil
}
