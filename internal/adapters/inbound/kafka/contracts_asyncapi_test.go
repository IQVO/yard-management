package kafka_test

// Pinned AsyncAPI coverage of this service's subscriptions (E1). The four
// specs under testdata/contracts are byte-copies pinned in contracts/pinned.json
// (dock-scheduling, facility-layout, inbound-receiving and this repo's own
// yard-management spec for the analytics self-subscription). The subscription
// list is derived from the reader wiring code — the topic constants the
// consumer constructors feed into Factory.NewReader — never from the specs:
// a pinned spec is the message catalog, not the source of truth for what this
// service consumes.
//
// The fleet's spec shapes are not uniform (verified this wave): dock-scheduling,
// facility-layout and inbound-receiving publish AsyncAPI 2.6 with the
// producer's own topics under `subscribe:` and message.oneOf $refs; this repo's
// own spec is AsyncAPI 3.0 with channels keyed by logical name, an `address`
// per channel and channel-level `messages` maps. The loader below reads both.

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/claudioed/yard-management/internal/adapters/inbound/kafka"
)

const pinnedContractsDir = "testdata/contracts"

// pinnedAsyncAPI is a pinned spec loaded for inspection.
type pinnedAsyncAPI struct {
	t   *testing.T
	doc map[string]any
	raw string
}

// loadPinnedAsyncAPI reads one pinned spec from testdata/contracts.
func loadPinnedAsyncAPI(t *testing.T, file string) *pinnedAsyncAPI {
	t.Helper()
	raw, err := os.ReadFile(pinnedContractsDir + "/" + file)
	if err != nil {
		t.Fatalf("read pinned spec %s: %v", file, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse pinned spec %s: %v", file, err)
	}
	return &pinnedAsyncAPI{t: t, doc: doc, raw: string(raw)}
}

// asMap asserts a node is a mapping and returns it.
func (s *pinnedAsyncAPI) asMap(node any, where string) map[string]any {
	s.t.Helper()
	m, ok := node.(map[string]any)
	if !ok {
		s.t.Fatalf("pinned spec: %s is not a mapping (got %T)", where, node)
	}
	return m
}

// resolve follows $ref chains inside the spec, merging any sibling keys.
func (s *pinnedAsyncAPI) resolve(node any, depth int) any {
	if depth > 8 {
		return node
	}
	m, ok := node.(map[string]any)
	if !ok {
		return node
	}
	ref, ok := m["$ref"].(string)
	if !ok || len(m) > 2 {
		return s.resolveMapEntries(m, depth)
	}
	target := s.doc
	for _, seg := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		next, ok := target[seg].(map[string]any)
		if !ok {
			s.t.Fatalf("pinned spec: unresolvable $ref %q at segment %q", ref, seg)
		}
		target = next
	}
	merged := map[string]any{}
	for k, v := range s.asMap(s.resolve(target, depth+1), "ref target of "+ref) {
		merged[k] = v
	}
	for k, v := range m {
		if k != "$ref" {
			merged[k] = v
		}
	}
	return merged
}

// resolveMapEntries resolves the values of a mapping, keeping its keys.
func (s *pinnedAsyncAPI) resolveMapEntries(m map[string]any, depth int) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = s.resolve(v, depth+1)
	}
	return out
}

// documentedMessage is one message a channel of a pinned spec documents.
type documentedMessage struct {
	Key         string
	Name        string
	ContentType string
	Type        string           // the full CloudEvents type the payload schema fixes
	Examples    []map[string]any // whole-envelope golden payloads
}

// channelDocumentedMessages returns every message the spec documents on the
// given topic, in spec order. It reads both fleet shapes: AsyncAPI 2.6
// (channels keyed by topic; the producer's own topics under subscribe: or,
// app-perspective, under publish:) and AsyncAPI 3.0 (channels keyed by logical
// name with an address; channel-level messages maps).
func (s *pinnedAsyncAPI) channelDocumentedMessages(topic string) []documentedMessage {
	channel := s.channelOf(topic)
	var refs []any
	for _, opKey := range []string{"subscribe", "publish"} {
		op, ok := channel[opKey].(map[string]any)
		if !ok {
			continue
		}
		msg, _ := op["message"].(map[string]any)
		if oneOf, ok := msg["oneOf"].([]any); ok {
			refs = append(refs, oneOf...)
		} else if msg != nil {
			refs = append(refs, msg)
		}
	}
	if channelMsgs, ok := channel["messages"].(map[string]any); ok {
		for _, v := range channelMsgs {
			refs = append(refs, v)
		}
	}
	if len(refs) == 0 {
		s.t.Fatalf("pinned spec documents no messages on topic %q", topic)
	}
	out := make([]documentedMessage, 0, len(refs))
	for i, ref := range refs {
		m := s.asMap(s.resolve(ref, 0), "channel message")
		out = append(out, s.messageAt(i, m))
	}
	return out
}

