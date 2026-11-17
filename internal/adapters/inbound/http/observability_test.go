package http_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	inboundhttp "github.com/claudioed/yard-management/internal/adapters/inbound/http"
)

const requestDuration = "http.server.request.duration"

// manualMeter installs a ManualReader MeterProvider as the global one for the
// test. It must run BEFORE the router under test is built: otelchimetric binds
// the global MeterProvider when the router is constructed.
func manualMeter(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })
	return reader
}

// durationRoutes returns the http.route label of every data point of the
// request-duration histogram.
func durationRoutes(t *testing.T, reader *sdkmetric.ManualReader) map[string]bool {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	routes := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != requestDuration {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("%s is a %T, want a float64 Histogram", requestDuration, m.Data)
			}
			for _, dp := range hist.DataPoints {
				route, _ := dp.Attributes.Value("http.route")
				routes[route.AsString()] = true
			}
		}
	}
	return routes
}

// Tier 1 of the fleet metrics convention: a request to the api produces
// http.server.request.duration, labelled with the chi route PATTERN (never the
// raw path, which would mint one series per id).
func TestRouterExportsRequestDurationByRoutePattern(t *testing.T) {
	reader := manualMeter(t)
	g := newRig(t)
	g.seedSite()

	g.do(http.MethodGet, "/trailers/trl-1234", nil)

	routes := durationRoutes(t, reader)
	if !routes["/trailers/{trailerId}"] {
		t.Fatalf("http.route labels = %v, want /trailers/{trailerId}", routes)
	}
	if routes["/trailers/trl-1234"] {
		t.Fatalf("the raw path leaked into the http.route label: %v", routes)
	}
}

// The reports router (cmd/yard-reports) and the probe-only admin router
// (cmd/yard-projector) are chi routers too: the same Tier 1 histogram.
func TestReportsAndAdminRoutersExportRequestDuration(t *testing.T) {
	reader := manualMeter(t)
	reports := inboundhttp.NewReportsRouter(&inboundhttp.Server{ServiceName: "yard-management-reports"})
	admin := inboundhttp.NewAdminRouter(inboundhttp.NewReadiness(0, 0), nil, "yard-management-projector")

	reports.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	admin.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/readyz", nil))

	routes := durationRoutes(t, reader)
	if !routes["/healthz"] || !routes["/readyz"] {
		t.Fatalf("http.route labels = %v, want /healthz (reports) and /readyz (admin)", routes)
	}
}

// spanCapturingHandler stands in for the trace-correlating slog handler of the
// composition root (it lives in the outbound telemetry adapter, which this
// inbound adapter must not import): what this layer owns is handing the
// request log a context that carries the request's server span.
type spanCapturingHandler struct {
	slog.Handler
	seen trace.SpanContext
}

func (h *spanCapturingHandler) Handle(ctx context.Context, rec slog.Record) error {
	h.seen = trace.SpanContextFromContext(ctx)
	return h.Handler.Handle(ctx, rec)
}

func TestRouterLogsUnderTheServerSpanNamedAfterTheRoutePattern(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	t.Cleanup(func() { otel.SetTracerProvider(previous) })

	logs := &spanCapturingHandler{Handler: slog.NewJSONHandler(&bytes.Buffer{}, nil)}
	g := newRig(t)
	g.server.Logger = slog.New(logs)
	g.handler = inboundhttp.NewRouter(g.server)
	g.seedSite()

	g.do(http.MethodGet, "/trailers/trl-1234", nil)

	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].SpanKind() != trace.SpanKindServer {
		t.Fatalf("spans = %d, want one server span", len(spans))
	}
	if name := spans[0].Name(); name != "/trailers/{trailerId}" {
		t.Errorf("span name = %q, want the route pattern", name)
	}
	if !logs.seen.IsValid() || logs.seen.TraceID() != spans[0].SpanContext().TraceID() || logs.seen.SpanID() != spans[0].SpanContext().SpanID() {
		t.Fatalf("the request log was written under %v, want the server span %v: middleware order or the Context logger variants are wrong", logs.seen, spans[0].SpanContext())
	}
}

// /healthz stays exactly what it was: unlogged, unauthenticated, {"status":"ok"}.
func TestHealthzIsUnchangedByTheObservabilityMiddleware(t *testing.T) {
	g := newRig(t)
	got := g.do(http.MethodGet, "/healthz", nil)
	if got.Code != http.StatusOK || got.JSON()["status"] != "ok" {
		t.Fatalf("GET /healthz = %d %s, want 200 {\"status\":\"ok\"}", got.Code, got.Body)
	}
}
