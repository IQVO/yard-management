package http_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	inboundhttp "github.com/claudioed/yard-management/internal/adapters/inbound/http"
	"github.com/claudioed/yard-management/internal/testing/yardtest"
)

// rig is the REST adapter over the in-memory store: the real router, the real
// Idempotency-Key middleware and the real use cases.
type rig struct {
	t       *testing.T
	env     *yardtest.Env
	server  *inboundhttp.Server
	handler http.Handler
	keys    atomic.Int64
}

func newRig(t *testing.T) *rig {
	t.Helper()
	env := yardtest.New()
	deps := inboundhttp.IdempotencyDeps{UoW: env.Store, Store: env.Store, Locks: env.Store, Clock: env.Store}
	server := &inboundhttp.Server{
		Yard: env.Yard, Idempotency: inboundhttp.NewIdempotency(deps), SweepIdempotency: inboundhttp.NewOptionalIdempotency(deps),
		Readiness: inboundhttp.NewReadiness(0, 0),
	}
	return &rig{t: t, env: env, server: server, handler: inboundhttp.NewRouter(server)}
}

// seedSite gives the site two spots and two doors.
func (g *rig) seedSite() {
	g.env.Site("WH1", []string{"WH1-Y01", "WH1-Y02", "WH1-Y03"}, map[string]string{"WH1-D01": "Inbound", "WH1-D02": "Both"})
}

// reply is a recorded response.
type reply struct {
	Code   int
	Header http.Header
	Body   []byte
}

// JSON decodes the body into a generic map.
func (p reply) JSON() map[string]any {
	var m map[string]any
	if err := json.Unmarshal(p.Body, &m); err != nil {
		panic(err.Error() + ": " + string(p.Body))
	}
	return m
}

// Slug is the problem slug of an error reply.
func (p reply) Slug() string {
	t, _ := p.JSON()["type"].(string)
	return strings.TrimPrefix(t, "https://errors.yard-management.warehouse-systems.dev/")
}

func (g *rig) do(method, path string, body any, headers ...string) reply {
	g.t.Helper()
	var rdr io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rdr = strings.NewReader(b)
	case []byte:
		rdr = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			g.t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rdr)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	g.handler.ServeHTTP(rec, req)
	return reply{Code: rec.Code, Header: rec.Header(), Body: rec.Body.Bytes()}
}

// post sends a POST with a fresh Idempotency-Key.
func (g *rig) post(path string, body any) reply {
	g.t.Helper()
	return g.do(http.MethodPost, path, body, "Idempotency-Key", g.freshKey())
}

func (g *rig) get(path string) reply {
	g.t.Helper()
	return g.do(http.MethodGet, path, nil)
}

func (g *rig) freshKey() string { return fmt.Sprintf("key-%d", g.keys.Add(1)) }

func (g *rig) mustCode(p reply, want int) reply {
	g.t.Helper()
	if p.Code != want {
		g.t.Fatalf("status %d, want %d: %s", p.Code, want, p.Body)
	}
	return p
}

// dropVisit checks a DROP visit in and returns the reply.
func (g *rig) dropVisit(plate, unit string) reply {
	g.t.Helper()
	return g.post("/visits", map[string]any{
		"siteId": "WH1", "carrier": "Acme Freight", "carrierScac": "ACME", "tractorPlate": plate, "purpose": "DROP",
		"trailers": []map[string]any{{"unitNumber": unit, "kind": "DRY_VAN", "loadState": "LOADED"}},
	})
}
