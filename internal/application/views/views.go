// Package views holds the read DTOs the use cases return and the local-copy
// facts they read. The JSON tags are the camelCase REST contract of
// apis/openapi.yaml, so the inbound adapters encode them as they are.
package views

import (
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// Visit is a YardVisit as the API shows it.
type Visit struct {
	VisitID         string         `json:"visitId"`
	SiteID          string         `json:"siteId"`
	Carrier         string         `json:"carrier"`
	CarrierSCAC     *string        `json:"carrierScac"`
	TractorPlate    string         `json:"tractorPlate"`
	DriverRef       *string        `json:"driverRef"`
	Purpose         shared.Purpose `json:"purpose"`
	AppointmentID   *string        `json:"appointmentId"`
	State           string         `json:"state"`
	RejectionReason *string        `json:"rejectionReason"`
	TrailersIn      []string       `json:"trailersIn"`
	TrailersOut     []string       `json:"trailersOut"`
	ArrivedAt       time.Time      `json:"arrivedAt"`
	DepartedAt      *time.Time     `json:"departedAt"`
	Version         int64          `json:"version"`
}

// Seal is the seal of a trailer.
type Seal struct {
	Number *string `json:"number"`
	State  string  `json:"state"`
}

// Trailer is a Trailer as the API shows it.
type Trailer struct {
	TrailerID     string             `json:"trailerId"`
	SiteID        string             `json:"siteId"`
	UnitNumber    string             `json:"unitNumber"`
	CarrierSCAC   *string            `json:"carrierScac"`
	Kind          shared.TrailerKind `json:"kind"`
	LoadState     shared.LoadState   `json:"loadState"`
	State         string             `json:"state"`
	Location      *shared.Location   `json:"location"`
	DoorWork      *string            `json:"doorWork"`
	Seal          Seal               `json:"seal"`
	Hold          *string            `json:"hold"`
	ASNNumbers    []string           `json:"asnNumbers"`
	OutboundRef   *string            `json:"outboundRef"`
	VisitID       *string            `json:"visitId"`
	AppointmentID *string            `json:"appointmentId"`
	ArrivedAt     time.Time          `json:"arrivedAt"`
	DepartedAt    *time.Time         `json:"departedAt"`
	OpenMoveID    *string            `json:"openMoveId"`
	Version       int64              `json:"version"`
}

// Priority is the claim order of a move.
type Priority struct {
	Band  int        `json:"band"`
	DueBy *time.Time `json:"dueBy,omitempty"`
}

// Move is a YardMove as the API shows it.
type Move struct {
	MoveID         string           `json:"moveId"`
	SiteID         string           `json:"siteId"`
	TrailerID      string           `json:"trailerId"`
	AppointmentID  *string          `json:"appointmentId"`
	Performer      string           `json:"performer"`
	From           *shared.Location `json:"from"`
	To             shared.Location  `json:"to"`
	Reason         string           `json:"reason"`
	Priority       Priority         `json:"priority"`
	State          string           `json:"state"`
	HostlerRef     *string          `json:"hostlerRef"`
	LeaseExpiresAt *time.Time       `json:"leaseExpiresAt"`
	ActualLocation *shared.Location `json:"actualLocation"`
	ClaimedAt      *time.Time       `json:"claimedAt"`
	CompletedAt    *time.Time       `json:"completedAt"`
	RequestedAt    time.Time        `json:"requestedAt"`
	Version        int64            `json:"version"`
}

// CheckIn is the answer of CheckInVisit.
type CheckIn struct {
	Visit    Visit     `json:"visit"`
	Trailers []Trailer `json:"trailers"`
	Moves    []Move    `json:"moves"`
}

// ObservationView is one observation of a yard check.
type ObservationView struct {
	LocationCode string  `json:"locationCode"`
	UnitNumber   *string `json:"unitNumber"`
}

// DiscrepancyView is one discrepancy of a closed yard check.
type DiscrepancyView struct {
	Kind         string  `json:"kind"`
	LocationCode string  `json:"locationCode"`
	TrailerID    *string `json:"trailerId"`
	UnitNumber   *string `json:"unitNumber"`
}

// YardCheck is a YardCheck as the API shows it.
type YardCheck struct {
	YardCheckID   string            `json:"yardCheckId"`
	SiteID        string            `json:"siteId"`
	State         string            `json:"state"`
	OpenedAt      time.Time         `json:"openedAt"`
	ClosedAt      *time.Time        `json:"closedAt"`
	Observations  []ObservationView `json:"observations"`
	Discrepancies []DiscrepancyView `json:"discrepancies"`
	Version       int64             `json:"version"`
}

// YardPolicy is a YardPolicy as the API shows it.
type YardPolicy struct {
	SiteID                string           `json:"siteId"`
	RequireAppointment    bool             `json:"requireAppointment"`
	HoldingSpotCodes      []string         `json:"holdingSpotCodes"`
	ParkingZoneIDs        []string         `json:"parkingZoneIds"`
	DwellThresholdMinutes map[string]int64 `json:"dwellThresholdMinutes"`
	LeaseSeconds          int              `json:"leaseSeconds"`
	SealMismatchAction    string           `json:"sealMismatchAction"`
	FreeTimeMinutes       *int             `json:"freeTimeMinutes"`
	Version               int64            `json:"version"`
	Declared              bool             `json:"declared"`
}

// BoardPlace is one spot or door of the yard board.
type BoardPlace struct {
	Code     string              `json:"code"`
	Kind     shared.LocationKind `json:"kind"`
	DockFlow *string             `json:"dockFlow"`
	Closed   bool                `json:"closed"`
	Trailer  *Trailer            `json:"trailer"`
}

// YardBoard is the live spot and door map of a site.
type YardBoard struct {
	SiteID    string       `json:"siteId"`
	AsOf      time.Time    `json:"asOf"`
	Spots     []BoardPlace `json:"spots"`
	Doors     []BoardPlace `json:"doors"`
	InMove    []Trailer    `json:"inMove"`
	OpenMoves []Move       `json:"openMoves"`
}

// Sweep is the answer of a sweep.
type Sweep struct {
	SweptAt time.Time `json:"sweptAt"`
	Count   int       `json:"count"`
}

// Page is one keyset page; Next is the position of its last row, nil on the
// last page.
type Page[T any] struct {
	Items []T
	Next  *Key
}

// Key is the keyset position of a list: the sort instant and the id of the
// last row of the previous page.
type Key struct {
	At time.Time
	ID string
}

// VisitFilter narrows ListVisits.
type VisitFilter struct{ SiteID, State, TractorPlate string }

// TrailerFilter narrows ListTrailers. An empty State means "not DEPARTED".
type TrailerFilter struct{ SiteID, State, Kind, LocationCode, UnitNumber, VisitID string }

// MoveFilter narrows ListMoves.
type MoveFilter struct{ SiteID, State, TrailerID, HostlerRef string }

// LocationInfo is the local copy of one facility-layout slot.
type LocationInfo struct {
	Code     string
	ZoneID   string
	SiteCode string // "" until the zone is known
	Kind     shared.LocationKind
	DockFlow string
	Active   bool
}

// Appointment statuses of the local copy (ADR 0003).
const (
	ApptBooked    = "BOOKED"
	ApptCheckedIn = "CHECKED_IN"
	ApptUsed      = "USED"
	ApptCancelled = "CANCELLED"
	ApptNoShow    = "NO_SHOW"
)

// Appointment is the local copy of one dock-scheduling appointment.
type Appointment struct {
	ID               string
	SiteID           string
	Direction        string
	DoorCode         string
	CarrierSCAC      string
	WindowStart      time.Time
	WindowEnd        time.Time
	ASNNumbers       []string
	ShipmentRefs     []string
	ExpectedSeal     string
	ToleranceMinutes int
	Status           string
	Version          int64
}

// Closure is the local copy of one door closure.
type Closure struct {
	ID       string
	SiteID   string
	DoorCode string
	From, To time.Time
	Lifted   bool
}

// Receipt is the local copy of one inbound-receiving receipt.
type Receipt struct {
	ID            string
	ASNNumber     string
	AppointmentID string
	DoorCode      string
	ClosedAt      time.Time // zero while open
}

// IdempotencyRecord is the stored answer of one Idempotency-Key: what the
// first request with that key answered, so a replay can return it verbatim.
type IdempotencyRecord struct {
	Key         string
	Fingerprint string
	Status      int
	ContentType string
	Body        []byte
	CreatedAt   time.Time
}
