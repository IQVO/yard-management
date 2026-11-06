package bdd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"

	inboundhttp "github.com/claudioed/yard-management/internal/adapters/inbound/http"
	"github.com/claudioed/yard-management/internal/testing/yardtest"
)

// reply is a recorded REST response.
type reply struct {
	Code   int
	Header http.Header
	Body   []byte
}

// doc decodes the body as JSON.
func (r reply) doc() any {
	var v any
	if err := json.Unmarshal(r.Body, &v); err != nil {
		return nil
	}
	return v
}

// slug is the problem slug of an error reply.
func (r reply) slug() string {
	m, _ := r.doc().(map[string]any)
	t, _ := m["type"].(string)
	return t[strings.LastIndex(t, "/")+1:]
}

// world is the state of one scenario: a fresh in-memory yard behind the real
// REST router, the aliases the steps give to ids, and the last response.
type world struct {
	env      *yardtest.Env
	handler  http.Handler
	keys     int
	last     reply
	visits   map[string]string // alias -> visit id
	trailers map[string]string // unit number -> trailer id
	racing   []reply           // the answers of a concurrent step
	site     string            // the site the last Given declared
	mu       sync.Mutex
}

// reset gives the world a fresh yard (one per scenario).
func (w *world) reset() {
	fresh := newWorld()
	w.env, w.handler, w.keys, w.last = fresh.env, fresh.handler, 0, reply{}
	w.visits, w.trailers, w.racing, w.site = fresh.visits, fresh.trailers, nil, ""
}

func newWorld() *world {
	env := yardtest.New()
	deps := inboundhttp.IdempotencyDeps{UoW: env.Store, Store: env.Store, Locks: env.Store, Clock: env.Store}
	server := &inboundhttp.Server{
		Yard: env.Yard, Idempotency: inboundhttp.NewIdempotency(deps), SweepIdempotency: inboundhttp.NewOptionalIdempotency(deps),
		Readiness: inboundhttp.NewReadiness(0, 0),
	}
	return &world{env: env, handler: inboundhttp.NewRouter(server), visits: map[string]string{}, trailers: map[string]string{}}
}

func (w *world) freshKey() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.keys++
	return "bdd-key-" + strconv.Itoa(w.keys)
}

// call sends one request. A POST or PUT carries a fresh Idempotency-Key unless
// key is given ("-" sends none).
func (w *world) call(method, path string, body any, key string) reply {
	return w.callHeaders(method, path, body, nil, key)
}

// callHeaders is call with extra request headers.
func (w *world) callHeaders(method, path string, body any, headers map[string]string, keys ...string) reply {
	key := ""
	if len(keys) > 0 {
		key = keys[0]
	}
	var rdr io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		if b != "" {
			rdr = strings.NewReader(b)
		}
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			panic(err)
		}
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rdr)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	switch {
	case key == "-":
	case key != "":
		req.Header.Set("Idempotency-Key", key)
	case method == http.MethodPost || method == http.MethodPut:
		req.Header.Set("Idempotency-Key", w.freshKey())
	}
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, req)
	return reply{Code: rec.Code, Header: rec.Header(), Body: rec.Body.Bytes()}
}

// send is call whose answer becomes the response the Then steps look at.
func (w *world) send(method, path string, body any) reply {
	w.last = w.call(method, path, body, "")
	return w.last
}

// must fails the step unless the answer has the status; it is for Given steps,
// where a broken fixture must never look like a failing assertion.
func (w *world) must(r reply, want int, what string) error {
	if r.Code != want {
		return fmt.Errorf("%s: %d, want %d: %s", what, r.Code, want, r.Body)
	}
	return nil
}

// recordIDs remembers the visit and trailer ids a check-in answered.
func (w *world) recordIDs(alias string, r reply) {
	m, _ := r.doc().(map[string]any)
	if v, ok := m["visit"].(map[string]any); ok {
		w.visits[alias], _ = v["visitId"].(string)
	}
	if ts, ok := m["trailers"].([]any); ok {
		for _, t := range ts {
			tm := t.(map[string]any)
			w.trailers[tm["unitNumber"].(string)], _ = tm["trailerId"].(string)
		}
	}
}

func (w *world) trailerID(unit string) (string, error) {
	id, ok := w.trailers[unit]
	if !ok {
		return "", fmt.Errorf("no trailer %q was checked in", unit)
	}
	return id, nil
}

func (w *world) visitID(alias string) (string, error) {
	id, ok := w.visits[alias]
	if !ok {
		return "", fmt.Errorf("no visit %q was checked in", alias)
	}
	return id, nil
}

// moveOf returns the newest move of the trailer in the given states ("" any).
func (w *world) moveOf(unit, state string) (map[string]any, error) {
	id, err := w.trailerID(unit)
	if err != nil {
		return nil, err
	}
	path := "/moves?trailerId=" + id
	if state != "" {
		path += "&state=" + state
	}
	items, _ := lookup(w.call(http.MethodGet, path, nil, "").doc(), "items").([]any)
	if len(items) == 0 {
		return nil, fmt.Errorf("trailer %s has no move %s", unit, state)
	}
	return items[len(items)-1].(map[string]any), nil
}

// openMoveID is the id of the REQUESTED or CLAIMED move of the trailer.
func (w *world) openMoveID(unit string) (string, error) {
	for _, state := range []string{"CLAIMED", "REQUESTED"} {
		if m, err := w.moveOf(unit, state); err == nil {
			return m["moveId"].(string), nil
		}
	}
	return "", fmt.Errorf("trailer %s has no open move", unit)
}

// trailerDoc fetches the trailer.
func (w *world) trailerDoc(unit string) (map[string]any, error) {
	id, err := w.trailerID(unit)
	if err != nil {
		return nil, err
	}
	r := w.call(http.MethodGet, "/trailers/"+id, nil, "")
	if r.Code != http.StatusOK {
		return nil, fmt.Errorf("GET trailer %s: %d %s", unit, r.Code, r.Body)
	}
	m, _ := r.doc().(map[string]any)
	return m, nil
}

// lookup follows a dotted path ("moves.0.state", "" is the root) in a decoded
// JSON document; nil when the path leads nowhere.
func lookup(doc any, path string) any {
	if path == "" {
		return doc
	}
	cur := doc
	for _, seg := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			cur = node[seg]
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(node) {
				return nil
			}
			cur = node[i]
		default:
			return nil
		}
	}
	return cur
}

// present reports whether the dotted path exists (a null value counts).
func present(doc any, path string) bool {
	parent, last := "", path
	if i := strings.LastIndex(path, "."); i >= 0 {
		parent, last = path[:i], path[i+1:]
	}
	switch node := lookup(doc, parent).(type) {
	case map[string]any:
		_, ok := node[last]
		return ok
	case []any:
		i, err := strconv.Atoi(last)
		return err == nil && i >= 0 && i < len(node)
	}
	return false
}

// literal parses the expected value of a step: a JSON literal, or a bare word
// taken as a string.
func literal(expr string) any {
	var v any
	if err := json.Unmarshal([]byte(strings.TrimSpace(expr)), &v); err != nil {
		return strings.TrimSpace(expr)
	}
	return v
}

func sameValue(a, b any) bool { return reflect.DeepEqual(a, b) }

var splitList = regexp.MustCompile(`\s*,\s*`)

func list(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return splitList.Split(s, -1)
}
