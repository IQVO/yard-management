package main

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	inboundhttp "github.com/claudioed/yard-management/internal/adapters/inbound/http"
	inboundmcp "github.com/claudioed/yard-management/internal/adapters/inbound/mcp"
	"github.com/claudioed/yard-management/internal/adapters/inbound/requestlog"
)

// newRouter builds the HTTP surface of the binary:
//
//   - GET /healthz -> 200 {"status":"ok"} (open; liveness, never flipped by shutdown).
//   - GET /readyz  -> 200 {"status":"ready"}; 503 {"status":"not_ready"} while
//     draining; 503 {"status":"unavailable","failing":[...]} while Postgres or
//     Kafka is unreachable (same gate as cmd/api).
//   - /mcp and /   -> the MCP Streamable HTTP handler. Both paths are served so
//     warehouse-ops-agent's *_MCP_ENDPOINT convention (".../mcp") and a
//     root-mounted endpoint both work.
//
// The routes sit behind the fleet observability middleware, in the standard
// order: RequestID, otelchi (a server span named after the route pattern), the
// http.server.request.duration histogram, RequestLogger, Recoverer.
// telemetry.Setup must have run before this call (otelchimetric binds the
// global MeterProvider at construction).
//
// No auth middleware is ever added here (TestNoAuthIsRequired).
func newRouter(rt *runtime, logger *slog.Logger, serviceName string) http.Handler {
	return routes(inboundmcp.Handler(inboundmcp.NewServer(rt.deps)), rt.readiness, logger, serviceName)
}

// routes mounts the MCP handler and the probes; readiness backs /readyz.
func routes(mcpHandler http.Handler, readiness *inboundhttp.Readiness, logger *slog.Logger, serviceName string) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware(serviceName, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(otelchimetric.NewBaseConfig(serviceName)))
	r.Use(requestlog.RequestLogger(logger))
	r.Use(middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusOK, statusBody{Status: "ok"})
	})
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		if !readiness.Ready() {
			writeStatus(w, http.StatusServiceUnavailable, statusBody{Status: "not_ready"})
			return
		}
		if failing := readiness.Failing(req.Context()); len(failing) > 0 {
			writeStatus(w, http.StatusServiceUnavailable, statusBody{Status: "unavailable", Failing: failing})
			return
		}
		writeStatus(w, http.StatusOK, statusBody{Status: "ready"})
	})
	// Everything else is the MCP endpoint: "/" and "/mcp" (and any other path)
	// reach the Streamable HTTP handler, as the ServeMux "/" pattern did.
	r.Handle("/*", mcpHandler)
	return r
}

type statusBody struct {
	Status  string   `json:"status"`
	Failing []string `json:"failing,omitempty"`
}

func writeStatus(w http.ResponseWriter, code int, body statusBody) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
