package outbox

import (
	"encoding/json"
	"fmt"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// envelope is the persisted payload shape of every outbox row: the domain
// event's name plus its ALREADY-pinned CloudEvents data (ADR 0004: snake_case
// field names, RFC 3339 instants, JSON null for an absent nullable field).
type envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Payload maps a domain event to the payload of its outbox row. The event
// structs of the domain carry the contract's json tags, so the data is the
// event itself; the one source of truth for the golden tests and the rows.
func Payload(e shared.Event) ([]byte, error) {
	raw, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("encode %s payload: %w", e.EventName(), err)
	}
	b, err := json.Marshal(envelope{Type: e.EventName(), Data: raw})
	if err != nil {
		return nil, fmt.Errorf("encode %s payload: %w", e.EventName(), err)
	}
	return b, nil
}
