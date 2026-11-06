package postgres

import (
	"github.com/claudioed/yard-management/internal/application/ports"
)

// Compile-time proof that every outbound port has a Postgres implementation.
var (
	_ ports.VisitRepo        = (*Visits)(nil)
	_ ports.TrailerRepo      = (*Trailers)(nil)
	_ ports.MoveRepo         = (*Moves)(nil)
	_ ports.CheckRepo        = (*Checks)(nil)
	_ ports.PolicyRepo       = (*Policies)(nil)
	_ ports.Locations        = (*Locations)(nil)
	_ ports.LocationFeed     = (*Locations)(nil)
	_ ports.Appointments     = (*Appointments)(nil)
	_ ports.AppointmentFeed  = (*Appointments)(nil)
	_ ports.Closures         = (*Closures)(nil)
	_ ports.ClosureFeed      = (*Closures)(nil)
	_ ports.Asns             = (*Asns)(nil)
	_ ports.AsnFeed          = (*Asns)(nil)
	_ ports.Receipts         = (*Receipts)(nil)
	_ ports.ReceiptFeed      = (*Receipts)(nil)
	_ ports.LocationLock     = (*Locks)(nil)
	_ ports.ProcessedEvents  = (*ProcessedEvents)(nil)
	_ ports.IdempotencyStore = (*IdempotencyStore)(nil)
	_ ports.EventPublisher   = (*Outbox)(nil)
	_ ports.UnitOfWork       = (*UnitOfWork)(nil)
)