// channelOf finds the channel entry of a topic by key (2.6) or address (3.0).
func (s *pinnedAsyncAPI) channelOf(topic string) map[string]any {
	channels := s.asMap(s.doc["channels"], "channels")
	if ch, ok := channels[topic].(map[string]any); ok {
		return ch
	}
	for _, ch := range channels {
		m, ok := ch.(map[string]any)
		if ok && m["address"] == topic {
			return m
		}
	}
	s.t.Fatalf("pinned spec does not document topic %q", topic)
	return nil
}

// messageAt lifts one resolved channel message into a documentedMessage.
func (s *pinnedAsyncAPI) messageAt(_ int, m map[string]any) documentedMessage {
	msg := documentedMessage{Name: anonymousMessage}
	if name, ok := m["name"].(string); ok {
		msg.Name, msg.Key = name, name
	}
	if ct, ok := m["contentType"].(string); ok {
		msg.ContentType = ct
	}
	msg.Type = s.schemaType(m["payload"])
	if msg.Type == "" {
		msg.Type = exampleType(m)
	}
	for _, ex := range examplesOf(m) {
		if payload, ok := ex.(map[string]any); ok {
			msg.Examples = append(msg.Examples, payload)
		}
	}
	return msg
}

// anonymousMessage names a spec message that carries no name of its own.
const anonymousMessage = "<anonymous>"

// schemaType extracts the CloudEvents type a payload schema fixes: the const,
// or the single value of an enum, searched through allOf wrappers.
func (s *pinnedAsyncAPI) schemaType(node any) string {
	m, ok := node.(map[string]any)
	if !ok {
		return ""
	}
	if t := typeConstOf(m["properties"]); t != "" {
		return t
	}
	if allOf, ok := m["allOf"].([]any); ok {
		for _, sub := range allOf {
			if t := s.schemaType(sub); t != "" {
				return t
			}
		}
	}
	return ""
}

// typeConstOf reads properties.type.const (or a one-value enum) of a schema.
func typeConstOf(props any) string {
	m, ok := props.(map[string]any)
	if !ok {
		return ""
	}
	t, ok := m["type"].(map[string]any)
	if !ok {
		return ""
	}
	if c, ok := t["const"].(string); ok {
		return c
	}
	if enum, ok := t["enum"].([]any); ok && len(enum) == 1 {
		if s, ok := enum[0].(string); ok {
			return s
		}
	}
	return ""
}

// examplesOf returns the whole-envelope payload of every example of a message.
func examplesOf(m map[string]any) []any {
	var out []any
	for _, ex := range drill(m, "examples") {
		if e, ok := ex.(map[string]any); ok {
			if p, ok := e["payload"]; ok {
				out = append(out, p)
			}
		}
	}
	return out
}

// exampleType reads the CloudEvents type off a message's first example.
func exampleType(m map[string]any) string {
	for _, p := range examplesOf(m) {
		if payload, ok := p.(map[string]any); ok {
			if t, ok := payload["type"].(string); ok {
				return t
			}
		}
	}
	return ""
}

// drill returns doc[path] as a slice, tolerating a missing key.
func drill(m map[string]any, path string) []any {
	v, ok := m[path].([]any)
	if !ok {
		return nil
	}
	return v
}

// subscription is one thing this service consumes, built from the reader
// wiring: the topic constant the constructor feeds Factory.NewReader and the
// handlers it registers.
type subscription struct {
	name        string // producer context (or "yard-management" for the self-pin)
	spec        string // pinned spec file
	urnProducer string // the <producer> segment of urn:warehouse:<producer>:...
	build       func(t *testing.T, f kafka.Factory) *kafka.Consumer
}

