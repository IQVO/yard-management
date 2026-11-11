package kafka

import (
	"encoding/json"
	"fmt"

	"github.com/claudioed/yard-management/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/yard-management/internal/application/outbox"
)

// The topics this service writes. The relay's Kafka writer carries no Topic
// and routes every message by Message.Topic (the topic-not-set-twice rule), so
// the DLQ write needs no writer of its own.
const (
	// TopicEvents carries the eleven integration types of ADR 0004 (ADR 0010 added DoorWorkCompleted and TrailerDeparted, ADR 0013 TrailerSecured and TrailerSecuringReleased).
	TopicEvents = "warehouse.yard-management.events"
	// TopicAnalytics carries EVERY published type: the eleven integration
	// types mirrored (same id, type, subject and byte-identical data, only
	// the dataschema stream segment differs) plus the ten analytics-only
	// types, which go nowhere else.
	TopicAnalytics = "warehouse.yard-management.analytics"
	// TopicDLQ receives, verbatim, the outbox rows that can never be encoded.
	TopicDLQ = "warehouse.yard-management.events.dlq"

	// Partitions is the partition count of the events and analytics topics.
	Partitions = 8
)

// Topics lists the topics the service needs pre-created (ensure-topics): the
// integration topic AND its analytics twin, plus the dead-letter topic.
func Topics() []string { return []string{TopicEvents, TopicAnalytics, TopicDLQ} }

// schemaVersion is the dataschema version of every payload
// (urn:warehouse:yard-management:<events|analytics>:<EventName>:v1).
const schemaVersion = 1

// published is the catalogue of ADR 0004: aggregate segment -> event name ->
// whether it is also an integration event (true: events + analytics) or
// analytics-only (false). A row outside it is poison: deterministic, never
// retryable, dead-lettered.
var published = map[string]map[string]bool{
	outbox.AggregVisit: {"VisitCheckedIn": true, "VisitRejected": true, "VisitCheckedOut": false},
	outbox.AggregTrailer: {
		"TrailerDocked": true, "TrailerUndocked": true, "SealExceptionRaised": true, "TrailerDwellExceeded": true,
		"TrailerParked": false, "DoorWorkStarted": false, "DoorWorkCompleted": true, "TrailerDeparted": true,
		"TrailerHeld": false, "TrailerHoldReleased": false, "TrailerSecured": true, "TrailerSecuringReleased": true,
	},
	outbox.AggregMove: {
		"YardMoveRequested": false, "YardMoveClaimed": false, "YardMoveLeaseExpired": false,
		"YardMoveCompleted": false, "YardMoveCancelled": false,
	},
	outbox.AggregCheck: {"YardCheckClosed": true},
}

// IsIntegration reports whether the event is published on the integration
// topic (and mirrored on analytics); ok=false for a type outside the catalogue.
func IsIntegration(aggregate, eventName string) (integration, ok bool) {
	integration, ok = published[aggregate][eventName]
	return integration, ok
}

// PublishedCount returns how many types the catalogue holds and how many of
// them are integration events.
func PublishedCount() (total, integration int) {
	for _, events := range published {
		for _, isIntegration := range events {
			total++
			if isIntegration {
				integration++
			}
		}
	}
	return total, integration
}

// maxCause bounds a dead-letter reason: Kafka headers are for operators, not
// for payloads.
const maxCause = 512

// Encoder maps claimed outbox rows to their Kafka wire form. The zero value
// is usable (it falls back to the fleet topics); a test overrides
// EventsTopic/AnalyticsTopic/DLQTopic so each run owns its own topics.
type Encoder struct {
	EventsTopic    string
	AnalyticsTopic string
	DLQTopic       string
}

