package inspection

import (
	"github.com/claudioed/yard-management/internal/domain/shared"
)

// The events of the Inspection aggregate (ADR 0004, ADR 0015). The json tags
// are the snake_case `data` of the CloudEvents contract; the embedded Header
// is not part of it. Nullable fields are pointers and serialise as null. Both
// are integration events and advisory: the inspection is final once completed,
// so neither carries a `version`.

// InspectionCompleted: an inspection ended, PASSED or FAILED.
type InspectionCompleted struct {
	shared.Header   `json:"-"`
	InspectionID    string   `json:"inspection_id"`
	SiteID          string   `json:"site_id"`
	TrailerID       string   `json:"trailer_id"`
	VisitID         *string  `json:"visit_id"`
	TemplateID      string   `json:"template_id"`
	TemplateVersion int      `json:"template_version"`
	Gate            string   `json:"gate"`
	Outcome         string   `json:"outcome"`
	ConditionGrade  *string  `json:"condition_grade"`
	CompletedAt     string   `json:"completed_at"`
	FailedItemCodes []string `json:"failed_item_codes"`
}

// FailedItem is one item that recorded FAIL.
type FailedItem struct {
	ItemCode string  `json:"item_code"`
	Note     *string `json:"note"`
	Required bool    `json:"required"`
}

// InspectionFailed: an inspection ended FAILED because a required item failed.
type InspectionFailed struct {
	shared.Header `json:"-"`
	InspectionID  string       `json:"inspection_id"`
	SiteID        string       `json:"site_id"`
	TrailerID     string       `json:"trailer_id"`
	TemplateID    string       `json:"template_id"`
	Gate          string       `json:"gate"`
	FailedItems   []FailedItem `json:"failed_items"`
	FailedAt      string       `json:"failed_at"`
	HoldPlaced    bool         `json:"hold_placed"`
}

func (i *Inspection) failedItems() []FailedItem {
	out := []FailedItem{}
	for _, it := range i.items {
		if it.Result == ResultFail {
			out = append(out, FailedItem{ItemCode: it.Code, Note: shared.Str(it.Note), Required: it.Required})
		}
	}
	return out
}

func (i *Inspection) completedEvent() InspectionCompleted {
	codes := []string{}
	for _, f := range i.failedItems() {
		codes = append(codes, f.ItemCode)
	}
	return InspectionCompleted{
		Header:       shared.NewHeader(Entity, "InspectionCompleted", i.id, i.completedAt),
		InspectionID: i.id, SiteID: i.site, TrailerID: i.trailerID, VisitID: shared.Str(i.visitID), TemplateID: i.templateID,
		TemplateVersion: i.templateVersion, Gate: string(i.gate), Outcome: string(i.status), ConditionGrade: shared.Str(string(i.grade)),
		CompletedAt: shared.Stamp(i.completedAt), FailedItemCodes: codes,
	}
}

func (i *Inspection) failedEvent(holdPlaced bool) InspectionFailed {
	return InspectionFailed{
		Header:       shared.NewHeader(Entity, "InspectionFailed", i.id, i.completedAt),
		InspectionID: i.id, SiteID: i.site, TrailerID: i.trailerID, TemplateID: i.templateID, Gate: string(i.gate),
		FailedItems: i.failedItems(), FailedAt: shared.Stamp(i.completedAt), HoldPlaced: holdPlaced,
	}
}
