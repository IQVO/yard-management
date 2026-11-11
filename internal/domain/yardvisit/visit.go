// Package yardvisit holds the YardVisit aggregate (ADR 0002): one tractor and
// driver passing the gate, from check-in to check-out, with the trailers it
// brings and takes. It is pure: no I/O, no clock reads. Facts it cannot know
// (the appointment copy, whether a trailer is released) are passed in.
package yardvisit

import (
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// Entity is the aggregate segment of the CloudEvents type of this package.
const Entity = "yardvisit"

// Errors of the YardVisit aggregate (compare with errors.Is).
var (
	// ErrVisitAlreadyOpen: at most one CHECKED_IN visit per tractor plate per
	// site. Raised by the use case and the partial unique index.
	ErrVisitAlreadyOpen = errors.New("a visit is already open for this tractor plate")
	// ErrAppointmentRequired: the site requires an appointment and the visit
	// names none that is BOOKED.
	ErrAppointmentRequired = errors.New("an appointment is required at this site")
	// ErrUnknownAppointment: the named appointment is not in the local copy or
	// does not cover exactly one trailer of the visit.
	ErrUnknownAppointment = errors.New("unknown appointment")
	// ErrTrailerNotReleased: a trailer taken out is not on site or not released.
	ErrTrailerNotReleased = errors.New("trailer is not released")
	// ErrSealRequired: a loaded trailer leaving on LIVE_LOAD or PICKUP carries
	// no seal.
	ErrSealRequired = errors.New("a loaded outbound trailer needs a seal")
)

// State is the lifecycle state of a visit.
type State string

// The visit states.
const (
	CheckedIn  State = "CHECKED_IN"
	CheckedOut State = "CHECKED_OUT"
	Rejected   State = "REJECTED"
)

// RejectionReason says why a visit was turned away.
type RejectionReason string

// The rejection reasons.
const (
	ReasonAppointmentRequired  RejectionReason = "APPOINTMENT_REQUIRED"
	ReasonUnknownAppointment   RejectionReason = "UNKNOWN_APPOINTMENT"
	ReasonSealMismatch         RejectionReason = "SEAL_MISMATCH"
	ReasonTrailerAlreadyOnSite RejectionReason = "TRAILER_ALREADY_ON_SITE"
	ReasonSiteRequest          RejectionReason = "SITE_REQUEST"
)

// Valid reports whether r is a rejection reason.
func (r RejectionReason) Valid() bool {
	switch r {
	case ReasonAppointmentRequired, ReasonUnknownAppointment, ReasonSealMismatch, ReasonTrailerAlreadyOnSite, ReasonSiteRequest:
		return true
	}
	return false
}

// Visit is the aggregate root.
type Visit struct {
	id, site                        string
	carrier, scac, plate, driverRef string
	purpose                         shared.Purpose
	appointmentID                   string
	state                           State
	reason                          RejectionReason
	trailersIn, trailersOut         []string
	arrivedAt, departedAt           time.Time
	version                         int64
	events                          []shared.Event
}

// TrailerRef is a trailer the visit brings, as the VisitCheckedIn event
// carries it.
type TrailerRef struct {
	ID, UnitNumber, SCAC string
	Kind                 shared.TrailerKind
	Load                 shared.LoadState
	// UnitScheme is the numbering scheme the caller declared for the unit
	// number ("" = none); SealClass the declared ISO 17712 class of its seal
	// ("" = none). Both are optional and additive (ADR 0012).
	UnitScheme shared.UnitScheme
	SealClass  shared.SealClass
}

// Gate is what the local copies say about the site and the named appointment.
type Gate struct {
	// RequireAppointment is the site's policy.
	RequireAppointment bool
	// AppointmentKnown: the named appointment is in the local copy.
	AppointmentKnown bool
	// AppointmentBooked: it is BOOKED for this site.
	AppointmentBooked bool
}

// CheckInInput is everything CheckIn needs.
type CheckInInput struct {
	ID, SiteID    string
	Carrier, SCAC string
	TractorPlate  string
	DriverRef     string
	Purpose       shared.Purpose
	AppointmentID string
	Trailers      []TrailerRef
	Gate          Gate
	At            time.Time
}

// CheckIn creates a CHECKED_IN visit at version 1 and raises VisitCheckedIn.
// A named appointment must be known and cover exactly one trailer
// (ErrUnknownAppointment); a site that requires appointments needs a BOOKED
// one (ErrAppointmentRequired); on a site that does not, a named appointment
// that is not BOOKED is dropped and the visit is a walk-in. A refused check-in
// creates nothing.
func CheckIn(in CheckInInput) (*Visit, error) {
	if err := validateParty(in.ID, in.SiteID, in.Carrier, in.SCAC, in.TractorPlate, in.DriverRef, in.At); err != nil {
		return nil, err
	}
	schemes, err := validateTrailers(in)
	if err != nil {
		return nil, err
	}
	appointment := in.AppointmentID
	named := appointment != ""
	if named && (!in.Gate.AppointmentKnown || len(in.Trailers) != 1) {
		return nil, fmt.Errorf("%w: %s must be in the local copy and cover exactly one trailer of the visit", ErrUnknownAppointment, appointment)
	}
	if in.Gate.RequireAppointment && (!named || !in.Gate.AppointmentBooked) {
		return nil, ErrAppointmentRequired
	}
	if named && !in.Gate.AppointmentBooked {
		appointment = ""
	}
	v := newVisit(in.ID, in.SiteID, in.Carrier, in.SCAC, in.TractorPlate, in.DriverRef, in.Purpose, appointment, in.At)
	v.state = CheckedIn
	refs := make([]VisitTrailer, 0, len(in.Trailers))
	for i, tr := range in.Trailers {
		v.trailersIn = append(v.trailersIn, tr.ID)
		refs = append(refs, VisitTrailer{
			TrailerID: tr.ID, UnitNumber: tr.UnitNumber, CarrierSCAC: shared.Str(tr.SCAC), Kind: tr.Kind, LoadState: tr.Load,
			UnitNumberScheme: shared.Str(string(schemes[i])), SealClass: shared.Str(string(tr.SealClass)),
		})
	}
	v.emit(VisitCheckedIn{
		Header: v.header("VisitCheckedIn", in.At), VisitID: v.id, SiteID: v.site, Carrier: v.carrier, CarrierSCAC: shared.Str(v.scac), Purpose: v.purpose,
		AppointmentID: shared.Str(v.appointmentID), Trailers: refs, ArrivedAt: shared.Stamp(in.At), Version: v.version,
	})
	return v, nil
}

func newVisit(id, site, carrier, scac, plate, driver string, purpose shared.Purpose, appointment string, at time.Time) *Visit {
	return &Visit{
		id: id, site: site, carrier: carrier, scac: scac, plate: plate, driverRef: driver, purpose: purpose,
		appointmentID: appointment, trailersIn: []string{}, trailersOut: []string{}, arrivedAt: at.UTC(), version: 1,
	}
}

func validateParty(id, site, carrier, scac, plate, driver string, at time.Time) error {
	for _, c := range []struct {
		name, v string
		max     int
		req     bool
	}{{"visit id", id, 64, true}, {"site id", site, 64, true}, {"tractor plate", plate, 32, true}, {"driver reference", driver, 64, false}} {
		check := shared.Check
		if c.req {
			check = shared.Require
		}
		if err := check(c.name, c.v, c.max); err != nil {
			return err
		}
	}
	if shared.IsBlank(carrier) || len([]rune(carrier)) > 100 {
		return fmt.Errorf("%w: carrier name must be 1..100 characters", shared.ErrInvalidCarrier)
	}
	if err := shared.Check("carrier", carrier, 100); err != nil {
		return err
	}
	if err := shared.ValidateSCAC(scac); err != nil {
		return err
	}
	if at.IsZero() {
		return shared.Invalid("arrival time is required")
	}
	return nil
}

// validateTrailers checks the trailers a visit brings and returns the unit
// number scheme each one was validated against ("" = free-form).
func validateTrailers(in CheckInInput) ([]shared.UnitScheme, error) {
	if !in.Purpose.Valid() {
		return nil, shared.Invalid("purpose %q is not valid", in.Purpose)
	}
	n := len(in.Trailers)
	if in.Purpose == shared.PurposeBobtail && n > 0 {
		return nil, shared.Invalid("a BOBTAIL visit brings no trailer")
	}
	if n == 0 && in.Purpose != shared.PurposeBobtail && in.Purpose != shared.PurposePickup {
		return nil, shared.Invalid("a %s visit brings at least one trailer", in.Purpose)
	}
	seen := map[string]bool{}
	schemes := make([]shared.UnitScheme, 0, n)
	for _, tr := range in.Trailers {
		scheme, err := validateTrailer(tr)
		if err != nil {
			return nil, err
		}
		if seen[tr.ID] {
			return nil, shared.Invalid("trailer %s: kind and load state must be valid and the trailer listed once", tr.ID)
		}
		seen[tr.ID] = true
		schemes = append(schemes, scheme)
	}
	return schemes, nil
}

func validateTrailer(tr TrailerRef) (shared.UnitScheme, error) {
	if err := shared.Require("trailer id", tr.ID, 64); err != nil {
		return "", err
	}
	if err := shared.Require("unit number", tr.UnitNumber, 64); err != nil {
		return "", err
	}
	if err := shared.ValidateSCAC(tr.SCAC); err != nil {
		return "", err
	}
	if !tr.Kind.Valid() || !tr.Load.Valid() {
		return "", shared.Invalid("trailer %s: kind and load state must be valid and the trailer listed once", tr.ID)
	}
	unit, err := shared.NewUnitNumber(tr.UnitNumber, tr.Kind, tr.UnitScheme)
	if err != nil {
		return "", err
	}
	if _, err := shared.ParseSealClass(string(tr.SealClass)); err != nil {
		return "", err
	}
	return unit.Scheme(), nil
}

// RejectInput is everything RejectAtGate needs.
type RejectInput struct {
	ID, SiteID    string
	Carrier, SCAC string
	TractorPlate  string
	DriverRef     string
	AppointmentID string
	Reason        RejectionReason
	At            time.Time
}

// RejectAtGate records a REJECTED visit at version 1 and raises VisitRejected.
func RejectAtGate(in RejectInput) (*Visit, error) {
	if err := validateParty(in.ID, in.SiteID, in.Carrier, in.SCAC, in.TractorPlate, in.DriverRef, in.At); err != nil {
		return nil, err
	}
	if err := shared.Check("appointment id", in.AppointmentID, 64); err != nil {
		return nil, err
	}
	if !in.Reason.Valid() {
		return nil, shared.Invalid("rejection reason %q is not valid", in.Reason)
	}
	v := newVisit(in.ID, in.SiteID, in.Carrier, in.SCAC, in.TractorPlate, in.DriverRef, "", in.AppointmentID, in.At)
	v.state, v.reason = Rejected, in.Reason
	v.emit(VisitRejected{
		Header: v.header("VisitRejected", in.At), VisitID: v.id, SiteID: v.site, CarrierSCAC: shared.Str(v.scac),
		AppointmentID: shared.Str(v.appointmentID), Reason: v.reason, RejectedAt: shared.Stamp(in.At),
	})
	return v, nil
}

// Departure is what CheckOut needs to know about a trailer taken out.
type Departure struct {
	TrailerID  string
	OnSite     bool
	Released   bool
	Loaded     bool
	SealNumber string
}

// CheckOut lets a CHECKED_IN visit leave with the listed trailers. Every
// trailer must be on site and released (ErrTrailerNotReleased) and every
// loaded trailer of a LIVE_LOAD or PICKUP visit must carry a seal
// (ErrSealRequired). A trailer the visit brought and does not take stays on
// site (a drop).
func (v *Visit) CheckOut(out []Departure, at time.Time) error {
	if v.state != CheckedIn {
		return fmt.Errorf("%w: CheckOut on a %s visit", shared.ErrIllegalTransition, v.state)
	}
	ids := make([]string, 0, len(out))
	seen := map[string]bool{}
	for _, d := range out {
		if shared.IsBlank(d.TrailerID) || seen[d.TrailerID] {
			return shared.Invalid("trailersOut lists each trailer once")
		}
		seen[d.TrailerID] = true
		if !d.OnSite || !d.Released {
			return fmt.Errorf("%w: %s", ErrTrailerNotReleased, d.TrailerID)
		}
		if v.purpose.Outbound() && d.Loaded && d.SealNumber == "" {
			return fmt.Errorf("%w: %s", ErrSealRequired, d.TrailerID)
		}
		ids = append(ids, d.TrailerID)
	}
	v.state, v.trailersOut, v.departedAt = CheckedOut, ids, at.UTC()
	v.version++
	v.emit(VisitCheckedOut{
		Header: v.header("VisitCheckedOut", at), VisitID: v.id, SiteID: v.site, TrailersOut: shared.Strings(ids), DepartedAt: shared.Stamp(at),
		OnSiteMinutes: max(int64(at.Sub(v.arrivedAt)/time.Minute), 0), Version: v.version,
	})
	return nil
}

func (v *Visit) emit(e shared.Event) { v.events = append(v.events, e) }

func (v *Visit) header(name string, at time.Time) shared.Header {
	return shared.NewHeader(Entity, name, v.id, at)
}

// PullEvents returns the pending domain events and clears them.
func (v *Visit) PullEvents() []shared.Event {
	ev := v.events
	v.events = nil
	return ev
}

// ID returns the visit id.
func (v *Visit) ID() string { return v.id }

// SiteID returns the site code.
func (v *Visit) SiteID() string { return v.site }

// State returns the lifecycle state.
func (v *Visit) State() State { return v.state }

// Purpose returns why the visit came.
func (v *Visit) Purpose() shared.Purpose { return v.purpose }

// AppointmentID returns the appointment the visit kept ("" for a walk-in).
func (v *Visit) AppointmentID() string { return v.appointmentID }

// Version returns the optimistic-concurrency version.
func (v *Visit) Version() int64 { return v.version }
