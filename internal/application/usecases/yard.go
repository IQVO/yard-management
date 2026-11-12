// Package usecases holds the application's use cases: one method of Yard per
// command, query and policy of the model (ADR 0002). Each command runs in one
// UnitOfWork that saves the aggregates AND appends the outbox rows, so a state
// change and its facts commit or roll back together. The package imports the
// domain and the ports only.
package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/domain/shared"
)

// Typed misses of the addressed resources (all wrap ports.ErrNotFound, so the
// adapters pick the problem slug by the specific error).
var (
	ErrVisitNotFound     = fmt.Errorf("visit %w", ports.ErrNotFound)
	ErrTrailerNotFound   = fmt.Errorf("trailer %w", ports.ErrNotFound)
	ErrMoveNotFound      = fmt.Errorf("move %w", ports.ErrNotFound)
	ErrYardCheckNotFound = fmt.Errorf("yard check %w", ports.ErrNotFound)
	ErrSiteNotFound      = fmt.Errorf("site %w", ports.ErrNotFound)
)

// Errors of the use cases themselves.
var (
	// ErrNoFreeSpot: DirectOnCheckIn / ClearDoorAfterWork found no free Yard
	// slot; the gate queues (Y-12).
	ErrNoFreeSpot = errors.New("no free spot")
	// ErrNoClaimableMove: ClaimNextMove found no REQUESTED hostler move.
	ErrNoClaimableMove = errors.New("no claimable move")
	// ErrStalePolicy: the If-Match version is not the policy's version.
	ErrStalePolicy = errors.New("policy version changed")
)

// Deps are the ports the use cases depend on. Every field is required in
// production wiring; UoW may be nil in tests (calls then run unwrapped).
type Deps struct {
	UoW          ports.UnitOfWork
	Visits       ports.VisitRepo
	Trailers     ports.TrailerRepo
	Moves        ports.MoveRepo
	Checks       ports.CheckRepo
	Policies     ports.PolicyRepo
	Inspections  ports.InspectionRepo
	Templates    ports.TemplateRepo
	Locations    ports.Locations
	Appointments ports.Appointments
	Closures     ports.Closures
	Carriers     ports.Carriers
	Asns         ports.Asns
	Receipts     ports.Receipts
	Locks        ports.LocationLock
	Processed    ports.ProcessedEvents
	ApptFeed     ports.AppointmentFeed
	LocationFeed ports.LocationFeed
	ClosureFeed  ports.ClosureFeed
	CarrierFeed  ports.CarrierFeed
	AsnFeed      ports.AsnFeed
	ReceiptFeed  ports.ReceiptFeed
	Events       ports.EventPublisher
	IDs          ports.IDGen
	Clock        ports.Clock
}

// Yard is the application service of the yard-management context.
type Yard struct{ d Deps }

// New builds the application service.
func New(d Deps) *Yard { return &Yard{d: d} }

// atomically runs fn inside the unit of work when one is wired.
func (y *Yard) atomically(ctx context.Context, fn func(ctx context.Context) error) error {
	if y.d.UoW == nil {
		return fn(ctx)
	}
	return y.d.UoW.Do(ctx, fn)
}

func (y *Yard) now() time.Time { return y.d.Clock.Now().UTC() }

// publish hands the pending events of an aggregate to the outbox.
func (y *Yard) publish(ctx context.Context, events []shared.Event) error {
	if len(events) == 0 {
		return nil
	}
	return y.d.Events.Publish(ctx, events...)
}

func notFoundAs(err, specific error) error {
	if errors.Is(err, ports.ErrNotFound) {
		return specific
	}
	return err
}
