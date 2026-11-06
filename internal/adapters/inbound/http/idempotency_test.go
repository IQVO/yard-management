package http_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	inboundhttp "github.com/claudioed/yard-management/internal/adapters/inbound/http"
)

func TestIdempotencyKeyIsRequiredAndValidated(t *testing.T) {
	g := newRig(t)
	g.seedSite()
	body := map[string]any{"siteId": "WH1"}

	missing := g.do("POST", "/yard-checks", body)
	if missing.Code != http.StatusBadRequest || missing.Slug() != "idempotency-key-required" {
		t.Fatalf("no key: %d %s", missing.Code, missing.Body)
	}
	for _, key := range []string{strings.Repeat("k", 129), "caf\u00e9"} {
		r := g.do("POST", "/yard-checks", body, "Idempotency-Key", key)
		if r.Code != http.StatusBadRequest || r.Slug() != "idempotency-key-invalid" {
			t.Fatalf("key %q: %d %s", key, r.Code, r.Body)
		}
	}
	if n := len(g.env.Store.Events()); n != 0 {
		t.Fatalf("a refused key recorded %d events", n)
	}
}

func TestSameKeyAndRequestReplaysTheFirstAnswer(t *testing.T) {
	g := newRig(t)
	g.seedSite()
	visit := map[string]any{"siteId": "WH1", "carrier": "Acme", "tractorPlate": "P1", "purpose": "BOBTAIL"}

	first := g.do("POST", "/visits", visit, "Idempotency-Key", "k-1")
	second := g.do("POST", "/visits", visit, "Idempotency-Key", "k-1")
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("statuses %d %d", first.Code, second.Code)
	}
	if string(first.Body) != string(second.Body) {
		t.Fatalf("replay differs:\n%s\n%s", first.Body, second.Body)
	}
	if first.Header.Get("Idempotency-Replayed") != "" || second.Header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replayed headers %q %q", first.Header.Get("Idempotency-Replayed"), second.Header.Get("Idempotency-Replayed"))
	}
	if first.Header.Get("Location") == "" || second.Header.Get("Location") != first.Header.Get("Location") {
		t.Fatalf("Location %q vs %q", first.Header.Get("Location"), second.Header.Get("Location"))
	}
	if n := len(g.get("/visits").JSON()["items"].([]any)); n != 1 {
		t.Fatalf("%d visits after a replay, want 1", n)
	}

	other := map[string]any{"siteId": "WH1", "carrier": "Acme", "tractorPlate": "P2", "purpose": "BOBTAIL"}
	if r := g.do("POST", "/visits", other, "Idempotency-Key", "k-1"); r.Code != http.StatusConflict || r.Slug() != "idempotency-key-reused" {
		t.Fatalf("same key, another body: %d %s", r.Code, r.Body)
	}
	if r := g.do("POST", "/visit-rejections", visit, "Idempotency-Key", "k-1"); r.Code != http.StatusConflict || r.Slug() != "idempotency-key-reused" {
		t.Fatalf("same key, another path: %d %s", r.Code, r.Body)
	}
}

func TestARefusalIsStoredAndReplayed(t *testing.T) {
	g := newRig(t)
	g.seedSite()
	g.mustCode(g.dropVisit("P1", "TR-1"), http.StatusCreated)
	refused := map[string]any{"siteId": "WH1", "carrier": "Acme", "tractorPlate": "P1", "purpose": "BOBTAIL"}

	first := g.do("POST", "/visits", refused, "Idempotency-Key", "k-refused")
	second := g.do("POST", "/visits", refused, "Idempotency-Key", "k-refused")
	if first.Code != http.StatusConflict || second.Code != http.StatusConflict || second.Header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("refusal: %d then %d replayed=%q", first.Code, second.Code, second.Header.Get("Idempotency-Replayed"))
	}
	if string(first.Body) != string(second.Body) || second.Slug() != "visit-already-open" {
		t.Fatalf("replayed refusal %s", second.Body)
	}
}

