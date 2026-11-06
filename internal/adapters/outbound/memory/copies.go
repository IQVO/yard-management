package memory

import (
	"context"
	"sort"
	"time"

	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
)

// Locations implements ports.Locations and ports.LocationFeed.
type Locations struct{ s *Store }

// Locations returns the location copy.
func (s *Store) Locations() *Locations { return &Locations{s} }

// resolve fills the site of every slot whose zone is known (X5).
func (l *Locations) resolve(zoneID string) {
	site, ok := l.s.st.zones[zoneID]
	if !ok {
		return
	}
	for code, loc := range l.s.st.locations {
		if loc.ZoneID == zoneID && loc.SiteCode == "" {
			loc.SiteCode = site
			l.s.st.locations[code] = loc
		}
	}
}

// ApplyZone implements ports.LocationFeed.
func (l *Locations) ApplyZone(_ context.Context, zoneID, siteCode string) error {
	l.s.mu.Lock()
	defer l.s.mu.Unlock()
	l.s.st.zones[zoneID] = siteCode
	l.resolve(zoneID)
	return nil
}

// ApplySlot implements ports.LocationFeed: a slot is (re)registered active.
func (l *Locations) ApplySlot(_ context.Context, code, zoneID string, kind shared.LocationKind, dockFlow string) error {
	l.s.mu.Lock()
	defer l.s.mu.Unlock()
	l.s.st.locations[code] = views.LocationInfo{Code: code, ZoneID: zoneID, SiteCode: l.s.st.zones[zoneID], Kind: kind, DockFlow: dockFlow, Active: true}
	return nil
}

// Decommission implements ports.LocationFeed.
func (l *Locations) Decommission(_ context.Context, code string) error {
	l.s.mu.Lock()
	defer l.s.mu.Unlock()
	if loc, ok := l.s.st.locations[code]; ok {
		loc.Active = false
		l.s.st.locations[code] = loc
	}
	return nil
}

// Get implements ports.Locations.
func (l *Locations) Get(_ context.Context, code string) (views.LocationInfo, bool, error) {
	l.s.mu.Lock()
	defer l.s.mu.Unlock()
	loc, ok := l.s.st.locations[code]
	return loc, ok, nil
}

