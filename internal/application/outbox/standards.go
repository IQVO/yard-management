package outbox

import (
	"encoding/json"
	"fmt"

	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/standards"
)

// standardFields are the optional ADR 0018 fields a published fact can carry. A
// field with no honest value is omitted (not null), so a payload that carries
// none is byte-identical to one built before the fields existed.
type standardFields struct {
	X12Status      string `json:"x12_status,omitempty"`
	CBVBizStep     string `json:"cbv_biz_step,omitempty"`
	CBVDisposition string `json:"cbv_disposition,omitempty"`
}

// variantOf reads, from the already-encoded data, the value of the one field the
// standard meaning of the event depends on (standards.Decision.Field: the outcome
// of an InspectionCompleted, the reason of a TrailerHeld). It reads the wire data
// rather than the Go type so that an event wrapped by the MCP attribution (which
// marshals through a map and adds `initiated_by`) means the same as the bare one.
// "" for an event decided as a whole, and for data that does not hold the field as
// a string: the table then maps nothing, it never guesses.
func variantOf(event string, data []byte) string {
	field := standards.VariantField(event)
	if field == "" {
		return ""
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(data, &doc) != nil {
		return ""
	}
	var value string
	if json.Unmarshal(doc[field], &value) != nil {
		return ""
	}
	return value
}

// withStandards appends the standard fields of the event to its already-encoded
// data object, after every existing field, and returns data untouched when the
// mapping table has nothing honest to add. Appending (not re-marshalling through
// a map) keeps the bytes of every existing field exactly as they were.
func withStandards(e shared.Event, data []byte) ([]byte, error) {
	s, _ := standards.Of(e.EventName(), variantOf(e.EventName(), data))
	if s.IsZero() {
		return data, nil
	}
	extra, err := json.Marshal(standardFields{X12Status: s.X12Status, CBVBizStep: s.CBVBizStep, CBVDisposition: s.CBVDisposition})
	if err != nil {
		return nil, fmt.Errorf("encode %s standard fields: %w", e.EventName(), err)
	}
	if len(data) < 2 || data[0] != '{' || data[len(data)-1] != '}' {
		return nil, fmt.Errorf("%s data is not a JSON object: %.40s", e.EventName(), data)
	}
	sep := []byte{','}
	if len(data) == 2 {
		sep = nil
	}
	out := append([]byte(nil), data[:len(data)-1]...)
	out = append(out, sep...)
	out = append(out, extra[1:]...)
	return out, nil
}
