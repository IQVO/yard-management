package shared

import "time"

// Event is a domain fact raised by an aggregate, to be written to the
// outbox. It carries no CloudEvents or Kafka type: the outbound adapter maps
// it to the wire (ADR 0004). The concrete event structs carry the snake_case
// `data` of the contract as json tags; Header is not part of `data`.
type Event interface {
	// EventName is the PascalCase event name, e.g. "TrailerDocked".
	EventName() string
	// Entity is the aggregate segment of the CloudEvents type.
	Entity() string
	// AggregateID is the subject and Kafka key.
	AggregateID() string
	// OccurredAt is the domain occurred-at instant.
	OccurredAt() time.Time
}

// Header is embedded (json:"-") in every event.
type Header struct {
	Name    string
	Ent     string
	Subject string
	At      time.Time
}

// NewHeader builds the header of an event.
func NewHeader(entity, name, subject string, at time.Time) Header {
	return Header{Name: name, Ent: entity, Subject: subject, At: at.UTC()}
}

// EventName implements Event.
func (h Header) EventName() string { return h.Name }

// Entity implements Event.
func (h Header) Entity() string { return h.Ent }

// AggregateID implements Event.
func (h Header) AggregateID() string { return h.Subject }

// OccurredAt implements Event.
func (h Header) OccurredAt() time.Time { return h.At }

// Stamp formats an instant as the RFC 3339 UTC string of the contracts.
func Stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// StampPtr is Stamp for a nullable instant: nil for the zero time.
func StampPtr(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := Stamp(t)
	return &s
}

// Str returns nil for an empty string, a pointer to it otherwise (nullable
// event fields).
func Str(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Strings returns s, or an empty non-nil slice, so a list serialises as []
// and never as null.
func Strings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return append([]string{}, s...)
}
