package inspection

import (
	"fmt"
	"slices"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
)

// Status is the lifecycle state of an inspection.
type Status string

// The inspection statuses. PASSED and FAILED are final.
const (
	StatusOpen   Status = "OPEN"
	StatusPassed Status = "PASSED"
	StatusFailed Status = "FAILED"
)

// Result is what the inspector found at one item. The empty result is
// "pending": nothing was recorded yet.
type Result string

// The item results.
const (
	ResultPending       Result = ""
	ResultPass          Result = "PASS"
	ResultFail          Result = "FAIL"
	ResultNotApplicable Result = "NOT_APPLICABLE"
)

// recordable reports whether r can be recorded (pending cannot: an item that
// was looked at has a result).
func (r Result) recordable() bool {
	return r == ResultPass || r == ResultFail || r == ResultNotApplicable
}

// ConditionGrade is the overall condition of the equipment the inspector
// notes on completion; it is a record, never a verdict (the items decide).
type ConditionGrade string

// The condition grades.
const (
	GradeNone    ConditionGrade = ""
	GradeClean   ConditionGrade = "CLEAN"
	GradeDirty   ConditionGrade = "DIRTY"
	GradeOdour   ConditionGrade = "ODOUR"
	GradeDamaged ConditionGrade = "DAMAGED"
)

// Valid reports whether g is a grade or none.
func (g ConditionGrade) Valid() bool {
	switch g {
	case GradeNone, GradeClean, GradeDirty, GradeOdour, GradeDamaged:
		return true
	}
	return false
}

// Item is one line of an inspection: the snapshot of a template item plus
// what was found.
type Item struct {
	TemplateItem
	Result       Result
	Note         string
	EvidenceRefs []string
}

// Inspection is the aggregate root: one checklist filled in for one trailer.
// Its items are a snapshot of the template version it was opened from, so a
// later template never rewrites it.
type Inspection struct {
	id, site, trailerID, visitID string
	templateID                   string
	templateVersion              int
	gate                         shared.Gate
	status                       Status
	items                        []Item
	performerRef                 string
	grade                        ConditionGrade
	openedAt, completedAt        time.Time
	version                      int64
	events                       []shared.Event
}

// OpenInput is everything Open needs.
type OpenInput struct {
	ID, SiteID, TrailerID string
	// VisitID is the visit the inspection is made for (optional).
	VisitID  string
	Template *Template
	Gate     shared.Gate
	// PerformerRef is the optional reference of the inspector (an employee id,
	// never a driver's name).
	PerformerRef string
	At           time.Time
}

// Open starts an inspection at version 1 with every item pending. It raises no
// event.
func Open(in OpenInput) (*Inspection, error) {
	if err := validateRefs(in.ID, in.SiteID, in.TrailerID, in.VisitID, in.PerformerRef); err != nil {
		return nil, err
	}
	if in.Template == nil {
		return nil, fmt.Errorf("%w: no template", ErrUnknownTemplate)
	}
	if !in.Gate.Valid() {
		return nil, shared.Invalid("gate must be PRE_DOCK, PRE_LOAD, PRE_DEPARTURE or ON_DEMAND")
	}
	if in.At.IsZero() {
		return nil, shared.Invalid("open time is required")
	}
	items := make([]Item, 0, len(in.Template.items))
	for _, ti := range in.Template.items {
		items = append(items, Item{TemplateItem: ti, EvidenceRefs: []string{}})
	}
	return &Inspection{
		id: in.ID, site: in.SiteID, trailerID: in.TrailerID, visitID: in.VisitID, templateID: in.Template.id,
		templateVersion: in.Template.version, gate: in.Gate, status: StatusOpen, items: items, performerRef: in.PerformerRef,
		openedAt: in.At.UTC(), version: 1,
	}, nil
}

// validateRefs checks the identifying strings of an inspection: the three ids
// are required, the visit and the performer optional, every one at most 64
// characters.
func validateRefs(id, site, trailerID, visit, performer string) error {
	for _, c := range []struct {
		name, v string
		req     bool
	}{{"inspection id", id, true}, {"site id", site, true}, {"trailer id", trailerID, true}, {"visit id", visit, false}, {"performer reference", performer, false}} {
		check := shared.Check
		if c.req {
			check = shared.Require
		}
		if err := check(c.name, c.v, maxRef); err != nil {
			return err
		}
	}
	return nil
}

// ItemRecord is what the inspector found at one item.
type ItemRecord struct {
	Result       Result
	Note         string
	EvidenceRefs []string
}

