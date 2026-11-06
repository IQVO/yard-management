package trailer

import (
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// Snapshot is the persisted form of a Trailer: scalars only. Restore
// validates it.
type Snapshot struct {
	ID             string
	SiteID         string
	SCAC           string
	Number         string
	Kind           shared.TrailerKind
	LoadState      shared.LoadState
	ASNNumbers     []string
	OutboundRef    string
	Purpose        shared.Purpose
	SealNumber     string
	SealState      SealState
	Hold           HoldReason
	State          State
	Location       shared.Location
	DoorWork       DoorWork
	Origin         shared.Location
	OriginWork     DoorWork
	VisitID        string
	AppointmentID  string
	ArrivedAt      time.Time
	DepartedAt     time.Time
	DwellAlertedAt time.Time
	Version        int64
}

// Snapshot returns the persisted form (no events: a restored trailer has
// none pending).
func (t *Trailer) Snapshot() Snapshot {
	return Snapshot{
		ID: t.id, SiteID: t.site, SCAC: t.scac, Number: t.number, Kind: t.kind, LoadState: t.load,
		ASNNumbers: shared.Strings(t.asn), OutboundRef: t.outboundRef, Purpose: t.purpose,
		SealNumber: t.sealNumber, SealState: t.sealState, Hold: t.hold, State: t.state, Location: t.loc,
		DoorWork: t.work, Origin: t.origin, OriginWork: t.originWork, VisitID: t.visitID, AppointmentID: t.appointmentID,
		ArrivedAt: t.arrivedAt, DepartedAt: t.departedAt, DwellAlertedAt: t.dwellAlertedAt, Version: t.version,
	}
}

// Restore rebuilds a persisted trailer, validating every invariant the
// snapshot can show, so a corrupt row can never become a live aggregate.
func Restore(s Snapshot) (*Trailer, error) {
	if err := validateIdentity(s); err != nil {
		return nil, err
	}
	if err := validatePlace(s); err != nil {
		return nil, err
	}
	if s.SealState != SealNone && s.SealState != SealIntact && s.SealState != SealBroken {
		return nil, shared.Invalid("seal state %q is not valid", s.SealState)
	}
	if (s.SealState == SealNone) != (s.SealNumber == "") {
		return nil, shared.Invalid("a seal number is present exactly when the seal state is not NONE")
	}
	if s.Hold != "" && !s.Hold.Valid() {
		return nil, shared.Invalid("hold reason %q is not valid", s.Hold)
	}
	if s.Version < 1 || s.ArrivedAt.IsZero() {
		return nil, shared.Invalid("version and arrival time are required")
	}
	return &Trailer{
		id: s.ID, site: s.SiteID, scac: s.SCAC, number: s.Number, kind: s.Kind, load: s.LoadState,
		asn: shared.Strings(s.ASNNumbers), outboundRef: s.OutboundRef, purpose: s.Purpose,
		sealNumber: s.SealNumber, sealState: s.SealState, hold: s.Hold, state: s.State, loc: s.Location,
		work: s.DoorWork, origin: s.Origin, originWork: s.OriginWork, visitID: s.VisitID, appointmentID: s.AppointmentID,
		arrivedAt: s.ArrivedAt.UTC(), departedAt: s.DepartedAt.UTC(), dwellAlertedAt: s.DwellAlertedAt.UTC(), version: s.Version,
	}, nil
}

func validateIdentity(s Snapshot) error {
	if err := shared.Require("trailer id", s.ID, 64); err != nil {
		return err
	}
	if err := shared.Require("site id", s.SiteID, 64); err != nil {
		return err
	}
	if err := shared.Require("unit number", s.Number, 64); err != nil {
		return err
	}
	if err := shared.ValidateSCAC(s.SCAC); err != nil {
		return err
	}
	if !s.Kind.Valid() || !s.LoadState.Valid() || !s.Purpose.Valid() {
		return shared.Invalid("kind, load state and purpose must be valid enumeration values")
	}
	return nil
}

// validatePlace checks that state, location and door work agree: PARKED
// holds a spot, DOCKED a door with door work, IN_MOVE and DEPARTED neither.
func validatePlace(s Snapshot) error {
	switch s.State {
	case Parked:
		if s.Location.Kind != shared.Spot || s.Location.Code == "" || s.DoorWork != "" {
			return shared.Invalid("a PARKED trailer holds a spot and no door work")
		}
	case Docked:
		if s.Location.Kind != shared.Door || s.Location.Code == "" || !s.DoorWork.valid() {
			return shared.Invalid("a DOCKED trailer holds a door and has door work")
		}
	case InMove, Departed:
		if !s.Location.IsZero() || s.DoorWork != "" {
			return shared.Invalid("a %s trailer holds no place", s.State)
		}
	default:
		return shared.Invalid("state %q is not valid", s.State)
	}
	return validateOrigin(s)
}

func validateOrigin(s Snapshot) error {
	if s.Origin.IsZero() {
		return nil
	}
	if s.State != InMove || (s.Origin.Kind != shared.Spot && s.Origin.Kind != shared.Door) {
		return shared.Invalid("only an IN_MOVE trailer remembers a spot or door origin")
	}
	return nil
}

func (w DoorWork) valid() bool { return w == WorkWaiting || w == WorkInProgress || w == WorkDone }
