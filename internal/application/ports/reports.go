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
	// TurnTime, DetentionExposure and Exceptions are the KPI standard reports
	// (ADR 0011). thresholdMinutes is already validated (1..10080).
	TurnTime(ctx context.Context, w views.ReportWindow) ([]views.TurnTimeRow, error)
	DetentionExposure(ctx context.Context, w views.ReportWindow, thresholdMinutes int) ([]views.DetentionRow, error)
	Exceptions(ctx context.Context, w views.ReportWindow) ([]views.ExceptionRow, error)
	// LastEventAt is the instant of the newest event the projector folded;
	// ok is false until the first one.
	LastEventAt(ctx context.Context) (at time.Time, ok bool, err error)
}
