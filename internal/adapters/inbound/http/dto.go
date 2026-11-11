package http

import (
	"time"

	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
)

// The request bodies of apis/openapi.yaml. A string a caller leaves out is
// "" and the domain rules refuse it where it is required (422 validation).

type trailerInRequest struct {
	UnitNumber  string `json:"unitNumber"`
	CarrierSCAC string `json:"carrierScac"`
	Kind        string `json:"kind"`
	LoadState   string `json:"loadState"`
	SealNumber  string `json:"sealNumber"`
	// UnitNumberScheme (ISO6346) and SealClass (I, S or H) are optional.
	UnitNumberScheme string `json:"unitNumberScheme"`
	SealClass        string `json:"sealClass"`
}

type checkInRequest struct {
	SiteID        string             `json:"siteId"`
	Carrier       string             `json:"carrier"`
	CarrierSCAC   string             `json:"carrierScac"`
	TractorPlate  string             `json:"tractorPlate"`
	DriverRef     string             `json:"driverRef"`
	Purpose       string             `json:"purpose"`
	AppointmentID string             `json:"appointmentId"`
	Trailers      []trailerInRequest `json:"trailers"`
}

func (q checkInRequest) input() usecases.CheckInInput {
	in := usecases.CheckInInput{
		SiteID: q.SiteID, Carrier: q.Carrier, CarrierSCAC: q.CarrierSCAC, TractorPlate: q.TractorPlate, DriverRef: q.DriverRef,
		Purpose: q.Purpose, AppointmentID: q.AppointmentID, Trailers: make([]usecases.TrailerInput, 0, len(q.Trailers)),
	}
	for _, t := range q.Trailers {
		in.Trailers = append(in.Trailers, usecases.TrailerInput{
			UnitNumber: t.UnitNumber, CarrierSCAC: t.CarrierSCAC, Kind: t.Kind, LoadState: t.LoadState, SealNumber: t.SealNumber,
			UnitNumberScheme: t.UnitNumberScheme, SealClass: t.SealClass,
		})
	}
	return in
}

type rejectRequest struct {
	SiteID        string `json:"siteId"`
	Carrier       string `json:"carrier"`
	CarrierSCAC   string `json:"carrierScac"`
	TractorPlate  string `json:"tractorPlate"`
	DriverRef     string `json:"driverRef"`
	AppointmentID string `json:"appointmentId"`
	Reason        string `json:"reason"`
}

func (q rejectRequest) input() usecases.RejectInput {
	return usecases.RejectInput{
		SiteID: q.SiteID, Carrier: q.Carrier, CarrierSCAC: q.CarrierSCAC, TractorPlate: q.TractorPlate, DriverRef: q.DriverRef,
		AppointmentID: q.AppointmentID, Reason: q.Reason,
	}
}

type trailerOutRequest struct {
	TrailerID string `json:"trailerId"`
}

// checkOutRequest keeps trailersOut a pointer so that an absent list (a
// malformed request) is told apart from an empty one (a drop or a bobtail).
type checkOutRequest struct {
	TrailersOut *[]trailerOutRequest `json:"trailersOut"`
}

type locationRequest struct {
	Kind string `json:"kind"`
	Code string `json:"code"`
}

func (l locationRequest) location() shared.Location {
	return shared.Location{Kind: shared.LocationKind(l.Kind), Code: l.Code}
}

type requestMoveRequest struct {
	TrailerID    string          `json:"trailerId"`
	To           locationRequest `json:"to"`
	Reason       string          `json:"reason"`
	PriorityBand int             `json:"priorityBand"`
	DueBy        *time.Time      `json:"dueBy"`
}

func (q requestMoveRequest) input() usecases.RequestMoveInput {
	in := usecases.RequestMoveInput{TrailerID: q.TrailerID, To: q.To.location(), Reason: yardmove.Reason(q.Reason), Band: q.PriorityBand}
	if q.DueBy != nil {
		in.DueBy = q.DueBy.UTC()
	}
	return in
}

type claimRequest struct {
	SiteID     string `json:"siteId"`
	HostlerRef string `json:"hostlerRef"`
}

type completeMoveRequest struct {
	HostlerRef     string          `json:"hostlerRef"`
	ActualLocation locationRequest `json:"actualLocation"`
	DeviationNote  string          `json:"deviationNote"`
}

func (q completeMoveRequest) input(moveID string) usecases.CompleteMoveInput {
	return usecases.CompleteMoveInput{MoveID: moveID, HostlerRef: q.HostlerRef, Actual: q.ActualLocation.location(), Note: q.DeviationNote}
}

type cancelRequest struct {
	Reason string `json:"reason"`
}

type doorWorkCompletionRequest struct {
	LoadState string `json:"loadState"`
}

type holdRequest struct {
	Reason string `json:"reason"`
}

func (q holdRequest) reason() trailer.HoldReason { return trailer.HoldReason(q.Reason) }

type sealRequest struct {
	State      string `json:"state"`
	SealNumber string `json:"sealNumber"`
	SealClass  string `json:"sealClass"`
}

// securingRequest is the body of POST /trailers/{trailerId}/securings:
// Method is RESTRAINT or WHEEL_CHOCKS, SecurerRef optional (ADR 0013).
type securingRequest struct {
	Method     string `json:"method"`
	SecurerRef string `json:"securerRef"`
}

type openYardCheckRequest struct {
	SiteID string `json:"siteId"`
}

type observationRequest struct {
	LocationCode string `json:"locationCode"`
	UnitNumber   string `json:"unitNumber"`
}

type declarePolicyRequest struct {
	RequireAppointment    bool             `json:"requireAppointment"`
	HoldingSpotCodes      []string         `json:"holdingSpotCodes"`
	ParkingZoneIDs        []string         `json:"parkingZoneIds"`
	DwellThresholdMinutes map[string]int64 `json:"dwellThresholdMinutes"`
	LeaseSeconds          int              `json:"leaseSeconds"`
	SealMismatchAction    string           `json:"sealMismatchAction"`
	FreeTimeMinutes       int              `json:"freeTimeMinutes"`
	MinimumSealClass      string           `json:"minimumSealClass"`
	// RequireSecuringBeforeDoorWork is optional and false when left out (ADR 0013).
	RequireSecuringBeforeDoorWork bool `json:"requireSecuringBeforeDoorWork"`
}

func (q declarePolicyRequest) input() yardpolicy.DeclareInput {
	dwell := make(map[shared.TrailerKind]int64, len(q.DwellThresholdMinutes))
	for kind, minutes := range q.DwellThresholdMinutes {
		dwell[shared.TrailerKind(kind)] = minutes
	}
	return yardpolicy.DeclareInput{
		RequireAppointment: q.RequireAppointment, HoldingSpotCodes: q.HoldingSpotCodes, ParkingZoneIDs: q.ParkingZoneIDs,
		DwellThresholdMinutes: dwell, LeaseSeconds: q.LeaseSeconds, SealMismatchAction: yardpolicy.SealAction(q.SealMismatchAction),
		FreeTimeMinutes: q.FreeTimeMinutes, MinimumSealClass: shared.SealClass(q.MinimumSealClass),
		RequireSecuringBeforeDoorWork: q.RequireSecuringBeforeDoorWork,
	}
}

type dwellSweepRequest struct {
	SiteID string `json:"siteId"`
}