// RecordItem records (or replaces) the result of one item while the
// inspection is OPEN. NOT_APPLICABLE needs the template's allowance. The
// evidence refs are opaque references to photos or documents held elsewhere.
func (i *Inspection) RecordItem(code string, rec ItemRecord) error {
	if i.status != StatusOpen {
		return fmt.Errorf("%w: the inspection is %s", ErrInspectionClosed, i.status)
	}
	idx := slices.IndexFunc(i.items, func(it Item) bool { return it.Code == code })
	if idx < 0 {
		return fmt.Errorf("%w: %q", ErrUnknownItem, code)
	}
	if !rec.Result.recordable() {
		return shared.Invalid("result must be PASS, FAIL or NOT_APPLICABLE")
	}
	if rec.Result == ResultNotApplicable && !i.items[idx].AllowNotApplicable {
		return fmt.Errorf("%w: %q", ErrNotApplicableNotAllowed, code)
	}
	if err := validateEvidence(rec); err != nil {
		return err
	}
	i.items[idx].Result, i.items[idx].Note = rec.Result, rec.Note
	i.items[idx].EvidenceRefs = append([]string{}, rec.EvidenceRefs...)
	i.version++
	return nil
}

func validateEvidence(rec ItemRecord) error {
	if err := shared.Check("note", rec.Note, maxNote); err != nil {
		return err
	}
	if len(rec.EvidenceRefs) > maxEvidenceRefs {
		return shared.Invalid("at most %d evidence references per item", maxEvidenceRefs)
	}
	seen := map[string]bool{}
	for _, ref := range rec.EvidenceRefs {
		if err := shared.Require("evidence reference", ref, maxEvidenceRef); err != nil {
			return err
		}
		if seen[ref] {
			return shared.Invalid("evidence reference %q is listed twice", ref)
		}
		seen[ref] = true
	}
	return nil
}

// CompleteInput is the body of Complete.
type CompleteInput struct {
	Grade ConditionGrade
	At    time.Time
	// TrailerHoldable says whether the use case will place the hold of a
	// failure on the trailer (it is on site and not already held); the
	// InspectionFailed event carries it as hold_placed.
	TrailerHoldable bool
}

// Complete ends the inspection: FAILED when a required item failed, PASSED
// otherwise. It needs every required item to have a result; it is final. It
// raises InspectionCompleted and, for a failure, InspectionFailed.
func (i *Inspection) Complete(in CompleteInput) error {
	if i.status != StatusOpen {
		return fmt.Errorf("%w: the inspection is %s", ErrInspectionClosed, i.status)
	}
	if !in.Grade.Valid() {
		return shared.Invalid("condition grade must be CLEAN, DIRTY, ODOUR or DAMAGED")
	}
	if in.At.IsZero() {
		return shared.Invalid("completion time is required")
	}
	if pending := i.pendingRequired(); len(pending) > 0 {
		return fmt.Errorf("%w: %v", ErrRequiredItemPending, pending)
	}
	i.status, i.grade, i.completedAt = StatusPassed, in.Grade, in.At.UTC()
	if len(i.failedRequired()) > 0 {
		i.status = StatusFailed
	}
	i.version++
	i.events = append(i.events, i.completedEvent())
	if i.status == StatusFailed {
		i.events = append(i.events, i.failedEvent(in.TrailerHoldable))
	}
	return nil
}

func (i *Inspection) pendingRequired() []string {
	var out []string
	for _, it := range i.items {
		if it.Required && it.Result == ResultPending {
			out = append(out, it.Code)
		}
	}
	return out
}

func (i *Inspection) failedRequired() []Item {
	var out []Item
	for _, it := range i.items {
		if it.Required && it.Result == ResultFail {
			out = append(out, it)
		}
	}
	return out
}

// holdSeverity ranks the hold reasons a failure can ask for; the most severe
// failed required item decides the reason of the one hold a trailer carries.
var holdSeverity = []trailer.HoldReason{trailer.HoldSecurity, trailer.HoldSealBroken, trailer.HoldDamaged, trailer.HoldQuality, trailer.HoldNotFound}

// HoldReason is the reason of the hold a FAILED inspection places on the
// trailer: the most severe reason among its failed required items (SECURITY,
// SEAL_BROKEN, DAMAGED, QUALITY, NOT_FOUND). It is empty for an inspection
// that did not fail.
func (i *Inspection) HoldReason() trailer.HoldReason {
	failed := i.failedRequired()
	if i.status != StatusFailed {
		return ""
	}
	for _, reason := range holdSeverity {
		if slices.ContainsFunc(failed, func(it Item) bool { return it.HoldReason == reason }) {
			return reason
		}
	}
	return trailer.HoldQuality
}

// PullEvents returns the pending domain events and clears them.
func (i *Inspection) PullEvents() []shared.Event {
	ev := i.events
	i.events = nil
	return ev
}

// ID returns the inspection id.
func (i *Inspection) ID() string { return i.id }

// SiteID returns the site code.
func (i *Inspection) SiteID() string { return i.site }

// TrailerID returns the inspected trailer.
func (i *Inspection) TrailerID() string { return i.trailerID }

// TemplateID returns the template the inspection was opened from.
func (i *Inspection) TemplateID() string { return i.templateID }

// Gate returns the gate the inspection was opened for.
func (i *Inspection) Gate() shared.Gate { return i.gate }

// Status returns the lifecycle state.
func (i *Inspection) Status() Status { return i.status }

// Version returns the optimistic-concurrency version.
func (i *Inspection) Version() int64 { return i.version }