func TestReplayOfAPolicyKeepsItsETag(t *testing.T) {
	g := newRig(t)
	g.seedSite()
	declare := map[string]any{"requireAppointment": false, "sealMismatchAction": "FLAG", "leaseSeconds": 300}
	first := g.mustCode(g.do("PUT", "/sites/WH1/yard-policy", declare, "Idempotency-Key", "k-pol"), http.StatusOK)
	second := g.mustCode(g.do("PUT", "/sites/WH1/yard-policy", declare, "Idempotency-Key", "k-pol"), http.StatusOK)
	if first.Header.Get("ETag") != `W/"2"` || second.Header.Get("ETag") != `W/"2"` || second.Header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("ETags %q %q", first.Header.Get("ETag"), second.Header.Get("ETag"))
	}
}

func TestSweepKeyIsOptionalButHonouredWhenSent(t *testing.T) {
	g := newRig(t)
	g.seedSite()
	first := g.do("POST", "/move-lease-sweeps", nil, "Idempotency-Key", "k-sweep")
	second := g.do("POST", "/move-lease-sweeps", nil, "Idempotency-Key", "k-sweep")
	if first.Code != http.StatusOK || second.Header.Get("Idempotency-Replayed") != "true" || string(first.Body) != string(second.Body) {
		t.Fatalf("sweep replay: %d %q", second.Code, second.Header.Get("Idempotency-Replayed"))
	}
	if r := g.do("POST", "/move-lease-sweeps", nil, "Idempotency-Key", "k-sweep", "Content-Type", "application/json"); r.Code != http.StatusOK {
		t.Fatalf("same sweep again: %d", r.Code)
	}
	if r := g.do("POST", "/dwell-sweeps", nil, "Idempotency-Key", "k-sweep"); r.Code != http.StatusConflict || r.Slug() != "idempotency-key-reused" {
		t.Fatalf("a sweep key reused on another sweep: %d %s", r.Code, r.Body)
	}
	if r := g.do("POST", "/dwell-sweeps", nil, "Idempotency-Key", ""); r.Code != http.StatusOK {
		t.Fatalf("an empty key on a sweep is no key: %d", r.Code)
	}
	if r := g.do("POST", "/dwell-sweeps", nil, "Idempotency-Key", strings.Repeat("x", 200)); r.Code != http.StatusBadRequest {
		t.Fatalf("an invalid key on a sweep: %d", r.Code)
	}
}

// counting is a handler that answers status and counts how often it ran.
func counting(status int, runs *atomic.Int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		runs.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

func middlewareRig(t *testing.T) inboundhttp.IdempotencyDeps {
	g := newRig(t)
	return inboundhttp.IdempotencyDeps{UoW: g.env.Store, Store: g.env.Store, Locks: g.env.Store, Clock: g.env.Store}
}

func send(h http.Handler, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/things", strings.NewReader(`{"a":1}`))
	req.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAServerFailureIsNeverStored(t *testing.T) {
	deps := middlewareRig(t)
	for name, mw := range map[string]func(http.Handler) http.Handler{"required": inboundhttp.NewIdempotency(deps), "optional": inboundhttp.NewOptionalIdempotency(deps)} {
		var runs atomic.Int64
		h := mw(counting(http.StatusInternalServerError, &runs))
		send(h, "k-"+name)
		send(h, "k-"+name)
		if runs.Load() != 2 {
			t.Errorf("%s: the handler ran %d times, want 2 (a 5xx is retried)", name, runs.Load())
		}
	}
}

func TestConcurrentRequestsWithOneKeyRunTheHandlerOnce(t *testing.T) {
	deps := middlewareRig(t)
	var runs atomic.Int64
	h := inboundhttp.NewIdempotency(deps)(counting(http.StatusCreated, &runs))

	var wg sync.WaitGroup
	var replays atomic.Int64
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := send(h, "k-race")
			if rec.Code != http.StatusCreated {
				t.Errorf("status %d", rec.Code)
			}
			if rec.Header().Get("Idempotency-Replayed") == "true" {
				replays.Add(1)
			}
		}()
	}
	wg.Wait()
	if runs.Load() != 1 || replays.Load() != 19 {
		t.Fatalf("handler ran %d times with %d replays, want 1 and 19", runs.Load(), replays.Load())
	}
}