// NewEncoder returns an Encoder on the fleet topics.
func NewEncoder() *Encoder {
	return &Encoder{EventsTopic: TopicEvents, AnalyticsTopic: TopicAnalytics, DLQTopic: TopicDLQ}
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// claimed is the parsed payload of one row.
type claimed struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// destination is one topic a row is published to, with the CloudEvents
// dataschema stream segment that names it.
type destination struct {
	topic  string
	stream string
}

// destinations returns where an occurrence goes: an integration type to the
// events topic AND the analytics twin, an analytics-only type to analytics
// only. The data is byte-identical on both; only the dataschema differs.
func (e *Encoder) destinations(integration bool) []destination {
	analytics := destination{topic: orDefault(e.AnalyticsTopic, TopicAnalytics), stream: cloudevents.StreamAnalytics}
	if !integration {
		return []destination{analytics}
	}
	return []destination{{topic: orDefault(e.EventsTopic, TopicEvents), stream: cloudevents.StreamEvents}, analytics}
}

// Encode maps row to its exact CloudEvents messages: one per destination
// topic. Every message carries the full `type` built from the row's aggregate
// segment and the payload's event name, `source` /warehouse/yard-management,
// `subject` and Kafka key the aggregate id, `time` the domain occurred-at UTC,
// the same CloudEvents `id` (a relay retry republishes the same bytes on both
// topics, and consumers dedupe on it), `dataschema`
// urn:warehouse:yard-management:<events|analytics>:<EventName>:v1 and the
// structured JSON value with the content-type header.
//
// ok=false marks a poison row - a payload that is not valid JSON, an event the
// service does not publish, or a row the envelope rejects (e.g. an empty
// aggregate id) - together with a human-readable cause: such a row is
// dead-lettered, never retried, because no retry can change what it is.
func (e *Encoder) Encode(row outbox.Row) (msgs []outbox.Message, cause string, ok bool) {
	var env claimed
	if err := json.Unmarshal(row.Payload, &env); err != nil {
		return nil, fmt.Sprintf("malformed outbox payload: %v", err), false
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return nil, fmt.Sprintf("%s.%s row has no data", row.AggregType, env.Type), false
	}
	integration, known := IsIntegration(row.AggregType, env.Type)
	if !known {
		return nil, fmt.Sprintf("%s.%s is not a published event of ADR 0004", row.AggregType, env.Type), false
	}
	dests := e.destinations(integration)
	out := make([]outbox.Message, 0, len(dests))
	for _, d := range dests {
		msg, cause, ok := e.message(row, env, d)
		if !ok {
			return nil, cause, false
		}
		out = append(out, msg)
	}
	return out, "", true
}

// message builds the wire form of row for one destination.
func (e *Encoder) message(row outbox.Row, env claimed, d destination) (outbox.Message, string, bool) {
	value, err := cloudevents.New(cloudevents.Spec{
		ID:        row.EventID,
		Entity:    row.AggregType,
		EventName: env.Type,
		Subject:   row.AggregID,
		Time:      row.OccurredAt,
		Stream:    d.stream,
		Version:   schemaVersion,
		Data:      env.Data,
	})
	if err != nil {
		return outbox.Message{}, fmt.Sprintf("unencodable %s.%s row: %v", row.AggregType, env.Type, err), false
	}
	ct := cloudevents.ContentTypeHeader()
	return outbox.Message{
		EventID:    row.EventID,
		Topic:      d.topic,
		EventType:  cloudevents.Type(row.AggregType, env.Type),
		Subject:    row.AggregID,
		Key:        []byte(row.AggregID),
		DataSchema: cloudevents.DataSchema(d.stream, env.Type, schemaVersion),
		Value:      value,
		Headers:    []outbox.Header{{Key: ct.Key, Value: string(ct.Value)}},
	}, "", true
}

// DeadLetter builds the dead-letter message of a poison row: the row's
// payload VERBATIM as the value (an operator sees exactly what failed and a
// redrive tool can replay it), the aggregate id as the key, an
// application/json content-type header and the cause in dlq-reason.
func (e *Encoder) DeadLetter(row outbox.Row, cause string) outbox.Message {
	if len(cause) > maxCause {
		cause = cause[:maxCause]
	}
	return outbox.Message{
		EventID: row.EventID,
		Topic:   orDefault(e.DLQTopic, TopicDLQ),
		Subject: row.AggregID,
		Key:     []byte(row.AggregID),
		Value:   row.Payload,
		Headers: []outbox.Header{{Key: "content-type", Value: "application/json"}, {Key: "dlq-reason", Value: cause}},
	}
}
