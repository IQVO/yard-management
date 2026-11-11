//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	// Real Postgres and Kafka come from the shared testcontainers helpers.
	_ "github.com/testcontainers/testcontainers-go/modules/kafka"
	_ "github.com/testcontainers/testcontainers-go/modules/postgres"

	outboundkafka "github.com/claudioed/yard-management/internal/adapters/outbound/kafka"
	"github.com/claudioed/yard-management/internal/adapters/outbound/postgres"
	"github.com/claudioed/yard-management/internal/adapters/outbound/system"
	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/testing/kafkatest"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
}

// server is a booted cmd/mcp run() and an MCP client over its real HTTP port.
type server struct {
	t       *testing.T
	base    string
	session *sdk.ClientSession
	done    chan error
	stop    context.CancelFunc
}

// boot runs the real entry point on a fresh database and a free port, waits
// until /readyz is 200 and connects a real MCP client over Streamable HTTP.
func boot(t *testing.T) (*server, string) {
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
	t.Setenv("MCP_ADDR", addr)
	t.Setenv("SHUTDOWN_DRAIN_DELAY", "0s")
	t.Setenv("LOG_LEVEL", "error")

	ctx, cancel := context.WithCancel(context.Background())
	s := &server{t: t, base: "http://" + addr, done: make(chan error, 1), stop: cancel}
	go func() { s.done <- run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-s.done:
		case <-time.After(30 * time.Second):
		}
	})
	deadline := time.Now().Add(90 * time.Second)
	for {
		if resp, err := http.Get(s.base + "/readyz"); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the MCP server never became ready")
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.session, err = sdk.NewClient(&sdk.Implementation{Name: "itest", Version: "0"}, nil).
		Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: s.base + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = s.session.Close() })
	return s, dsn
}

// result is a tool answer: the structured JSON, or the error text.
type result struct {
	IsError  bool
	Text     string
	Data     map[string]any
	Replayed bool
}

func (s *server) call(tool string, args map[string]any) result {
	s.t.Helper()
	res, err := s.session.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		s.t.Fatalf("%s: protocol error %v", tool, err)
	}
	out := result{IsError: res.IsError, Replayed: res.Meta["com.warehouse/idempotency-replayed"] == true}
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			out.Text += tc.Text
		}
	}
	if res.StructuredContent != nil {
		raw, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(raw, &out.Data)
	}
	return out
}

func (s *server) mustCall(tool string, args map[string]any) result {
	s.t.Helper()
	r := s.call(tool, args)
	if r.IsError {
		s.t.Fatalf("%s %v: %s", tool, args, r.Text)
	}
	return r
}

// slug is the machine-readable prefix of a tool error ("<slug>: <detail>").
func (r result) slug() string {
	slug, _, _ := strings.Cut(r.Text, ":")
	return slug
}

var keys struct {
	sync.Mutex
	n int
}

func key() string {
	keys.Lock()
	defer keys.Unlock()
	keys.n++
	return fmt.Sprintf("mcp-itest-%d", keys.n)
}

// fixture seeds the local copies the way the consumers will (one site, three
// spots, two doors) and drives two DROP visits through the SAME use cases the
// REST API runs, so two trailers are PARKED when the MCP calls start. It
// returns the two trailer ids.
func fixture(t *testing.T, dsn string) (first, second string) {
	t.Helper()
	ctx := context.Background()
	pool := pgtest.Open(t, dsn)
	feed := postgres.NewLocations(pool)
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
	yard := usecases.New(yardDeps(pool, postgres.NewUnitOfWork(pool), postgres.NewLocks(), system.NewClock(), postgres.NewOutbox(pool)))
	parked := func(plate, unit string) string {
		in, err := yard.CheckInVisit(ctx, usecases.CheckInInput{
			SiteID: "WH1", Carrier: "Acme Freight", CarrierSCAC: "ACME", TractorPlate: plate, Purpose: "DROP",
			Trailers: []usecases.TrailerInput{{UnitNumber: unit, Kind: "DRY_VAN", LoadState: "LOADED"}},
		})
		if err != nil {
			t.Fatalf("check in %s: %v", unit, err)
		}
		if _, err := yard.CompleteMove(ctx, usecases.CompleteMoveInput{MoveID: in.Moves[0].MoveID}); err != nil {
			t.Fatalf("complete the arrival move of %s: %v", unit, err)
		}
		return in.Trailers[0].TrailerID
	}
	return parked("ABC1D23", "TR-1"), parked("XYZ9K87", "TR-2")
}

