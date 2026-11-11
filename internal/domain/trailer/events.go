package trailer

import (
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// The events of the Trailer aggregate (ADR 0004). The json tags are the
// snake_case `data` of the CloudEvents contract; the embedded Header is not
// part of it. Nullable fields are pointers and serialise as null.

// TrailerParked: the trailer came to rest on a yard spot (analytics only).
type TrailerParked struct {
	shared.Header `json:"-"`
	TrailerID     string `json:"trailer_id"`
	SiteID        string `json:"site_id"`
	SpotCode      string `json:"spot_code"`
	Version       int64  `json:"version"`
}

// TrailerDocked: the trailer is at a door (integration).
type TrailerDocked struct {
	shared.Header `json:"-"`
	TrailerID     string   `json:"trailer_id"`
	SiteID        string   `json:"site_id"`
	DoorCode      string   `json:"door_code"`
	AppointmentID *string  `json:"appointment_id"`
	ASNNumbers    []string `json:"asn_numbers"`
	VisitID       *string  `json:"visit_id"`
	DockedAt      string   `json:"docked_at"`
	Version       int64    `json:"version"`
	// SealState and SealClass are additive (ADR 0012): null when the trailer
	// has no seal, SealClass also when no class was declared.
	SealState *string `json:"seal_state"`
	SealClass *string `json:"seal_class"`
	// HazmatClass, UNNumber and RequiresTemperatureControl are additive
	// (ADR 0014): null (false) when the trailer declares none.
	HazmatClass                *string `json:"hazmat_class"`
	UNNumber                   *string `json:"un_number"`
	RequiresTemperatureControl bool    `json:"requires_temperature_control"`
}

// TrailerUndocked: the trailer left a door (integration).
type TrailerUndocked struct {
	shared.Header `json:"-"`
	TrailerID     string  `json:"trailer_id"`
	SiteID        string  `json:"site_id"`
	DoorCode      string  `json:"door_code"`
	AppointmentID *string `json:"appointment_id"`
	UndockedAt    string  `json:"undocked_at"`
	Version       int64   `json:"version"`
}

// DoorWorkStarted: unloading or loading began (analytics only).
type DoorWorkStarted struct {
	shared.Header `json:"-"`
	TrailerID     string  `json:"trailer_id"`
	SiteID        string  `json:"site_id"`
	DoorCode      string  `json:"door_code"`
	AppointmentID *string `json:"appointment_id"`
	StartedAt     string  `json:"started_at"`
	Version       int64   `json:"version"`
}

// DoorWorkCompleted: door work finished (analytics only).
type DoorWorkCompleted struct {
	shared.Header `json:"-"`
	TrailerID     string           `json:"trailer_id"`
	SiteID        string           `json:"site_id"`
	DoorCode      string           `json:"door_code"`
	AppointmentID *string          `json:"appointment_id"`
	CompletedAt   string           `json:"completed_at"`
	LoadState     shared.LoadState `json:"load_state"`
	Version       int64            `json:"version"`
}

// TrailerDeparted: the trailer left the site (analytics only).
type TrailerDeparted struct {
	shared.Header `json:"-"`
	TrailerID     string           `json:"trailer_id"`
	SiteID        string           `json:"site_id"`
	VisitID       *string          `json:"visit_id"`
	DepartedAt    string           `json:"departed_at"`
	DwellMinutes  int64            `json:"dwell_minutes"`
	LoadState     shared.LoadState `json:"load_state"`
	SealNumber    *string          `json:"seal_number"`
	Version       int64            `json:"version"`
	// SealState and SealClass are additive (ADR 0012), null as on
	// TrailerDocked.
	SealState *string `json:"seal_state"`
	SealClass *string `json:"seal_class"`
}

// TrailerSecured: a docked trailer was secured by a vehicle restraint or
// wheel chocks (integration, ADR 0013).
type TrailerSecured struct {
	shared.Header `json:"-"`
	TrailerID     string         `json:"trailer_id"`
	SiteID        string         `json:"site_id"`
	DoorCode      string         `json:"door_code"`
	Method        SecuringMethod `json:"method"`
	SecuredAt     string         `json:"secured_at"`
	Version       int64          `json:"version"`
}

// TrailerSecuringReleased: the restraint was released or the chocks removed
// (integration, ADR 0013).
type TrailerSecuringReleased struct {
	shared.Header `json:"-"`
	TrailerID     string `json:"trailer_id"`
	SiteID        string `json:"site_id"`
	DoorCode      string `json:"door_code"`
	ReleasedAt    string `json:"released_at"`
	Version       int64  `json:"version"`
}

// SealExceptionRaised: the seal found at the gate differs from the expected
// one (integration). TrailerID is null when the visit was rejected and no
// trailer was persisted.
type SealExceptionRaised struct {
	shared.Header      `json:"-"`
	TrailerID          *string `json:"trailer_id"`
	SiteID             string  `json:"site_id"`
	AppointmentID      *string `json:"appointment_id"`
	ExpectedSealNumber string  `json:"expected_seal_number"`
	FoundSealNumber    string  `json:"found_seal_number"`
	RaisedAt           string  `json:"raised_at"`
}

// NewSealException builds the event for a visit rejected at the gate, where
// there is no trailer aggregate: the subject is the visit id and trailer_id
// is null.
func NewSealException(visitID, siteID, appointmentID, expected, found string, at time.Time) SealExceptionRaised {
	return SealExceptionRaised{
		Header: shared.NewHeader(Entity, "SealExceptionRaised", visitID, at), SiteID: siteID, AppointmentID: shared.Str(appointmentID),
		ExpectedSealNumber: expected, FoundSealNumber: found, RaisedAt: shared.Stamp(at),
	}
}

// TrailerHeld: a hold was placed (analytics only).
type TrailerHeld struct {
	shared.Header `json:"-"`
	TrailerID     string     `json:"trailer_id"`
	SiteID        string     `json:"site_id"`
	Reason        HoldReason `json:"reason"`
	Version       int64      `json:"version"`
}

// TrailerHoldReleased: a hold was released (analytics only).
type TrailerHoldReleased struct {
	shared.Header `json:"-"`
	TrailerID     string `json:"trailer_id"`
	SiteID        string `json:"site_id"`
	Version       int64  `json:"version"`
}

// TrailerDwellExceeded: the dwell crossed the kind's threshold
// (integration, no version).
type TrailerDwellExceeded struct {
	shared.Header    `json:"-"`
	TrailerID        string             `json:"trailer_id"`
	SiteID           string             `json:"site_id"`
	Kind             shared.TrailerKind `json:"kind"`
	DwellMinutes     int64              `json:"dwell_minutes"`
	ThresholdMinutes int64              `json:"threshold_minutes"`
	LocationCode     *string            `json:"location_code"`
}
