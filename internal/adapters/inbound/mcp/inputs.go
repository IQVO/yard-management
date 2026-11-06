package mcp

// The tool inputs. Argument names are camelCase like the REST contract of this
// context (apis/openapi.yaml); the only snake_case names are the keys inside
// the `initiated_by` object of the events, which follow the event data
// convention. A field a caller may leave out carries `omitempty` and the word
// "optional" in its description, so the inferred schema does not mark it
// required (internal/architecture/mcp_optional_inputs_test.go).

// callArgs are the arguments every WRITE tool takes: the idempotency key
// (required, like the REST Idempotency-Key) and the optional attribution.
type callArgs struct {
	IdempotencyKey string `json:"idempotencyKey" jsonschema:"required, 1 to 128 printable ASCII characters, unique per logical request. A retry with the same key and the same arguments returns the first answer; the same key with other arguments is refused with idempotency-key-reused"`
	ProposalID     string `json:"proposalId,omitempty" jsonschema:"optional id of the proposal this call executes, recorded on the events it raises for attribution (max 128 characters)"`
	ApprovedBy     string `json:"approvedBy,omitempty" jsonschema:"optional id of the human who approved the proposal, recorded on the events it raises for attribution (max 128 characters)"`
	AgentID        string `json:"agentId,omitempty" jsonschema:"optional id of the calling agent, recorded on the events it raises for attribution (max 128 characters)"`
}

// callArgKeys are the JSON names of callArgs; they are not part of what a
// call means, so they stay out of its idempotency fingerprint.
var callArgKeys = []string{"idempotencyKey", "proposalId", "approvedBy", "agentId"}

// writeMeta lets the write wrapper read the arguments every write input
// embeds.
func (c callArgs) writeMeta() callArgs { return c }

// --- read inputs --------------------------------------------------------------

type siteInput struct {
	SiteID string `json:"siteId" jsonschema:"the site code (the facility-layout siteCode, e.g. WH1)"`
}

type trailerIDInput struct {
	TrailerID string `json:"trailerId" jsonschema:"the trailer id, e.g. trl-1b4e28ba-2fa1-4d3c-8f6e-0a1b2c3d4e5f"`
}

type visitIDInput struct {
	VisitID string `json:"visitId" jsonschema:"the visit id, e.g. visit-1b4e28ba-2fa1-4d3c-8f6e-0a1b2c3d4e5f"`
}

type listVisitsInput struct {
	Limit        int    `json:"limit,omitempty" jsonschema:"page size, 1..500; omitted or 0 means 100"`
	Cursor       string `json:"cursor,omitempty" jsonschema:"the opaque nextCursor returned by the previous page; omitted means the first page"`
	SiteID       string `json:"siteId,omitempty" jsonschema:"optional: keep only visits of this site"`
	State        string `json:"state,omitempty" jsonschema:"optional: keep only visits in this state: CHECKED_IN, CHECKED_OUT or REJECTED"`
	TractorPlate string `json:"tractorPlate,omitempty" jsonschema:"optional: keep only visits of this tractor plate"`
}

type listTrailersInput struct {
	Limit        int    `json:"limit,omitempty" jsonschema:"page size, 1..500; omitted or 0 means 100"`
	Cursor       string `json:"cursor,omitempty" jsonschema:"the opaque nextCursor returned by the previous page; omitted means the first page"`
	SiteID       string `json:"siteId,omitempty" jsonschema:"optional: keep only trailers of this site"`
	State        string `json:"state,omitempty" jsonschema:"optional: keep only trailers in this state: PARKED, IN_MOVE, DOCKED or DEPARTED; omitted means every trailer still on site (not DEPARTED)"`
	Kind         string `json:"kind,omitempty" jsonschema:"optional: keep only this kind: DRY_VAN, REEFER, FLATBED or CONTAINER"`
	LocationCode string `json:"locationCode,omitempty" jsonschema:"optional: keep only the trailer parked or docked at this spot or door code"`
	UnitNumber   string `json:"unitNumber,omitempty" jsonschema:"optional: keep only trailers with this unit number"`
	VisitID      string `json:"visitId,omitempty" jsonschema:"optional: keep only trailers brought by this visit"`
}

