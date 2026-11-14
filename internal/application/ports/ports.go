// Package ports declares the application's OUT ports: the repository,
// local-copy and driven interfaces the use cases rely on and the outbound
// adapters (Postgres, the transactional outbox, the in-memory doubles)
// implement. The package holds interfaces and the typed misses only; every
// read DTO lives one package over, in internal/application/views.
package ports

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/inspection"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardcheck"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

// ErrNotFound is the typed miss every Find port returns.
var ErrNotFound = errors.New("ports: not found")

// ErrConcurrentModification is the typed optimistic-concurrency conflict
// every repo Save can return: the stored row's version is not the version
// before the aggregate's change (another writer got there first). Save
// INSERTs an aggregate at version 1 and UPDATEs a later version guarded by
// version - 1. The HTTP adapter maps it to 409.
var ErrConcurrentModification = errors.New("ports: concurrent modification")

// VisitRepo persists YardVisit aggregates. Inside a UnitOfWork it joins the
// transaction carried in ctx. Save maps the unique index of one open visit
// per plate and site to yardvisit.ErrVisitAlreadyOpen.
type VisitRepo interface {
	Save(ctx context.Context, v *yardvisit.Visit) error
	FindByID(ctx context.Context, id string) (*yardvisit.Visit, error)
	// FindOpenByPlate returns the CHECKED_IN visit of the plate at the site, or ErrNotFound.
	FindOpenByPlate(ctx context.Context, siteID, plate string) (*yardvisit.Visit, error)
	// List returns at most limit visits, newest first, strictly after the keyset position (arrival, id).
	List(ctx context.Context, f views.VisitFilter, after *views.Key, limit int) ([]*yardvisit.Visit, error)
}

// TrailerRepo persists Trailer aggregates. Save maps the partial unique
// indexes to trailer.ErrLocationOccupied (one trailer per spot or door) and
// trailer.ErrTrailerAlreadyOnSite (one trailer per unit on site).
type TrailerRepo interface {
	Save(ctx context.Context, t *trailer.Trailer) error
	FindByID(ctx context.Context, id string) (*trailer.Trailer, error)
	// FindAt returns the trailer PARKED or DOCKED at the location, or ErrNotFound.
	FindAt(ctx context.Context, loc shared.Location) (*trailer.Trailer, error)
	// FindOnSiteByUnit returns the not-DEPARTED trailer of the unit at the site, or ErrNotFound.
	FindOnSiteByUnit(ctx context.Context, siteID, scac, number string) (*trailer.Trailer, error)
	// FindByAppointment returns the not-DEPARTED trailer that came for the appointment, or ErrNotFound.
	FindByAppointment(ctx context.Context, appointmentID string) (*trailer.Trailer, error)
	// ListOnSite returns the not-DEPARTED trailers of the site (all sites when siteID is empty).
	ListOnSite(ctx context.Context, siteID string) ([]*trailer.Trailer, error)
	// List returns at most limit trailers by arrival, strictly after the keyset position.
	List(ctx context.Context, f views.TrailerFilter, after *views.Key, limit int) ([]*trailer.Trailer, error)
}

// MoveRepo persists YardMove aggregates. Save maps the unique indexes to
// yardmove.ErrTrailerHasOpenMove, yardmove.ErrDestinationReserved and
// yardmove.ErrHostlerBusy.
type MoveRepo interface {
	Save(ctx context.Context, m *yardmove.Move) error
	FindByID(ctx context.Context, id string) (*yardmove.Move, error)
	// FindOpenByTrailer returns the REQUESTED or CLAIMED move of the trailer, or ErrNotFound.
	FindOpenByTrailer(ctx context.Context, trailerID string) (*yardmove.Move, error)
	// FindOpenByDestination returns the open move reserving the location, or ErrNotFound.
	FindOpenByDestination(ctx context.Context, loc shared.Location) (*yardmove.Move, error)
	// FindClaimedByHostler returns the CLAIMED move the hostler holds at the site, or ErrNotFound.
	FindClaimedByHostler(ctx context.Context, siteID, hostlerRef string) (*yardmove.Move, error)
	// ClaimNext returns, and row-locks, the REQUESTED HOSTLER move of the site that comes first by priority
	// band, due-by (none last) and request time, or ErrNotFound.
	ClaimNext(ctx context.Context, siteID string) (*yardmove.Move, error)
	// ListExpiredClaims returns up to limit CLAIMED HOSTLER moves whose lease ended at or before now.
	ListExpiredClaims(ctx context.Context, now time.Time, limit int) ([]*yardmove.Move, error)
	// ListOpenByAppointment returns the open moves that serve the appointment.
	ListOpenByAppointment(ctx context.Context, appointmentID string) ([]*yardmove.Move, error)
	// ListOpen returns the open moves of the site.
	ListOpen(ctx context.Context, siteID string) ([]*yardmove.Move, error)
	// List returns at most limit moves, oldest first, strictly after the keyset position (request time, id).
	List(ctx context.Context, f views.MoveFilter, after *views.Key, limit int) ([]*yardmove.Move, error)
}

