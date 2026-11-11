package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/usecases"
)

// Deps is everything the MCP tools need, injected by the composition root. The
// use cases are the SAME ones the REST adapter calls; the adapter never
// constructs an outbound adapter itself.
type Deps struct {
	// Yard is the application service: every tool is one of its methods.
	Yard *usecases.Yard
	// Reports is the read side of the analytics reports (ADR 0006). Nil until
	// the projector's read side is wired: get_yard_dwell_report then answers
	// no rows, exactly like GET /reports/yard-dwell.
	Reports ports.ReportReader
	// Idempotency guards the write tools: the key is required and replays the
	// first answer, over the same store the REST Idempotency-Key uses.
	Idempotency IdempotencyDeps
}

// maxReportDays bounds a report window (the REST bound).
const maxReportDays = 92

// replayMeta marks the result of a write tool answered from an earlier call
// with the same idempotencyKey (the REST Idempotency-Replayed header).
var replayMeta = mcp.Meta{"com.warehouse/idempotency-replayed": true}

// --- registry -----------------------------------------------------------------

// registerTools adds the eight read tools (L1) and the eight write tools (L3).
// Every annotations literal stays inline on its tool so the authority fitness
// sensor can prove ReadOnlyHint (internal/architecture). The gate, hostler
// and dock-operator actions (check-in, check-out, claim, complete, door work)
// are floor-client actions on REST and are deliberately NOT tools
// (docs/adr/0007).
func (d Deps) registerTools(server *mcp.Server) {
	d.registerReadTools(server)
	d.registerWriteTools(server)
}

// addRead registers one read tool. A handler error is returned to the SDK as
// the handler's error, which the SDK turns into an isError tool result (never
// a transport/protocol failure), carrying the error text.
func addRead[In, Out any](server *mcp.Server, tool *mcp.Tool, handle func(context.Context, In) (Out, error)) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := handle(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, out, nil
	})
}

// writeInput is what the write wrapper needs of an input: the arguments every
// write tool embeds.
type writeInput interface{ writeMeta() callArgs }

// addWrite registers one write tool behind the idempotency guard: the caller's
// attribution rides the context so every event the call raises carries
// `initiated_by`, and the key makes a retry return the first answer.
func addWrite[In writeInput, Out any](server *mcp.Server, d Deps, tool *mcp.Tool, handle func(context.Context, In) (Out, error)) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		args := in.writeMeta()
		by, err := newInitiator(args.ProposalID, args.ApprovedBy, args.AgentID)
		if err != nil {
			return nil, zero, mapError(err)
		}
		fp, err := fingerprint(tool.Name, in)
		if err != nil {
			return nil, zero, mapError(err)
		}
		out, replayed, err := keyed(withInitiator(ctx, by), d.Idempotency, args.IdempotencyKey, fp, func(ctx context.Context) (Out, error) {
			return handle(ctx, in)
		})
		if err != nil {
			return nil, zero, mapError(err)
		}
		if replayed {
			return &mcp.CallToolResult{Meta: replayMeta}, out, nil
		}
		return nil, out, nil
	})
}
