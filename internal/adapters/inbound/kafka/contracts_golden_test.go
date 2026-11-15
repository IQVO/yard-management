package kafka_test

// Golden consumer contract tests against the pinned producer specs (E2, E3).
//
// E2 — TestGoldenEventsDecode: every message the producer's pinned spec
// documents on a subscribed channel WITH examples is run through the REAL
// decode + dispatch path (the consumer's own HandleMessage via its run loop):
// exactly one routing per event, no dead letters, and CloudEvents-id dedupe on
// replay (the sink claims each id exactly once, the processed_events rule).
// Fixture provenance: the specs' own examples, byte-for-byte. A documented type
// without examples is a NOTE (E1 covers it; producer example backfill pending).
// facility-layout reuses one CloudEvents id across a message's examples; such
// clones are minted a fresh id so every example still exercises routing.
//
// E3 — TestGoldenEnvelopeInvariants: every golden example carries the fleet's
// CloudEvents 1.0 envelope: specversion 1.0, id/source/subject/time present,
// datacontenttype (per-message or inherited from the spec's default), and a
// dataschema under urn:warehouse:<producer>: whose name segment matches the
// documented event name (analytics twins reuse the integration event name).

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/yard-management/internal/adapters/inbound/kafka"
	"github.com/claudioed/yard-management/internal/analytics/report"
	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
)

// contractSink stands in for the three use-case interfaces, claiming each
// CloudEvents id exactly once (the processed_events rule of the real
// UnitOfWork) and recording one line per first application.
type contractSink struct {
	mu     sync.Mutex
	claims map[string]bool
	calls  []string
}

func newContractSink() *contractSink { return &contractSink{claims: map[string]bool{}} }

// claim reports whether id is fresh; a seen id is a replay to be deduped.
func (s *contractSink) claim(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claims[id] {
		return false
	}
	s.claims[id] = true
	return true
}

// apply claims id and records one line, or dedupes a replay (the real
// UnitOfWork's processed_events rule).
func (s *contractSink) apply(id, line string) error {
	if !s.claim(id) {
		return nil
	}
	return s.record(line)
}

func (s *contractSink) applied() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func (s *contractSink) record(line string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, line)
	return nil
}

// The dock-scheduling use cases (views/usecases mirror the recorder's).
func (s *contractSink) ApplyAppointment(_ context.Context, id string, _ usecases.AppointmentFact) error {
	return s.apply(id, "appointment "+id)
}
func (s *contractSink) ScheduleClosure(_ context.Context, id string, _ views.Closure) error {
	return s.apply(id, "closure "+id)
}
func (s *contractSink) LiftClosure(_ context.Context, id, _ string) error {
	return s.apply(id, "lift "+id)
}
func (s *contractSink) ApplyCarrierRegistered(_ context.Context, id string, _ views.Carrier, _ time.Time) error {
	return s.apply(id, "carrier-registered "+id)
}
func (s *contractSink) ApplyCarrierSuspended(_ context.Context, id, _, _ string, _ int64, _ time.Time) error {
	return s.apply(id, "carrier-suspended "+id)
}
func (s *contractSink) ApplyCarrierReinstated(_ context.Context, id, _ string, _ int64, _ time.Time) error {
	return s.apply(id, "carrier-reinstated "+id)
}

// The facility use cases.
func (s *contractSink) ApplyZone(_ context.Context, id, _, _ string) error {
	return s.apply(id, "zone "+id)
}
func (s *contractSink) ApplySlot(_ context.Context, id, _, _, _, _, _ string) error {
	return s.apply(id, "slot "+id)
}
func (s *contractSink) DecommissionSlot(_ context.Context, id, _ string) error {
	return s.apply(id, "decommission "+id)
}

// The inbound-receiving use cases.
func (s *contractSink) RegisterAsn(_ context.Context, id, _ string) error {
	return s.apply(id, "asn "+id)
}
func (s *contractSink) CancelAsn(_ context.Context, id, _ string) error {
	return s.apply(id, "asn-cancel "+id)
}
func (s *contractSink) OpenReceipt(_ context.Context, id string, _ views.Receipt) error {
	return s.apply(id, "receipt-open "+id)
}
func (s *contractSink) CloseReceipt(_ context.Context, id, _, _ string, _ time.Time) error {
	return s.apply(id, "receipt-close "+id)
}

