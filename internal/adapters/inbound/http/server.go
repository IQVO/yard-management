// Package http is the REST inbound adapter: every route of apis/openapi.yaml
// on a chi router, RFC 7807 problem documents for every error, weak ETags and
// If-Match on the policy, the Idempotency-Key middleware and the
// liveness/readiness endpoints (ADR 0008). It has no auth middleware (fleet
// decision 2026-09-11).
package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/claudioed/yard-management/internal/adapters/inbound/requestlog"
	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/usecases"
)

// Server holds the use cases the handlers call.
type Server struct {
	Yard *usecases.Yard
	// Reports backs GET /reports/*: the analytics read side. nil answers the
	// contract shape with no rows until the analytics read side is wired.
	Reports ports.ReportReader
	// Now stamps the freshness answer; time.Now when nil.
	Now func() time.Time

	// Idempotency wraps every POST and PUT (RequireIdempotencyKey, built by
	// cmd/ over the Postgres ports); nil (handler unit tests) leaves the
	// routes unwrapped.
	Idempotency func(http.Handler) http.Handler
	// SweepIdempotency wraps the sweeps, where the key is optional.
	SweepIdempotency func(http.Handler) http.Handler
	// Readiness backs GET /readyz: the drain gate plus the Postgres and Kafka
	// checks (NewReadiness). nil (handler unit tests) means always ready.
	Readiness *Readiness
	// Logger writes the access log (one line per request, none for the
	// probes); slog.Default() when nil. Build it with the trace-correlating
	// handler of the composition root so a line carries trace_id/span_id.
	Logger *slog.Logger
	// ServiceName labels the server spans and the request-duration histogram
	// (the OTEL_SERVICE_NAME of the binary); DefaultServiceName when empty.
	ServiceName string
}

// DefaultServiceName labels the spans and metrics of a router built without a
// ServiceName; it is the service.name of the OTel resource of cmd/api.
const DefaultServiceName = "yard-management"

// NewRouter builds the chi router for every route of apis/openapi.yaml.
func NewRouter(s *Server) http.Handler {
	r := newBase(s)
	keyed := wrap(r, s.Idempotency)
	swept := wrap(r, s.SweepIdempotency)

	keyed.Post("/visits", s.handleCheckIn)
	r.Get("/visits", s.handleListVisits)
	r.Get("/visits/{visitId}", s.handleGetVisit)
	keyed.Post("/visits/{visitId}/check-outs", s.handleCheckOut)
	keyed.Post("/visit-rejections", s.handleRejectAtGate)

	r.Get("/trailers", s.handleListTrailers)
	r.Get("/trailers/{trailerId}", s.handleGetTrailer)
	keyed.Post("/trailers/{trailerId}/door-work-starts", s.handleStartDoorWork)
	keyed.Post("/trailers/{trailerId}/door-work-completions", s.handleCompleteDoorWork)
	keyed.Post("/trailers/{trailerId}/holds", s.handlePlaceHold)
	keyed.Post("/trailers/{trailerId}/hold-releases", s.handleReleaseHold)
	keyed.Post("/trailers/{trailerId}/seals", s.handleRecordSeal)
	keyed.Post("/trailers/{trailerId}/securings", s.handleSecureTrailer)
	keyed.Post("/trailers/{trailerId}/securing-releases", s.handleReleaseTrailerSecuring)
	keyed.Post("/trailers/{trailerId}/classifications", s.handleClassifyTrailer)
	keyed.Post("/trailers/{trailerId}/inspections", s.handleOpenInspection)
	r.Get("/trailers/{trailerId}/inspections", s.handleListTrailerInspections)

	r.Get("/inspections/{inspectionId}", s.handleGetInspection)
	keyed.Put("/inspections/{inspectionId}/items/{itemCode}", s.handleRecordInspectionItem)
	keyed.Post("/inspections/{inspectionId}/completions", s.handleCompleteInspection)
	r.Get("/inspection-templates", s.handleListTemplates)
	r.Get("/inspection-templates/{templateId}", s.handleGetTemplate)
	r.Get("/inspection-templates/{templateId}/versions/{version}", s.handleGetTemplate)
	keyed.Put("/inspection-templates/{templateId}/versions/{version}", s.handlePublishTemplate)

	keyed.Post("/moves", s.handleRequestMove)
	r.Get("/moves", s.handleListMoves)
	keyed.Post("/moves/claims", s.handleClaimNext)
	keyed.Post("/moves/{moveId}/completions", s.handleCompleteMove)
	keyed.Post("/moves/{moveId}/cancellations", s.handleCancelMove)

	keyed.Post("/yard-checks", s.handleOpenYardCheck)
	keyed.Post("/yard-checks/{yardCheckId}/observations", s.handleRecordObservation)
	keyed.Post("/yard-checks/{yardCheckId}/closures", s.handleCloseYardCheck)

	r.Get("/sites/{siteId}/yard-board", s.handleYardBoard)
	r.Get("/sites/{siteId}/yard-policy", s.handleGetPolicy)
	keyed.Put("/sites/{siteId}/yard-policy", s.handleDeclarePolicy)

	swept.Post("/dwell-sweeps", s.handleDwellSweep)
	swept.Post("/move-lease-sweeps", s.handleLeaseSweep)

	s.mountReports(r)
	return r
}

