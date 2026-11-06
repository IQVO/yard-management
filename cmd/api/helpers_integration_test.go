//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	// Real Postgres and Kafka come from the shared testcontainers helpers.
	_ "github.com/testcontainers/testcontainers-go/modules/kafka"
	_ "github.com/testcontainers/testcontainers-go/modules/postgres"

	outboundkafka "github.com/claudioed/yard-management/internal/adapters/outbound/kafka"
	"github.com/claudioed/yard-management/internal/adapters/outbound/postgres"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/testing/kafkatest"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
}

// api is a booted cmd/api run() and a client for it.
type api struct {
	t    *testing.T
	base string
	done chan error
	stop context.CancelFunc
	keys int
	mu   sync.Mutex
}

type reply struct {
	Code   int
	Header http.Header
	Body   []byte
}

func (r reply) JSON() map[string]any {
	var m map[string]any
	if err := json.Unmarshal(r.Body, &m); err != nil {
		panic(fmt.Sprintf("%v: %s", err, r.Body))
	}
	return m
}

func (r reply) Slug() string {
	s, _ := r.JSON()["type"].(string)
	return s[strings.LastIndex(s, "/")+1:]
}

func (a *api) key() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.keys++
	return fmt.Sprintf("itest-%d", a.keys)
}

func (a *api) call(method, path string, body any, key string) reply {
	a.t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, a.base+path, rdr)
	if err != nil {
		a.t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return reply{Code: resp.StatusCode, Header: resp.Header, Body: raw}
}

func (a *api) post(path string, body any) reply { return a.call(http.MethodPost, path, body, a.key()) }

func (a *api) mustPost(path string, body any, want int) map[string]any {
	a.t.Helper()
	r := a.post(path, body)
	if r.Code != want {
		a.t.Fatalf("POST %s = %d %s, want %d", path, r.Code, r.Body, want)
	}
	return r.JSON()
}

// boot runs the real entry point on a fresh database and a free port and
// waits until /readyz is 200.
func boot(t *testing.T) (*api, string) {
	t.Helper()
	dsn := pgtest.EmptyURL(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("KAFKA_BROKERS", strings.Join(kafkatest.Brokers(t), ","))
	t.Setenv("HTTP_ADDR", addr)
	t.Setenv("SHUTDOWN_DRAIN_DELAY", "0s")
	t.Setenv("LOG_LEVEL", "error")

	ctx, cancel := context.WithCancel(context.Background())
	a := &api{t: t, base: "http://" + addr, done: make(chan error, 1), stop: cancel}
	go func() { a.done <- run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-a.done:
		case <-time.After(30 * time.Second):
		}
	})
	deadline := time.Now().Add(90 * time.Second)
	for {
		if resp, err := http.Get(a.base + "/readyz"); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return a, dsn
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the API never became ready")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// seedSite feeds the local copies the way the consumers will: one site, three
// spots and two doors.
func seedSite(t *testing.T, dsn string) {
	t.Helper()
	ctx := context.Background()
	feed := postgres.NewLocations(pgtest.Open(t, dsn))
	for _, step := range []error{
		feed.ApplyZone(ctx, "WH1-yard", "WH1"), feed.ApplyZone(ctx, "WH1-dock", "WH1"),
		feed.ApplySlot(ctx, "WH1-Y01", "WH1-yard", shared.Spot, ""), feed.ApplySlot(ctx, "WH1-Y02", "WH1-yard", shared.Spot, ""),
		feed.ApplySlot(ctx, "WH1-Y03", "WH1-yard", shared.Spot, ""),
		feed.ApplySlot(ctx, "WH1-D01", "WH1-dock", shared.Door, "Inbound"), feed.ApplySlot(ctx, "WH1-D02", "WH1-dock", shared.Door, "Both"),
	} {
		if step != nil {
			t.Fatal(step)
		}
	}
}

// startRelay drains the outbox of dsn onto the real topics, as cmd/relay does.
func startRelay(t *testing.T, dsn string) {
	t.Helper()
	pool := pgtest.Open(t, dsn)
	sink := outboundkafka.NewRelaySink(strings.Join(kafkatest.Brokers(t), ","))
	relay := postgres.NewOutboxRelay(pool, outboundkafka.NewEncoder(), sink,
		postgres.WithInterval(20*time.Millisecond), postgres.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = relay.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done; _ = sink.Close() })
}

