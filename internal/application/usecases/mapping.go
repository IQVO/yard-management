package usecases

import (
	"time"

	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardcheck"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

func ptr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func tptr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func strs(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func visitView(v *yardvisit.Visit) views.Visit {
	s := v.Snapshot()
	return views.Visit{
		VisitID: s.ID, SiteID: s.SiteID, Carrier: s.Carrier, CarrierSCAC: ptr(s.SCAC), TractorPlate: s.Plate, DriverRef: ptr(s.DriverRef),
		Purpose: s.Purpose, AppointmentID: ptr(s.AppointmentID), State: string(s.State), RejectionReason: ptr(string(s.Reason)), CarrierFlag: ptr(string(s.CarrierFlag)),
		TrailersIn: strs(s.TrailersIn), TrailersOut: strs(s.TrailersOut), ArrivedAt: s.ArrivedAt, DepartedAt: tptr(s.DepartedAt), Version: s.Version,
	}
}

func trailerView(t *trailer.Trailer, openMoveID string) views.Trailer {
	s := t.Snapshot()
	return views.Trailer{
		TrailerID: s.ID, SiteID: s.SiteID, UnitNumber: s.Number, CarrierSCAC: ptr(s.SCAC), Kind: s.Kind, LoadState: s.LoadState, State: string(s.State),
		Location: s.Location.Ptr(), DoorWork: ptr(string(s.DoorWork)), SecuredAt: tptr(s.SecuredAt), SecuredBy: ptr(string(s.SecuredBy)), SecurerRef: ptr(s.SecurerRef), HazmatClass: ptr(string(s.Classification.HazmatClass)), UNNumber: ptr(string(s.Classification.UNNumber)),
		RequiresTemperatureControl: s.Classification.RequiresTemperatureControl, Seal: views.Seal{Number: ptr(s.SealNumber), State: string(s.SealState), Class: ptr(string(s.SealClass))},
		UnitNumberScheme: ptr(string(s.UnitScheme)),
		Hold:             ptr(string(s.Hold)), ASNNumbers: strs(s.ASNNumbers), OutboundRef: ptr(s.OutboundRef), VisitID: ptr(s.VisitID),
		AppointmentID: ptr(s.AppointmentID), ArrivedAt: s.ArrivedAt, DepartedAt: tptr(s.DepartedAt), OpenMoveID: ptr(openMoveID), Version: s.Version,
	}
}

func moveView(m *yardmove.Move) views.Move {
	s := m.Snapshot()
	return views.Move{
		MoveID: s.ID, SiteID: s.SiteID, TrailerID: s.TrailerID, AppointmentID: ptr(s.AppointmentID), Performer: string(s.Performer),
		From: s.From.Ptr(), To: s.To, Reason: string(s.Reason), Priority: views.Priority{Band: s.Priority.Band, DueBy: tptr(s.Priority.DueBy)},
		State: string(s.State), HostlerRef: ptr(s.HostlerRef), LeaseExpiresAt: tptr(s.LeaseExpiresAt), ActualLocation: s.Actual.Ptr(),
		ClaimedAt: tptr(s.ClaimedAt), CompletedAt: tptr(s.CompletedAt), RequestedAt: s.RequestedAt, Version: s.Version,
	}
}

func checkView(c *yardcheck.Check) views.YardCheck {
	s := c.Snapshot()
	v := views.YardCheck{
		YardCheckID: s.ID, SiteID: s.SiteID, State: string(s.State), OpenedAt: s.OpenedAt, ClosedAt: tptr(s.ClosedAt), Version: s.Version,
		Observations: make([]views.ObservationView, 0, len(s.Observations)), Discrepancies: make([]views.DiscrepancyView, 0, len(s.Discrepancies)),
	}
	for _, o := range s.Observations {
		v.Observations = append(v.Observations, views.ObservationView{LocationCode: o.LocationCode, UnitNumber: ptr(o.UnitNumber)})
	}
	for _, d := range s.Discrepancies {
		v.Discrepancies = append(v.Discrepancies, views.DiscrepancyView{Kind: string(d.Kind), LocationCode: d.LocationCode, TrailerID: ptr(d.TrailerID), UnitNumber: ptr(d.UnitNumber)})
	}
	return v
}

func policyView(p *yardpolicy.Policy) views.YardPolicy {
	s := p.Snapshot()
	dwell := make(map[string]int64, len(s.DwellThresholdMinutes))
	for k, v := range s.DwellThresholdMinutes {
		dwell[string(k)] = v
	}
	var free *int
	if s.FreeTimeMinutes > 0 {
		free = &s.FreeTimeMinutes
	}
	return views.YardPolicy{
		SiteID: s.SiteID, RequireAppointment: s.RequireAppointment, HoldingSpotCodes: strs(s.HoldingSpotCodes), ParkingZoneIDs: strs(s.ParkingZoneIDs),
		DwellThresholdMinutes: dwell, LeaseSeconds: s.LeaseSeconds, SealMismatchAction: string(s.SealMismatchAction), FreeTimeMinutes: free,
		MinimumSealClass: ptr(string(s.MinimumSealClass)), RequireSecuringBeforeDoorWork: s.RequireSecuring, AllowHazmatOnUnclassifiedSpots: s.AllowHazmatOnUnclassified, RequiredInspections: rulesView(s.RequiredInspections),
		SuspendedCarrierAction: string(s.SuspendedCarrierAction), Version: s.Version, Declared: s.Declared,
	}
}
