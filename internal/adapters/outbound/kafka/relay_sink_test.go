package kafka

import (
	"context"
	"errors"
	"strings"
	"testing"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/yard-management/internal/application/outbox"
)

type fakeWriter struct {
	got []kafkago.Message
	err error
}

func (f *fakeWriter) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	f.got = append(f.got, msgs...)
	return f.err
}

func TestRelaySinkWritesEachMessageToItsOwnTopicWithItsHeaders(t *testing.T) {
	w := &fakeWriter{}
	sink := NewRelaySinkWithWriter(w)
	err := sink.Send(context.Background(),
		outbox.Message{Topic: "t.events", Key: []byte("k1"), Value: []byte("v1"), Headers: []outbox.Header{{Key: "content-type", Value: "x"}}},
		outbox.Message{Topic: "t.analytics", Key: []byte("k1"), Value: []byte("v1")},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.got) != 2 || w.got[0].Topic != "t.events" || w.got[1].Topic != "t.analytics" || string(w.got[0].Key) != "k1" || string(w.got[0].Value) != "v1" {
		t.Fatalf("written %+v", w.got)
	}
	if len(w.got[0].Headers) != 1 || w.got[0].Headers[0].Key != "content-type" || string(w.got[0].Headers[0].Value) != "x" || len(w.got[1].Headers) != 0 {
		t.Fatalf("headers %+v", w.got)
	}
}

func TestRelaySinkRefusesAMessageWithoutATopicBeforeWritingAnything(t *testing.T) {
	w := &fakeWriter{}
	err := NewRelaySinkWithWriter(w).Send(context.Background(), outbox.Message{Topic: "t"}, outbox.Message{EventType: "x.Y"})
	if err == nil || !strings.Contains(err.Error(), "no topic") || len(w.got) != 0 {
		t.Fatalf("err = %v, written %d", err, len(w.got))
	}
}

func TestRelaySinkNothingToSendIsANoOpAndWriteErrorsAreWrapped(t *testing.T) {
	w := &fakeWriter{err: errors.New("broker down")}
	sink := NewRelaySinkWithWriter(w)
	if err := sink.Send(context.Background()); err != nil || len(w.got) != 0 {
		t.Fatalf("empty send = %v, written %d", err, len(w.got))
	}
	err := sink.Send(context.Background(), outbox.Message{Topic: "t"})
	if err == nil || !errors.Is(err, w.err) || !strings.Contains(err.Error(), "write 1 message") {
		t.Fatalf("err = %v", err)
	}
}

func TestRelaySinkCloseOnlyClosesAKafkaWriter(t *testing.T) {
	if err := NewRelaySinkWithWriter(&fakeWriter{}).Close(); err != nil {
		t.Fatal(err)
	}
	if err := NewRelaySink("broker.invalid:1").Close(); err != nil {
		t.Fatal(err)
	}
}
