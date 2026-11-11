package http

import (
	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/domain/inspection"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
)

// openInspectionRequest is the body of POST /trailers/{trailerId}/inspections
// (ADR 0015). TemplateVersion 0 (left out) is the latest version.
type openInspectionRequest struct {
	TemplateID      string `json:"templateId"`
	TemplateVersion int    `json:"templateVersion"`
	Gate            string `json:"gate"`
	VisitID         string `json:"visitId"`
	PerformerRef    string `json:"performerRef"`
}

func (q openInspectionRequest) input(trailerID string) usecases.OpenInspectionInput {
	return usecases.OpenInspectionInput{
		TrailerID: trailerID, TemplateID: q.TemplateID, TemplateVersion: q.TemplateVersion, Gate: shared.Gate(q.Gate),
		VisitID: q.VisitID, PerformerRef: q.PerformerRef,
	}
}

// recordItemRequest is the body of PUT /inspections/{inspectionId}/items/{itemCode}.
type recordItemRequest struct {
	Result       string   `json:"result"`
	Note         string   `json:"note"`
	EvidenceRefs []string `json:"evidenceRefs"`
}

func (q recordItemRequest) record() inspection.ItemRecord {
	return inspection.ItemRecord{Result: inspection.Result(q.Result), Note: q.Note, EvidenceRefs: q.EvidenceRefs}
}

// completeInspectionRequest is the optional body of POST /inspections/{inspectionId}/completions.
type completeInspectionRequest struct {
	ConditionGrade string `json:"conditionGrade"`
}

type templateItemRequest struct {
	ItemCode           string `json:"itemCode"`
	Text               string `json:"text"`
	Required           bool   `json:"required"`
	AllowNotApplicable bool   `json:"allowNotApplicable"`
	HoldReason         string `json:"holdReason"`
}

// publishTemplateRequest is the body of PUT /inspection-templates/{templateId}/versions/{version}.
type publishTemplateRequest struct {
	Title string                `json:"title"`
	Items []templateItemRequest `json:"items"`
}

func (q publishTemplateRequest) items() []inspection.TemplateItem {
	out := make([]inspection.TemplateItem, 0, len(q.Items))
	for _, it := range q.Items {
		out = append(out, inspection.TemplateItem{
			Code: it.ItemCode, Text: it.Text, Required: it.Required, AllowNotApplicable: it.AllowNotApplicable, HoldReason: trailer.HoldReason(it.HoldReason),
		})
	}
	return out
}

// inspectionRuleRequest is one entry of requiredInspections in the policy body.
type inspectionRuleRequest struct {
	Gate       string `json:"gate"`
	TemplateID string `json:"templateId"`
	AppliesTo  struct {
		Purposes     []string `json:"purposes"`
		TrailerKinds []string `json:"trailerKinds"`
	} `json:"appliesTo"`
}

func inspectionRules(in []inspectionRuleRequest) []yardpolicy.InspectionRule {
	out := make([]yardpolicy.InspectionRule, 0, len(in))
	for _, r := range in {
		rule := yardpolicy.InspectionRule{Gate: shared.Gate(r.Gate), TemplateID: r.TemplateID}
		for _, p := range r.AppliesTo.Purposes {
			rule.Purposes = append(rule.Purposes, shared.Purpose(p))
		}
		for _, k := range r.AppliesTo.TrailerKinds {
			rule.TrailerKinds = append(rule.TrailerKinds, shared.TrailerKind(k))
		}
		out = append(out, rule)
	}
	return out
}
