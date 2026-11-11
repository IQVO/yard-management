package views

import "time"

// InspectionItem is one line of an inspection as the API shows it: the snapshot
// of the template item plus what was found (Result null while pending).
type InspectionItem struct {
	ItemCode           string   `json:"itemCode"`
	Text               string   `json:"text"`
	Required           bool     `json:"required"`
	AllowNotApplicable bool     `json:"allowNotApplicable"`
	HoldReason         string   `json:"holdReason"`
	Result             *string  `json:"result"`
	Note               *string  `json:"note"`
	EvidenceRefs       []string `json:"evidenceRefs"`
}

// Inspection is an Inspection as the API shows it (ADR 0015).
type Inspection struct {
	InspectionID    string           `json:"inspectionId"`
	SiteID          string           `json:"siteId"`
	TrailerID       string           `json:"trailerId"`
	VisitID         *string          `json:"visitId"`
	TemplateID      string           `json:"templateId"`
	TemplateVersion int              `json:"templateVersion"`
	Gate            string           `json:"gate"`
	Status          string           `json:"status"`
	Items           []InspectionItem `json:"items"`
	PerformerRef    *string          `json:"performerRef"`
	ConditionGrade  *string          `json:"conditionGrade"`
	OpenedAt        time.Time        `json:"openedAt"`
	CompletedAt     *time.Time       `json:"completedAt"`
	Version         int64            `json:"version"`
}

// TemplateItem is one line of a checklist as the API shows it.
type TemplateItem struct {
	ItemCode           string `json:"itemCode"`
	Text               string `json:"text"`
	Required           bool   `json:"required"`
	AllowNotApplicable bool   `json:"allowNotApplicable"`
	HoldReason         string `json:"holdReason"`
}

// InspectionTemplate is one version of a checklist as the API shows it.
// LatestVersion is the highest published version of the same template id;
// PublishedAt is null for a built-in template.
type InspectionTemplate struct {
	TemplateID    string         `json:"templateId"`
	Version       int            `json:"version"`
	LatestVersion int            `json:"latestVersion"`
	Title         string         `json:"title"`
	BuiltIn       bool           `json:"builtIn"`
	Items         []TemplateItem `json:"items"`
	PublishedAt   *time.Time     `json:"publishedAt"`
}