// The real entry point on real Postgres and Kafka, driven only through a real
// MCP client over HTTP: the surface and its authority tags, reads over rows
// the use cases wrote, writes with idempotency and attribution, the events the
// writes raised on the real topic, then the broker goes away and /readyz says
// so. The steps share one booted process and run in this order.
func TestMCPOnRealPostgresAndKafka(t *testing.T) {
	for _, topic := range outboundkafka.Topics() {
		kafkatest.CreateTopic(t, topic)
	}
	s, dsn := boot(t)
	a, b := fixture(t, dsn)
	for _, step := range []struct {
		name string
		run  func(*testing.T, *server, string, string, string)
	}{
		{"the surface is eight L1 reads and eight L3 writes over stateless HTTP", surface},
		{"reads answer from the rows the REST use cases wrote", reads},
		{"a write is idempotent, attributed and refuses with the REST slugs", writes},
		{"two moves to one door: exactly one wins", doorRace},
		{"one idempotencyKey runs once even when sent concurrently", concurrentKey},
		{"the attributed events leave through the outbox relay with initiated_by", attributedEventsOnKafka},
		{"readiness turns 503 when Kafka stops and liveness does not", readinessWhenKafkaStops},
		{"cancelling the context stops the process gracefully", gracefulStop},
	} {
		t.Run(step.name, func(t *testing.T) { step.run(t, s, dsn, a, b) })
	}
}

func surface(t *testing.T, s *server, _, _, _ string) {
	t.Helper()
	tools, err := s.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	levels := map[string]string{}
	for _, tool := range tools.Tools {
		level, _ := tool.Meta["com.warehouse/authority"].(string)
		levels[tool.Name] = level
		wantRead := level == "L1"
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint != wantRead {
			t.Errorf("%s: level %s with annotations %+v", tool.Name, level, tool.Annotations)
		}
	}
	want := map[string]string{
		"get_yard_board": "L1", "list_trailers": "L1", "get_trailer": "L1", "list_moves": "L1", "list_visits": "L1",
		"get_visit": "L1", "get_yard_policy": "L1", "get_yard_dwell_report": "L1",
		"request_move": "L3", "cancel_move": "L3", "place_hold": "L3", "release_hold": "L3", "record_seal": "L3", "open_yard_check": "L3",
	}
	if len(levels) != len(want) {
		t.Fatalf("tools %v, want %v", levels, want)
	}
	for name, level := range want {
		if levels[name] != level {
			t.Errorf("%s is %q, want %s", name, levels[name], level)
		}
	}

	// Stateless: a legacy initialize works and issues no session id.
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
	req, _ := http.NewRequest(http.MethodPost, s.base+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Mcp-Session-Id") != "" {
		t.Fatalf("initialize = %d with session id %q, want 200 and none (Stateless: true)", resp.StatusCode, resp.Header.Get("Mcp-Session-Id"))
	}
}

