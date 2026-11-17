package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestWrongMethodCarriesAllowHeader pins RFC 9110 section 15.5.6: a 405 MUST
// list the methods the resource does support. chi sets Allow only in its
// DEFAULT 405 handler; once a custom handler is registered (the RFC 7807 one)
// it is called as-is, so the handler derives the list from the router itself.
func TestWrongMethodCarriesAllowHeader(t *testing.T) {
	routers := map[string]http.Handler{
		"api":     NewRouter(&Server{}),
		"reports": NewReportsRouter(&Server{}),
		"admin":   NewAdminRouter(NewReadiness(0, 0), nil, ""),
	}
	cases := []struct {
		router, method, path, wantAllow string
	}{
		{"api", http.MethodDelete, "/healthz", "GET"},
		{"api", http.MethodPost, "/readyz", "GET"},
		{"api", http.MethodDelete, "/visits", "GET, POST"},
		{"api", http.MethodPut, "/moves/claims", "POST"},
		{"api", http.MethodDelete, "/sites/s1/yard-policy", "GET, PUT"},
		{"api", http.MethodPost, "/reports/freshness", "GET"},
		{"reports", http.MethodPost, "/healthz", "GET"},
		{"reports", http.MethodDelete, "/reports/yard-dwell", "GET"},
		{"reports", http.MethodPut, "/reports/freshness", "GET"},
		{"admin", http.MethodPost, "/healthz", "GET"},
		{"admin", http.MethodPut, "/readyz", "GET"},
	}
	for _, tc := range cases {
		t.Run(tc.router+" "+tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			routers[tc.router].ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, http.NoBody))
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Errorf("Content-Type = %q, want application/problem+json", got)
			}
			if got := rec.Header().Get("Allow"); got != tc.wantAllow {
				t.Errorf("Allow = %q, want %q", got, tc.wantAllow)
			}
		})
	}
}

// TestUnknownPathHasNoAllowHeader: a 404 is not a 405, so it lists no methods.
func TestUnknownPathHasNoAllowHeader(t *testing.T) {
	for name, h := range map[string]http.Handler{
		"api":     NewRouter(&Server{}),
		"reports": NewReportsRouter(&Server{}),
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/nowhere", http.NoBody))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", name, rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != "" {
			t.Errorf("%s: Allow = %q on an unknown path, want empty", name, got)
		}
	}
}

// TestAllowedMethodsListsEveryRegisteredMethod covers a path with several
// methods, so the list is derived from the router and not hard-coded.
func TestAllowedMethodsListsEveryRegisteredMethod(t *testing.T) {
	r := chi.NewRouter()
	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	r.Get("/x", noop)
	r.Post("/x", noop)
	r.Delete("/x", noop)
	r.Get("/y", noop)
	if got, want := allowedMethods(r, "/x"), "GET, POST, DELETE"; got != want {
		t.Errorf("allowedMethods(/x) = %q, want %q", got, want)
	}
	if got, want := allowedMethods(r, "/y"), "GET"; got != want {
		t.Errorf("allowedMethods(/y) = %q, want %q", got, want)
	}
	if got := allowedMethods(r, "/nope"); got != "" {
		t.Errorf("allowedMethods(/nope) = %q, want empty", got)
	}
}
