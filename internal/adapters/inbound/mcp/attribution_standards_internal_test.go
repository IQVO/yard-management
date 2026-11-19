package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/outbox"
	"github.com/claudioed/yard-management/internal/domain/inspection"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
)

// The standard meaning of a fact (ADR 0018) is the same whether a REST write or an
// MCP write raised it: the MCP attribution wraps the event in another Go type and
// re-marshals it through a map, and the standard fields must still be appended.
func TestAnMCPAttributedFactCarriesItsStandardFields(t *testing.T) {
	at := time.Date(2026, 11, 5, 14, 0, 0, 0, time.UTC)
	held := trailer.TrailerHeld{Header: shared.NewHeader("trailer", "TrailerHeld", "trl-1", at), TrailerID: "trl-1", SiteID: "WH1", Reason: trailer.HoldDamaged, Version: 2}
	done := inspection.InspectionCompleted{Header: shared.NewHeader("inspection", "InspectionCompleted", "insp-1", at), InspectionID: "insp-1", SiteID: "WH1", Outcome: "PASSED"}
	by, err := newInitiator("prop-1", "sup-1", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	inner := &recorder{}
	if err := NewAttributingPublisher(inner).Publish(withInitiator(context.Background(), by), held, done); err != nil {
		t.Fatal(err)
	}
	want := []string{`"cbv_disposition":"urn:epcglobal:cbv:disp:damaged"`, `"cbv_disposition":"urn:epcglobal:cbv:disp:conformant"`}
	for i, e := range inner.got {
		payload, err := outbox.Payload(e)
		if err != nil {
			t.Fatal(err)
		}
		var env struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(payload, &env); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(env.Data), want[i]) || !strings.Contains(string(env.Data), `"initiated_by"`) {
			t.Errorf("%s raised through MCP: data %s, want %s next to initiated_by", e.EventName(), env.Data, want[i])
		}
	}
}