type listMovesInput struct {
	Limit      int    `json:"limit,omitempty" jsonschema:"page size, 1..500; omitted or 0 means 100"`
	Cursor     string `json:"cursor,omitempty" jsonschema:"the opaque nextCursor returned by the previous page; omitted means the first page"`
	SiteID     string `json:"siteId,omitempty" jsonschema:"optional: keep only moves of this site"`
	State      string `json:"state,omitempty" jsonschema:"optional: keep only moves in this state: REQUESTED, CLAIMED, COMPLETED or CANCELLED"`
	TrailerID  string `json:"trailerId,omitempty" jsonschema:"optional: keep only moves of this trailer"`
	HostlerRef string `json:"hostlerRef,omitempty" jsonschema:"optional: keep only moves leased to this hostler"`
}

type dwellReportInput struct {
	From   string `json:"from" jsonschema:"RFC 3339 start of the report window"`
	To     string `json:"to" jsonschema:"RFC 3339 end of the report window, after from and at most 92 days later"`
	SiteID string `json:"siteId,omitempty" jsonschema:"optional: restrict the report to one site; omitted means every site"`
}

// --- write inputs -------------------------------------------------------------

type locationInput struct {
	Kind string `json:"kind" jsonschema:"SPOT for a yard spot or DOOR for a dock door"`
	Code string `json:"code" jsonschema:"the facility-layout location code of the spot or door"`
}

type requestMoveInput struct {
	callArgs
	TrailerID    string        `json:"trailerId" jsonschema:"the trailer to move"`
	To           locationInput `json:"to" jsonschema:"the destination: a free, active spot or door of the trailer's site. It is reserved for the move until the move ends"`
	Reason       string        `json:"reason,omitempty" jsonschema:"optional: TO_DOOR, FROM_DOOR, RELOCATE or TO_HOLDING; omitted derives it from the destination (a door is TO_DOOR, a spot is FROM_DOOR from a door and RELOCATE otherwise)"`
	PriorityBand int           `json:"priorityBand,omitempty" jsonschema:"optional claim priority 1 (most urgent) .. 9; omitted means the default band"`
	DueBy        string        `json:"dueBy,omitempty" jsonschema:"optional RFC 3339 instant the trailer should be there by; breaks ties inside a band"`
}

type cancelMoveInput struct {
	callArgs
	MoveID string `json:"moveId" jsonschema:"the open (REQUESTED or CLAIMED) move to cancel"`
	Reason string `json:"reason,omitempty" jsonschema:"optional free-text reason, recorded on the cancellation"`
}

type placeHoldInput struct {
	callArgs
	TrailerID string `json:"trailerId" jsonschema:"the trailer to hold"`
	Reason    string `json:"reason" jsonschema:"why: DAMAGED, SEAL_BROKEN, NOT_FOUND, QUALITY or SECURITY"`
}

type releaseHoldInput struct {
	callArgs
	TrailerID string `json:"trailerId" jsonschema:"the held trailer to release"`
}

type recordSealInput struct {
	callArgs
	TrailerID  string `json:"trailerId" jsonschema:"the trailer whose seal was checked"`
	State      string `json:"state" jsonschema:"INTACT records the seal number; BROKEN marks the seal broken and places a SEAL_BROKEN hold on the trailer"`
	SealNumber string `json:"sealNumber,omitempty" jsonschema:"optional: the seal number read off the trailer; required when state is INTACT"`
}

type openYardCheckInput struct {
	callArgs
	SiteID string `json:"siteId" jsonschema:"the site to audit; at most one yard check is open per site"`
}
