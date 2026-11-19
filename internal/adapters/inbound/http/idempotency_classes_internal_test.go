package http

// The Idempotency-Key middleware per replay class (ADR 0019), driven with a
// fake handler that makes a write in the request's unit of work and then
// answers the refusal of a catalogue entry. The write is what tells the
// classes apart: FINAL and RETRYABLE roll it back, OUTCOME commits it.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/testing/yardtest"
)

// fakeCommand is a handler that records a REJECTED visit (a write, with its
// VisitRejected event) and then answers what answer says for this run: a
// refusal error, or nil for a 201.
type fakeCommand struct {
	env         *yardtest.Env
	runs        atomic.Int64
	inflight    atomic.Int64
	maxInflight atomic.Int64
	answer      func(run int64) error
}

func (f *fakeCommand) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	run := f.runs.Add(1)
	now := f.inflight.Add(1)
	defer f.inflight.Add(-1)
	for seen := f.maxInflight.Load(); now > seen && !f.maxInflight.CompareAndSwap(seen, now); seen = f.maxInflight.Load() {
	}
	_, err := f.env.Yard.RejectAtGate(r.Context(), usecases.RejectInput{
		SiteID: "WH1", Carrier: "Acme", TractorPlate: fmt.Sprintf("P%d", run), Reason: "SITE_REQUEST",
	})
	if err == nil {
		err = f.answer(run)
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"run": run})
}

func newFake(answer func(run int64) error) (*fakeCommand, IdempotencyDeps) {
	env := yardtest.New()
	return &fakeCommand{env: env, answer: answer}, IdempotencyDeps{UoW: env.Store, Store: env.Store, Locks: env.Store, Clock: env.Store}
}

func (f *fakeCommand) events() int { return len(f.env.Store.Events()) }

func (f *fakeCommand) stored(key string) bool {
	_, ok, _ := f.env.Store.Lookup(f.env.Ctx, key)
	return ok
}