// Compile-time proof the sink stands in for every use-case interface.
var (
	_ kafka.DockSchedulingUseCases   = (*contractSink)(nil)
	_ kafka.FacilityUseCases         = (*contractSink)(nil)
	_ kafka.InboundReceivingUseCases = (*contractSink)(nil)
)

// contractProjection stands in for report.Projection with the same id-claim.
type contractProjection struct {
	mu     sync.Mutex
	claims map[string]bool
	kinds  []string
}

func (p *contractProjection) Apply(_ context.Context, e report.Event) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.claims[e.EventID] {
		return false, nil // the projector's replay of an already-applied id
	}
	p.claims[e.EventID] = true
	p.kinds = append(p.kinds, string(e.Kind))
	return true, nil
}

func (p *contractProjection) applied() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.kinds...)
}

// goldenSub is one subscription with a builder over a shared sink, so a
// replay pass reuses the first pass's id claims.
type goldenSub struct {
	name  string
	spec  string
	build func(t *testing.T, f kafka.Factory, sink any) *kafka.Consumer
	sink  func() any
	calls func(sink any) []string
}

// goldenSubs pairs each subscription with its sink plumbing.
func goldenSubs() []goldenSub {
	dock := func(t *testing.T, f kafka.Factory, sink any) *kafka.Consumer {
		c, err := kafka.NewDockSchedulingConsumer(f, "contract-test", sink.(*contractSink), quietLogger())
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	facility := func(t *testing.T, f kafka.Factory, sink any) *kafka.Consumer {
		c, err := kafka.NewFacilityConsumer(f, "contract-test", sink.(*contractSink), quietLogger())
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	inbound := func(t *testing.T, f kafka.Factory, sink any) *kafka.Consumer {
		c, err := kafka.NewInboundReceivingConsumer(f, "contract-test", sink.(*contractSink), quietLogger())
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	analytics := func(t *testing.T, f kafka.Factory, sink any) *kafka.Consumer {
		c, err := kafka.NewAnalyticsConsumer(f, "contract-test", sink.(*contractProjection), quietLogger())
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	sinkCalls := func(s any) []string { return s.(*contractSink).applied() }
	projCalls := func(s any) []string { return s.(*contractProjection).applied() }
	return []goldenSub{
		{"dock-scheduling", "dock-scheduling.asyncapi.yaml", dock,
			func() any { return newContractSink() }, sinkCalls},
		{"facility-layout", "facility-layout.asyncapi.yaml", facility,
			func() any { return newContractSink() }, sinkCalls},
		{"inbound-receiving", "inbound-receiving.asyncapi.yaml", inbound,
			func() any { return newContractSink() }, sinkCalls},
		{"yard-management", "yard-management.asyncapi.yaml", analytics,
			func() any { return &contractProjection{claims: map[string]bool{}} }, projCalls},
	}
}

// goldenEvent is one spec example readied for the broker.
type goldenEvent struct {
	Type    string
	ID      string
	payload map[string]any
}

// collectGolden lifts the spec's own examples of the routed types, minting a
// fresh id for a clone that reuses another example's id (facility-layout does).
func collectGolden(t *testing.T, spec *pinnedAsyncAPI, c *kafka.Consumer) []goldenEvent {
	t.Helper()
	seen := map[string]bool{}
	var out []goldenEvent
	for _, m := range spec.channelDocumentedMessages(c.Topic) {
		if _, routed := c.Handlers[m.Type]; !routed {
			continue
		}
		if len(m.Examples) == 0 {
			t.Logf("NOTE %s documents %q with no examples: covered by the pinned-spec test only; producer example backfill pending", spec.t.Name(), m.Type)
			continue
		}
		for _, ex := range m.Examples {
			out = append(out, cloneGolden(t, m, ex, seen))
		}
	}
	if len(out) == 0 {
		t.Fatalf("no golden examples collected for %q", c.Topic)
	}
	return out
}

// cloneGolden copies one example, minting a fresh id when the spec reused one.
func cloneGolden(t *testing.T, m documentedMessage, ex map[string]any, seen map[string]bool) goldenEvent {
	t.Helper()
	payload := map[string]any{}
	for k, v := range ex {
		payload[k] = v
	}
	id, _ := payload["id"].(string)
	if id == "" || seen[id] {
		fresh := uuid.NewString()
		t.Logf("NOTE %s: example of %q reuses CloudEvents id %q; the clone is minted a fresh id so it still routes", m.Name, m.Type, id)
		id = fresh
		payload["id"] = fresh
	}
	seen[id] = true
	return goldenEvent{Type: m.Type, ID: id, payload: payload}
}

// goldenBytes serializes a golden payload the way the producer ships it.
func goldenBytes(t *testing.T, g goldenEvent) []byte {
	t.Helper()
	raw, err := json.Marshal(normalizeYAML(g.payload))
	if err != nil {
		t.Fatalf("marshal golden %s: %v", g.Type, err)
	}
	return raw
}

// normalizeYAML re-renders the scalar types yaml.v3 produces that JSON cannot
// carry (time.Time) and leaves everything else untouched.
func normalizeYAML(v any) any {
	switch n := v.(type) {
	case time.Time:
		return n.UTC().Format(time.RFC3339Nano)
	case map[string]any:
		for k, item := range n {
			n[k] = normalizeYAML(item)
		}
		return n
	case []any:
		for i, item := range n {
			n[i] = normalizeYAML(item)
		}
		return n
	default:
		return v
	}
}

// routingProbe counts, per CloudEvents id, how often the consumer DISPATCHED
// the event to its type's handler — the observable definition of routing,
// independent of the business filters a handler may apply after dispatch
// (a slot that is neither Yard nor Dock never reaches the use case).
type routingProbe struct {
	mu     sync.Mutex
	counts map[string]int
}

func newRoutingProbe() *routingProbe { return &routingProbe{counts: map[string]int{}} }

func (p *routingProbe) mark(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts[id]++
}

// wrap instruments every registered handler of c.
func (p *routingProbe) wrap(c *kafka.Consumer) {
	for typ, h := range c.Handlers {
		h := h
		c.Handlers[typ] = func(ctx context.Context, e ce.Event) error {
			p.mark(e.ID())
			return h(ctx, e)
		}
	}
}

// exactlyOnce reports the ids that were not dispatched exactly once.
func (p *routingProbe) notExactlyOnce(want []string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var bad []string
	for _, id := range want {
		if p.counts[id] != 1 {
			bad = append(bad, fmt.Sprintf("%s routed %d time(s)", id, p.counts[id]))
		}
	}
	return bad
}

// times reports how often id was dispatched to a handler.
func (p *routingProbe) times(id string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counts[id]
}

// runGolden drains one pass of the golden bytes through the real consumer,
// handler dispatch instrumented by the returned probe.
func runGolden(t *testing.T, sub goldenSub, goldens []goldenEvent, sink any) (*fakeKafka, *routingProbe) {
	t.Helper()
	values := make([][]byte, len(goldens))
	for i, g := range goldens {
		values[i] = goldenBytes(t, g)
	}
	fk := newFakeKafka(values...)
	c := sub.build(t, fk.factory(), sink)
	probe := newRoutingProbe()
	probe.wrap(c)
	c.Retry = fastRetry
	drain(t, c)
	return fk, probe
}

// TestGoldenEventsDecode (E2): the pinned spec's own examples of every routed
// type are dispatched exactly once each through the real decode + dispatch
// path, nothing dead-letters, and a replay under the same id claims applies
// nothing new (the processed_events dedupe of the real UnitOfWork).
func TestGoldenEventsDecode(t *testing.T) {
	for _, sub := range goldenSubs() {
		t.Run(sub.name, func(t *testing.T) {
			spec := loadPinnedAsyncAPI(t, sub.spec)
			sink := sub.sink()
			fk := newFakeKafka()
			c := sub.build(t, fk.factory(), sink)
			goldens := collectGolden(t, spec, c)
			ids := goldenIDs(goldens)

			first, probe := runGolden(t, sub, goldens, sink)
			if bad := probe.notExactlyOnce(ids); len(bad) > 0 {
				t.Errorf("%s: want exactly one routing per golden event:\n%s", sub.name, strings.Join(bad, "\n"))
			}
			for _, l := range first.dlq.letters() {
				t.Errorf("%s: golden event dead-lettered: %s", sub.name, l.cause)
			}
			if filtered := len(ids) - len(sub.calls(sink)); filtered > 0 {
				t.Logf("NOTE %s: %d golden event(s) dispatched but filtered by a documented business rule before the use case (no dead letter)",
					sub.name, filtered)
			}
			before := len(sub.calls(sink))
			runGolden(t, sub, goldens, sink)
			if after := len(sub.calls(sink)); after != before {
				t.Errorf("%s: replay applied %d new event(s); the CloudEvents id dedupe must make a replay a no-op", sub.name, after-before)
			}
		})
	}
}

// goldenIDs lists the ids of the golden events, in order.
func goldenIDs(goldens []goldenEvent) []string {
	ids := make([]string, len(goldens))
	for i, g := range goldens {
		ids[i] = g.ID
	}
	return ids
}

// TestGoldenEnvelopeInvariants (E3): every example on a subscribed channel of
// every pinned spec carries the fleet CloudEvents 1.0 envelope.
func TestGoldenEnvelopeInvariants(t *testing.T) {
	for _, sub := range pinnedSubscriptions() {
		t.Run(sub.name, func(t *testing.T) {
			spec := loadPinnedAsyncAPI(t, sub.spec)
			fk := newFakeKafka()
			c := sub.build(t, fk.factory())
			messages := spec.channelDocumentedMessages(c.Topic)
			checked := 0
			for _, m := range messages {
				for _, ex := range m.Examples {
					assertEnvelope(t, sub.name, m, ex, spec)
					checked++
				}
			}
			if checked == 0 {
				t.Fatalf("%s: no examples to check on %q", sub.name, c.Topic)
			}
		})
	}
}

// assertEnvelope checks one golden example's envelope invariants.
func assertEnvelope(t *testing.T, subName string, m documentedMessage, ex map[string]any, spec *pinnedAsyncAPI) {
	t.Helper()
	where := fmt.Sprintf("%s %s example", subName, m.Type)
	assertStringField(t, where, ex, "specversion", "1.0")
	for _, field := range []string{"id", "source", "subject", "time"} {
		if s, ok := ex[field].(string); !ok || s == "" {
			t.Errorf("%s: envelope field %q must be a non-empty string", where, field)
		}
	}
	assertContentType(t, where, ex, spec)
	assertDataSchema(t, where, subName, m, ex)
}

// assertStringField asserts a field equals want.
func assertStringField(t *testing.T, where string, ex map[string]any, field, want string) {
	t.Helper()
	if got, _ := ex[field].(string); got != want {
		t.Errorf("%s: %s = %q, want %q", where, field, got, want)
	}
}

// assertContentType accepts a per-message datacontenttype or the spec's
// defaultContentType (some fleet specs inherit it).
func assertContentType(t *testing.T, where string, ex map[string]any, spec *pinnedAsyncAPI) {
	t.Helper()
	if s, ok := ex["datacontenttype"].(string); ok && s != "" {
		return
	}
	if def, ok := spec.doc["defaultContentType"].(string); ok && def != "" {
		return
	}
	t.Errorf("%s: neither datacontenttype nor a spec defaultContentType", where)
}

// assertDataSchema pins urn:warehouse:<producer>:<stream>:<Name>:vN — the
// analytics twins reuse the INTEGRATION event name in the name segment.
func assertDataSchema(t *testing.T, where, subName string, m documentedMessage, ex map[string]any) {
	t.Helper()
	ds, ok := ex["dataschema"].(string)
	if !ok || ds == "" {
		t.Errorf("%s: dataschema must be a non-empty string", where)
		return
	}
	prefix := "urn:warehouse:" + subName + ":"
	if !strings.HasPrefix(ds, prefix) {
		t.Errorf("%s: dataschema %q must start with %q", where, ds, prefix)
		return
	}
	name, version := splitDataSchema(ds[len(prefix):])
	if version == "" {
		return // no .vN suffix documented: nothing more to assert
	}
	lastType := m.Type[strings.LastIndex(m.Type, ".")+1:]
	if name != lastType && name != m.Name {
		t.Errorf("%s: dataschema %q names %q but the event is %q (message name %q)",
			where, ds, name, lastType, m.Name)
	}
}

// splitDataSchema splits "<stream>:<Name>:vN" into its name and version.
func splitDataSchema(rest string) (name, version string) {
	parts := strings.Split(rest, ":")
	if len(parts) < 2 {
		return "", ""
	}
	last := parts[len(parts)-1]
	if !strings.HasPrefix(last, "v") {
		return "", ""
	}
	if _, err := strconv.Atoi(last[1:]); err != nil {
		return "", ""
	}
	return parts[len(parts)-2], last
}