// CheckRepo persists YardCheck aggregates. Save maps the unique index of one
// open check per site to yardcheck.ErrYardCheckOpen.
type CheckRepo interface {
	Save(ctx context.Context, c *yardcheck.Check) error
	FindByID(ctx context.Context, id string) (*yardcheck.Check, error)
}

// InspectionRepo persists Inspection aggregates (ADR 0015). Save maps the
// partial unique index of one OPEN inspection per trailer, template and gate to
// inspection.ErrInspectionAlreadyOpen.
type InspectionRepo interface {
	Save(ctx context.Context, i *inspection.Inspection) error
	FindByID(ctx context.Context, id string) (*inspection.Inspection, error)
	// FindOpen returns the OPEN inspection of the trailer for the template and gate, or ErrNotFound.
	FindOpen(ctx context.Context, trailerID, templateID string, gate shared.Gate) (*inspection.Inspection, error)
	// ListByTrailer returns at most limit inspections of the trailer, newest first by (opened at, id),
	// strictly after the keyset position.
	ListByTrailer(ctx context.Context, trailerID string, after *views.Key, limit int) ([]*inspection.Inspection, error)
	// Completed returns the trailer's PASSED and FAILED inspections in the order they completed, which is
	// what inspection.Judge reads.
	Completed(ctx context.Context, trailerID string) ([]inspection.Record, error)
}

// TemplateRepo persists the CUSTOM InspectionTemplates (the built-in ones live in
// code, inspection.BuiltIns). A template version is immutable: Save inserts and a
// taken (id, version) is ErrConcurrentModification.
type TemplateRepo interface {
	Save(ctx context.Context, t *inspection.Template) error
	// Find returns one version of a template, or ErrNotFound.
	Find(ctx context.Context, id string, version int) (*inspection.Template, error)
	// Latest returns the highest version of a template, or ErrNotFound.
	Latest(ctx context.Context, id string) (*inspection.Template, error)
	// ListLatest returns the latest version of at most limit templates, by id, strictly after afterID.
	ListLatest(ctx context.Context, afterID string, limit int) ([]*inspection.Template, error)
}

// PolicyRepo persists the YardPolicy of a site (one row per site).
type PolicyRepo interface {
	// Save inserts a policy at its first declaration (version 2: the default is version 1) and
	// updates a later version guarded by version - 1.
	Save(ctx context.Context, p *yardpolicy.Policy) error
	// Find returns the declared policy, or ErrNotFound.
	Find(ctx context.Context, siteID string) (*yardpolicy.Policy, error)
}

// Locations is the read side of the LocationView local copy (ADR 0003).
type Locations interface {
	// Get returns the copy of the location, or ok=false when unknown.
	Get(ctx context.Context, code string) (views.LocationInfo, bool, error)
	// ListBySite returns the active slots of the site, ordered by code.
	ListBySite(ctx context.Context, siteID string) ([]views.LocationInfo, error)
	// FreeSpots returns up to limit active Yard slots of the site, ordered by code, that hold no trailer and
	// are reserved by no open move; zoneIDs, when not empty, restricts them to those zones and kinds, when
	// not empty, to spots of those kinds (shared.SpotKindUnknown selects the spots with no kind, ADR 0014).
	FreeSpots(ctx context.Context, siteID string, zoneIDs []string, kinds []shared.SpotKind, limit int) ([]string, error)
}

// LocationFeed is the write side of the LocationView.
type LocationFeed interface {
	ApplyZone(ctx context.Context, zoneID, siteCode string) error
	// ApplySlot (re)registers a slot active; spotKind is the facility-layout spotKind of a Yard slot, empty when
	// none was published (ADR 0014).
	ApplySlot(ctx context.Context, code, zoneID string, kind shared.LocationKind, dockFlow string, spotKind shared.SpotKind) error
	Decommission(ctx context.Context, code string) error
}

// Appointments is the read side of the AppointmentView.
type Appointments interface {
	Get(ctx context.Context, id string) (views.Appointment, bool, error)
	// ListByDoor returns the appointments of the door in the given statuses by window start.
	ListByDoor(ctx context.Context, doorCode string, statuses ...string) ([]views.Appointment, error)
}

// AppointmentFeed is the write side of the AppointmentView.
type AppointmentFeed interface {
	// Apply folds a Booked / Rescheduled / Cancelled / NoShow fact into the copy: an unknown appointment
	// is inserted from the event (status BOOKED when none is given); for a known one only a NEWER version
	// takes effect, the non-zero fields of a overwrite the stored ones, a status other than "" is set
	// (Cancelled and NoShow are terminal and never overwritten), and the local CHECKED_IN / USED statuses
	// survive a reschedule.
	Apply(ctx context.Context, a views.Appointment) error
	// MarkStatus sets a local status (CHECKED_IN, USED) of a BOOKED or CHECKED_IN appointment; ok=false when
	// the appointment is unknown or already terminal.
	MarkStatus(ctx context.Context, id, status string) (bool, error)
}