func reads(t *testing.T, s *server, _, a, b string) {
	t.Helper()
	board := s.mustCall("get_yard_board", map[string]any{"siteId": "WH1"})
	if raw, _ := json.Marshal(board.Data); !strings.Contains(string(raw), a) || !strings.Contains(string(raw), b) {
		t.Fatalf("the yard board does not show both parked trailers: %s", raw)
	}
	if r := s.call("get_yard_board", map[string]any{"siteId": "NOPE"}); !r.IsError || r.slug() != "site-not-found" {
		t.Fatalf("board of an unknown site: %+v", r)
	}
	list := s.mustCall("list_trailers", map[string]any{"siteId": "WH1", "state": "PARKED"})
	if items, _ := list.Data["items"].([]any); len(items) != 2 {
		t.Fatalf("list_trailers PARKED = %v", list.Data)
	}
	one := s.mustCall("get_trailer", map[string]any{"trailerId": a})
	if one.Data["trailerId"] != a || one.Data["state"] != "PARKED" || one.Data["unitNumber"] != "TR-1" {
		t.Fatalf("get_trailer = %v", one.Data)
	}
	if r := s.call("get_trailer", map[string]any{"trailerId": "trl-nope"}); r.slug() != "trailer-not-found" {
		t.Fatalf("unknown trailer: %+v", r)
	}
	visits := s.mustCall("list_visits", map[string]any{"siteId": "WH1", "state": "CHECKED_IN"})
	items, _ := visits.Data["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("list_visits = %v", visits.Data)
	}
	visitID := items[0].(map[string]any)["visitId"].(string)
	if v := s.mustCall("get_visit", map[string]any{"visitId": visitID}); v.Data["state"] != "CHECKED_IN" {
		t.Fatalf("get_visit = %v", v.Data)
	}
	if r := s.call("list_moves", map[string]any{"limit": 501}); r.slug() != "invalid-limit" {
		t.Fatalf("limit 501: %+v", r)
	}
	if p := s.mustCall("get_yard_policy", map[string]any{"siteId": "WH1"}); p.Data["declared"] != false {
		t.Fatalf("an undeclared policy answers the defaults: %v", p.Data)
	}
	rep := s.mustCall("get_yard_dwell_report", map[string]any{"from": "2026-11-01T00:00:00Z", "to": "2026-11-08T00:00:00Z"})
	if rows, ok := rep.Data["rows"].([]any); !ok || len(rows) != 0 {
		t.Fatalf("dwell report rows %v: the read side is not wired, want an empty list", rep.Data["rows"])
	}
}

