package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundhttp "github.com/claudioed/yard-management/internal/adapters/inbound/http"
	inboundmcp "github.com/claudioed/yard-management/internal/adapters/inbound/mcp"
	"github.com/claudioed/yard-management/internal/adapters/outbound/memory"
	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/testing/yardtest"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// memoryDeps wires the tools over the in-memory TEST DOUBLES behind the real
// router. The Postgres wiring is proven in main_integration_test.go.
func memoryDeps() inboundmcp.Deps {
	store := memory.NewStore(yardtest.T0)
	d := yardtest.Deps(store)
	d.Events = inboundmcp.NewAttributingPublisher(store)
	return inboundmcp.Deps{
		Yard:        usecases.New(d),
		Idempotency: inboundmcp.IdempotencyDeps{UoW: store, Store: store, Locks: store, Clock: store},
	}
}

// gate builds the readiness gate over one fake dependency per name.
func gate(checks map[string]func(context.Context) error) *inboundhttp.Readiness {
	var deps []inboundhttp.Dependency
	for _, name := range []string{"postgres", "kafka"} {
		if check, ok := checks[name]; ok {
			deps = append(deps, inboundhttp.Dependency{Name: name, Check: check})
		}
	}
	return inboundhttp.NewReadiness(time.Nanosecond, inboundhttp.DefaultReadinessTimeout, deps...)
}

func allUp() *inboundhttp.Readiness {
	return gate(map[string]func(context.Context) error{"postgres": up, "kafka": up})
}

func newTestServer(t *testing.T, readiness *inboundhttp.Readiness) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(routes(inboundmcp.Handler(inboundmcp.NewServer(memoryDeps())), readiness))
	t.Cleanup(srv.Close)
	return srv
}

func up(context.Context) error { return nil }

func TestRouter_HealthzIsOpenAndServesOK(t *testing.T) {
	srv := newTestServer(t, allUp())
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != `{"status":"ok"}` {
		t.Fatalf("GET /healthz = %d %q, want 200 {\"status\":\"ok\"}", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
}

func TestRouter_ReadyzFollowsPostgresAndKafka(t *testing.T) {
	down := func(context.Context) error { return errors.New("connection refused") }
	stuck := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	for name, tc := range map[string]struct {
		checks  map[string]func(context.Context) error
		want    int
		failing string
	}{
		"both answer":      {map[string]func(context.Context) error{"postgres": up, "kafka": up}, http.StatusOK, ""},
		"postgres is down": {map[string]func(context.Context) error{"postgres": down, "kafka": up}, http.StatusServiceUnavailable, `"failing":["postgres"]`},
		"kafka is down":    {map[string]func(context.Context) error{"postgres": up, "kafka": down}, http.StatusServiceUnavailable, `"failing":["kafka"]`},
		"kafka is stuck":   {map[string]func(context.Context) error{"postgres": up, "kafka": stuck}, http.StatusServiceUnavailable, `"failing":["kafka"]`},
	} {
		t.Run(name, func(t *testing.T) {
			srv := newTestServer(t, gate(tc.checks))
			start := time.Now()
			resp, err := http.Get(srv.URL + "/readyz")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != tc.want || !strings.Contains(string(body), tc.failing) {
				t.Fatalf("GET /readyz = %d %s, want %d containing %q", resp.StatusCode, body, tc.want, tc.failing)
			}
			if time.Since(start) > inboundhttp.DefaultReadinessTimeout+time.Second {
				t.Fatalf("/readyz took %v, longer than its %v bound", time.Since(start), inboundhttp.DefaultReadinessTimeout)
			}
		})
	}
}

func TestRouter_ReadyzIsNotReadyWhileDraining(t *testing.T) {
	g := allUp()
	srv := newTestServer(t, g)
	g.SetNotReady()
	resp, err := http.Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "not_ready") {
		t.Fatalf("GET /readyz while draining = %d %s", resp.StatusCode, body)
	}
	if resp, err = http.Get(srv.URL + "/healthz"); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz must stay 200 while draining: %v %v", resp, err)
	}
	_ = resp.Body.Close()
}