// Carriers is the read side of the CarrierView, the local copy of the dock-scheduling carrier
// registry (ADR 0016).
type Carriers interface {
	// Get returns the copy of the carrier with the SCAC, or ok=false when it is not registered here.
	Get(ctx context.Context, scac string) (views.Carrier, bool, error)
}

// CarrierFeed is the write side of the CarrierView. Every method is version-guarded: a fact whose version
// is not higher than the stored one changes nothing, so a replay or a reordering never undoes a newer fact.
type CarrierFeed interface {
	// Register folds CarrierRegistered, the whole snapshot after a registration or an update.
	Register(ctx context.Context, c views.Carrier) error
	// Suspend folds CarrierSuspended: an unseen carrier is inserted with no name.
	Suspend(ctx context.Context, scac, reason string, version int64, at time.Time) error
	// Reinstate folds CarrierReinstated: an unseen carrier is inserted ACTIVE with no name.
	Reinstate(ctx context.Context, scac string, version int64, at time.Time) error
}

// Closures is the DoorClosureView.
type Closures interface {
	// Closed reports whether the door is under a not-lifted closure at the instant.
	Closed(ctx context.Context, doorCode string, at time.Time) (bool, error)
}

// ClosureFeed is the write side of the DoorClosureView.
type ClosureFeed interface {
	Schedule(ctx context.Context, c views.Closure) error
	Lift(ctx context.Context, closureID string) error
}

// Asns is the AsnContentView.
type Asns interface {
	// FilterActive returns the ASN numbers that are not cancelled, in order.
	FilterActive(ctx context.Context, numbers []string) ([]string, error)
}

// AsnFeed is the write side of the AsnContentView.
type AsnFeed interface {
	Register(ctx context.Context, asnNumber string) error
	Cancel(ctx context.Context, asnNumber string) error
}

// Receipts is the ReceiptAtDoorView.
type Receipts interface {
	Get(ctx context.Context, receiptID string) (views.Receipt, bool, error)
	// ClosedASNs returns the ASN numbers whose receipt of the appointment is closed.
	ClosedASNs(ctx context.Context, appointmentID string) ([]string, error)
}

// ReceiptFeed is the write side of the ReceiptAtDoorView.
type ReceiptFeed interface {
	Open(ctx context.Context, r views.Receipt) error
	Close(ctx context.Context, receiptID, asnNumber string, closedAt time.Time) error
}

// LocationLock serialises writers of the same locations: inside a
// UnitOfWork it takes the per-location advisory locks (in a stable order) and
// they are released at commit or rollback.
type LocationLock interface {
	Lock(ctx context.Context, codes ...string) error
}

// EventPublisher is the write side of the transactional outbox: it is called
// in the same UnitOfWork as the aggregate's Save, so a state change and its
// facts commit or roll back together.
type EventPublisher interface {
	Publish(ctx context.Context, events ...shared.Event) error
}

// CheckInMetrics records the business outcome of every gate check-in (Tier 2
// of the fleet metrics convention, the instrument yard.visits.checked_in). Each
// attempt calls exactly one method, after its unit of work ended. reason is the
// problem slug of the failure (visit-already-open, seal-mismatch,
// carrier-suspended, internal-error, ...), never free text. A nil
// CheckInMetrics in the use case deps records nothing.
type CheckInMetrics interface {
	// CheckInAdmitted counts a visit recorded CHECKED_IN.
	CheckInAdmitted(ctx context.Context)
	// CheckInRejected counts a visit recorded REJECTED: the gate turned the
	// truck away.
	CheckInRejected(ctx context.Context, reason string)
	// CheckInRefused counts a check-in that persisted nothing.
	CheckInRefused(ctx context.Context, reason string)
}

// ProcessedEvents is the inbound consumers' idempotency guard: a consumed
// CloudEvent's id is claimed in the SAME unit of work as its effect.
type ProcessedEvents interface {
	// Claim records eventID as processed and reports whether THIS call claimed it first.
	Claim(ctx context.Context, eventID string) (bool, error)
}

// IDGen mints aggregate ids: New("trl") returns "trl-<uuid>".
type IDGen interface {
	New(prefix string) string
}

// Clock is the service's notion of "now"; the domain never reads a clock.
type Clock interface {
	Now() time.Time
}

// IdempotencyStore keeps the answers of the POSTs/PUTs made with an
// Idempotency-Key. Only 2xx and 4xx answers are stored; the first writer wins.
type IdempotencyStore interface {
	Lookup(ctx context.Context, key string) (views.IdempotencyRecord, bool, error)
	Save(ctx context.Context, rec views.IdempotencyRecord) error
}

// UnitOfWork runs fn as ONE atomic unit: every port call fn makes with the ctx
// it is handed commits together when fn returns nil, or rolls back together
// when fn returns an error (or panics). Do is re-entrant: if ctx already
// carries an open unit of work, Do JOINS it. The error fn returns is returned
// unchanged.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}
