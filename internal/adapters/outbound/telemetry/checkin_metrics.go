package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/claudioed/yard-management/internal/application/ports"
)

// meterName scopes this service's own instruments, keeping them distinct from
// the ones otelchi and the runtime collector register.
const meterName = "github.com/claudioed/yard-management"

// checkedInName is the Tier 2 business metric of the context (ADR 0017, the
// fleet's standard metrics convention): every gate check-in, by outcome.
const checkedInName = "yard.visits.checked_in"

const (
	outcomeKey = attribute.Key("outcome")
	reasonKey  = attribute.Key("reason")

	outcomeAdmitted = "admitted"
	outcomeRejected = "rejected"
	outcomeRefused  = "refused"
)

// CheckInMetrics implements ports.CheckInMetrics against the global
// MeterProvider. Until Setup installs a real provider the global one is a
// no-op, so recording is cheap and safe in tests and local runs.
type CheckInMetrics struct {
	counter metric.Int64Counter
}

var _ ports.CheckInMetrics = (*CheckInMetrics)(nil)

// NewCheckInMetrics registers the counter. It only fails if the instrument name
// is invalid, which is a programming error, not a runtime condition. Call it
// after Setup so the instrument binds to the real MeterProvider.
func NewCheckInMetrics() (*CheckInMetrics, error) {
	counter, err := otel.Meter(meterName).Int64Counter(
		checkedInName,
		metric.WithDescription("Gate check-ins by outcome: admitted (a CHECKED_IN visit), rejected (a REJECTED visit: the gate turned the truck away; "+
			"reason is seal-mismatch or carrier-suspended) or refused (nothing persisted; reason is the problem slug of the failure, "+
			"such as visit-already-open or appointment-required). "+
			"A rising rejected rate means the site policy is turning trucks away; a rising refused rate with a client reason "+
			"(visit-already-open, trailer-already-on-site, validation) means the gate clients send check-ins the rules refuse, "+
			"not that the service is failing; a rising internal-error or concurrent-modification means the service itself is."),
		metric.WithUnit("{visit}"),
	)
	if err != nil {
		return nil, err
	}
	return &CheckInMetrics{counter: counter}, nil
}

// CheckInAdmitted counts a visit recorded CHECKED_IN.
func (m *CheckInMetrics) CheckInAdmitted(ctx context.Context) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeAdmitted)))
}

// CheckInRejected counts a visit recorded REJECTED, with the reason why.
func (m *CheckInMetrics) CheckInRejected(ctx context.Context, reason string) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeRejected), reasonKey.String(reason)))
}

// CheckInRefused counts a check-in that persisted nothing, with the reason why.
func (m *CheckInMetrics) CheckInRefused(ctx context.Context, reason string) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeRefused), reasonKey.String(reason)))
}
