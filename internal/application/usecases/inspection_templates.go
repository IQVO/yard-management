package usecases

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/inspection"
)

// ErrTemplateNotFound: no such template or version (404, slug template-not-found).
var ErrTemplateNotFound = fmt.Errorf("inspection template %w", ports.ErrNotFound)

// findTemplate returns one version of a template (version 0 = the latest),
// built-in ones first, and the latest version number of the id. A miss is
// ports.ErrNotFound.
func (y *Yard) findTemplate(ctx context.Context, id string, version int) (*inspection.Template, int, error) {
	if inspection.IsBuiltInID(id) {
		for _, t := range inspection.BuiltIns() {
			if t.ID() == id && (version == 0 || version == t.Version()) {
				return t, t.Version(), nil
			}
		}
		return nil, 0, ports.ErrNotFound
	}
	latest, err := y.d.Templates.Latest(ctx, id)
	if err != nil || version == 0 || version == latest.Version() {
		if err != nil {
			return nil, 0, err
		}
		return latest, latest.Version(), nil
	}
	t, err := y.d.Templates.Find(ctx, id, version)
	return t, latest.Version(), err
}

// GetInspectionTemplate returns one version of a template, the latest when
// version is 0 (GET /inspection-templates/{templateId}).
func (y *Yard) GetInspectionTemplate(ctx context.Context, id string, version int) (views.InspectionTemplate, error) {
	t, latest, err := y.findTemplate(ctx, id, version)
	if err != nil {
		return views.InspectionTemplate{}, notFoundAs(err, ErrTemplateNotFound)
	}
	return templateView(t, latest), nil
}

// ListInspectionTemplates returns one page of templates, the latest version of
// each, ordered by id: the built-in ones and the published custom ones.
// afterID is the id the page starts after ("" = the first page).
func (y *Yard) ListInspectionTemplates(ctx context.Context, afterID string, limit int) (views.Page[views.InspectionTemplate], error) {
	limit = pageLimit(limit)
	var all []*inspection.Template
	for _, t := range inspection.BuiltIns() {
		if t.ID() > afterID {
			all = append(all, t)
		}
	}
	stored, err := y.d.Templates.ListLatest(ctx, afterID, limit+1)
	if err != nil {
		return views.Page[views.InspectionTemplate]{}, err
	}
	all = append(all, stored...)
	slices.SortFunc(all, func(a, b *inspection.Template) int { return strings.Compare(a.ID(), b.ID()) })
	items := make([]views.InspectionTemplate, 0, len(all))
	keys := make([]views.Key, 0, len(all))
	for _, t := range all {
		items = append(items, templateView(t, t.Version()))
		keys = append(keys, views.Key{ID: t.ID()})
	}
	return trim(items, keys, limit)
}

// PublishTemplateInput is the body of PublishInspectionTemplate.
type PublishTemplateInput struct {
	TemplateID string
	Version    int
	Title      string
	Items      []inspection.TemplateItem
}

// PublishInspectionTemplate publishes one version of a custom template
// (PUT /inspection-templates/{templateId}/versions/{version}). A published
// version never changes: the same content again is an idempotent repeat, other
// content under it is ErrTemplateVersionImmutable, and a built-in id is
// ErrTemplateIsBuiltIn. The second result says whether the version was created.
func (y *Yard) PublishInspectionTemplate(ctx context.Context, in PublishTemplateInput) (views.InspectionTemplate, bool, error) {
	var out views.InspectionTemplate
	created := false
	err := y.atomically(ctx, func(ctx context.Context) error {
		if inspection.IsBuiltInID(in.TemplateID) {
			return fmt.Errorf("%w: %s", inspection.ErrTemplateIsBuiltIn, in.TemplateID)
		}
		t, err := inspection.Publish(inspection.PublishInput{ID: in.TemplateID, Version: in.Version, Title: in.Title, Items: in.Items, At: y.now()})
		if err != nil {
			return err
		}
		if err := y.d.Locks.Lock(ctx, "inspection-template:"+in.TemplateID); err != nil {
			return err
		}
		existing, err := y.d.Templates.Find(ctx, in.TemplateID, in.Version)
		switch {
		case err == nil && existing.SameContent(t):
			t = existing
		case err == nil:
			return fmt.Errorf("%w: version %d of %s was published with other content; publish a new version", inspection.ErrTemplateVersionImmutable, in.Version, in.TemplateID)
		case !errors.Is(err, ports.ErrNotFound):
			return err
		default:
			if err := y.d.Templates.Save(ctx, t); err != nil {
				return err
			}
			created = true
		}
		latest, err := y.d.Templates.Latest(ctx, in.TemplateID)
		if err != nil {
			return err
		}
		out = templateView(t, latest.Version())
		return nil
	})
	return out, created, err
}
