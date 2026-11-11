// Package inspection holds two aggregates of ADR 0015: the versioned
// InspectionTemplate (a checklist, immutable once published) and the
// Inspection (one checklist filled in for one trailer, with a snapshot of the
// template it used). It is pure: no I/O, no clock reads, ids from the caller.
package inspection

import (
	"regexp"
	"slices"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
)

// Entity is the aggregate segment of the CloudEvents type of this package.
const Entity = "inspection"

// Size limits of a template and of what is recorded against it.
const (
	MaxItems        = 100
	maxIDLength     = 64
	maxTitle        = 120
	maxItemText     = 300
	maxNote         = 500
	maxEvidenceRefs = 20
	maxEvidenceRef  = 256
	maxRef          = 64
	maxVersion      = 1_000_000
)

var codePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidCode reports whether s is a template id or item code: lower-case
// letters, digits and hyphens, starting with a letter or digit, at most 64
// characters.
func ValidCode(s string) bool { return len(s) <= maxIDLength && codePattern.MatchString(s) }

// TemplateItem is one line of a checklist.
type TemplateItem struct {
	Code string
	Text string
	// Required: the inspection cannot complete while it has no result, and a
	// FAIL on it fails the inspection.
	Required bool
	// AllowNotApplicable: the item may be marked NOT_APPLICABLE even when it
	// is required (a refrigeration unit on a dry van).
	AllowNotApplicable bool
	// HoldReason is the reason of the hold a failed required item places on
	// the trailer; QUALITY when the template says nothing.
	HoldReason trailer.HoldReason
}

// PublishInput is everything Publish needs.
type PublishInput struct {
	ID      string
	Version int
	Title   string
	Items   []TemplateItem
	At      time.Time
}

// Template is the aggregate root of a versioned checklist: keyed by id and
// version, never changed after publication (a change is a new version).
type Template struct {
	id          string
	version     int
	title       string
	builtIn     bool
	items       []TemplateItem
	publishedAt time.Time
}

// Publish validates a custom template and returns it. Built-in templates are
// not built here; they live in code (BuiltIns).
func Publish(in PublishInput) (*Template, error) {
	if in.At.IsZero() {
		return nil, shared.Invalid("publication time is required")
	}
	t, err := build(in, false)
	if err != nil {
		return nil, err
	}
	t.publishedAt = in.At.UTC()
	return t, nil
}

func build(in PublishInput, builtIn bool) (*Template, error) {
	if !ValidCode(in.ID) {
		return nil, shared.Invalid("template id must be lower-case letters, digits and hyphens, at most %d characters", maxIDLength)
	}
	if in.Version < 1 || in.Version > maxVersion {
		return nil, shared.Invalid("template version must be between 1 and %d", maxVersion)
	}
	if err := shared.Require("template title", in.Title, maxTitle); err != nil {
		return nil, err
	}
	items, err := validateItems(in.Items)
	if err != nil {
		return nil, err
	}
	return &Template{id: in.ID, version: in.Version, title: in.Title, builtIn: builtIn, items: items}, nil
}

func validateItems(in []TemplateItem) ([]TemplateItem, error) {
	if len(in) < 1 || len(in) > MaxItems {
		return nil, shared.Invalid("a template has 1 to %d items", MaxItems)
	}
	seen := map[string]bool{}
	out := make([]TemplateItem, 0, len(in))
	for _, it := range in {
		if !ValidCode(it.Code) {
			return nil, shared.Invalid("item code %q must be lower-case letters, digits and hyphens, at most %d characters", it.Code, maxIDLength)
		}
		if seen[it.Code] {
			return nil, shared.Invalid("item code %q is listed twice", it.Code)
		}
		seen[it.Code] = true
		if err := shared.Require("item text", it.Text, maxItemText); err != nil {
			return nil, err
		}
		if it.HoldReason == "" {
			it.HoldReason = trailer.HoldQuality
		}
		if !it.HoldReason.Valid() {
			return nil, shared.Invalid("item %q hold reason %q is not valid", it.Code, it.HoldReason)
		}
		out = append(out, it)
	}
	return out, nil
}

// ID returns the template id.
func (t *Template) ID() string { return t.id }

// Version returns the template version.
func (t *Template) Version() int { return t.version }

// Title returns the human title.
func (t *Template) Title() string { return t.title }

// BuiltIn reports whether the template ships with the service (read-only).
func (t *Template) BuiltIn() bool { return t.builtIn }

// PublishedAt returns the publication instant (zero for a built-in).
func (t *Template) PublishedAt() time.Time { return t.publishedAt }

// Items returns a copy of the checklist.
func (t *Template) Items() []TemplateItem { return slices.Clone(t.items) }

// SameContent reports whether o has the same title and items as t: what makes
// a republication of a version an idempotent repeat instead of a conflict.
func (t *Template) SameContent(o *Template) bool {
	return t.title == o.title && slices.Equal(t.items, o.items)
}

// TemplateSnapshot is the persisted form of a Template.
type TemplateSnapshot struct {
	ID          string
	Version     int
	Title       string
	Items       []TemplateItem
	PublishedAt time.Time
}

// Snapshot returns the persisted form.
func (t *Template) Snapshot() TemplateSnapshot {
	return TemplateSnapshot{ID: t.id, Version: t.version, Title: t.title, Items: t.Items(), PublishedAt: t.publishedAt}
}

// RestoreTemplate rebuilds a stored (custom) template, validating it as
// Publish does.
func RestoreTemplate(s TemplateSnapshot) (*Template, error) {
	t, err := build(PublishInput{ID: s.ID, Version: s.Version, Title: s.Title, Items: s.Items}, false)
	if err != nil {
		return nil, err
	}
	t.publishedAt = s.PublishedAt.UTC()
	return t, nil
}
