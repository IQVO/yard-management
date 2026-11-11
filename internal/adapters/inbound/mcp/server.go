// Package mcp is the inbound Model Context Protocol adapter: it exposes this
// bounded context to the AI ecosystem as a second driving adapter over the
// same application-layer use cases the REST adapter uses. It is built on the
// official MCP Go SDK and served over Streamable HTTP only, stateless.
//
// Per docs/adr/0007-mcp-server.md the surface is eight L1 read tools
// (get_yard_board, list_trailers, get_trailer, list_moves, list_visits,
// get_visit, get_yard_policy, get_yard_dwell_report) and eight L3 write tools
// (request_move, cancel_move, place_hold, release_hold, record_seal,
// secure_trailer, release_securing, open_yard_check) that carry an idempotencyKey like the REST Idempotency-Key
// and stamp `initiated_by` into the events they raise. The actions of the
// gate clerk, the hostler and the dock operator (check-in, check-out, claim,
// complete, door work) are floor-client actions on REST and are NOT tools.
//
// This package depends inward on the application layer and the domain only --
// never on an outbound adapter or the REST adapter -- and nothing else may
// depend on it (internal/architecture's TestMCPAdapterDependencyRule). The
// composition root (cmd/mcp) wires concrete repositories into the use cases.
// There is no auth of any kind (fleet-wide revert 2026-09-11;
// TestNoAuthMiddlewareReintroduced).
package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverName and serverVersion identify this server in the MCP initialize
// handshake.
const (
	serverName    = "yard-management-mcp"
	serverVersion = "1.0.0"
)

// instructions is what an MCP client is told about this server at initialize.
const instructions = "Yard workflow of the warehouse: everything physically on site outside the four walls. Read with get_yard_board " +
	"(the live map of spots, doors, trailers in motion and open moves of a site), list_trailers / get_trailer, list_moves, " +
	"list_visits / get_visit, get_yard_policy and get_yard_dwell_report. Change things with request_move, cancel_move, place_hold, " +
	"release_hold, record_seal and open_yard_check: each is an L3 write, so call it only for a change a human approved, send a " +
	"unique idempotencyKey (a retry with the same key and arguments returns the first answer) and pass proposalId, approvedBy and " +
	"agentId so the events it raises are attributable. Gate check-in and check-out, hostler claims and completions and dock-door " +
	"work are floor-client actions of the people on the yard and are not offered here."

// NewServer builds the MCP server for this bounded context with every tool,
// resource and workflow prompt registered.
func NewServer(deps Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion},
		&mcp.ServerOptions{Instructions: instructions},
	)
	deps.registerTools(server)
	deps.registerResources(server)
	deps.registerPrompts(server)
	return server
}

// Handler returns the Streamable HTTP handler for the MCP server.
func Handler(server *mcp.Server) http.Handler {
	// Stateless (Agentic Standard v2, AS-4; docs/adr/0007): no Mcp-Session-Id
	// is issued, so the server also answers the 2026-07-28 revision's
	// per-request handshake (server/discover) while legacy initialize
	// clients keep working. Nothing in this adapter relies on cross-call
	// session state: a write carries its own idempotencyKey.
	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
}