// Both mount points speak Streamable HTTP, with no credentials, and a real
// client lists all sixteen tools and calls a read and a write.
func TestStreamableHTTP_RootAndMCPPathsServeTools(t *testing.T) {
	srv := newTestServer(t, allUp())
	for _, path := range []string{"/", "/mcp"} {
		t.Run(path, func(t *testing.T) { exerciseEndpoint(t, srv.URL+path) })
	}
}

func exerciseEndpoint(t *testing.T, endpoint string) {
	t.Helper()
	ctx := context.Background()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, &sdk.StreamableClientTransport{Endpoint: endpoint}, nil)
	if err != nil {
		t.Fatalf("connect %s: %v", endpoint, err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	reads, writes := 0, 0
	for _, tool := range tools.Tools {
		if tool.Annotations != nil && tool.Annotations.ReadOnlyHint {
			reads++
		} else {
			writes++
		}
	}
	if reads != 8 || writes != 8 {
		t.Fatalf("tools = %d read + %d write, want 8 + 8", reads, writes)
	}
	res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "list_moves", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("list_moves: err=%v res=%+v", err, res)
	}
	res, err = session.CallTool(ctx, &sdk.CallToolParams{Name: "get_yard_board", Arguments: map[string]any{"siteId": "NOPE"}})
	if err != nil || !res.IsError {
		t.Fatalf("get_yard_board on an unseeded store: err=%v res=%+v, want an isError result", err, res)
	}
	exerciseWrite(t, session, endpoint)
}

// A write runs once and its retry with the same key replays the first answer.
func exerciseWrite(t *testing.T, session *sdk.ClientSession, endpoint string) {
	t.Helper()
	ctx := context.Background()
	write := &sdk.CallToolParams{Name: "open_yard_check", Arguments: map[string]any{"idempotencyKey": endpoint, "siteId": endpoint}}
	res, err := session.CallTool(ctx, write)
	if err != nil || res.IsError {
		t.Fatalf("open_yard_check: err=%v res=%+v", err, res)
	}
	if res, err = session.CallTool(ctx, write); err != nil || res.IsError || res.Meta["com.warehouse/idempotency-replayed"] != true {
		t.Fatalf("the retry with the same key: err=%v res=%+v, want a replay", err, res)
	}
}

// The binary reads no API key: requests carrying (or lacking) any
// Authorization header behave identically.
func TestNoAuthIsRequired(t *testing.T) {
	srv := newTestServer(t, allUp())
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
	for _, auth := range []string{"", "Bearer nonsense"} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("initialize with Authorization=%q = %d, want 200", auth, resp.StatusCode)
		}
	}
}

// There is no in-memory runtime: an unset DATABASE_URL or KAFKA_BROKERS is a
// boot error naming it, both reported together, before anything opens.
func TestLoadConfig(t *testing.T) {
	_, err := loadConfig(env(nil))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") || !strings.Contains(err.Error(), "KAFKA_BROKERS") {
		t.Fatalf("loadConfig(empty) = %v, want a boot error naming DATABASE_URL and KAFKA_BROKERS", err)
	}
	_, err = loadConfig(env(map[string]string{"DATABASE_URL": "postgres://u@db/x", "KAFKA_BROKERS": " , "}))
	if err == nil || strings.Contains(err.Error(), "DATABASE_URL") || !strings.Contains(err.Error(), "KAFKA_BROKERS") {
		t.Fatalf("only KAFKA_BROKERS is missing: %v", err)
	}
	_, err = loadConfig(env(map[string]string{"DATABASE_URL": "  ", "KAFKA_BROKERS": "k:9092"}))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") || strings.Contains(err.Error(), "KAFKA_BROKERS") {
		t.Fatalf("a blank DATABASE_URL = %v", err)
	}
}

