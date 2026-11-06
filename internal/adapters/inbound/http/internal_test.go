package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

var kebab = regexp.MustCompile(`^[a-z]+(-[a-z]+)*$`)

func TestEveryProblemHasAKebabSlugAndAnAllowedStatus(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range problemCatalogue {
		if !kebab.MatchString(e.p.slug) {
			t.Errorf("slug %q is not kebab-case", e.p.slug)
		}
		switch e.p.status {
		case http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusPreconditionFailed, http.StatusUnprocessableEntity:
		default:
			t.Errorf("%s: status %d", e.p.slug, e.p.status)
		}
		if seen[e.p.slug] {
			t.Errorf("slug %q is listed twice", e.p.slug)
		}
		seen[e.p.slug] = true
		if e.p.title == "" {
			t.Errorf("%s has no title", e.p.slug)
		}
	}
}

func TestAnErrorWrappedWithContextKeepsItsProblem(t *testing.T) {
	for _, e := range problemCatalogue {
		wrapped := fmt.Errorf("while doing %d things: %w", 3, e.err)
		if got := problemFor(wrapped); got != e.p {
			t.Errorf("%v wrapped maps to %v, want %v", e.err, got, e.p)
		}
	}
}

func TestAnUnmappedErrorIsAnInternalErrorThatLeaksNothing(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, httptest.NewRequest(http.MethodGet, "/visits", nil), errors.New("pq: password authentication failed for user yard"))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "password") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestIfMatchForms(t *testing.T) {
	for raw, want := range map[string]int64{"": 0, "*": 0, `W/"3"`: 3, `"4"`: 4, "5": 5} {
		got, err := versionFromETag(raw)
		if err != nil || got != want {
			t.Errorf("%q: %d, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"abc", `W/"0"`, "-2", `W/""`} {
		if _, err := versionFromETag(raw); !errors.Is(err, errInvalidIfMatch) {
			t.Errorf("%q: %v", raw, err)
		}
	}
}

func TestNULIsAClientError(t *testing.T) {
	h := rejectNUL(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for name, c := range map[string]struct {
		target, body string
		want         int
	}{
		"clean":          {"/visits", `{"a":"b"}`, http.StatusNoContent},
		"escaped path":   {"/visits/a%00b", ``, http.StatusNotFound},
		"query":          {"/visits?plate=a%00b", ``, http.StatusBadRequest},
		"raw body":       {"/visits", "{\"a\":\"b\x00\"}", http.StatusBadRequest},
		"escaped body":   {"/visits", `{"a":"b\u0000"}`, http.StatusBadRequest},
		"escaped slash":  {"/visits", `{"a":"b\\u0000"}`, http.StatusNoContent},
		"double escaped": {"/visits", `{"a":"b\\\u0000"}`, http.StatusBadRequest},
	} {
		req := httptest.NewRequest(http.MethodPost, c.target, strings.NewReader(c.body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: %d, want %d (%s)", name, rec.Code, c.want, rec.Body)
		}
	}
}

func TestReadinessCombinesTheDrainGateAndTheDependencies(t *testing.T) {
	down := errors.New("connection refused")
	var kafkaErr error
	gate := NewReadiness(time.Nanosecond, time.Second,
		Dependency{Name: "postgres", Check: func(context.Context) error { return nil }},
		Dependency{Name: "kafka", Check: func(context.Context) error { return kafkaErr }},
	)
	s := &Server{Readiness: gate}
	get := func() (int, string) {
		rec := httptest.NewRecorder()
		s.handleReadyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		return rec.Code, rec.Body.String()
	}

	if code, body := get(); code != http.StatusOK || !strings.Contains(body, `"ready"`) {
		t.Fatalf("healthy: %d %s", code, body)
	}
	kafkaErr = down
	time.Sleep(2 * time.Millisecond)
	code, body := get()
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "dependency-unavailable") || !strings.Contains(body, "kafka") || strings.Contains(body, "postgres") {
		t.Fatalf("kafka down: %d %s", code, body)
	}
	kafkaErr = nil
	gate.SetNotReady()
	if code, body := get(); code != http.StatusServiceUnavailable || !strings.Contains(body, "not_ready") {
		t.Fatalf("draining: %d %s", code, body)
	}
	if (*Readiness)(nil).Ready() != true || (*Readiness)(nil).Failing(context.Background()) != nil {
		t.Fatal("a nil gate is ready")
	}
}

func TestReadinessCachesAVerdict(t *testing.T) {
	calls := 0
	gate := NewReadiness(time.Hour, time.Second, Dependency{Name: "postgres", Check: func(context.Context) error { calls++; return nil }})
	gate.Failing(context.Background())
	gate.Failing(context.Background())
	if calls != 1 {
		t.Fatalf("the dependency was asked %d times within the ttl", calls)
	}
}

func TestAReportWindowIsBounded(t *testing.T) {
	ok := httptest.NewRequest(http.MethodGet, "/reports/yard-dwell?from=2026-11-01T00:00:00Z&to=2026-11-02T00:00:00Z", nil)
	if w, err := reportWindow(ok); err != nil || w.To.Sub(w.From) != 24*time.Hour {
		t.Fatalf("%v %v", w, err)
	}
	exact := httptest.NewRequest(http.MethodGet, "/reports/yard-dwell?from=2026-08-01T00:00:00Z&to=2026-11-01T00:00:00Z", nil)
	if _, err := reportWindow(exact); err != nil {
		t.Fatalf("92 days exactly is allowed: %v", err)
	}
	over := httptest.NewRequest(http.MethodGet, "/reports/yard-dwell?from=2026-07-31T00:00:00Z&to=2026-11-01T00:00:00Z", nil)
	if _, err := reportWindow(over); !errors.Is(err, errInvalidReportRange) {
		t.Fatalf("93 days: %v", err)
	}
}
