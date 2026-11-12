package yardvisit

import (
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// The events of the YardVisit aggregate (ADR 0004).

// VisitTrailer is one trailer of VisitCheckedIn.
type VisitTrailer struct {
	TrailerID   string             `json:"trailer_id"`
	UnitNumber  string             `json:"unit_number"`
	CarrierSCAC *string            `json:"carrier_scac"`
	Kind        shared.TrailerKind `json:"kind"`
	LoadState   shared.LoadState   `json:"load_state"`
	// UnitNumberScheme is ISO6346 when the unit number was validated as an
	// ISO 6346 container number, null for a free-form one. SealClass is the
	// ISO 17712 class declared for the seal, null when none. Both additive
	// (ADR 0012).
	UnitNumberScheme *string `json:"unit_number_scheme"`
	SealClass        *string `json:"seal_class"`
	// HazmatClass and UNNumber are the US DOT hazard class and the UN number
	// the shipper declared, null when none; RequiresTemperatureControl is
	// false when none was declared. All three additive (ADR 0014).
	HazmatClass                *string `json:"hazmat_class"`
	UNNumber                   *string `json:"un_number"`
	RequiresTemperatureControl bool    `json:"requires_temperature_control"`
}

// VisitCheckedIn: a visit passed the gate (integration).
type VisitCheckedIn struct {
	shared.Header `json:"-"`
	VisitID       string         `json:"visit_id"`
	SiteID        string         `json:"site_id"`
	Carrier       string         `json:"carrier"`
	CarrierSCAC   *string        `json:"carrier_scac"`
	Purpose       shared.Purpose `json:"purpose"`
	AppointmentID *string        `json:"appointment_id"`
	Trailers      []VisitTrailer `json:"trailers"`
	ArrivedAt     string         `json:"arrived_at"`
	Version       int64          `json:"version"`
	// CarrierFlag is SUSPENDED when the gate admitted the visit of a carrier
	// the registry copy says is suspended (policy FLAG), null otherwise;
	// additive (ADR 0016).
	CarrierFlag *string `json:"carrier_flag"`
}

// VisitRejected: a visit was turned away (integration, no version).
type VisitRejected struct {
	shared.Header `json:"-"`
	VisitID       string          `json:"visit_id"`
	SiteID        string          `json:"site_id"`
	CarrierSCAC   *string         `json:"carrier_scac"`
	AppointmentID *string         `json:"appointment_id"`
	Reason        RejectionReason `json:"reason"`
	RejectedAt    string          `json:"rejected_at"`
}

// VisitCheckedOut: the visit left the site (analytics only).
type VisitCheckedOut struct {
	shared.Header `json:"-"`
	VisitID       string   `json:"visit_id"`
	SiteID        string   `json:"site_id"`
	TrailersOut   []string `json:"trailers_out"`
	DepartedAt    string   `json:"departed_at"`
	OnSiteMinutes int64    `json:"on_site_minutes"`
	Version       int64    `json:"version"`
}

// Snapshot is the persisted form of a Visit: scalars only. Restore
// validates it.
type Snapshot struct {
	ID, SiteID                      string
	Carrier, SCAC, Plate, DriverRef string
	Purpose                         shared.Purpose
	AppointmentID                   string
	State                           State
	Reason                          RejectionReason
	CarrierFlag                     CarrierFlag
	TrailersIn, TrailersOut         []string
	ArrivedAt, DepartedAt           time.Time
	Version                         int64
}

// Snapshot returns the persisted form (no events).
func (v *Visit) Snapshot() Snapshot {
	return Snapshot{
		ID: v.id, SiteID: v.site, Carrier: v.carrier, SCAC: v.scac, Plate: v.plate, DriverRef: v.driverRef, Purpose: v.purpose,
		AppointmentID: v.appointmentID, State: v.state, Reason: v.reason, CarrierFlag: v.carrierFlag, TrailersIn: shared.Strings(v.trailersIn),
		TrailersOut: shared.Strings(v.trailersOut), ArrivedAt: v.arrivedAt, DepartedAt: v.departedAt, Version: v.version,
	}
}

// Restore rebuilds a persisted visit, validating that state, reason and
// departure facts agree.
func Restore(s Snapshot) (*Visit, error) {
	if err := validateParty(s.ID, s.SiteID, s.Carrier, s.SCAC, s.Plate, s.DriverRef, s.ArrivedAt); err != nil {
		return nil, err
	}
	if err := validateState(s); err != nil {
		return nil, err
	}
	if s.Version < 1 {
		return nil, shared.Invalid("version must be at least 1")
	}
	v := newVisit(s.ID, s.SiteID, s.Carrier, s.SCAC, s.Plate, s.DriverRef, s.Purpose, s.AppointmentID, s.ArrivedAt)
	v.state, v.reason, v.carrierFlag, v.version = s.State, s.Reason, s.CarrierFlag, s.Version
	v.trailersIn, v.trailersOut = shared.Strings(s.TrailersIn), shared.Strings(s.TrailersOut)
	if !s.DepartedAt.IsZero() {
		v.departedAt = s.DepartedAt.UTC()
	}
	return v, nil
}

func validateState(s Snapshot) error {
	if !s.CarrierFlag.Valid() || (s.State == Rejected && s.CarrierFlag != "") {
		return shared.Invalid("carrier flag %q is not valid on a %s visit", s.CarrierFlag, s.State)
	}
	switch s.State {
	case CheckedIn:
		if !s.Purpose.Valid() || s.Reason != "" || !s.DepartedAt.IsZero() || len(s.TrailersOut) > 0 {
			return shared.Invalid("a CHECKED_IN visit has a purpose, no reason and has not left")
		}
	case CheckedOut:
		if !s.Purpose.Valid() || s.Reason != "" || s.DepartedAt.IsZero() {
			return shared.Invalid("a CHECKED_OUT visit has a purpose, no reason and a departure time")
		}
	case Rejected:
		return validateRejected(s)
	default:
		return shared.Invalid("state %q is not valid", s.State)
	}
	return nil
}

func validateRejected(s Snapshot) error {
	if !s.Reason.Valid() || s.Purpose != "" || !s.DepartedAt.IsZero() || len(s.TrailersIn) > 0 || len(s.TrailersOut) > 0 {
		return shared.Invalid("a REJECTED visit has a reason and no purpose, trailers or departure")
	}
	return nil
}
