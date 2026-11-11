package inspection

import "errors"

// Errors of the Inspection and InspectionTemplate aggregates (ADR 0015;
// compare with errors.Is). The REST adapter turns each into a problem document
// whose slug is the kebab-case of the name without Err.
var (
	// ErrInspectionRequired: the site policy requires a PASSED inspection of a
	// template before this step and the trailer has none (409).
	ErrInspectionRequired = errors.New("inspection required")
	// ErrInspectionFailed: the latest completed inspection the policy asks
	// for FAILED, so the step stays closed until a re-inspection passes (409).
	ErrInspectionFailed = errors.New("inspection failed")
	// ErrInspectionClosed: results change only while the inspection is OPEN;
	// completing is final (409).
	ErrInspectionClosed = errors.New("inspection is closed")
	// ErrRequiredItemPending: an inspection cannot be completed while a
	// required item has no result (409).
	ErrRequiredItemPending = errors.New("a required item is still pending")
	// ErrInspectionAlreadyOpen: one OPEN inspection per trailer, template and
	// gate. Raised by the use case and the partial unique index (409).
	ErrInspectionAlreadyOpen = errors.New("an inspection is already open for this trailer, template and gate")
	// ErrUnknownItem: the inspection has no item with that code (422).
	ErrUnknownItem = errors.New("unknown inspection item")
	// ErrNotApplicableNotAllowed: the template does not let this item be
	// marked NOT_APPLICABLE (422).
	ErrNotApplicableNotAllowed = errors.New("the item cannot be marked not applicable")
	// ErrUnknownTemplate: the template (or version) a body names is not
	// published (422).
	ErrUnknownTemplate = errors.New("unknown inspection template")
	// ErrTemplateVersionImmutable: a published template version never
	// changes; publishing other content under it is refused (409).
	ErrTemplateVersionImmutable = errors.New("a published template version is immutable")
	// ErrTemplateIsBuiltIn: the built-in templates are read-only (409).
	ErrTemplateIsBuiltIn = errors.New("the template is built in and read-only")
)
