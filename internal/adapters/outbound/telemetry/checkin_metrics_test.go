package telemetry_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/claudioed/yard-management/internal/adapters/outbound/telemetry"
)

// collectCheckIns reads the yard.visits.checked_in data points as
// "outcome/reason" -> value (just "outcome" when there is no reason).
func collectCheckIns(t *testing.T, reader sdkmetric.Reader) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "yard.visits.checked_in" {
				addCheckInPoints(t, m, got)
			}
		}
	}
	return got
}

// addCheckInPoints checks the instrument is a monotonic int64 Sum (a counter)
// with a UCUM {noun} unit and a description, then records its data points.
func addCheckInPoints(t *testing.T, m metricdata.Metrics, got map[string]int64) {
	t.Helper()
	if m.Unit != "{visit}" || m.Description == "" {
		t.Errorf("unit = %q, description empty = %v; the convention wants a UCUM {noun} unit and a description", m.Unit, m.Description == "")
	}
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok || !sum.IsMonotonic {
		t.Fatalf("yard.visits.checked_in is a %T, want a monotonic int64 Sum", m.Data)
	}
	for _, dp := range sum.DataPoints {
		if dp.Attributes.Len() > 2 {
			t.Errorf("data point carries more than outcome and reason: %v", dp.Attributes)
		}
		outcome, _ := dp.Attributes.Value("outcome")
		key := outcome.AsString()
		if reason, ok := dp.Attributes.Value("reason"); ok {
			key += "/" + reason.AsString()
		}
		got[key] = dp.Value
	}
}

func TestCheckInMetricsCountsByOutcomeAndReason(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })

	metrics, err := telemetry.NewCheckInMetrics()
	if err != nil {
		t.Fatalf("NewCheckInMetrics: %v", err)
	}
	ctx := context.Background()
	metrics.CheckInAdmitted(ctx)
	metrics.CheckInAdmitted(ctx)
	metrics.CheckInRejected(ctx, "seal-mismatch")
	metrics.CheckInRejected(ctx, "carrier-suspended")
	metrics.CheckInRefused(ctx, "visit-already-open")
	metrics.CheckInRefused(ctx, "visit-already-open")
	metrics.CheckInRefused(ctx, "internal-error")

	got := collectCheckIns(t, reader)
	want := map[string]int64{
		"admitted":                   2,
		"rejected/seal-mismatch":     1,
		"rejected/carrier-suspended": 1,
		"refused/visit-already-open": 2,
		"refused/internal-error":     1,
	}
	if len(got) != len(want) {
		t.Fatalf("data points = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d (all: %v)", k, got[k], v, got)
		}
	}
}

// Whatever the global provider is, recording is fire-and-forget.
func TestCheckInMetricsRecordingNeverPanics(t *testing.T) {
	metrics, err := telemetry.NewCheckInMetrics()
	if err != nil {
		t.Fatal(err)
	}
	metrics.CheckInAdmitted(context.Background())
	metrics.CheckInRejected(context.Background(), "seal-mismatch")
	metrics.CheckInRefused(context.Background(), "internal-error")
}
