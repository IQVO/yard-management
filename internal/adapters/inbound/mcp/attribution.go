package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/domain/shared"
)

// Agent attribution (Agentic Standard v2; fleet rule agent-authority; ADR
// 0007). A state change that arrives through MCP is attributable: the adapter
// stamps an `initiated_by` object into the DATA of every domain event the call
// raises: {"via": "mcp", "proposal_id", "approved_by", "agent_id"}, the last
// three only when the caller gave them. REST writes carry no `initiated_by`.
// It is a data field on the existing events, never a CloudEvents extension
// attribute, and it grants nothing: it never changes what is accepted.
//
// The domain events stay untouched. The stamp rides the context of the call
// and is applied by AttributingPublisher at the ports.EventPublisher seam, in
// the same unit of work as the aggregate's save.

// viaMCP is the agent surface this adapter is; the adapter, not the caller,
// sets it.
const viaMCP = "mcp"

// maxInitiatorFieldLen bounds each caller-supplied attribution field.
const maxInitiatorFieldLen = 128

// Initiator says which agent surface made a change and, optionally, on whose
// proposal and approval.
type Initiator struct {
	Via        string
	ProposalID string
	ApprovedBy string
	AgentID    string
}

// newInitiator builds the Initiator of an MCP call. The three attribution
// fields are optional; each is trimmed and an empty result is absent.
func newInitiator(proposalID, approvedBy, agentID string) (Initiator, error) {
	fields := []struct{ name, value string }{
		{"proposalId", strings.TrimSpace(proposalID)},
		{"approvedBy", strings.TrimSpace(approvedBy)},
		{"agentId", strings.TrimSpace(agentID)},
	}
	for _, f := range fields {
		if strings.ContainsRune(f.value, 0) {
			return Initiator{}, shared.Invalid("%s must not contain a NUL byte", f.name)
		}
		if n := utf8.RuneCountInString(f.value); n > maxInitiatorFieldLen {
			return Initiator{}, shared.Invalid("%s is %d characters, the maximum is %d", f.name, n, maxInitiatorFieldLen)
		}
	}
	return Initiator{Via: viaMCP, ProposalID: fields[0].value, ApprovedBy: fields[1].value, AgentID: fields[2].value}, nil
}

// data is the `initiated_by` object of an event's data.
func (i Initiator) data() map[string]string {
	by := map[string]string{"via": i.Via}
	if i.ProposalID != "" {
		by["proposal_id"] = i.ProposalID
	}
	if i.ApprovedBy != "" {
		by["approved_by"] = i.ApprovedBy
	}
	if i.AgentID != "" {
		by["agent_id"] = i.AgentID
	}
	return by
}

type initiatorKey struct{}

// withInitiator returns ctx carrying the attribution of the call.
func withInitiator(ctx context.Context, by Initiator) context.Context {
	return context.WithValue(ctx, initiatorKey{}, by)
}

func initiatorFrom(ctx context.Context) (Initiator, bool) {
	by, ok := ctx.Value(initiatorKey{}).(Initiator)
	return by, ok
}

// AttributingPublisher decorates the transactional outbox the MCP binary
// wires into the use cases: every event published under a context that
// carries an Initiator gets `initiated_by` in its data. Without an Initiator
// (a REST write, a sweep) the events pass through unchanged.
type AttributingPublisher struct{ inner ports.EventPublisher }

// NewAttributingPublisher wraps inner.
func NewAttributingPublisher(inner ports.EventPublisher) *AttributingPublisher {
	return &AttributingPublisher{inner: inner}
}

// Publish implements ports.EventPublisher.
func (p *AttributingPublisher) Publish(ctx context.Context, events ...shared.Event) error {
	by, ok := initiatorFrom(ctx)
	if !ok {
		return p.inner.Publish(ctx, events...)
	}
	stamped := make([]shared.Event, len(events))
	for i, e := range events {
		stamped[i] = attributed{Event: e, by: by}
	}
	return p.inner.Publish(ctx, stamped...)
}

// attributed is an event whose data also carries `initiated_by`. The embedded
// interface keeps the name, entity, subject and instant of the original.
type attributed struct {
	shared.Event
	by Initiator
}

// MarshalJSON writes the original event's data plus `initiated_by`.
func (a attributed) MarshalJSON() ([]byte, error) {
	raw, err := json.Marshal(a.Event)
	if err != nil {
		return nil, err
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("attribute %s: its data is not a JSON object: %w", a.EventName(), err)
	}
	by, err := json.Marshal(a.by.data())
	if err != nil {
		return nil, err
	}
	data["initiated_by"] = by
	return json.Marshal(data)
}
