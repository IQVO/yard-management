package repocontract

import (
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardcheck"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

const fixtureSite = "WH1"

func spot(code string) shared.Location { return shared.Location{Kind: shared.Spot, Code: code} }
func door(code string) shared.Location { return shared.Location{Kind: shared.Door, Code: code} }

// NewVisit checks a LIVE_UNLOAD visit of plate in at site, at T0 + offset,
// bringing the given trailers (none: a PICKUP).
func NewVisit(t *testing.T, id, site, plate string, offset time.Duration, trailers ...yardvisit.TrailerRef) *yardvisit.Visit {
	t.Helper()
	purpose := shared.PurposeLiveUnload
	if len(trailers) == 0 {
		purpose = shared.PurposePickup
	}
	v, err := yardvisit.CheckIn(yardvisit.CheckInInput{
		ID: id, SiteID: site, Carrier: "Acme Freight", SCAC: "ACME", TractorPlate: plate, DriverRef: "drv-1",
		Purpose: purpose, Trailers: trailers, At: T0.Add(offset),
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Ref is a trailer reference of a visit.
func Ref(id, unit string) yardvisit.TrailerRef {
	return yardvisit.TrailerRef{ID: id, UnitNumber: unit, SCAC: "ACME", Kind: shared.KindDryVan, Load: shared.LoadLoaded}
}

// Rejected records a REJECTED visit.
func Rejected(t *testing.T, id, site, plate string) *yardvisit.Visit {
	t.Helper()
	v, err := yardvisit.RejectAtGate(yardvisit.RejectInput{
		ID: id, SiteID: site, Carrier: "Acme Freight", SCAC: "ACME", TractorPlate: plate, AppointmentID: "appt-1",
		Reason: yardvisit.ReasonSiteRequest, At: T0,
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Arrived creates a trailer IN_MOVE at version 1: the way the gate does.
func Arrived(t *testing.T, id, site, unit string, offset time.Duration) *trailer.Trailer {
	t.Helper()
	tr, err := trailer.RecordArrival(trailer.ArrivalInput{
		ID: id, SiteID: site, SCAC: "ACME", Number: unit, Kind: shared.KindDryVan, Load: shared.LoadLoaded,
		ASNNumbers: []string{"ASN-1", "ASN-2"}, OutboundRef: "SHP-1", Purpose: shared.PurposeLiveUnload, SealNumber: "SL-1",
		VisitID: "visit-1", AppointmentID: "appt-1", At: T0.Add(offset),
	})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func placement(tr *trailer.Trailer, loc shared.Location) trailer.Placement {
	return trailer.Placement{Target: loc, Slot: trailer.Slot{Location: loc, Site: tr.SiteID(), Active: true, DockFlow: trailer.FlowBoth}}
}

// ArriveAt puts an IN_MOVE trailer at loc (version +1).
func ArriveAt(t *testing.T, tr *trailer.Trailer, loc shared.Location, offset time.Duration) {
	t.Helper()
	if err := tr.ArriveAt(placement(tr, loc), T0.Add(offset)); err != nil {
		t.Fatal(err)
	}
}

// Depart takes a PARKED trailer off the site (version +1).
func Depart(t *testing.T, tr *trailer.Trailer, offset time.Duration) {
	t.Helper()
	if err := tr.Depart("visit-2", T0.Add(offset)); err != nil {
		t.Fatal(err)
	}
}

// NewMove requests a HOSTLER move of trailerID to loc at T0 + offset.
func NewMove(t *testing.T, id, trailerID string, to shared.Location, band int, dueBy time.Time, offset time.Duration) *yardmove.Move {
	t.Helper()
	reason := yardmove.Relocate
	if to.Kind == shared.Door {
		reason = yardmove.ToDoor
	}
	m, err := yardmove.Request(yardmove.RequestInput{
		ID: id, SiteID: fixtureSite, TrailerID: trailerID, AppointmentID: "appt-1", Performer: yardmove.Hostler, To: to, Reason: reason,
		Priority: yardmove.Priority{Band: band, DueBy: dueBy}, At: T0.Add(offset),
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// GateMove requests the first move of a trailer from the gate: created
// CLAIMED by the visiting driver.
func GateMove(t *testing.T, id, trailerID string, to shared.Location) *yardmove.Move {
	t.Helper()
	m, err := yardmove.Request(yardmove.RequestInput{
		ID: id, SiteID: fixtureSite, TrailerID: trailerID, Performer: yardmove.VisitingDriver, To: to, Reason: yardmove.ToHolding,
		Priority: yardmove.Priority{Band: yardmove.DefaultBand}, At: T0,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// Claim leases a move to hostler for five minutes from T0 + offset.
func Claim(t *testing.T, m *yardmove.Move, hostler string, offset time.Duration) {
	t.Helper()
	if err := m.Claim(hostler, T0.Add(offset), 5*time.Minute); err != nil {
		t.Fatal(err)
	}
}

// NewCheck opens a yard check of site at T0.
func NewCheck(t *testing.T, id, site string) *yardcheck.Check {
	t.Helper()
	c, err := yardcheck.OpenYardCheck(id, site, T0)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Declared returns the policy of site with a first declaration applied (version 2).
func Declared(t *testing.T, site string) *yardpolicy.Policy {
	t.Helper()
	p, err := yardpolicy.Default(site)
	if err != nil {
		t.Fatal(err)
	}
	err = p.Declare(yardpolicy.DeclareInput{
		RequireAppointment: true, HoldingSpotCodes: []string{"WH1-H1", "WH1-H2"}, ParkingZoneIDs: []string{"z-yard"},
		DwellThresholdMinutes: map[shared.TrailerKind]int64{shared.KindDryVan: 240, shared.KindReefer: 60},
		LeaseSeconds:          120, SealMismatchAction: yardpolicy.Reject, FreeTimeMinutes: 90, MinimumSealClass: shared.SealClassHighSecurity,
		RequireSecuringBeforeDoorWork: true, AllowHazmatOnUnclassifiedSpots: true,
	}, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Spot is the yard spot location with the given code.
func Spot(code string) shared.Location { return spot(code) }

// Door is the dock door location with the given code.
func Door(code string) shared.Location { return door(code) }

// Container creates a CONTAINER trailer IN_MOVE at version 1 carrying an ISO
// 6346 unit number and a class H seal (ADR 0012).
func Container(t *testing.T, id, site, unit string, offset time.Duration) *trailer.Trailer {
	t.Helper()
	tr, err := trailer.RecordArrival(trailer.ArrivalInput{
		ID: id, SiteID: site, SCAC: "ACME", Number: unit, Kind: shared.KindContainer, Load: shared.LoadLoaded,
		Purpose: shared.PurposeLiveUnload, SealNumber: "SL-1", SealClass: shared.SealClassHighSecurity,
		VisitID: "visit-1", At: T0.Add(offset),
	})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}