// ListBySite implements ports.Locations.
func (l *Locations) ListBySite(_ context.Context, siteID string) ([]views.LocationInfo, error) {
	l.s.mu.Lock()
	defer l.s.mu.Unlock()
	var out []views.LocationInfo
	for _, loc := range l.s.st.locations {
		if loc.SiteCode == siteID && loc.Active {
			out = append(out, loc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out, nil
}

// FreeSpots implements ports.Locations.
func (l *Locations) FreeSpots(ctx context.Context, siteID string, zoneIDs []string, limit int) ([]string, error) {
	spots, _ := l.ListBySite(ctx, siteID)
	l.s.mu.Lock()
	defer l.s.mu.Unlock()
	taken := map[shared.Location]bool{}
	for _, t := range l.s.st.trailers {
		taken[t.Location] = true
	}
	for _, m := range l.s.st.moves {
		if open(m) {
			taken[m.To] = true
		}
	}
	var out []string
	for _, loc := range spots {
		if loc.Kind != shared.Spot || taken[shared.Location{Kind: shared.Spot, Code: loc.Code}] || !inZones(loc.ZoneID, zoneIDs) {
			continue
		}
		if out = append(out, loc.Code); len(out) == limit {
			break
		}
	}
	return out, nil
}

func inZones(zone string, zones []string) bool {
	if len(zones) == 0 {
		return true
	}
	for _, z := range zones {
		if z == zone {
			return true
		}
	}
	return false
}

// Appointments implements ports.Appointments and ports.AppointmentFeed.
type Appointments struct{ s *Store }

// Appointments returns the appointment copy.
func (s *Store) Appointments() *Appointments { return &Appointments{s} }

// Get implements ports.Appointments.
func (a *Appointments) Get(_ context.Context, id string) (views.Appointment, bool, error) {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	v, ok := a.s.st.appts[id]
	return v, ok, nil
}

// ListByDoor implements ports.Appointments.
func (a *Appointments) ListByDoor(_ context.Context, doorCode string, statuses ...string) ([]views.Appointment, error) {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	var out []views.Appointment
	for _, v := range a.s.st.appts {
		if v.DoorCode != doorCode {
			continue
		}
		for _, st := range statuses {
			if v.Status == st {
				out = append(out, v)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return keyBefore(out[i].WindowStart, out[i].ID, out[j].WindowStart, out[j].ID) })
	return out, nil
}

func terminal(status string) bool { return status == views.ApptCancelled || status == views.ApptNoShow }

// Apply implements ports.AppointmentFeed.
func (a *Appointments) Apply(_ context.Context, in views.Appointment) error {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	cur, ok := a.s.st.appts[in.ID]
	if !ok {
		if in.Status == "" {
			in.Status = views.ApptBooked
		}
		a.s.st.appts[in.ID] = in
		return nil
	}
	if in.Version <= cur.Version {
		return nil
	}
	a.s.st.appts[in.ID] = merge(cur, in)
	return nil
}

// merge overlays the non-zero fields of in on cur; a terminal status is kept.
func merge(cur, in views.Appointment) views.Appointment {
	out := cur
	out.Version = in.Version
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&out.SiteID, in.SiteID)
	set(&out.Direction, in.Direction)
	set(&out.DoorCode, in.DoorCode)
	set(&out.CarrierSCAC, in.CarrierSCAC)
	set(&out.ExpectedSeal, in.ExpectedSeal)
	if !in.WindowStart.IsZero() {
		out.WindowStart, out.WindowEnd = in.WindowStart, in.WindowEnd
	}
	if in.ASNNumbers != nil {
		out.ASNNumbers = in.ASNNumbers
	}
	if in.ShipmentRefs != nil {
		out.ShipmentRefs = in.ShipmentRefs
	}
	if in.ToleranceMinutes > 0 {
		out.ToleranceMinutes = in.ToleranceMinutes
	}
	if terminal(in.Status) || (in.Status != "" && !terminal(cur.Status) && in.Status != views.ApptBooked) {
		out.Status = in.Status
	}
	return out
}

// MarkStatus implements ports.AppointmentFeed.
func (a *Appointments) MarkStatus(_ context.Context, id, status string) (bool, error) {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	cur, ok := a.s.st.appts[id]
	if !ok || terminal(cur.Status) {
		return false, nil
	}
	cur.Status = status
	a.s.st.appts[id] = cur
	return true, nil
}

// Closures implements ports.Closures and ports.ClosureFeed.
type Closures struct{ s *Store }

// Closures returns the closure copy.
func (s *Store) Closures() *Closures { return &Closures{s} }

// Closed implements ports.Closures.
func (c *Closures) Closed(_ context.Context, doorCode string, at time.Time) (bool, error) {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	for _, v := range c.s.st.closures {
		if v.DoorCode == doorCode && !v.Lifted && !at.Before(v.From) && at.Before(v.To) {
			return true, nil
		}
	}
	return false, nil
}

// Schedule implements ports.ClosureFeed.
func (c *Closures) Schedule(_ context.Context, v views.Closure) error {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	if cur, ok := c.s.st.closures[v.ID]; ok {
		v.Lifted = cur.Lifted
	}
	c.s.st.closures[v.ID] = v
	return nil
}

// Lift implements ports.ClosureFeed: lifting an unseen closure leaves a lifted marker.
func (c *Closures) Lift(_ context.Context, id string) error {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	v := c.s.st.closures[id]
	v.ID, v.Lifted = id, true
	c.s.st.closures[id] = v
	return nil
}

// Asns implements ports.Asns and ports.AsnFeed.
type Asns struct{ s *Store }

// Asns returns the ASN copy.
func (s *Store) Asns() *Asns { return &Asns{s} }

// FilterActive implements ports.Asns.
func (a *Asns) FilterActive(_ context.Context, numbers []string) ([]string, error) {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	out := []string{}
	for _, n := range numbers {
		if !a.s.st.asns[n] {
			out = append(out, n)
		}
	}
	return out, nil
}

// Register implements ports.AsnFeed: a cancelled ASN stays cancelled.
func (a *Asns) Register(_ context.Context, asnNumber string) error {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	if _, ok := a.s.st.asns[asnNumber]; !ok {
		a.s.st.asns[asnNumber] = false
	}
	return nil
}

// Cancel implements ports.AsnFeed.
func (a *Asns) Cancel(_ context.Context, asnNumber string) error {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	a.s.st.asns[asnNumber] = true
	return nil
}

// Receipts implements ports.Receipts and ports.ReceiptFeed.
type Receipts struct{ s *Store }

// Receipts returns the receipt copy.
func (s *Store) Receipts() *Receipts { return &Receipts{s} }

// Get implements ports.Receipts.
func (r *Receipts) Get(_ context.Context, id string) (views.Receipt, bool, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	v, ok := r.s.st.receipts[id]
	return v, ok, nil
}

// ClosedASNs implements ports.Receipts.
func (r *Receipts) ClosedASNs(_ context.Context, appointmentID string) ([]string, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	out := []string{}
	for _, v := range r.s.st.receipts {
		if v.AppointmentID == appointmentID && !v.ClosedAt.IsZero() {
			out = append(out, v.ASNNumber)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Open implements ports.ReceiptFeed: a receipt already closed stays closed.
func (r *Receipts) Open(_ context.Context, v views.Receipt) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if cur, ok := r.s.st.receipts[v.ID]; ok {
		v.ClosedAt = cur.ClosedAt
	}
	r.s.st.receipts[v.ID] = v
	return nil
}

// Close implements ports.ReceiptFeed: closing an unseen receipt leaves a closed marker.
func (r *Receipts) Close(_ context.Context, id, asnNumber string, closedAt time.Time) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	v := r.s.st.receipts[id]
	v.ID, v.ClosedAt = id, closedAt
	if v.ASNNumber == "" {
		v.ASNNumber = asnNumber
	}
	r.s.st.receipts[id] = v
	return nil
}
