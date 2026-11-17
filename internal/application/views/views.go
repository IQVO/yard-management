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
	// CarrierFlag is SUSPENDED when the gate admitted the visit of a carrier the
	// registry copy says is suspended (policy FLAG, ADR 0016); null otherwise.
	CarrierFlag *string    `json:"carrierFlag"`
	TrailersIn  []string   `json:"trailersIn"`
	TrailersOut []string   `json:"trailersOut"`
	ArrivedAt   time.Time  `json:"arrivedAt"`
	DepartedAt  *time.Time `json:"departedAt"`
	Version     int64      `json:"version"`
}

// Seal is the seal of a trailer.
type Seal struct {
	Number *string `json:"number"`
	State  string  `json:"state"`
	// Class is the ISO 17712 class (I, S or H), null when none was declared.
	Class *string `json:"class"`
}

// Trailer is a Trailer as the API shows it.
type Trailer struct {
	TrailerID  string `json:"trailerId"`
	SiteID     string `json:"siteId"`
	UnitNumber string `json:"unitNumber"`
	// UnitNumberScheme is ISO6346 when the number was validated as an ISO 6346
	// container number, null for a free-form one.
	UnitNumberScheme *string            `json:"unitNumberScheme"`
	CarrierSCAC      *string            `json:"carrierScac"`
	Kind             shared.TrailerKind `json:"kind"`
	LoadState        shared.LoadState   `json:"loadState"`
	State            string             `json:"state"`
	Location         *shared.Location   `json:"location"`
	DoorWork         *string            `json:"doorWork"`
	// SecuredAt, SecuredBy (RESTRAINT or WHEEL_CHOCKS) and SecurerRef are the
	// dock safety interlock (ADR 0013); null while the trailer is not secured.
	SecuredAt  *time.Time `json:"securedAt"`
	SecuredBy  *string    `json:"securedBy"`
	SecurerRef *string    `json:"securerRef"`
	// HazmatClass, UNNumber and RequiresTemperatureControl are what the trailer
	// carries (ADR 0014); null (false) when it declares none.
	HazmatClass                *string    `json:"hazmatClass"`
	UNNumber                   *string    `json:"unNumber"`
	RequiresTemperatureControl bool       `json:"requiresTemperatureControl"`
	Seal                       Seal       `json:"seal"`
	Hold                       *string    `json:"hold"`
	ASNNumbers                 []string   `json:"asnNumbers"`
	OutboundRef                *string    `json:"outboundRef"`
	VisitID                    *string    `json:"visitId"`
	AppointmentID              *string    `json:"appointmentId"`
	ArrivedAt                  time.Time  `json:"arrivedAt"`
	DepartedAt                 *time.Time `json:"departedAt"`
	OpenMoveID                 *string    `json:"openMoveId"`
	Version                    int64      `json:"version"`
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
	// MinimumSealClass is the lowest ISO 17712 class a seal may have; null =
	// no minimum.
	MinimumSealClass *string `json:"minimumSealClass"`
	// RequireSecuringBeforeDoorWork: door work starts only on a secured
	// trailer (ADR 0013); false = the behaviour before the interlock.
	RequireSecuringBeforeDoorWork bool `json:"requireSecuringBeforeDoorWork"`
	// AllowHazmatOnUnclassifiedSpots: a hazmat trailer may take a spot with no
	// spotKind at a site with no HAZMAT spot (ADR 0014); false = refused.
	AllowHazmatOnUnclassifiedSpots bool `json:"allowHazmatOnUnclassifiedSpots"`
	// RequiredInspections are the inspections a trailer needs before it docks,
	// loads or leaves (ADR 0015); empty = none, the behaviour before them.
	RequiredInspections []InspectionRule `json:"requiredInspections"`
	// SuspendedCarrierAction is what the gate does with a visit of a carrier
	// the registry copy says is SUSPENDED: NONE (the default and the behaviour
	// before the rule), FLAG or REJECT (ADR 0016).
	SuspendedCarrierAction string `json:"suspendedCarrierAction"`
	Version                int64  `json:"version"`
	Declared               bool   `json:"declared"`
}

// InspectionRule is one entry of YardPolicy.requiredInspections.
type InspectionRule struct {
	Gate       string `json:"gate"`
	TemplateID string `json:"templateId"`
	// AppliesTo narrows the rule; an empty list means every purpose or kind.
	AppliesTo InspectionAppliesTo `json:"appliesTo"`
}

// InspectionAppliesTo is the purpose and trailer kind filter of a rule.
type InspectionAppliesTo struct {
	Purposes     []string `json:"purposes"`
	TrailerKinds []string `json:"trailerKinds"`
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
	// SpotKind is the facility-layout spotKind of a yard spot; empty when the
	// slot was registered without one (ADR 0014).
	SpotKind shared.SpotKind
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

// Carrier is the local copy of one dock-scheduling carrier (ADR 0016). USDOT, MC and
// Reason are "" when the producer sent null.
type Carrier struct {
	SCAC      string
	Name      string
	USDOT     string
	MC        string
	Status    string
	Reason    string
	Version   int64
	UpdatedAt time.Time
}

// The carrier standings of the copy, the dock-scheduling carrier status values.
const (
	CarrierActive    = "ACTIVE"
	CarrierSuspended = "SUSPENDED"
)

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
