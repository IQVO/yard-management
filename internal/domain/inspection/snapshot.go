package inspection

import (
	"fmt"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// Snapshot is the persisted form of an Inspection.
type Snapshot struct {
	ID, SiteID, TrailerID, VisitID string
	TemplateID                     string
	TemplateVersion                int
	Gate                           shared.Gate
	Status                         Status
	Items                          []Item
	PerformerRef                   string
	Grade                          ConditionGrade
	OpenedAt, CompletedAt          time.Time
	Version                        int64
}

func cloneItems(in []Item) []Item {
	out := make([]Item, len(in))
	for n, it := range in {
		it.EvidenceRefs = append([]string{}, it.EvidenceRefs...)
		out[n] = it
	}
	return out
}

// Snapshot returns the persisted form.
func (i *Inspection) Snapshot() Snapshot {
	return Snapshot{
		ID: i.id, SiteID: i.site, TrailerID: i.trailerID, VisitID: i.visitID, TemplateID: i.templateID, TemplateVersion: i.templateVersion,
		Gate: i.gate, Status: i.status, Items: cloneItems(i.items), PerformerRef: i.performerRef, Grade: i.grade,
		OpenedAt: i.openedAt, CompletedAt: i.completedAt, Version: i.version,
	}
}

// Restore rebuilds a stored inspection and refuses a row that breaks an
// invariant: an OPEN one with a completion, a final one without, a PASSED one
// with a required item pending or failed, a FAILED one without a failed
// required item.
func Restore(s Snapshot) (*Inspection, error) {
	if err := validateStored(s); err != nil {
		return nil, err
	}
	i := &Inspection{
		id: s.ID, site: s.SiteID, trailerID: s.TrailerID, visitID: s.VisitID, templateID: s.TemplateID, templateVersion: s.TemplateVersion,
		gate: s.Gate, status: s.Status, items: cloneItems(s.Items), performerRef: s.PerformerRef, grade: s.Grade,
		openedAt: s.OpenedAt.UTC(), completedAt: s.CompletedAt.UTC(), version: s.Version,
	}
	if s.CompletedAt.IsZero() {
		i.completedAt = time.Time{}
	}
	if i.status == StatusOpen {
		return i, nil
	}
	if len(i.pendingRequired()) > 0 {
		return nil, shared.Invalid("a %s inspection has a required item pending", i.status)
	}
	if failed := len(i.failedRequired()) > 0; failed != (i.status == StatusFailed) {
		return nil, shared.Invalid("status %s does not match the recorded results", i.status)
	}
	return i, nil
}

func validateStored(s Snapshot) error {
	if err := validateRefs(s.ID, s.SiteID, s.TrailerID, s.VisitID, s.PerformerRef); err != nil {
		return err
	}
	if !ValidCode(s.TemplateID) {
		return shared.Invalid("template id %q is not valid", s.TemplateID)
	}
	return validateShape(s)
}

// validateShape checks the versions, the enumerations, the item count and the
// agreement of the status with the completion time and the item results.
func validateShape(s Snapshot) error {
	switch {
	case s.Version < 1 || s.TemplateVersion < 1:
		return shared.Invalid("version must be at least 1")
	case !s.Gate.Valid() || !s.Grade.Valid():
		return shared.Invalid("gate or condition grade is not valid")
	case len(s.Items) < 1 || len(s.Items) > MaxItems:
		return shared.Invalid("an inspection has 1 to %d items", MaxItems)
	case s.OpenedAt.IsZero():
		return shared.Invalid("open time is required")
	case (s.Status == StatusOpen) != s.CompletedAt.IsZero():
		return shared.Invalid("a completion time goes with a completed inspection only")
	case s.Status != StatusOpen && s.Status != StatusPassed && s.Status != StatusFailed:
		return shared.Invalid("status %q is not valid", s.Status)
	}
	for _, it := range s.Items {
		if it.Result != ResultPending && !it.Result.recordable() {
			return fmt.Errorf("%w: item %q has result %q", shared.ErrValidation, it.Code, it.Result)
		}
	}
	return nil
}
