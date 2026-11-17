package usecases

import (
	"context"
	"errors"
	"fmt"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/inspection"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
)

// ErrInspectionNotFound: no such inspection (404, slug inspection-not-found).
var ErrInspectionNotFound = fmt.Errorf("inspection %w", ports.ErrNotFound)

// OpenInspectionInput is the body of OpenInspection. TemplateVersion 0 means
// the latest published version; VisitID empty means the visit the trailer came
// with.
type OpenInspectionInput struct {
	TrailerID       string
	TemplateID      string
	TemplateVersion int
	Gate            shared.Gate
	VisitID         string
	PerformerRef    string
}

// OpenInspection starts a checklist for a trailer on site: a snapshot of the
// template version (the latest by default) with every item pending. There is
// at most one OPEN inspection per trailer, template and gate.
func (y *Yard) OpenInspection(ctx context.Context, in OpenInspectionInput) (views.Inspection, error) {
	var out views.Inspection
	err := y.atomically(ctx, func(ctx context.Context) error {
		tr, err := y.loadTrailer(ctx, in.TrailerID)
		if err != nil {
			return err
		}
		if tr.State() == trailer.Departed {
			return fmt.Errorf("%w: OpenInspection on a DEPARTED trailer", shared.ErrIllegalTransition)
		}
		tpl, _, err := y.findTemplate(ctx, in.TemplateID, in.TemplateVersion)
		if errors.Is(err, ports.ErrNotFound) {
			return fmt.Errorf("%w: %s version %d", inspection.ErrUnknownTemplate, in.TemplateID, in.TemplateVersion)
		}
		if err != nil {
			return err
		}
		visitID, err := y.inspectionVisit(ctx, tr, in.VisitID)
		if err != nil {
			return err
		}
		switch open, err := y.d.Inspections.FindOpen(ctx, tr.ID(), tpl.ID(), in.Gate); {
		case err == nil:
			return fmt.Errorf("%w: %s", inspection.ErrInspectionAlreadyOpen, open.ID())
		case !errors.Is(err, ports.ErrNotFound):
			return err
		}
		insp, err := inspection.Open(inspection.OpenInput{
			ID: y.d.IDs.New("insp"), SiteID: tr.SiteID(), TrailerID: tr.ID(), VisitID: visitID, Template: tpl, Gate: in.Gate,
			PerformerRef: in.PerformerRef, At: y.now(),
		})
		if err != nil {
			return err
		}
		if err := y.d.Inspections.Save(ctx, insp); err != nil {
			return err
		}
		out = inspectionView(insp)
		return nil
	})
	return out, err
}

// inspectionVisit resolves the visit an inspection is made for: the one named
// (it must be a visit of the trailer's site), else the visit the trailer came
// with (a trailer is minted per arrival, so that is its presence on site).
func (y *Yard) inspectionVisit(ctx context.Context, tr *trailer.Trailer, named string) (string, error) {
	if named == "" || named == tr.Snapshot().VisitID {
		return tr.Snapshot().VisitID, nil
	}
	v, err := y.d.Visits.FindByID(ctx, named)
	if err != nil {
		return "", notFoundAs(err, ErrVisitNotFound)
	}
	if v.SiteID() != tr.SiteID() {
		return "", shared.Invalid("visit %s is a visit of site %s, the trailer is at site %s", named, v.SiteID(), tr.SiteID())
	}
	return named, nil
}

// RecordInspectionItem records the result of one item (PASS, FAIL or
// NOT_APPLICABLE where the template allows it) with an optional note and opaque
// evidence references, while the inspection is OPEN. Recording again replaces.
func (y *Yard) RecordInspectionItem(ctx context.Context, id, itemCode string, rec inspection.ItemRecord) (views.Inspection, error) {
	return y.mutateInspection(ctx, id, func(_ context.Context, i *inspection.Inspection) error { return i.RecordItem(itemCode, rec) })
}

// CompleteInspection ends an inspection: PASSED, or FAILED when a required item
// failed. A failure holds the trailer in the same unit of work (reason of the most severe failed
// item, QUALITY unless the template says otherwise) unless the trailer already
// carries a hold or has left; the hold is released by the existing human action.
func (y *Yard) CompleteInspection(ctx context.Context, id string, grade inspection.ConditionGrade) (views.Inspection, error) {
	return y.mutateInspection(ctx, id, func(ctx context.Context, i *inspection.Inspection) error {
		tr, err := y.loadTrailer(ctx, i.TrailerID())
		if err != nil {
			return err
		}
		now := y.now()
		holdable := tr.State() != trailer.Departed && tr.Hold() == ""
		if err := i.Complete(inspection.CompleteInput{Grade: grade, At: now, TrailerHoldable: holdable}); err != nil {
			return err
		}
		if err := y.publish(ctx, i.PullEvents()); err != nil {
			return err
		}
		if i.Status() != inspection.StatusFailed || !holdable {
			return nil
		}
		if err := tr.PlaceHold(i.HoldReason(), now); err != nil {
			return err
		}
		return y.saveTrailer(ctx, tr)
	})
}

// mutateInspection loads an inspection, applies one command, saves it and
// answers its view, all in one unit of work.
func (y *Yard) mutateInspection(ctx context.Context, id string, fn func(context.Context, *inspection.Inspection) error) (views.Inspection, error) {
	var out views.Inspection
	err := y.atomically(ctx, func(ctx context.Context) error {
		i, err := y.d.Inspections.FindByID(ctx, id)
		if err != nil {
			return notFoundAs(err, ErrInspectionNotFound)
		}
		if err := fn(ctx, i); err != nil {
			return err
		}
		if err := y.d.Inspections.Save(ctx, i); err != nil {
			return err
		}
		out = inspectionView(i)
		return nil
	})
	return out, err
}

// GetInspection returns one inspection.
func (y *Yard) GetInspection(ctx context.Context, id string) (views.Inspection, error) {
	i, err := y.d.Inspections.FindByID(ctx, id)
	if err != nil {
		return views.Inspection{}, notFoundAs(err, ErrInspectionNotFound)
	}
	return inspectionView(i), nil
}

// ListTrailerInspections returns one keyset page of the inspections of a
// trailer, newest first.
func (y *Yard) ListTrailerInspections(ctx context.Context, trailerID string, after *views.Key, limit int) (views.Page[views.Inspection], error) {
	if _, err := y.loadTrailer(ctx, trailerID); err != nil {
		return views.Page[views.Inspection]{}, err
	}
	limit = pageLimit(limit)
	rows, err := y.d.Inspections.ListByTrailer(ctx, trailerID, after, limit+1)
	if err != nil {
		return views.Page[views.Inspection]{}, err
	}
	keys := make([]views.Key, 0, len(rows))
	items := make([]views.Inspection, 0, len(rows))
	for _, i := range rows {
		s := i.Snapshot()
		keys = append(keys, views.Key{At: s.OpenedAt, ID: s.ID})
		items = append(items, inspectionView(i))
	}
	return trim(items, keys, limit)
}
