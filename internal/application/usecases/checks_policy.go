package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/yardcheck"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
)

// OpenYardCheck opens an audit; at most one is OPEN per site.
func (y *Yard) OpenYardCheck(ctx context.Context, siteID string) (views.YardCheck, error) {
	var out views.YardCheck
	err := y.atomically(ctx, func(ctx context.Context) error {
		c, err := yardcheck.OpenYardCheck(y.d.IDs.New("yck"), siteID, y.now())
		if err != nil {
			return err
		}
		if err := y.d.Checks.Save(ctx, c); err != nil {
			return err
		}
		out = checkView(c)
		return nil
	})
	return out, err
}

func (y *Yard) loadCheck(ctx context.Context, id string) (*yardcheck.Check, error) {
	c, err := y.d.Checks.FindByID(ctx, id)
	return c, notFoundAs(err, ErrYardCheckNotFound)
}

// RecordObservation records what was found at one location.
func (y *Yard) RecordObservation(ctx context.Context, yardCheckID, locationCode, unitNumber string) (views.YardCheck, error) {
	var out views.YardCheck
	err := y.atomically(ctx, func(ctx context.Context) error {
		c, err := y.loadCheck(ctx, yardCheckID)
		if err != nil {
			return err
		}
		if err := c.RecordObservation(locationCode, unitNumber); err != nil {
			return err
		}
		if err := y.d.Checks.Save(ctx, c); err != nil {
			return err
		}
		out = checkView(c)
		return nil
	})
	return out, err
}

// CloseYardCheck reconciles the observations with every trailer the model
// places at the site and raises YardCheckClosed with the discrepancies.
func (y *Yard) CloseYardCheck(ctx context.Context, yardCheckID string) (views.YardCheck, error) {
	var out views.YardCheck
	err := y.atomically(ctx, func(ctx context.Context) error {
		c, err := y.loadCheck(ctx, yardCheckID)
		if err != nil {
			return err
		}
		onSite, err := y.d.Trailers.ListOnSite(ctx, c.SiteID())
		if err != nil {
			return err
		}
		expected := make([]yardcheck.Expected, 0, len(onSite))
		for _, tr := range onSite {
			if loc := tr.Location(); !loc.IsZero() {
				expected = append(expected, yardcheck.Expected{LocationCode: loc.Code, TrailerID: tr.ID(), UnitNumber: tr.UnitNumber()})
			}
		}
		if err := c.Close(expected, y.now()); err != nil {
			return err
		}
		if err := y.d.Checks.Save(ctx, c); err != nil {
			return err
		}
		out = checkView(c)
		return y.publish(ctx, c.PullEvents())
	})
	return out, err
}

// GetYardPolicy answers the declared policy, or the default one for a site
// that never declared one.
func (y *Yard) GetYardPolicy(ctx context.Context, siteID string) (views.YardPolicy, error) {
	p, err := y.policyOf(ctx, siteID)
	if err != nil {
		return views.YardPolicy{}, err
	}
	return policyView(p), nil
}

// DeclarePolicyInput is the body of DeclareYardPolicy; IfMatch is the policy
// version the caller read (0 = unconditional).
type DeclarePolicyInput struct {
	SiteID  string
	IfMatch int64
	Policy  yardpolicy.DeclareInput
}

// DeclareYardPolicy replaces the policy of a site. Holding spots must be known
// Yard slots of the site.
func (y *Yard) DeclareYardPolicy(ctx context.Context, in DeclarePolicyInput) (views.YardPolicy, error) {
	var out views.YardPolicy
	err := y.atomically(ctx, func(ctx context.Context) error {
		p, err := y.policyOf(ctx, in.SiteID)
		if err != nil {
			return err
		}
		if in.IfMatch != 0 && in.IfMatch != p.Version() {
			return fmt.Errorf("%w: it is at version %d", ErrStalePolicy, p.Version())
		}
		spots, err := y.spotCodes(ctx, in.SiteID)
		if err != nil {
			return err
		}
		if err := p.Declare(in.Policy, func(code string) bool { return spots[code] }); err != nil {
			return err
		}
		if err := y.d.Policies.Save(ctx, p); err != nil {
			return err
		}
		out = policyView(p)
		return nil
	})
	return out, err
}

// spotCodes returns the active Yard slots of the site.
func (y *Yard) spotCodes(ctx context.Context, siteID string) (map[string]bool, error) {
	locs, err := y.d.Locations.ListBySite(ctx, siteID)
	if err != nil {
		return nil, err
	}
	spots := map[string]bool{}
	for _, l := range locs {
		if l.Kind == shared.Spot && l.Active {
			spots[l.Code] = true
		}
	}
	return spots, nil
}

// SweepDwell is the DwellSweep: every trailer on site whose dwell reached its
// kind's threshold raises TrailerDwellExceeded, once per trailer. siteID
// limits the sweep to one site.
func (y *Yard) SweepDwell(ctx context.Context, siteID string) (views.Sweep, error) {
	now := y.now()
	onSite, err := y.d.Trailers.ListOnSite(ctx, siteID)
	if err != nil {
		return views.Sweep{}, err
	}
	policies := map[string]*yardpolicy.Policy{}
	count := 0
	for _, candidate := range onSite {
		pol, ok := policies[candidate.SiteID()]
		if !ok {
			if pol, err = y.policyOf(ctx, candidate.SiteID()); err != nil {
				return views.Sweep{}, err
			}
			policies[candidate.SiteID()] = pol
		}
		threshold := pol.DwellThreshold(candidate.Kind())
		if threshold < 1 || candidate.DwellMinutes(now) < threshold {
			continue
		}
		alerted, err := y.alertDwell(ctx, candidate.ID(), now, threshold)
		if err != nil {
			return views.Sweep{SweptAt: now, Count: count}, err
		}
		if alerted {
			count++
		}
	}
	return views.Sweep{SweptAt: now, Count: count}, nil
}

// alertDwell raises the alert of one trailer in its own unit of work; a trailer
// that changed meanwhile is skipped (the next sweep sees it).
func (y *Yard) alertDwell(ctx context.Context, id string, now time.Time, threshold int64) (bool, error) {
	alerted := false
	err := y.atomically(ctx, func(ctx context.Context) error {
		tr, err := y.d.Trailers.FindByID(ctx, id)
		if err != nil {
			return err
		}
		if alerted = tr.SweepDwell(now, threshold); !alerted {
			return nil
		}
		return y.saveTrailer(ctx, tr)
	})
	switch {
	case err == nil:
		return alerted, nil
	case errors.Is(err, ports.ErrConcurrentModification), errors.Is(err, ports.ErrNotFound):
		return false, nil
	}
	return false, err
}