func writes(t *testing.T, s *server, dsn, a, _ string) {
	t.Helper()
	pool := pgtest.Open(t, dsn)
	k := key()
	args := map[string]any{
		"idempotencyKey": k, "trailerId": a, "to": map[string]any{"kind": "DOOR", "code": "WH1-D02"},
		"proposalId": "prop-1", "approvedBy": "supervisor-7", "agentId": "ops-agent",
	}
	first := s.mustCall("request_move", args)
	moveID, _ := first.Data["moveId"].(string)
	if first.Replayed || moveID == "" || first.Data["state"] != "REQUESTED" {
		t.Fatalf("first call = %+v", first)
	}
	again := s.mustCall("request_move", args)
	if !again.Replayed || again.Data["moveId"] != moveID {
		t.Fatalf("the retry with the same key = %+v, want a replay of %s", again, moveID)
	}
	if n := pgtest.Count(t, pool, "yard_moves WHERE trailer_id = '"+a+"' AND state = 'REQUESTED'"); n != 1 {
		t.Fatalf("%d open moves after a replay, want 1", n)
	}

	// The same key with other arguments, and a missing key, are refused.
	other := map[string]any{"idempotencyKey": k, "trailerId": a, "to": map[string]any{"kind": "DOOR", "code": "WH1-D01"}}
	if r := s.call("request_move", other); !r.IsError || r.slug() != "idempotency-key-reused" {
		t.Fatalf("the key with another body: %+v", r)
	}
	if r := s.call("cancel_move", map[string]any{"moveId": moveID}); !r.IsError {
		t.Fatalf("a write without an idempotencyKey must be refused: %+v", r)
	}
	// A second open move for the same trailer is a domain refusal, stored and
	// replayed under its own key.
	dup := map[string]any{"idempotencyKey": key(), "trailerId": a, "to": map[string]any{"kind": "SPOT", "code": "WH1-Y03"}}
	if r := s.call("request_move", dup); !r.IsError || r.slug() != "trailer-has-open-move" {
		t.Fatalf("a second open move: %+v", r)
	}
	if r := s.call("request_move", dup); !r.IsError || r.slug() != "trailer-has-open-move" {
		t.Fatalf("the refusal replayed: %+v", r)
	}

	// cancel_move frees the reservation; the audit shows the attribution.
	cancel := s.mustCall("cancel_move", map[string]any{"idempotencyKey": key(), "moveId": moveID, "agentId": "ops-agent"})
	if cancel.Data["state"] != "CANCELLED" {
		t.Fatalf("cancel_move = %v", cancel.Data)
	}
	if r := s.call("cancel_move", map[string]any{"idempotencyKey": key(), "moveId": moveID}); r.slug() != "illegal-transition" {
		t.Fatalf("cancelling twice: %+v", r)
	}

	// Holds and seals on the parked trailer.
	held := s.mustCall("place_hold", map[string]any{"idempotencyKey": key(), "trailerId": a, "reason": "DAMAGED"})
	if raw, _ := json.Marshal(held.Data); !strings.Contains(string(raw), "DAMAGED") {
		t.Fatalf("place_hold = %s", raw)
	}
	if r := s.call("request_move", map[string]any{"idempotencyKey": key(), "trailerId": a, "to": map[string]any{"kind": "DOOR", "code": "WH1-D02"}}); r.slug() != "trailer-on-hold" {
		t.Fatalf("a door move of a held trailer: %+v", r)
	}
	s.mustCall("release_hold", map[string]any{"idempotencyKey": key(), "trailerId": a})
	if r := s.call("release_hold", map[string]any{"idempotencyKey": key(), "trailerId": a}); r.slug() != "trailer-not-on-hold" {
		t.Fatalf("releasing a trailer that is not held: %+v", r)
	}
	seal := s.mustCall("record_seal", map[string]any{"idempotencyKey": key(), "trailerId": a, "state": "INTACT", "sealNumber": "S-100"})
	if raw, _ := json.Marshal(seal.Data); !strings.Contains(string(raw), "S-100") {
		t.Fatalf("record_seal = %s", raw)
	}
}

func doorRace(t *testing.T, s *server, dsn, a, b string) {
	t.Helper()
	pool := pgtest.Open(t, dsn)
	slugs := make([]string, 2)
	wins := make([]bool, 2)
	var wg sync.WaitGroup
	for i, id := range []string{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := s.call("request_move", map[string]any{"idempotencyKey": key(), "trailerId": id, "to": map[string]any{"kind": "DOOR", "code": "WH1-D01"}})
			wins[i] = !r.IsError
			slugs[i] = r.slug()
		}()
	}
	wg.Wait()
	won := 0
	for i := range wins {
		if wins[i] {
			won++
		} else if slugs[i] != "destination-reserved" && slugs[i] != "location-occupied" {
			t.Errorf("the loser's slug is %q", slugs[i])
		}
	}
	if won != 1 {
		t.Fatalf("%d moves won the door (%v), want exactly one", won, slugs)
	}
	if n := pgtest.Count(t, pool, "yard_moves WHERE state IN ('REQUESTED','CLAIMED') AND to_code = 'WH1-D01'"); n != 1 {
		t.Fatalf("%d open moves to the door, want 1", n)
	}
}

func concurrentKey(t *testing.T, s *server, dsn, _, _ string) {
	t.Helper()
	pool := pgtest.Open(t, dsn)
	const k = "mcp-itest-concurrent-key"
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, replays := 0, 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := s.call("open_yard_check", map[string]any{"idempotencyKey": k, "siteId": "WH1"})
			mu.Lock()
			defer mu.Unlock()
			if !r.IsError {
				ok++
			}
			if r.Replayed {
				replays++
			}
		}()
	}
	wg.Wait()
	if ok != 8 || replays != 7 {
		t.Fatalf("%d of 8 concurrent calls succeeded with %d replays, want 8 and 7", ok, replays)
	}
	if n := pgtest.Count(t, pool, "yard_checks"); n != 1 {
		t.Fatalf("%d yard checks, want 1", n)
	}
	if r := s.call("open_yard_check", map[string]any{"idempotencyKey": key(), "siteId": "WH1"}); r.slug() != "yard-check-open" {
		t.Fatalf("a second open check under a new key: %+v", r)
	}
	reused := s.call("open_yard_check", map[string]any{"idempotencyKey": k, "siteId": "WH2"})
	if reused.slug() != "idempotency-key-reused" {
		t.Fatalf("the key with another body: %+v", reused)
	}
}

