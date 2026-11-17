package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"

	"github.com/claudioed/yard-management/internal/adapters/outbound/telemetry"
)

func TestLoadTelemetryEnvDefaultsToTheBinaryServiceName(t *testing.T) {
	got := loadTelemetryEnv(func(string) string { return "" })
	want := telemetryEnv{serviceName: "yard-management", serviceVersion: telemetry.DefaultServiceVersion, otlpEndpoint: telemetry.DefaultEndpoint}
	if got != want {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}
}

func TestLoadTelemetryEnvReadsTheOTelContract(t *testing.T) {
	vars := map[string]string{"OTEL_SERVICE_NAME": "svc", "SERVICE_VERSION": "1.2.3", "OTEL_EXPORTER_OTLP_ENDPOINT": "collector:4317"}
	got := loadTelemetryEnv(func(k string) string { return vars[k] })
	want := telemetryEnv{serviceName: "svc", serviceVersion: "1.2.3", otlpEndpoint: "collector:4317"}
	if got != want {
		t.Fatalf("env = %+v, want %+v", got, want)
	}
}

// With nothing listening on the endpoint, bring-up and flush both return within
// their bounds: a missing Collector costs telemetry, never availability.
func TestStartTelemetryDoesNotBlockWithoutACollector(t *testing.T) {
	tp, mp, prop := otel.GetTracerProvider(), otel.GetMeterProvider(), otel.GetTextMapPropagator()
	t.Cleanup(func() { otel.SetTracerProvider(tp); otel.SetMeterProvider(mp); otel.SetTextMapPropagator(prop) })
	done := make(chan struct{})
	go func() {
		defer close(done)
		flush, err := startTelemetry(slog.New(slog.NewTextHandler(io.Discard, nil)), telemetryEnv{serviceName: "t", serviceVersion: "t", otlpEndpoint: "127.0.0.1:14317"})
		if err != nil {
			t.Errorf("startTelemetry: %v", err)
			return
		}
		flush()
	}()
	select {
	case <-done:
	case <-time.After(3*telemetryFlushTimeout + 5*time.Second):
		t.Fatal("startTelemetry or its flush blocked on an unreachable collector")
	}
}

// A record written under a span carries that span's ids through the logger the
// binary really builds, and honours LOG_LEVEL.
func TestNewLoggerStampsTheTraceIDsOfTheSpanInContext(t *testing.T) {
	var buf bytes.Buffer
	logger := newLoggerTo(&buf, "info")
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled}))

	logger.InfoContext(ctx, "probe")

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log output is not one JSON line: %q (%v)", buf.String(), err)
	}
	if line[telemetry.TraceIDKey] != traceID.String() || line[telemetry.SpanIDKey] != spanID.String() {
		t.Fatalf("log ids = %v/%v, want %s/%s", line[telemetry.TraceIDKey], line[telemetry.SpanIDKey], traceID, spanID)
	}
}

// The Tier 2 counter of the gate check-in is registered on the MeterProvider
// installed before it, so a check-in shows up as yard.visits.checked_in.
func TestCheckInMetricsRegistersTheTier2Counter(t *testing.T) {
	prev := otel.GetMeterProvider()
	t.Cleanup(func() { otel.SetMeterProvider(prev) })
	reader := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))

	m := checkInMetrics(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if m == nil {
		t.Fatal("checkInMetrics returned nil: the counter was not registered")
	}
	m.CheckInRefused(context.Background(), "visit-already-open")

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, metric := range sm.Metrics {
			if metric.Name == "yard.visits.checked_in" {
				return
			}
		}
	}
	t.Fatal("yard.visits.checked_in was not exported after a refused check-in")
}
