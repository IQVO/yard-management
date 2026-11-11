package mcp

import "github.com/modelcontextprotocol/go-sdk/mcp"

// Authority levels (Agentic Standard v2, AS-1; docs/adr/0007). Every
// registered tool declares exactly one of these in its Meta under
// authorityMetaKey; the fleet rule is .claude/rules/fleet/agent-authority.md.
//
//   - L1 reads facts of this context and changes nothing (the eight read
//     tools).
//   - L3 changes state: the caller is accountable and a human approval sits
//     between the proposal and the call (the eight write tools). No tool here
//     computes a recommendation, so there is no L2, and no ADR grants L4: the
//     `unused` linter rejects constants nothing references, so neither
//     constant exists.
const (
	// authorityMetaKey is the tools/list _meta key carrying a tool's level.
	authorityMetaKey = "com.warehouse/authority"
	// authorityL1 reads facts and changes nothing.
	authorityL1 = "L1"
	// authorityL3 changes state on approval; the call is attributed.
	authorityL3 = "L3"
)

// authorityMeta is the Meta a tool registers to declare its authority level
// (the fleet-pinned declaration shape; internal/architecture
// mcp_authority_fitness_test.go enforces it).
func authorityMeta(level string) mcp.Meta { return mcp.Meta{authorityMetaKey: level} }
