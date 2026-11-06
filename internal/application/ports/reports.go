package ports

import (
	"context"
	"time"

	"github.com/claudioed/yard-management/internal/application/views"
)

// ReportReader is the read side of the analytics reports (ADR 0006): the
// rows of a window, folded from the analytics topic by the projector. It is a
// pure read port; the transactional use cases never use it.
type ReportReader interface {
	YardDwell(ctx context.Context, w views.ReportWindow) ([]views.DwellRow, error)
	DoorUtilisation(ctx context.Context, w views.ReportWindow) ([]views.DoorRow, error)
	HostlerProductivity(ctx context.Context, w views.ReportWindow) ([]views.HostlerRow, error)
	// LastEventAt is the instant of the newest event the projector folded;
	// ok is false until the first one.
	LastEventAt(ctx context.Context) (at time.Time, ok bool, err error)
}
