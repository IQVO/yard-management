package mcp

// replayClass is what the idempotency guard of the write tools does with the
// refusal of a problem type (ADR 0019): the classes of the REST adapter, with
// the same value for the same slug (replay_class_internal_test.go holds the two
// catalogues to each other). The OUTCOME class is not here: it is for a command
// that commits state and then refuses, and no write tool can answer one today
// (carrier-suspended is raised by a gate check-in, which is not a tool). A tool
// that could would need a commit path in keyed first.
type replayClass uint8

const (
	// replayUnset is the zero value: no class. No catalogue entry may have it.
	replayUnset replayClass = iota
	// replayFinal: the answer cannot change for this call; the refusal is
	// stored under the key and replayed, the tool's writes roll back.
	replayFinal
	// replayRetryable: nothing was committed and the answer depends on state
	// that can change. Nothing is stored and the key is free again.
	replayRetryable
)
