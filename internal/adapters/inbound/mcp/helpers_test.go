package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/yard-management/internal/adapters/inbound/mcp"
	"github.com/claudioed/yard-management/internal/adapters/outbound/memory"
	"github.com/claudioed/yard-management/internal/application/outbox"
	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/testing/yardtest"
)

// TestMain keeps the expected "unexpected error" log lines out of the output.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

var errBoom = errors.New("boom: pq: password authentication failed")

// fakeReports is a ReportReader with canned rows.
type fakeReports struct {
	rows    []views.DwellRow
	last    time.Time
	hasLast bool
	err     error
	window  views.ReportWindow
}

func (f *fakeReports) YardDwell(_ context.Context, w views.ReportWindow) ([]views.DwellRow, error) {
	f.window = w
	return f.rows, f.err
}

func (f *fakeReports) DoorUtilisation(context.Context, views.ReportWindow) ([]views.DoorRow, error) {
	return nil, nil
}

func (f *fakeReports) HostlerProductivity(context.Context, views.ReportWindow) ([]views.HostlerRow, error) {
	return nil, nil
}

func (f *fakeReports) TurnTime(context.Context, views.ReportWindow) ([]views.TurnTimeRow, error) {
	return nil, nil
}

func (f *fakeReports) DetentionExposure(context.Context, views.ReportWindow, int) ([]views.DetentionRow, error) {
	return nil, nil
}

func (f *fakeReports) Exceptions(context.Context, views.ReportWindow) ([]views.ExceptionRow, error) {
	return nil, nil
}

func (f *fakeReports) LastEventAt(context.Context) (time.Time, bool, error) {
	return f.last, f.hasLast, f.err
}

// rig is the MCP adapter over the in-memory store: the real tools, the real
// idempotency guard, the real use cases, the attributing publisher, and a real
// MCP client session over in-memory transports.
type rig struct {
	t       *testing.T
	env     *yardtest.Env
	deps    inboundmcp.Deps
	session *sdk.ClientSession
	reports *fakeReports
	keys    int
}

func newRig(t *testing.T) *rig {
	t.Helper()
	store := memory.NewStore(yardtest.T0)
	d := yardtest.Deps(store)
	d.Events = inboundmcp.NewAttributingPublisher(store)
	yard := usecases.New(d)
	reports := &fakeReports{}
	deps := inboundmcp.Deps{
		Yard:        yard,
		Reports:     reports,
		Idempotency: inboundmcp.IdempotencyDeps{UoW: store, Store: store, Locks: store, Clock: store},
	}
	r := &rig{t: t, env: &yardtest.Env{Yard: yard, Store: store, Ctx: context.Background()}, deps: deps, reports: reports}
	r.session = connect(t, inboundmcp.NewServer(deps))
	return r
}

// connect opens a client session on server over in-memory transports.
func connect(t *testing.T, server *sdk.Server) *sdk.ClientSession {
	t.Helper()
	ct, st := sdk.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	session, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// seedSite gives WH1 three spots and two doors.
func (r *rig) seedSite() {
	r.env.Site("WH1", []string{"WH1-Y01", "WH1-Y02", "WH1-Y03"}, map[string]string{"WH1-D01": "Inbound", "WH1-D02": "Both"})
}

// parkedTrailer checks a loaded DROP trailer in and completes its gate move,
// so it is PARKED; it returns the trailer id.
func (r *rig) parkedTrailer(plate, unit string) string {
	r.t.Helper()
	in, err := r.env.Yard.CheckInVisit(r.env.Ctx, usecases.CheckInInput{
		SiteID: "WH1", Carrier: "Acme Freight", CarrierSCAC: "ACME", TractorPlate: plate, Purpose: "DROP",
		Trailers: []usecases.TrailerInput{{UnitNumber: unit, Kind: "DRY_VAN", LoadState: "LOADED"}},
	})
	if err != nil {
		r.t.Fatalf("check in %s: %v", plate, err)
	}
	mv := in.Moves[0]
	if _, err := r.env.Yard.CompleteMove(r.env.Ctx, usecases.CompleteMoveInput{MoveID: mv.MoveID, Actual: mv.To}); err != nil {
		r.t.Fatalf("complete the gate move: %v", err)
	}
	return in.Trailers[0].TrailerID
}

// result is one tool call's answer.
type result struct {
	IsError  bool
	Text     string
	Data     map[string]any
	Meta     map[string]any
	Replayed bool
}

// call runs a tool and returns its answer; a protocol failure fails the test.
func (r *rig) call(name string, args map[string]any) result {
	r.t.Helper()
	res, err := r.session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		r.t.Fatalf("%s: protocol error: %v", name, err)
	}
	out := result{IsError: res.IsError, Meta: res.Meta}
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*sdk.TextContent); ok {
			out.Text = tc.Text
		}
	}
	if res.StructuredContent != nil {
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			r.t.Fatalf("%s: marshal structured content: %v", name, err)
		}
		if err := json.Unmarshal(raw, &out.Data); err != nil {
			r.t.Fatalf("%s: structured content is not an object: %v", name, err)
		}
	}
	out.Replayed, _ = res.Meta["com.warehouse/idempotency-replayed"].(bool)
	return out
}

// ok runs a tool and fails the test unless it succeeded.
func (r *rig) ok(name string, args map[string]any) result {
	r.t.Helper()
	res := r.call(name, args)
	if res.IsError {
		r.t.Fatalf("%s: %s", name, res.Text)
	}
	return res
}

// key mints a fresh idempotency key.
func (r *rig) key() string {
	r.keys++
	return fmt.Sprintf("key-%d", r.keys)
}

// write runs a write tool with a fresh key plus the given arguments.
func (r *rig) write(name string, args map[string]any) result {
	r.t.Helper()
	merged := map[string]any{"idempotencyKey": r.key()}
	for k, v := range args {
		merged[k] = v
	}
	return r.call(name, merged)
}

// wantError asserts the tool failed with the given problem slug.
func wantError(t *testing.T, res result, slug string) {
	t.Helper()
	if !res.IsError {
		t.Fatalf("want error %q, got success: %v", slug, res.Data)
	}
	if len(res.Text) < len(slug) || res.Text[:len(slug)] != slug {
		t.Fatalf("error = %q, want it to start with %q", res.Text, slug)
	}
}

// eventData returns the CloudEvents data of every published event named name,
// as the outbox would persist it.
func (r *rig) eventData(name string) []map[string]any {
	r.t.Helper()
	var out []map[string]any
	for _, e := range r.published(0) {
		if e.Name == name {
			out = append(out, e.Data)
		}
	}
	return out
}

// publishedEvent is one event as the outbox would persist it.
type publishedEvent struct {
	Name string
	Data map[string]any
}

// published returns the events published from index from on, with the data
// the outbox would persist for each.
func (r *rig) published(from int) []publishedEvent {
	r.t.Helper()
	var out []publishedEvent
	for _, e := range r.env.Store.Events()[from:] {
		payload, err := outbox.Payload(e)
		if err != nil {
			r.t.Fatalf("payload of %s: %v", e.EventName(), err)
		}
		var env struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(payload, &env); err != nil {
			r.t.Fatalf("decode payload of %s: %v", e.EventName(), err)
		}
		out = append(out, publishedEvent{Name: e.EventName(), Data: env.Data})
	}
	return out
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}