// pinnedSubscriptions is THE subscription table, derived from the reader
// config: each consumer constructor wires its topic constant into
// Factory.NewReader (kafka.readerConfig) and registers its handlers.
func pinnedSubscriptions() []subscription {
	return []subscription{
		{"dock-scheduling", "dock-scheduling.asyncapi.yaml", "dock-scheduling", func(t *testing.T, f kafka.Factory) *kafka.Consumer {
			c, err := kafka.NewDockSchedulingConsumer(f, "contract-test", &recorder{}, quietLogger())
			if err != nil {
				t.Fatal(err)
			}
			return c
		}},
		{"facility-layout", "facility-layout.asyncapi.yaml", "facility-layout", func(t *testing.T, f kafka.Factory) *kafka.Consumer {
			c, err := kafka.NewFacilityConsumer(f, "contract-test", &recorder{}, quietLogger())
			if err != nil {
				t.Fatal(err)
			}
			return c
		}},
		{"inbound-receiving", "inbound-receiving.asyncapi.yaml", "inbound-receiving", func(t *testing.T, f kafka.Factory) *kafka.Consumer {
			c, err := kafka.NewInboundReceivingConsumer(f, "contract-test", &recorder{}, quietLogger())
			if err != nil {
				t.Fatal(err)
			}
			return c
		}},
		{"yard-management", "yard-management.asyncapi.yaml", "yard-management", func(t *testing.T, f kafka.Factory) *kafka.Consumer {
			c, err := kafka.NewAnalyticsConsumer(f, "contract-test", &fakeProjection{}, quietLogger())
			if err != nil {
				t.Fatal(err)
			}
			return c
		}},
	}
}

// TestPinnedAsyncAPICoversSubscriptions (E1): every topic this service's
// reader config consumes is documented in the producer's pinned spec, and
// every CloudEvents type the consumer routes on is documented on that topic's
// channel. Types documented but not routed are NOTEs, not failures: the
// consumer's policy is to ignore types it has no handler for.
func TestPinnedAsyncAPICoversSubscriptions(t *testing.T) {
	for _, sub := range pinnedSubscriptions() {
		t.Run(sub.name, func(t *testing.T) {
			assertSubscriptionCovered(t, sub)
		})
	}
}

// assertSubscriptionCovered checks one subscription against its pinned spec.
func assertSubscriptionCovered(t *testing.T, sub subscription) {
	t.Helper()
	spec := loadPinnedAsyncAPI(t, sub.spec)
	fk := newFakeKafka()
	c := sub.build(t, fk.factory())
	messages := spec.channelDocumentedMessages(c.Topic)
	documented := documentedTypes(t, messages)
	for typ := range c.Handlers {
		if !documented[typ] {
			t.Errorf("%s: consumer routes on %q but the pinned spec documents no such type on %q",
				sub.name, typ, c.Topic)
		}
	}
	for _, m := range messages {
		if _, routed := c.Handlers[m.Type]; !routed {
			t.Logf("NOTE %s: %q is documented on %q but deliberately not routed (policy: ignored)",
				sub.name, m.Type, c.Topic)
		}
	}
	t.Logf("%s: %d handler type(s) covered by %d documented message(s) on %q",
		sub.name, len(c.Handlers), len(messages), c.Topic)
}

// documentedTypes indexes the CloudEvents types a channel's messages fix.
func documentedTypes(t *testing.T, messages []documentedMessage) map[string]bool {
	t.Helper()
	documented := map[string]bool{}
	for _, m := range messages {
		if m.Type == "" {
			t.Errorf("message %q documents no CloudEvents type", m.Name)
			continue
		}
		documented[m.Type] = true
	}
	return documented
}

// TestDeadLetterTopicsFollowTheFleetConvention pins the dead-letter topic
// naming the run loop uses: every consumed topic carries a .dlq twin
// (DLQSuffix), and this repo's own spec documents the events one. The events
// and analytics topics are named by literals (not the outbound encoder's
// constants) because inbound adapters — their test packages included — never
// import outbound ones, the arch test's rule.
func TestDeadLetterTopicsFollowTheFleetConvention(t *testing.T) {
	const ownEventsTopic = "warehouse.yard-management.events"
	for _, topic := range []string{
		kafka.DockSchedulingTopic, kafka.FacilityTopic, kafka.InboundReceivingTopic, kafka.AnalyticsTopic,
		ownEventsTopic,
	} {
		if got := kafka.DeadLetterTopic(topic); got != topic+".dlq" {
			t.Errorf("DeadLetterTopic(%q) = %q, want %q", topic, got, topic+".dlq")
		}
	}
	own := loadPinnedAsyncAPI(t, "yard-management.asyncapi.yaml")
	if !strings.Contains(own.raw, ownEventsTopic+".dlq") {
		t.Errorf("own spec does not document the %s dead-letter topic", ownEventsTopic+".dlq")
	}
	t.Logf("NOTE yard-management: the own spec documents %s.dlq but not %s.dlq (the projector's dead letter, documented in code); spec backfill pending",
		ownEventsTopic, kafka.AnalyticsTopic)
}
