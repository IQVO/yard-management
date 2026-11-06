// Package http is the REST inbound adapter: every route of apis/openapi.yaml
// on a chi router, RFC 7807 problem documents for every error, weak ETags and
// If-Match on the policy, the Idempotency-Key middleware and the
// liveness/readiness endpoints (ADR 0008). It has no auth middleware (fleet
// decision 2026-09-11).
package http

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

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
}

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
// /healthz and /readyz.
func NewAdminRouter(readiness *Readiness) http.Handler {
	return newBase(&Server{Readiness: readiness})
}

// newBase is the router every process shares: the NUL guard, the problem
// documents for unknown paths and methods, and the probes.
func newBase(s *Server) chi.Router {
	r := chi.NewRouter()
	// A NUL (U+0000) in the path, the query or a body is a uniform client
	// error here, never a Postgres 22021 -> 500; it runs before the
	// route-scoped idempotency middleware so a rejected request never
	// records its key.
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