func postKeyed(h http.Handler, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/things", strings.NewReader(body))
	req.Header.Set(IdempotencyKeyHeader, key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func replayed(rec *httptest.ResponseRecorder) bool {
	return rec.Header().Get(IdempotencyReplayedHeader) == "true"
}

// Every catalogue entry, answered by the fake handler under one key and asked
// again: the second request is the first answer replayed (FINAL, OUTCOME) or
// runs again with the key free (RETRYABLE). The handler's write commits only
// for an OUTCOME.
func TestEveryCatalogueEntryIsReplayedOrReleasedByItsClass(t *testing.T) {
	for _, e := range problemCatalogue {
		for _, path := range []struct {
			name string
			wrap func(IdempotencyDeps) func(http.Handler) http.Handler
		}{{"required", NewIdempotency}, {"optional", NewOptionalIdempotency}} {
			checkEntryUnderPath(t, e, path.name, path.wrap)
		}
	}
}

func checkEntryUnderPath(t *testing.T, e catalogueEntry, path string, wrap func(IdempotencyDeps) func(http.Handler) http.Handler) {
	t.Helper()
	fake, deps := newFake(func(int64) error { return e.err })
	h := wrap(deps)(fake)
	first, second := postKeyed(h, "k", `{"a":1}`), postKeyed(h, "k", `{"a":1}`)
	where := fmt.Sprintf("%s (%s, %s)", e.p.slug, e.replay, path)
	if first.Code != e.p.status || second.Code != e.p.status || replayed(first) {
		t.Errorf("%s: answers %d then %d (first replayed %v), want %d twice", where, first.Code, second.Code, replayed(first), e.p.status)
	}
	wantRuns, wantReplay, wantStored := int64(1), true, true
	if e.replay == replayRetryable {
		wantRuns, wantReplay, wantStored = 2, false, false
	}
	if fake.runs.Load() != wantRuns || replayed(second) != wantReplay || fake.stored("k") != wantStored {
		t.Errorf("%s: handler ran %d time(s), second replayed %v, key stored %v; want %d, %v, %v",
			where, fake.runs.Load(), replayed(second), fake.stored("k"), wantRuns, wantReplay, wantStored)
	}
	// The write is committed by an OUTCOME only; the optional (sweep) path has no
	// unit of work around the handler, so it commits whatever the handler wrote.
	if path == "required" && fake.events() != wantEvents(e.replay) {
		t.Errorf("%s: %d event(s) after two requests, want %d for this class", where, fake.events(), wantEvents(e.replay))
	}
}

// wantEvents is how many VisitRejected events the fake handler's write leaves
// after two requests: one for an OUTCOME (committed once, not repeated), none
// for the classes whose writes roll back.
func wantEvents(c replayClass) int {
	if c == replayOutcome {
		return 1
	}
	return 0
}

// A RETRYABLE refusal leaves nothing behind: the retry under the same key runs
// the handler again, and its success is stored and replayed.
func TestARetryableRefusalReleasesTheKeyForTheRetry(t *testing.T) {
	fake, deps := newFake(func(run int64) error {
		if run == 1 {
			return ports.ErrConcurrentModification
		}
		return nil
	})
	h := NewIdempotency(deps)(fake)

	if r := postKeyed(h, "k", `{"a":1}`); r.Code != http.StatusConflict || replayed(r) || fake.stored("k") || fake.events() != 0 {
		t.Fatalf("first: %d replayed=%v stored=%v events=%d, want a 409 that is not stored and wrote nothing", r.Code, replayed(r), fake.stored("k"), fake.events())
	}
	retry := postKeyed(h, "k", `{"a":1}`)
	if retry.Code != http.StatusCreated || replayed(retry) || !fake.stored("k") || fake.runs.Load() != 2 {
		t.Fatalf("retry: %d replayed=%v stored=%v runs=%d, want a fresh 201 that is now stored", retry.Code, replayed(retry), fake.stored("k"), fake.runs.Load())
	}
	if r := postKeyed(h, "k", `{"a":1}`); r.Code != http.StatusCreated || !replayed(r) || r.Body.String() != retry.Body.String() || fake.runs.Load() != 2 {
		t.Fatalf("after the success: %d replayed=%v runs=%d, want the stored 201 replayed", r.Code, replayed(r), fake.runs.Load())
	}
}

// A key released by a RETRYABLE refusal is free: a different body under it is a
// new request, not idempotency-key-reused.
func TestAReleasedKeyTakesADifferentBodyAsANewRequest(t *testing.T) {
	fake, deps := newFake(func(run int64) error {
		if run == 1 {
			return ports.ErrConcurrentModification
		}
		return nil
	})
	h := NewIdempotency(deps)(fake)

	if r := postKeyed(h, "k", `{"a":1}`); r.Code != http.StatusConflict || fake.stored("k") {
		t.Fatalf("first body: %d stored=%v, want a released 409", r.Code, fake.stored("k"))
	}
	if r := postKeyed(h, "k", `{"a":2}`); r.Code != http.StatusCreated || replayed(r) {
		t.Fatalf("a different body under the released key: %d replayed=%v, want a new request answered 201 (not 409 idempotency-key-reused)", r.Code, replayed(r))
	}
	if r := postKeyed(h, "k", `{"a":1}`); r.Code != http.StatusConflict || r.Header().Get(IdempotencyReplayedHeader) != "" {
		t.Fatalf("the first body once the key holds the second: %d, want 409 idempotency-key-reused", r.Code)
	}
}

// A 4xx that no catalogue entry classified (a handler wrote a bare problem) is
// never stored: the safe side, and unreachable through writeError.
func TestAnUnclassifiedClientErrorIsNotStored(t *testing.T) {
	env := yardtest.New()
	deps := IdempotencyDeps{UoW: env.Store, Store: env.Store, Locks: env.Store, Clock: env.Store}
	var runs atomic.Int64
	bare := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runs.Add(1)
		writeProblem(w, r, badRequest("bare", "A bare problem"), "no class")
	})
	for name, mw := range map[string]func(http.Handler) http.Handler{"required": NewIdempotency(deps), "optional": NewOptionalIdempotency(deps)} {
		h := mw(bare)
		first, second := postKeyed(h, "k-"+name, `{}`), postKeyed(h, "k-"+name, `{}`)
		if first.Code != http.StatusBadRequest || replayed(second) {
			t.Errorf("%s: %d then replayed=%v, want a 400 answered fresh both times", name, first.Code, replayed(second))
		}
	}
	if runs.Load() != 4 {
		t.Errorf("handler ran %d times, want 4", runs.Load())
	}
}

// Twelve copies of one request arrive together and the first ends RETRYABLE.
// The key is released, so a waiting copy does not replay the refusal: it takes
// the key's lock after the first rolled back and runs the handler itself, one
// at a time; its success is stored and the rest replay that success.
func TestACopyWaitingOnARetryableFirstRunsItself(t *testing.T) {
	fake, deps := newFake(func(run int64) error {
		if run == 1 {
			return ports.ErrConcurrentModification
		}
		return nil
	})
	h := NewIdempotency(deps)(fake)

	var wg sync.WaitGroup
	var refused, fresh, replays atomic.Int64
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch r := postKeyed(h, "k-race", `{"a":1}`); {
			case r.Code == http.StatusConflict && !replayed(r):
				refused.Add(1)
			case r.Code == http.StatusCreated && !replayed(r):
				fresh.Add(1)
			case r.Code == http.StatusCreated && replayed(r):
				replays.Add(1)
			default:
				t.Errorf("unexpected answer %d replayed=%v: a released refusal must never be replayed", r.Code, replayed(r))
			}
		}()
	}
	wg.Wait()
	if refused.Load() != 1 || fresh.Load() != 1 || replays.Load() != 10 || fake.runs.Load() != 2 || fake.maxInflight.Load() != 1 {
		t.Fatalf("refused=%d fresh=%d replays=%d runs=%d maxInflight=%d, want 1, 1, 10, 2 and 1",
			refused.Load(), fresh.Load(), replays.Load(), fake.runs.Load(), fake.maxInflight.Load())
	}
}
