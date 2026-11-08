package memory

import (
	"context"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/domain/yardcheck"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
)

// Checks implements ports.CheckRepo.
type Checks struct{ s *Store }

// Checks returns the yard check repository.
func (s *Store) Checks() *Checks { return &Checks{s} }

// Save implements ports.CheckRepo.
func (r *Checks) Save(_ context.Context, c *yardcheck.Check) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	snap := c.Snapshot()
	old, exists := r.s.st.checks[snap.ID]
	if err := guard(exists, old.Version, snap.Version); err != nil {
		return err
	}
	if snap.State == yardcheck.Open {
		for id, o := range r.s.st.checks {
			if id != snap.ID && o.State == yardcheck.Open && o.SiteID == snap.SiteID {
				return yardcheck.ErrYardCheckOpen
			}
		}
	}
	r.s.st.checks[snap.ID] = snap
	return nil
}

// FindByID implements ports.CheckRepo.
func (r *Checks) FindByID(_ context.Context, id string) (*yardcheck.Check, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	snap, ok := r.s.st.checks[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return yardcheck.Restore(snap)
}

// Policies implements ports.PolicyRepo.
type Policies struct{ s *Store }

// Policies returns the policy repository.
func (s *Store) Policies() *Policies { return &Policies{s} }

// Save implements ports.PolicyRepo: the first declaration is version 2.
func (r *Policies) Save(_ context.Context, p *yardpolicy.Policy) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	snap := p.Snapshot()
	old, exists := r.s.st.policies[snap.SiteID]
	if (!exists && snap.Version != 2) || (exists && old.Version != snap.Version-1) {
		return ports.ErrConcurrentModification
	}
	r.s.st.policies[snap.SiteID] = snap
	return nil
}

// Find implements ports.PolicyRepo.
func (r *Policies) Find(_ context.Context, siteID string) (*yardpolicy.Policy, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	snap, ok := r.s.st.policies[siteID]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return yardpolicy.Restore(snap)
}
