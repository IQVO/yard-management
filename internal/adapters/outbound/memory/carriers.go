package memory

import (
	"context"
	"time"

	"github.com/claudioed/yard-management/internal/application/views"
)

// Carriers implements ports.Carriers and ports.CarrierFeed: the local copy of the
// dock-scheduling carrier registry (ADR 0016). Every fact is version-guarded, as in
// the Postgres adapter: a version that is not higher than the stored one changes nothing.
type Carriers struct{ s *Store }

// Carriers returns the carrier copy.
func (s *Store) Carriers() *Carriers { return &Carriers{s} }

// Get implements ports.Carriers.
func (c *Carriers) Get(_ context.Context, scac string) (views.Carrier, bool, error) {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	v, ok := c.s.st.carriers[scac]
	return v, ok, nil
}

// apply stores next when it is newer than the stored carrier (or there is none).
func (c *Carriers) apply(scac string, version int64, change func(cur views.Carrier) views.Carrier) {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	cur, ok := c.s.st.carriers[scac]
	if ok && version <= cur.Version {
		return
	}
	cur.SCAC = scac
	c.s.st.carriers[scac] = change(cur)
}

// Register implements ports.CarrierFeed: the whole snapshot replaces the carrier.
func (c *Carriers) Register(_ context.Context, in views.Carrier) error {
	c.apply(in.SCAC, in.Version, func(views.Carrier) views.Carrier { return in })
	return nil
}

// Suspend implements ports.CarrierFeed: the name and identifiers are kept.
func (c *Carriers) Suspend(_ context.Context, scac, reason string, version int64, at time.Time) error {
	c.apply(scac, version, func(cur views.Carrier) views.Carrier {
		cur.Status, cur.Reason, cur.Version, cur.UpdatedAt = views.CarrierSuspended, reason, version, at
		return cur
	})
	return nil
}

// Reinstate implements ports.CarrierFeed: the suspension reason is cleared.
func (c *Carriers) Reinstate(_ context.Context, scac string, version int64, at time.Time) error {
	c.apply(scac, version, func(cur views.Carrier) views.Carrier {
		cur.Status, cur.Reason, cur.Version, cur.UpdatedAt = views.CarrierActive, "", version, at
		return cur
	})
	return nil
}
