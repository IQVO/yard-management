package usecases

import (
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/inspection"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
)

func inspectionView(i *inspection.Inspection) views.Inspection {
	s := i.Snapshot()
	items := make([]views.InspectionItem, 0, len(s.Items))
	for _, it := range s.Items {
		items = append(items, views.InspectionItem{
			ItemCode: it.Code, Text: it.Text, Required: it.Required, AllowNotApplicable: it.AllowNotApplicable, HoldReason: string(it.HoldReason),
			Result: ptr(string(it.Result)), Note: ptr(it.Note), EvidenceRefs: strs(it.EvidenceRefs),
		})
	}
	return views.Inspection{
		InspectionID: s.ID, SiteID: s.SiteID, TrailerID: s.TrailerID, VisitID: ptr(s.VisitID), TemplateID: s.TemplateID, TemplateVersion: s.TemplateVersion,
		Gate: string(s.Gate), Status: string(s.Status), Items: items, PerformerRef: ptr(s.PerformerRef), ConditionGrade: ptr(string(s.Grade)),
		OpenedAt: s.OpenedAt, CompletedAt: tptr(s.CompletedAt), Version: s.Version,
	}
}

func templateView(t *inspection.Template, latest int) views.InspectionTemplate {
	items := make([]views.TemplateItem, 0, len(t.Items()))
	for _, it := range t.Items() {
		items = append(items, views.TemplateItem{ItemCode: it.Code, Text: it.Text, Required: it.Required, AllowNotApplicable: it.AllowNotApplicable, HoldReason: string(it.HoldReason)})
	}
	return views.InspectionTemplate{
		TemplateID: t.ID(), Version: t.Version(), LatestVersion: latest, Title: t.Title(), BuiltIn: t.BuiltIn(), Items: items, PublishedAt: tptr(t.PublishedAt()),
	}
}

func rulesView(rules []yardpolicy.InspectionRule) []views.InspectionRule {
	out := make([]views.InspectionRule, 0, len(rules))
	for _, r := range rules {
		v := views.InspectionRule{Gate: string(r.Gate), TemplateID: r.TemplateID, AppliesTo: views.InspectionAppliesTo{Purposes: []string{}, TrailerKinds: []string{}}}
		for _, p := range r.Purposes {
			v.AppliesTo.Purposes = append(v.AppliesTo.Purposes, string(p))
		}
		for _, k := range r.TrailerKinds {
			v.AppliesTo.TrailerKinds = append(v.AppliesTo.TrailerKinds, string(k))
		}
		out = append(out, v)
	}
	return out
}
