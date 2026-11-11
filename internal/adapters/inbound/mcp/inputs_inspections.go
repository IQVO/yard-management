package mcp

// The inspection tool inputs (ADR 0015); see inputs.go for the conventions.

type inspectionIDInput struct {
	InspectionID string `json:"inspectionId" jsonschema:"the inspection id, e.g. insp-1b4e28ba-2fa1-4d3c-8f6e-0a1b2c3d4e5f"`
}

type listTrailerInspectionsInput struct {
	TrailerID string `json:"trailerId" jsonschema:"the trailer whose inspections are listed"`
	Limit     int    `json:"limit,omitempty" jsonschema:"page size, 1..500; omitted or 0 means 100"`
	Cursor    string `json:"cursor,omitempty" jsonschema:"the opaque nextCursor returned by the previous page; omitted means the first page"`
}

type openInspectionInput struct {
	callArgs
	TrailerID       string `json:"trailerId" jsonschema:"the trailer on site to inspect"`
	TemplateID      string `json:"templateId" jsonschema:"the checklist template, e.g. tractor-trailer-17-point, empty-container-7-point, reefer-pre-cool-and-sanitation or a template the site published"`
	TemplateVersion int    `json:"templateVersion,omitempty" jsonschema:"optional: the template version; omitted means the latest published version. The inspection keeps a snapshot of it"`
	Gate            string `json:"gate" jsonschema:"the yard step the inspection stands in front of: PRE_DOCK, PRE_LOAD, PRE_DEPARTURE or ON_DEMAND (an ON_DEMAND one opens no gate)"`
	VisitID         string `json:"visitId,omitempty" jsonschema:"optional: the visit the inspection is made for; omitted means the visit the trailer came with"`
	PerformerRef    string `json:"performerRef,omitempty" jsonschema:"optional reference of the inspector (an employee id, never a driver's name; max 64 characters)"`
}

type recordInspectionItemInput struct {
	callArgs
	InspectionID string   `json:"inspectionId" jsonschema:"the OPEN inspection"`
	ItemCode     string   `json:"itemCode" jsonschema:"the item code of the template, e.g. fifth-wheel"`
	Result       string   `json:"result" jsonschema:"PASS, FAIL or NOT_APPLICABLE (only where the template item allows it); recording again replaces the earlier result"`
	Note         string   `json:"note,omitempty" jsonschema:"optional note (max 500 characters)"`
	EvidenceRefs []string `json:"evidenceRefs,omitempty" jsonschema:"optional opaque references to photos or documents held elsewhere, at most 20, each at most 256 characters; strings only, never binary"`
}

type completeInspectionInput struct {
	callArgs
	InspectionID   string `json:"inspectionId" jsonschema:"the OPEN inspection to complete"`
	ConditionGrade string `json:"conditionGrade,omitempty" jsonschema:"optional overall condition of the equipment: CLEAN, DIRTY, ODOUR or DAMAGED; a record, never the verdict"`
}
