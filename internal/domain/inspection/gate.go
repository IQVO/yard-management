package inspection

import (
	"fmt"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// Record is what the gate needs to know about a COMPLETED inspection of a
// trailer: which template and gate it was for, and how it ended.
type Record struct {
	InspectionID string
	TemplateID   string
	Gate         shared.Gate
	Status       Status
}

// Judge decides whether a trailer may take the step behind gate, for one
// requirement of the site policy: a PASSED inspection of templateID made for
// that gate. records are the trailer's completed inspections in the order they
// completed, and the LATEST one for this template and gate decides: a failure
// followed by a passing re-inspection opens the gate, a pass followed by a
// failure closes it. No such inspection is ErrInspectionRequired, a failed
// one ErrInspectionFailed.
func Judge(gate shared.Gate, templateID string, records []Record) error {
	var latest *Record
	for idx := range records {
		if records[idx].TemplateID == templateID && records[idx].Gate == gate {
			latest = &records[idx]
		}
	}
	switch {
	case latest == nil:
		return fmt.Errorf("%w: a PASSED %s inspection of template %s is required first", ErrInspectionRequired, gate, templateID)
	case latest.Status == StatusFailed:
		return fmt.Errorf("%w: inspection %s of template %s FAILED; a passing %s re-inspection is required", ErrInspectionFailed, latest.InspectionID, templateID, gate)
	}
	return nil
}
