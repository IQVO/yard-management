package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// reviewYardSOP is the operator standard-operating-procedure the review_yard
// prompt hands to the model (Agentic Standard v2, AS-3; docs/adr/0007). Per the
// charter, prompts encode how to interpret results, when to escalate and what
// "done" means.
const reviewYardSOP = `You are helping a yard supervisor keep a site's yard moving. Use the read tools to establish facts; never assume state. Every write tool is an L3 change: propose it to the supervisor first and call it only for a change the supervisor approved, passing proposalId, approvedBy and agentId, and a fresh idempotencyKey per logical change (retry a failed call with the SAME key).

Procedure:
1. Read the board: get_yard_board(siteId) shows every spot and door with its trailer, the trailers in motion (inMove) and the open moves. A door that is closed takes no trailer; a spot or door holds at most one trailer.
2. Find what needs attention: list_trailers(siteId) for trailers on hold (hold), with a broken seal (seal.state BROKEN) or LOADED outbound trailers with no seal; list_moves(siteId, state REQUESTED) for work nobody has claimed, and list_moves(state CLAIMED) for leases about to expire. get_yard_policy(siteId) gives the dwell thresholds, holding spots and the seal mismatch action. get_yard_dwell_report(from, to, siteId) shows dwell by trailer kind when the analytics read side is wired.
3. Propose, do not act: for a trailer that should move, name the trailer, the free destination and why; for a trailer that must not move, propose place_hold with the right reason (DAMAGED, SEAL_BROKEN, NOT_FOUND, QUALITY, SECURITY); for an obsolete request, propose cancel_move.
4. After approval, call the write tool, then read back (get_trailer or list_moves) and report what changed.

Interpretation:
- Moves are pulled by hostlers from the floor client; request_move queues one, it does not move anything by itself. The destination is reserved the moment the move is requested, so a second move to the same place is refused (destination-reserved).
- A held trailer is not docked or moved to a door. Releasing a hold is the supervisor's decision, never yours.
- A yard check (open_yard_check) starts an audit; the walk and the closing are done on the floor.

Escalate to a human when: a trailer sits past its dwell threshold with no open move, a seal is broken or does not match, a yard check discrepancy appears, or a write is refused with a conflict you cannot resolve with a read.

Not yours, never attempted: gate check-in and check-out, claiming and completing moves, door work, yard policy changes. They are floor-client actions of the people on the yard.

Done means: you have reported the board's state for the site, the trailers and moves that need attention with the evidence you read, each proposed change with its approval status, and the result read back after every change that was made.`

// registerPrompts adds the workflow prompts (operational SOPs). Prompts are L1
// by nature and, per the charter, need no tool Meta.
func (d Deps) registerPrompts(server *mcp.Server) {
	server.AddPrompt(&mcp.Prompt{
		Name:        "review_yard",
		Description: "Standard operating procedure for reviewing a site's yard: read the board, find trailers and moves that need attention, propose changes for approval, apply only approved ones and read the result back.",
	}, func(ctx context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Description: "How to review a yard: establish facts with the read tools, propose moves and holds to the supervisor, act only on approval with an idempotencyKey and attribution, and escalate what only people on the floor can do.",
			Messages: []*mcp.PromptMessage{{
				Role:    "user",
				Content: &mcp.TextContent{Text: reviewYardSOP},
			}},
		}, nil
	})
}
