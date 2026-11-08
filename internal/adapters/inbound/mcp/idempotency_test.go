package mcp_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	inboundmcp "github.com/claudioed/yard-management/internal/adapters/inbound/mcp"
	"github.com/claudioed/yard-management/internal/adapters/outbound/memory"
	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/testing/yardtest"
)

// The same key and arguments replay the first answer verbatim, raise no
// second event and are marked as replayed.
func TestIdempotencyReplaysTheFirstAnswer(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	trailerID := r.parkedTrailer("PLATE1", "T-100")
	args := map[string]any{"idempotencyKey": "same", "trailerId": trailerID, "to": door("WH1-D02")}

	first := r.ok("request_move", args)
	if first.Replayed {
		t.Fatal("the first call is marked replayed")
	}
	events := len(r.env.Store.Events())

	second := r.ok("request_move", args)
	if !second.Replayed {
		t.Fatalf("the second call is not marked replayed (meta %v)", second.Meta)
	}
	if str(second.Data, "moveId") != str(first.Data, "moveId") || str(second.Data, "state") != "REQUESTED" {
		t.Fatalf("replay = %v, first = %v", second.Data, first.Data)
	}
	if n := len(r.env.Store.Events()); n != events {
		t.Fatalf("the replay raised %d event(s)", n-events)
	}
	if moves := items(t, r.ok("list_moves", map[string]any{"trailerId": trailerID, "state": "REQUESTED"})); len(moves) != 1 {
		t.Fatalf("%d moves after a replay, want 1", len(moves))
	}
}

// The attribution is not part of the call's identity: the same logical
// request retried by another agent replays.
func TestIdempotencyIgnoresAttribution(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	trailerID := r.parkedTrailer("PLATE1", "T-100")
	base := map[string]any{"idempotencyKey": "same", "trailerId": trailerID, "to": door("WH1-D02")}
	r.ok("request_move", base)

	retry := map[string]any{"agentId": "agent-2", "proposalId": "p-2"}
	for k, v := range base {
		retry[k] = v
	}
	if res := r.ok("request_move", retry); !res.Replayed {
		t.Fatal("a retry with another agentId was not replayed")
	}
}

func TestIdempotencyKeyReusedForOtherArguments(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	trailerID := r.parkedTrailer("PLATE1", "T-100")
	r.ok("request_move", map[string]any{"idempotencyKey": "same", "trailerId": trailerID, "to": door("WH1-D02")})

	wantError(t, r.call("request_move", map[string]any{"idempotencyKey": "same", "trailerId": trailerID, "to": door("WH1-D01")}), "idempotency-key-reused")
	wantError(t, r.call("place_hold", map[string]any{"idempotencyKey": "same", "trailerId": trailerID, "reason": "DAMAGED"}), "idempotency-key-reused")
}

// A refusal is stored under its key: the same call replays the same refusal
// even after the world changed, and nothing the refused call wrote survives.
func TestIdempotencyStoresRefusals(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	trailerID := r.parkedTrailer("PLATE1", "T-100")
	args := map[string]any{"idempotencyKey": "refused", "trailerId": trailerID, "to": spot("WH1-NOPE")}

	first := r.call("request_move", args)
	wantError(t, first, "unknown-location")

	// The place now exists, but the key still answers what it answered first.
	r.env.Site("WH1", []string{"WH1-NOPE"}, nil)
	again := r.call("request_move", args)
	wantError(t, again, "unknown-location")
	if again.Text != first.Text {
		t.Fatalf("refusal replay = %q, first = %q", again.Text, first.Text)
	}
	if moves := items(t, r.ok("list_moves", map[string]any{"trailerId": trailerID, "state": "REQUESTED"})); len(moves) != 0 {
		t.Fatalf("a refused call left %d move(s)", len(moves))
	}
}

func TestIdempotencyKeyIsValidated(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	for name, key := range map[string]string{
		"empty":       "",
		"too long":    strings.Repeat("k", 129),
		"control":     "a\tb",
		"non-ascii":   "chave-ção",
		"delete 0x7f": "a\x7fb",
	} {
		t.Run(name, func(t *testing.T) {
			res := r.call("open_yard_check", map[string]any{"idempotencyKey": key, "siteId": "WH1"})
			if !res.IsError {
				t.Fatalf("key %q accepted", key)
			}
		})
	}
	if res := r.call("open_yard_check", map[string]any{"idempotencyKey": strings.Repeat("k", 128), "siteId": "WH1"}); res.IsError {
		t.Fatalf("a 128-character key was refused: %s", res.Text)
	}
}

// failingStore makes the idempotency store fail on demand.
type failingStore struct {
	*memory.Store
	lookup, save error
}

func (f *failingStore) Lookup(ctx context.Context, key string) (views.IdempotencyRecord, bool, error) {
	if f.lookup != nil {
		return views.IdempotencyRecord{}, false, f.lookup
	}
	return f.Store.Lookup(ctx, key)
}

func (f *failingStore) Save(ctx context.Context, rec views.IdempotencyRecord) error {
	if f.save != nil {
		return f.save
	}
	return f.Store.Save(ctx, rec)
}

func rigWithStore(t *testing.T, mutate func(*failingStore)) (*rig, *failingStore) {
	t.Helper()
	store := memory.NewStore(yardtest.T0)
	fs := &failingStore{Store: store}
	d := yardtest.Deps(store)
	d.Events = inboundmcp.NewAttributingPublisher(store)
	yard := usecases.New(d)
	deps := inboundmcp.Deps{Yard: yard, Idempotency: inboundmcp.IdempotencyDeps{UoW: store, Store: fs, Locks: store, Clock: store}}
	r := &rig{t: t, env: &yardtest.Env{Yard: yard, Store: store, Ctx: context.Background()}, deps: deps}
	r.session = connect(t, inboundmcp.NewServer(deps))
	if mutate != nil {
		mutate(fs)
	}
	return r, fs
}

// An infrastructure failure rolls everything back and is NOT stored: the same
// key can retry the real work.
func TestIdempotencyDoesNotStoreInfrastructureFailures(t *testing.T) {
	r, fs := rigWithStore(t, nil)
	r.seedSite()
	args := map[string]any{"idempotencyKey": "retry-me", "siteId": "WH1"}

	fs.save = errors.New("disk on fire: password=hunter2")
	res := r.call("open_yard_check", args)
	wantError(t, res, "internal-error")
	if strings.Contains(res.Text, "hunter2") {
		t.Fatalf("an infrastructure error leaked to the model: %q", res.Text)
	}
	if n := len(r.env.Store.Events()); n != 0 {
		t.Fatalf("%d events survived the rollback", n)
	}

	fs.save = nil
	if res := r.call("open_yard_check", args); res.IsError || res.Replayed {
		t.Fatalf("the retry with the same key = %+v, want a fresh success", res)
	}
}

func TestIdempotencyLookupFailureIsInternal(t *testing.T) {
	r, fs := rigWithStore(t, nil)
	r.seedSite()
	fs.lookup = errors.New("connection reset")
	wantError(t, r.call("open_yard_check", map[string]any{"idempotencyKey": "k", "siteId": "WH1"}), "internal-error")
}
