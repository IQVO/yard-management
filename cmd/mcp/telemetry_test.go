package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	inboundmcp "github.com/claudioed/yard-management/internal/adapters/inbound/mcp"
	"github.com/claudioed/yard-management/internal/adapters/outbound/telemetry"
)

func TestLoadTelemetryEnvDefaultsToTheBinaryServiceName(t *testing.T) {
	got := loadTelemetryEnv(func(string) string { return "" })
	want := telemetryEnv{serviceName: "yard-management-mcp", serviceVersion: telemetry.DefaultServiceVersion, otlpEndpoint: telemetry.DefaultEndpoint}
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

// One MCP request yields a request-duration data point labelled with the chi
// route pattern, a server span named after it, and a request log line written
// under that span (the fleet middleware order: RequestID, otelchi, the
// histogram, RequestLogger, Recoverer).
func TestRouterEmitsTheFleetObservabilityStandard(t *testing.T) {
	prevTP, prevMP := otel.GetTracerProvider(), otel.GetMeterProvider()
	t.Cleanup(func() { otel.SetTracerProvider(prevTP); otel.SetMeterProvider(prevMP) })
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	reader := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	var buf bytes.Buffer
	logger := slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(&buf, nil)))
	router := routes(inboundmcp.Handler(inboundmcp.NewServer(memoryDeps())), allUp(), logger, defaultServiceName)

	// A GET on the MCP endpoint without a session is refused by the handler;
	// the request is still observed.
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/mcp", nil))

	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].SpanKind() != trace.SpanKindServer {
		t.Fatalf("spans = %d, want one server span", len(spans))
	}
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log output is not one JSON line: %q (%v)", buf.String(), err)
	}
	sc := spans[0].SpanContext()
	if line[telemetry.TraceIDKey] != sc.TraceID().String() || line[telemetry.SpanIDKey] != sc.SpanID().String() {
		t.Fatalf("log ids = %v/%v, want the server span's %s/%s", line[telemetry.TraceIDKey], line[telemetry.SpanIDKey], sc.TraceID(), sc.SpanID())
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			found = found || m.Name == "http.server.request.duration"
		}
	}
	if !found {
		t.Fatal("http.server.request.duration was not emitted by the MCP router")
	}
}

func TestRouterDoesNotLogTheProbes(t *testing.T) {
	var buf bytes.Buffer
	router := routes(http.NotFoundHandler(), allUp(), slog.New(slog.NewJSONHandler(&buf, nil)), defaultServiceName)
	for _, path := range []string{"/healthz", "/readyz"} {
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	if buf.Len() != 0 {
		t.Fatalf("probes were logged: %s", buf.String())
	}
}