// attributedEventsOnKafka starts the relay (as cmd/relay does) and reads the
// analytics topic, which carries every type: the events of the MCP writes
// carry `initiated_by` with via=mcp, the REST-path fixture events carry none.
func attributedEventsOnKafka(t *testing.T, _ *server, dsn, _, _ string) {
	t.Helper()
	pool := pgtest.Open(t, dsn)
	rows := pgtest.Count(t, pool, "outbox")
	sink := outboundkafka.NewRelaySink(strings.Join(kafkatest.Brokers(t), ","))
	relay := postgres.NewOutboxRelay(pool, outboundkafka.NewEncoder(), sink,
		postgres.WithInterval(20*time.Millisecond), postgres.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = relay.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done; _ = sink.Close() })

	msgs := kafkatest.ReadN(t, outboundkafka.TopicAnalytics, rows, 90*time.Second)
	var attributed, plain []string
	full := false
	for _, m := range msgs {
		var env struct {
			Type string         `json:"type"`
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(m.Value, &env); err != nil {
			t.Fatalf("%v: %s", err, m.Value)
		}
		name := env.Type[strings.LastIndex(env.Type, ".")+1:]
		by, has := env.Data["initiated_by"].(map[string]any)
		if !has {
			plain = append(plain, name)
			continue
		}
		attributed = append(attributed, name)
		if by["via"] != "mcp" {
			t.Errorf("%s: initiated_by.via = %v, want mcp", name, by["via"])
		}
		if name == "YardMoveRequested" && by["proposal_id"] == "prop-1" && by["approved_by"] == "supervisor-7" && by["agent_id"] == "ops-agent" {
			full = true
		}
	}
	if !full {
		t.Error("no YardMoveRequested carries the whole initiated_by {via, proposal_id, approved_by, agent_id} of the first request_move")
	}
	sort.Strings(attributed)
	if !contains(attributed, "YardMoveRequested") || !contains(attributed, "YardMoveCancelled") {
		t.Fatalf("attributed events %v (plain %v), want the move events of the MCP writes", attributed, plain)
	}
	if !contains(plain, "VisitCheckedIn") {
		t.Fatalf("the REST-path fixture events must carry no initiated_by: attributed %v, plain %v", attributed, plain)
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func readinessWhenKafkaStops(t *testing.T, s *server, _, _, _ string) {
	t.Helper()
	if code, body := get(t, s.base+"/readyz"); code != http.StatusOK {
		t.Fatalf("ready before: %d %s", code, body)
	}
	kafkatest.Stop(t)
	deadline := time.Now().Add(60 * time.Second)
	for {
		code, body := get(t, s.base+"/readyz")
		if code == http.StatusServiceUnavailable {
			if !strings.Contains(body, `"failing":["kafka"]`) {
				t.Fatalf("503 body %s, want kafka (and not postgres) failing", body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("/readyz stayed %d after Kafka stopped", code)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if code, _ := get(t, s.base+"/healthz"); code != http.StatusOK {
		t.Fatalf("liveness does not depend on Kafka: %d", code)
	}
}

func gracefulStop(t *testing.T, s *server, _, _, _ string) {
	t.Helper()
	s.stop()
	select {
	case err := <-s.done:
		if err != nil {
			t.Fatalf("run = %v, want nil after a graceful stop", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("run did not stop")
	}
	s.done <- nil
}
