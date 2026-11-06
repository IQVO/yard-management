package main

import (
	"encoding/json"
	"net/http"

	inboundhttp "github.com/claudioed/yard-management/internal/adapters/inbound/http"
	inboundmcp "github.com/claudioed/yard-management/internal/adapters/inbound/mcp"
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
// No auth middleware is ever added here (TestNoAuthIsRequired).
func newRouter(rt *runtime) http.Handler {
	return routes(inboundmcp.Handler(inboundmcp.NewServer(rt.deps)), rt.readiness)
}

// routes mounts the MCP handler and the probes; readiness backs /readyz.
func routes(mcpHandler http.Handler, readiness *inboundhttp.Readiness) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusOK, statusBody{Status: "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if !readiness.Ready() {
			writeStatus(w, http.StatusServiceUnavailable, statusBody{Status: "not_ready"})
			return
		}
		if failing := readiness.Failing(r.Context()); len(failing) > 0 {
			writeStatus(w, http.StatusServiceUnavailable, statusBody{Status: "unavailable", Failing: failing})
			return
		}
		writeStatus(w, http.StatusOK, statusBody{Status: "ready"})
	})
	mux.Handle("/", mcpHandler)
	return mux
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