func (a *api) dropVisit(plate, unit string) map[string]any {
	return a.mustPost("/visits", map[string]any{
		"siteId": "WH1", "carrier": "Acme Freight", "carrierScac": "ACME", "tractorPlate": plate, "purpose": "DROP",
		"trailers": []map[string]any{{"unitNumber": unit, "kind": "DRY_VAN", "loadState": "LOADED"}},
	}, http.StatusCreated)
}

func (a *api) parked(plate, unit string) (visitID, trailerID string) {
	in := a.dropVisit(plate, unit)
	move := in["moves"].([]any)[0].(map[string]any)["moveId"].(string)
	a.mustPost("/moves/"+move+"/completions", nil, http.StatusOK)
	return in["visit"].(map[string]any)["visitId"].(string), in["trailers"].([]any)[0].(map[string]any)["trailerId"].(string)
}

type envelope struct {
	SpecVersion     string         `json:"specversion"`
	ID              string         `json:"id"`
	Source          string         `json:"source"`
	Type            string         `json:"type"`
	DataContentType string         `json:"datacontenttype"`
	DataSchema      string         `json:"dataschema"`
	Subject         string         `json:"subject"`
	Time            string         `json:"time"`
	Data            map[string]any `json:"data"`
}

// wantEvent pins one CloudEvent of the events topic: the envelope, the Kafka
// key and the whole data object. A value of "<time>" must be an RFC 3339
// instant near now; "<version>" a version above 1.
func wantEvent(t *testing.T, m kafkatest.Message, name, entity, subject string, data map[string]any) {
	t.Helper()
	var e envelope
	if err := json.Unmarshal(m.Value, &e); err != nil {
		t.Fatalf("%s: %v: %s", name, err, m.Value)
	}
	checkEnvelope(t, e, m.Key, name, entity, subject)
	for field, want := range data {
		checkField(t, name, field, want, e.Data[field])
	}
	if len(e.Data) != len(data) {
		t.Errorf("%s: data has %d fields %v, want %d", name, len(e.Data), keysOf(e.Data), len(data))
	}
}

func checkEnvelope(t *testing.T, e envelope, key, name, entity, subject string) {
	t.Helper()
	if e.SpecVersion != "1.0" || e.ID == "" || e.Source != "/warehouse/yard-management" || e.DataContentType != "application/json" {
		t.Errorf("%s: envelope %+v", name, e)
	}
	if want := "com.warehouse.wms.yard-management." + entity + "." + name; e.Type != want {
		t.Errorf("type %q, want %q", e.Type, want)
	}
	if want := "urn:warehouse:yard-management:events:" + name + ":v1"; e.DataSchema != want {
		t.Errorf("%s: dataschema %q, want %q", name, e.DataSchema, want)
	}
	if e.Subject != subject || key != subject {
		t.Errorf("%s: subject %q key %q, want %q", name, e.Subject, key, subject)
	}
}

func checkField(t *testing.T, event, field string, want, got any) {
	t.Helper()
	switch want {
	case "<time>":
		at, err := time.Parse(time.RFC3339, fmt.Sprint(got))
		if err != nil || time.Since(at) > 10*time.Minute || time.Until(at) > time.Minute {
			t.Errorf("%s.%s = %v, want a recent instant", event, field, got)
		}
	case "<version>":
		if v, ok := got.(float64); !ok || v < 1 {
			t.Errorf("%s.%s = %v, want a version", event, field, got)
		}
	default:
		wj, _ := json.Marshal(want)
		gj, _ := json.Marshal(got)
		if string(wj) != string(gj) {
			t.Errorf("%s.%s = %s, want %s", event, field, gj, wj)
		}
	}
}

func keysOf(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
