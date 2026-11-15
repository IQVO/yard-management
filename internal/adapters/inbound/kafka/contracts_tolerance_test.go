package kafka_test

// Tolerance of the unknown (E4): the consumers must keep working when a
// producer ships MORE than this service reads.
//
//   - unknown ADDITIVE data keys must not fail decode or dispatch (the
//     tolerant-reader rule; the payload decoders ignore unknown fields), and an
//     unknown CloudEvents EXTENSION attribute must survive the SDK's Validate —
//     extension names must be lowercase-alphanumeric, so the probe key is
//     "xprobe", never "x_probe";
//   - an event TYPE the consumer has no handler for is skipped per this repo's
//     documented policy (WARN, commit past, never dead-lettered) — probed with
//     the producer's OWN example of a documented-but-unrouted type, not a
//     synthetic one;
//   - a message that is not a CloudEvent is skipped per the same policy (WARN
//     and commit past; the type is unknowable, so there is nothing to replay
//     from the DLQ).

import (
	"testing"

	"github.com/claudioed/yard-management/internal/adapters/inbound/kafka"
)

// TestUnknownAttributesTolerated runs the three probes for every
// subscription: additive data + extension attribute, an unrouted type's real
// example, and a non-CloudEvent message.
func TestUnknownAttributesTolerated(t *testing.T) {
	for _, sub := range goldenSubs() {
		t.Run(sub.name, func(t *testing.T) {
			spec := loadPinnedAsyncAPI(t, sub.spec)
			toleratedAdditiveAttributes(t, sub, spec)
			skipsUnroutedTypeByExample(t, sub, spec)
			skipsNotACloudEvent(t, sub)
		})
	}
}

// goldenOfRouted returns the first example of a routed type.
func goldenOfRouted(t *testing.T, spec *pinnedAsyncAPI, c *kafka.Consumer) goldenEvent {
	t.Helper()
	for _, m := range spec.channelDocumentedMessages(c.Topic) {
		if _, routed := c.Handlers[m.Type]; routed && len(m.Examples) > 0 {
			return cloneGolden(t, m, m.Examples[0], map[string]bool{})
		}
	}
	t.Fatalf("no routed example on %q", c.Topic)
	return goldenEvent{}
}

// goldenOfUnrouted returns the first example of a documented type the consumer
// deliberately does not route on.
func goldenOfUnrouted(t *testing.T, spec *pinnedAsyncAPI, c *kafka.Consumer) goldenEvent {
	t.Helper()
	for _, m := range spec.channelDocumentedMessages(c.Topic) {
		if _, routed := c.Handlers[m.Type]; !routed && len(m.Examples) > 0 {
			return cloneGolden(t, m, m.Examples[0], map[string]bool{})
		}
	}
	t.Fatalf("no documented-but-unrouted example on %q to probe the skip policy with", c.Topic)
	return goldenEvent{}
}

// toleratedAdditiveAttributes injects an unknown data key and an unknown
// lowercase-alphanumeric CloudEvents extension attribute into a routed event
// and asserts it still decodes, dispatches exactly once and applies.
func toleratedAdditiveAttributes(t *testing.T, sub goldenSub, spec *pinnedAsyncAPI) {
	t.Helper()
	sink := sub.sink()
	fk := newFakeKafka()
	c := sub.build(t, fk.factory(), sink)
	golden := goldenOfRouted(t, spec, c)

	data, ok := golden.payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("%s: golden %s carries no data object", sub.name, golden.Type)
	}
	data["x_future_field"] = "an additive key some future producer version ships"
	golden.payload["xprobe"] = "a lowercase-alphanumeric extension attribute"

	pass, probe := runGolden(t, sub, []goldenEvent{golden}, sink)
	if bad := probe.notExactlyOnce([]string{golden.ID}); len(bad) > 0 {
		t.Errorf("%s: additive attributes must not break dispatch: %v", sub.name, bad)
	}
	if len(pass.dlq.letters()) != 0 {
		t.Errorf("%s: additive attributes must not dead-letter: %s", sub.name, pass.dlq.letters()[0].cause)
	}
	if len(sub.calls(sink)) != 1 {
		t.Errorf("%s: additive attributes must not stop the effect (got %d application(s))", sub.name, len(sub.calls(sink)))
	}
}

// skipsUnroutedTypeByExample feeds the producer's own example of a documented
// type the consumer does not route on, and asserts the documented policy:
// skipped, committed past, never dead-lettered.
func skipsUnroutedTypeByExample(t *testing.T, sub goldenSub, spec *pinnedAsyncAPI) {
	t.Helper()
	sink := sub.sink()
	fk := newFakeKafka()
	c := sub.build(t, fk.factory(), sink)
	unknown := goldenOfUnrouted(t, spec, c)

	pass, probe := runGolden(t, sub, []goldenEvent{unknown}, sink)
	if n := probe.times(unknown.ID); n != 0 {
		t.Errorf("%s: %q was dispatched %d time(s); this consumer has no handler for it and must skip it",
			sub.name, unknown.Type, n)
	}
	if len(pass.dlq.letters()) != 0 {
		t.Errorf("%s: an unrouted type must be skipped, not dead-lettered", sub.name)
	}
	if calls := sub.calls(sink); len(calls) != 0 {
		t.Errorf("%s: an unrouted type must not apply anything (got %v)", sub.name, calls)
	}
	assertCommittedPast(t, sub, pass)
}

// skipsNotACloudEvent feeds a message without the CloudEvents 1.0 envelope and
// asserts the repo's documented policy: WARN and commit past, never a dead
// letter (the type is unknowable, there is nothing to replay from the DLQ).
func skipsNotACloudEvent(t *testing.T, sub goldenSub) {
	t.Helper()
	sink := sub.sink()
	values := [][]byte{[]byte(`{"id":"no-specversion","type":"com.warehouse.wms.someone.SomeThing","data":{}}`)}
	fk := newFakeKafka(values...)
	c := sub.build(t, fk.factory(), sink)
	c.Retry = fastRetry
	drain(t, c)
	if letters := fk.dlq.letters(); len(letters) != 0 {
		t.Errorf("%s: a non-CloudEvent must be skipped with a WARN, never dead-lettered", sub.name)
	}
	if calls := sub.calls(sink); len(calls) != 0 {
		t.Errorf("%s: a non-CloudEvent must not apply anything (got %v)", sub.name, calls)
	}
	assertCommittedPast(t, sub, fk)
}

// assertCommittedPast proves the run loop moved the offset beyond the message.
func assertCommittedPast(t *testing.T, sub goldenSub, fk *fakeKafka) {
	t.Helper()
	if commits := fk.reader.committed(); len(commits) == 0 {
		t.Errorf("%s: the offset was never committed past the skipped message", sub.name)
	}
}
