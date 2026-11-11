package usecases

import (
	"context"
	"errors"
	"fmt"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/domain/inspection"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
)

// requireInspections is the gate of ADR 0015: before the step behind gate the
// site policy may ask for a PASSED inspection of one or more templates, and the
// trailer must have one. A site that declared no requiredInspections for the
// gate costs nothing here beyond the policy read.
func (y *Yard) requireInspections(ctx context.Context, tr *trailer.Trailer, gate shared.Gate) error {
	pol, err := y.policyOf(ctx, tr.SiteID())
	if err != nil {
		return err
	}
	return y.judgeInspections(ctx, tr, pol, gate)
}

// judgeInspections judges the trailer against the rules of pol for gate. The
// trailer id is minted per arrival, so its inspections are those of its
// current presence on the site: an earlier stay can never satisfy a later one.
func (y *Yard) judgeInspections(ctx context.Context, tr *trailer.Trailer, pol *yardpolicy.Policy, gate shared.Gate) error {
	if gate == shared.GatePreLoad && !tr.Loading() {
		return nil // the load gate is about loading: an inbound unload is not judged
	}
	rules := pol.InspectionRules(gate, tr.Purpose(), tr.Kind())
	if len(rules) == 0 {
		return nil
	}
	records, err := y.d.Inspections.Completed(ctx, tr.ID())
	if err != nil {
		return err
	}
	for _, r := range rules {
		if err := inspection.Judge(gate, r.TemplateID, records); err != nil {
			return err
		}
	}
	return nil
}

// knownTemplates refuses a policy that requires a template nobody published: no
// inspection of it could ever be opened, so its gate would stay shut for good
// (ErrUnknownTemplate, 422).
func (y *Yard) knownTemplates(ctx context.Context, rules []yardpolicy.InspectionRule) error {
	for _, r := range rules {
		if _, _, err := y.findTemplate(ctx, r.TemplateID, 0); errors.Is(err, ports.ErrNotFound) {
			return fmt.Errorf("%w: required inspection template %q is not published", inspection.ErrUnknownTemplate, r.TemplateID)
		} else if err != nil {
			return err
		}
	}
	return nil
}
