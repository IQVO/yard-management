package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// recorder is a ports.EventPublisher that keeps what it is handed.
type recorder struct{ got []shared.Event }

func (r *recorder) Publish(_ context.Context, events ...shared.Event) error {
	r.got = append(r.got, events...)
	return nil
}

// fact is a minimal domain event with the contract's json tags.
type fact struct {
	shared.Header `json:"-"`
	TrailerID     string `json:"trailer_id"`
	Version       int64  `json:"version"`
}

func newFact(name, id string) fact {
	return fact{Header: shared.NewHeader("trailer", name, id, time.Date(2026, 11, 5, 14, 0, 0, 0, time.UTC)), TrailerID: id, Version: 2}
}

func TestAttributingPublisherStampsEveryEventOfACall(t *testing.T) {
	inner := &recorder{}
	pub := NewAttributingPublisher(inner)
	by, err := newInitiator("prop-1", "sup-1", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(withInitiator(context.Background(), by), newFact("TrailerHeld", "trl-1"), newFact("TrailerParked", "trl-2")); err != nil {
		t.Fatal(err)
	}
	if len(inner.got) != 2 {
		t.Fatalf("the inner publisher got %d events, want 2", len(inner.got))
	}
	for _, e := range inner.got {
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{"via": "mcp", "proposal_id": "prop-1", "approved_by": "sup-1", "agent_id": "agent-1"}
		got, _ := data["initiated_by"].(map[string]any)
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: initiated_by[%s] = %v, want %v (data %s)", e.EventName(), k, got[k], v, raw)
			}
		}
		if data["version"] != 2.0 || data["trailer_id"] == "" {
			t.Errorf("%s lost its own fields: %s", e.EventName(), raw)
		}
		// The envelope keeps the original's identity.
		if e.Entity() != "trailer" || e.AggregateID() == "" || e.OccurredAt().IsZero() {
			t.Errorf("%s lost its header: %v %v %v", e.EventName(), e.Entity(), e.AggregateID(), e.OccurredAt())
		}
	}
}

func TestAttributingPublisherPassesThroughWithoutAnInitiator(t *testing.T) {
	inner := &recorder{}
	e := newFact("TrailerHeld", "trl-1")
	if err := NewAttributingPublisher(inner).Publish(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if len(inner.got) != 1 {
		t.Fatalf("got %d events", len(inner.got))
	}
	if _, wrapped := inner.got[0].(attributed); wrapped {
		t.Fatal("an unattributed call's event was wrapped")
	}
	raw, _ := json.Marshal(inner.got[0])
	if strings.Contains(string(raw), "initiated_by") {
		t.Fatalf("initiated_by on a REST write: %s", raw)
	}
}

// scalar is an event whose data is not a JSON object: it cannot carry a field.
type scalar struct{ shared.Header }

func (scalar) MarshalJSON() ([]byte, error) { return []byte(`"not an object"`), nil }

func TestAttributedEventWhoseDataIsNotAnObjectFails(t *testing.T) {
	by, _ := newInitiator("", "", "")
	a := attributed{Event: scalar{shared.NewHeader("trailer", "Odd", "x", time.Now())}, by: by}
	if _, err := json.Marshal(a); err == nil || !strings.Contains(err.Error(), "Odd") {
		t.Fatalf("marshal err = %v, want one naming the event", err)
	}
}

type unmarshalable struct{ shared.Header }

func (unmarshalable) MarshalJSON() ([]byte, error) { return nil, context.Canceled }

func TestAttributedEventPropagatesItsOwnMarshalError(t *testing.T) {
	by, _ := newInitiator("", "", "")
	a := attributed{Event: unmarshalable{shared.NewHeader("trailer", "Bad", "x", time.Now())}, by: by}
	if _, err := json.Marshal(a); err == nil {
		t.Fatal("a failing inner marshal was swallowed")
	}
}

func TestNewInitiator(t *testing.T) {
	cases := []struct {
		name                  string
		proposal, appr, agent string
		wantErr               bool
		want                  map[string]string
	}{
		{"nothing given", "", "", "", false, map[string]string{"via": "mcp"}},
		{"all given", "p", "a", "g", false, map[string]string{"via": "mcp", "proposal_id": "p", "approved_by": "a", "agent_id": "g"}},
		{"trimmed", " p ", "\ta\n", "  ", false, map[string]string{"via": "mcp", "proposal_id": "p", "approved_by": "a"}},
		{"128 characters", strings.Repeat("é", 128), "", "", false, map[string]string{"via": "mcp", "proposal_id": strings.Repeat("é", 128)}},
		{"129 characters", "", strings.Repeat("x", 129), "", true, nil},
		{"NUL", "", "", "a\x00", true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := newInitiator(tc.proposal, tc.appr, tc.agent)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			data := got.data()
			if len(data) != len(tc.want) {
				t.Fatalf("data = %v, want %v", data, tc.want)
			}
			for k, v := range tc.want {
				if data[k] != v {
					t.Errorf("data[%s] = %q, want %q", k, data[k], v)
				}
			}
		})
	}
}

func TestInitiatorContext(t *testing.T) {
	if _, ok := initiatorFrom(context.Background()); ok {
		t.Fatal("a bare context carries an initiator")
	}
	by, _ := newInitiator("", "", "agent")
	got, ok := initiatorFrom(withInitiator(context.Background(), by))
	if !ok || got.AgentID != "agent" {
		t.Fatalf("initiator = %+v, %v", got, ok)
	}
}