func TestLoadConfigDefaultsAndOverrides(t *testing.T) {
	cfg, err := loadConfig(env(map[string]string{"DATABASE_URL": "postgres://u@db/x", "KAFKA_BROKERS": "a:9092, b:9092"}))
	if err != nil || cfg.addr != ":8090" || cfg.drainDelay != 5*time.Second || len(cfg.brokers) != 2 || cfg.brokers[1] != "b:9092" {
		t.Fatalf("defaults = %+v, %v", cfg, err)
	}
	cfg, err = loadConfig(env(map[string]string{"DATABASE_URL": "postgres://u@db/x", "KAFKA_BROKERS": "k:9092", "MCP_ADDR": "127.0.0.1:9", "LOG_LEVEL": "debug", "SHUTDOWN_DRAIN_DELAY": "0s"}))
	if err != nil || cfg.addr != "127.0.0.1:9" || cfg.logLevel != "debug" || cfg.drainDelay != 0 {
		t.Fatalf("overrides = %+v, %v", cfg, err)
	}
	for _, bad := range []string{"soon", "-1s"} {
		_, err = loadConfig(env(map[string]string{"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "k:9092", "SHUTDOWN_DRAIN_DELAY": bad}))
		if err == nil || !strings.Contains(err.Error(), "SHUTDOWN_DRAIN_DELAY") {
			t.Fatalf("SHUTDOWN_DRAIN_DELAY=%q = %v, want a boot error naming it", bad, err)
		}
	}
}

func TestRunRefusesAMisconfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("KAFKA_BROKERS", "")
	err := run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") || !strings.Contains(err.Error(), "KAFKA_BROKERS") {
		t.Fatalf("run without DATABASE_URL and KAFKA_BROKERS = %v, want a boot error naming both", err)
	}
}

// An unreachable database is a boot error after the bounded retries, never a
// silent fallback.
func TestWireBadDatabaseFailsBoot(t *testing.T) {
	savedAttempts, savedBackoff := bootAttempts, bootBackoff
	bootAttempts, bootBackoff = 2, time.Millisecond
	t.Cleanup(func() { bootAttempts, bootBackoff = savedAttempts, savedBackoff })

	dsn := "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=1"
	getenv := env(map[string]string{"DATABASE_URL": dsn, "KAFKA_BROKERS": "127.0.0.1:1"})
	_, err := wire(context.Background(), config{brokers: []string{"127.0.0.1:1"}}, getenv, quietLogger())
	if err == nil || !strings.Contains(err.Error(), "run migrations") {
		t.Fatalf("wire on an unreachable database = %v, want a run-migrations boot error", err)
	}
}

func TestRetry(t *testing.T) {
	savedAttempts, savedBackoff := bootAttempts, bootBackoff
	bootAttempts, bootBackoff = 3, time.Millisecond
	t.Cleanup(func() { bootAttempts, bootBackoff = savedAttempts, savedBackoff })

	calls := 0
	if err := retry(context.Background(), quietLogger(), "step", func() error {
		calls++
		if calls < 3 {
			return errors.New("not yet")
		}
		return nil
	}); err != nil || calls != 3 {
		t.Fatalf("a step that succeeds on its third try: err=%v calls=%d", err, calls)
	}

	calls = 0
	err := retry(context.Background(), quietLogger(), "dial", func() error { calls++; return errors.New("refused") })
	if err == nil || calls != 3 || !strings.Contains(err.Error(), "dial: refused") {
		t.Fatalf("a step that never succeeds: err=%v calls=%d", err, calls)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls = 0
	if err := retry(ctx, quietLogger(), "dial", func() error { calls++; return errors.New("refused") }); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("a cancelled context stops the retries at once: err=%v calls=%d", err, calls)
	}
}

func TestServeStopsOnContextCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, quietLogger(), &http.Server{Handler: routes(http.NotFoundHandler(), allUp())}, ln, allUp(), 0)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get("http://" + ln.Addr().String() + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the server never answered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("graceful stop = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop after the context was cancelled")
	}
}

func TestServeReturnsAListenerFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close() // Serve on a closed listener fails at once
	if err := serve(context.Background(), quietLogger(), &http.Server{}, ln, allUp(), 0); err == nil {
		t.Fatal("a dead listener was not reported")
	}
}

func TestNewLoggerLevels(t *testing.T) {
	for level, want := range map[string]slog.Level{"debug": slog.LevelDebug, "WARN": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError, "": slog.LevelInfo, "bogus": slog.LevelInfo} {
		if got := newLogger(level); !got.Handler().Enabled(context.Background(), want) || (want > slog.LevelDebug && got.Handler().Enabled(context.Background(), want-1)) {
			t.Errorf("newLogger(%q): level %v not the minimum enabled", level, want)
		}
	}
}
