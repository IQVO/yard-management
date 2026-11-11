// Package yardpolicy holds the YardPolicy aggregate (ADR 0002): the per-site
// yard rules every gate and move decision reads. One policy per site, keyed
// by the site code; an undeclared site answers the default policy (D8). It
// is pure: the known-spot facts are passed in.
package yardpolicy

import (
	"fmt"
	"slices"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// SealAction is what a seal mismatch at the gate does besides raising
// SealExceptionRaised.
type SealAction string

// The seal mismatch actions.
const (
	Flag   SealAction = "FLAG"
	Reject SealAction = "REJECT"
)

// DefaultLeaseSeconds is the hostler lease of an undeclared site.
const DefaultLeaseSeconds = 300

// Policy is the aggregate root.
type Policy struct {
	site          string
	requireAppt   bool
	holdingSpots  []string
	parkingZones  []string
	dwell         map[shared.TrailerKind]int64
	leaseSeconds  int
	sealAction    SealAction
	freeTimeMin   int
	minSealClass  shared.SealClass
	requireSecure bool
	version       int64
	declared      bool
}

// Default is the policy of a site that never declared one: appointments not
// required, seal mismatch FLAG, a 300 s lease, no dwell thresholds and no
// detention free time. It is version 1 and not declared.
func Default(siteID string) (*Policy, error) {
	if err := shared.Require("site id", siteID, 64); err != nil {
		return nil, err
	}
	return &Policy{
		site: siteID, holdingSpots: []string{}, parkingZones: []string{}, dwell: map[shared.TrailerKind]int64{},
		leaseSeconds: DefaultLeaseSeconds, sealAction: Flag, version: 1,
	}, nil
}

// DeclareInput is the full replacement of a policy.
type DeclareInput struct {
	RequireAppointment bool
	HoldingSpotCodes   []string
	ParkingZoneIDs     []string
	// DwellThresholdMinutes per trailer kind; a kind without an entry is
	// never alerted.
	DwellThresholdMinutes map[shared.TrailerKind]int64
	LeaseSeconds          int
	SealMismatchAction    SealAction
	// FreeTimeMinutes is the detention clock, optional: 0 means none.
	FreeTimeMinutes int
	// MinimumSealClass is the lowest ISO 17712 class a seal may have at
	// check-in and record-seal; "" (the default) means no minimum (ADR 0012).
	MinimumSealClass shared.SealClass
	// RequireSecuringBeforeDoorWork makes door work start only on a secured
	// trailer (restraint engaged or wheels chocked, ADR 0013). false (the
	// default) is the behaviour before the interlock existed.
	RequireSecuringBeforeDoorWork bool
}

// Declare replaces the policy (DeclareYardPolicy). Holding spots must be
// known Yard slots of the site (ErrUnknownLocation; knownSpot says so);
// thresholds, lease and free time must be positive (ErrValidation). The
// version rises by one, the first declaration included.
func (p *Policy) Declare(in DeclareInput, knownSpot func(code string) bool) error {
	if err := validateScalars(in); err != nil {
		return err
	}
	for kind, minutes := range in.DwellThresholdMinutes {
		if !kind.Valid() || minutes < 1 {
			return shared.Invalid("dwell threshold of %q must be a positive number of minutes for a valid kind", kind)
		}
	}
	if err := checkList("holding spot code", in.HoldingSpotCodes); err != nil {
		return err
	}
	if err := checkList("parking zone id", in.ParkingZoneIDs); err != nil {
		return err
	}
	for _, code := range in.HoldingSpotCodes {
		if !knownSpot(code) {
			return fmt.Errorf("%w: holding spot %q is not a Yard slot of site %s", shared.ErrUnknownLocation, code, p.site)
		}
	}
	p.requireAppt, p.leaseSeconds, p.sealAction, p.freeTimeMin = in.RequireAppointment, in.LeaseSeconds, in.SealMismatchAction, in.FreeTimeMinutes
	p.minSealClass, p.requireSecure = in.MinimumSealClass, in.RequireSecuringBeforeDoorWork
	p.holdingSpots, p.parkingZones = slices.Clone(in.HoldingSpotCodes), slices.Clone(in.ParkingZoneIDs)
	if p.holdingSpots == nil {
		p.holdingSpots = []string{}
	}
	if p.parkingZones == nil {
		p.parkingZones = []string{}
	}
	p.dwell = make(map[shared.TrailerKind]int64, len(in.DwellThresholdMinutes))
	for k, v := range in.DwellThresholdMinutes {
		p.dwell[k] = v
	}
	p.declared = true
	p.version++
	return nil
}

// validateScalars checks the single-valued fields of a declaration: lease,
// seal mismatch action, minimum seal class and detention free time.
func validateScalars(in DeclareInput) error {
	if in.LeaseSeconds < 1 {
		return shared.Invalid("lease seconds must be positive")
	}
	if in.SealMismatchAction != Flag && in.SealMismatchAction != Reject {
		return shared.Invalid("seal mismatch action must be FLAG or REJECT")
	}
	if _, err := shared.ParseSealClass(string(in.MinimumSealClass)); err != nil {
		return err
	}
	if in.FreeTimeMinutes < 0 {
		return shared.Invalid("free time minutes must be positive when given")
	}
	return nil
}

func checkList(name string, list []string) error {
	seen := map[string]bool{}
	for _, v := range list {
		if err := shared.Require(name, v, 128); err != nil {
			return err
		}
		if seen[v] {
			return shared.Invalid("%s %q is listed twice", name, v)
		}
		seen[v] = true
	}
	return nil
}

// SiteID returns the site code.
func (p *Policy) SiteID() string { return p.site }

// RequireAppointment reports whether the gate refuses walk-ins.
func (p *Policy) RequireAppointment() bool { return p.requireAppt }

// HoldingSpotCodes returns the spots for walk-ins and early arrivals.
func (p *Policy) HoldingSpotCodes() []string { return slices.Clone(p.holdingSpots) }

// ParkingZoneIDs returns the zones a trailer is parked in.
func (p *Policy) ParkingZoneIDs() []string { return slices.Clone(p.parkingZones) }

// SealMismatchAction returns FLAG or REJECT.
func (p *Policy) SealMismatchAction() SealAction { return p.sealAction }

// Lease returns the hostler lease.
func (p *Policy) Lease() time.Duration { return time.Duration(p.leaseSeconds) * time.Second }

// DwellThreshold returns the dwell threshold of a kind in minutes (0 = none).
func (p *Policy) DwellThreshold(kind shared.TrailerKind) int64 { return p.dwell[kind] }

// Version returns the policy version (the ETag).
func (p *Policy) Version() int64 { return p.version }

// Declared reports whether the site ever declared a policy.
func (p *Policy) Declared() bool { return p.declared }

// Snapshot is the persisted form of a Policy: scalars only.
type Snapshot struct {
	SiteID                string
	RequireAppointment    bool
	HoldingSpotCodes      []string
	ParkingZoneIDs        []string
	DwellThresholdMinutes map[shared.TrailerKind]int64
	LeaseSeconds          int
	SealMismatchAction    SealAction
	FreeTimeMinutes       int
	MinimumSealClass      shared.SealClass
	RequireSecuring       bool
	Version               int64
	Declared              bool
}

// Snapshot returns the persisted form.
func (p *Policy) Snapshot() Snapshot {
	d := make(map[shared.TrailerKind]int64, len(p.dwell))
	for k, v := range p.dwell {
		d[k] = v
	}
	return Snapshot{
		SiteID: p.site, RequireAppointment: p.requireAppt, HoldingSpotCodes: p.HoldingSpotCodes(), ParkingZoneIDs: p.ParkingZoneIDs(),
		DwellThresholdMinutes: d, LeaseSeconds: p.leaseSeconds, SealMismatchAction: p.sealAction, FreeTimeMinutes: p.freeTimeMin,
		MinimumSealClass: p.minSealClass, RequireSecuring: p.requireSecure, Version: p.version, Declared: p.declared,
	}
}

// Restore rebuilds a persisted policy, validating it as Declare does (the
// known-spot rule belongs to the declaration moment, not to the stored row).
func Restore(s Snapshot) (*Policy, error) {
	p, err := Default(s.SiteID)
	if err != nil {
		return nil, err
	}
	if s.Version < 1 {
		return nil, shared.Invalid("version must be at least 1")
	}
	if err := p.Declare(DeclareInput{
		RequireAppointment: s.RequireAppointment, HoldingSpotCodes: s.HoldingSpotCodes, ParkingZoneIDs: s.ParkingZoneIDs,
		DwellThresholdMinutes: s.DwellThresholdMinutes, LeaseSeconds: s.LeaseSeconds, SealMismatchAction: s.SealMismatchAction, FreeTimeMinutes: s.FreeTimeMinutes,
		MinimumSealClass: s.MinimumSealClass, RequireSecuringBeforeDoorWork: s.RequireSecuring,
	}, func(string) bool { return true }); err != nil {
		return nil, err
	}
	p.version, p.declared = s.Version, s.Declared
	return p, nil
}

// FreeTimeMinutes returns the detention free time (0 = none).
func (p *Policy) FreeTimeMinutes() int { return p.freeTimeMin }

// MinimumSealClass returns the lowest seal class the site accepts ("" = none).
func (p *Policy) MinimumSealClass() shared.SealClass { return p.minSealClass }

// RequireSecuringBeforeDoorWork reports whether door work starts only on a
// secured trailer (false = today's behaviour; a regulated site sets it true).
func (p *Policy) RequireSecuringBeforeDoorWork() bool { return p.requireSecure }