// NewReportsRouter builds the router of cmd/yard-reports: the probes and the
// four report routes of apis/openapi.yaml, nothing that writes.
func NewReportsRouter(s *Server) http.Handler {
	r := newBase(s)
	s.mountReports(r)
	return r
}

// NewAdminRouter builds the probe-only router of the workers (cmd/yard-projector):
// /healthz and /readyz. logger and serviceName are the Server fields of the same name.
func NewAdminRouter(readiness *Readiness, logger *slog.Logger, serviceName string) http.Handler {
	return newBase(&Server{Readiness: readiness, Logger: logger, ServiceName: serviceName})
}

// newBase is the router every process shares: the fleet observability
// middleware, the NUL guard, the problem documents for unknown paths and
// methods, and the probes.
//
// The order is the fleet standard (ADR 0016 of inventory-storage, adopted here
// by ADR 0017): RequestID, otelchi (server span named after the route PATTERN),
// the http.server.request.duration histogram, the request log, Recoverer.
// otelchi runs before the log so the request context already carries the span
// the trace-correlating slog handler reads. telemetry.Setup must have run
// before the router is built: otelchimetric binds the global MeterProvider at
// construction.
func newBase(s *Server) chi.Router {
	r := chi.NewRouter()
	name := s.ServiceName
	if name == "" {
		name = DefaultServiceName
	}
	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware(name, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(otelchimetric.NewBaseConfig(name)))
	r.Use(requestlog.RequestLogger(s.Logger))
	r.Use(middleware.Recoverer)
	// A NUL (U+0000) in the path, the query or a body is a uniform client
	// error here, never a Postgres 22021 -> 500; it runs before the
	// route-scoped idempotency middleware so a rejected request never
	// records its key. It sits INSIDE the observability middleware so a
	// refused request still shows up in the RED metrics and the log.
	r.Use(rejectNUL)
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		writeProblem(w, req, notFoundResource, "no resource matches the requested path")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		writeProblem(w, req, methodNotAllowed, "the resource does not take this method")
	})
	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", s.handleReadyz)
	return r
}

func (s *Server) mountReports(r chi.Router) {
	r.Get("/reports/yard-dwell", s.handleYardDwell)
	r.Get("/reports/door-utilisation", s.handleDoorUtilisation)
	r.Get("/reports/hostler-productivity", s.handleHostlerProductivity)
	r.Get("/reports/turn-time", s.handleTurnTime)
	r.Get("/reports/detention-exposure", s.handleDetentionExposure)
	r.Get("/reports/exceptions", s.handleExceptions)
	r.Get("/reports/freshness", s.handleFreshness)
}

// wrap returns the router with mw applied route by route; a nil mw (handler
// unit tests) leaves the routes unwrapped.
func wrap(r chi.Router, mw func(http.Handler) http.Handler) chi.Router {
	if mw == nil {
		return r.With()
	}
	return r.With(mw)
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
}
