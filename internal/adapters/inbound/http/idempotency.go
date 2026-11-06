package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
)

// IdempotencyKeyHeader is the request header a caller sends on a route
// wrapped by the Idempotency-Key middleware.
const IdempotencyKeyHeader = "Idempotency-Key"

// IdempotencyReplayedHeader is set to "true" on a response replayed from the
// stored answer of an earlier request with the same key, and only then.
const IdempotencyReplayedHeader = "Idempotency-Replayed"

// maxIdempotencyKeyLen is the longest accepted Idempotency-Key.
const maxIdempotencyKeyLen = 128

// validIdempotencyKey reports whether key is 1-128 printable ASCII characters.
func validIdempotencyKey(key string) bool {
	if len(key) == 0 || len(key) > maxIdempotencyKeyLen {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x20 || key[i] > 0x7e {
			return false
		}
	}
	return true
}

// IdempotencyDeps are the ports of the Idempotency-Key middleware. The
// middleware talks to ports only: cmd/ wires the Postgres adapters in.
type IdempotencyDeps struct {
	UoW   ports.UnitOfWork
	Store ports.IdempotencyStore
	// Locks serialises two requests that carry the same key: the second
	// waits for the first to commit and then replays its answer.
	Locks ports.LocationLock
	Clock ports.Clock
}

// NewIdempotency is the middleware of every POST and PUT that creates or
// changes something: the key is REQUIRED (400 idempotency-key-required) and
// the handler runs in the same unit of work as the stored answer, so the
// handler's writes, the outbox rows and the key commit or roll back together.
//
//   - A 2xx answer commits with its key; a replay returns it verbatim with
//     Idempotency-Replayed: true.
//   - A 4xx answer is a refusal: the handler's partial writes are rolled back
//     and the answer is stored on its own, so the same key and request
//     replay the same refusal.
//   - A 5xx answer rolls everything back and is never stored: the same key
//     can retry the real work.
//   - The same key with another method, path or body is 409
//     idempotency-key-reused.
func NewIdempotency(d IdempotencyDeps) func(http.Handler) http.Handler {
	return d.middleware(true)
}

// NewOptionalIdempotency is the middleware of the sweeps (F6): the key is
// optional because a sweep is naturally idempotent. When sent it behaves like
// the required one, except that the handler runs outside a unit of work (a
// sweep keeps one per item) and only the answer is stored.
func NewOptionalIdempotency(d IdempotencyDeps) func(http.Handler) http.Handler {
	return d.middleware(false)
}

func (d IdempotencyDeps) middleware(required bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(IdempotencyKeyHeader)
			switch {
			case key == "" && !required:
				next.ServeHTTP(w, r)
				return
			case key == "":
				writeProblem(w, r, idempotencyKeyRequired, "send an Idempotency-Key header with a unique value per logical request")
				return
			case !validIdempotencyKey(key):
				writeProblem(w, r, idempotencyKeyInvalid, "the Idempotency-Key must be 1 to 128 printable ASCII characters")
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
			if err != nil {
				writeError(w, r, errMalformedRequest)
				return
			}
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
			sum := sha256.Sum256(append([]byte(r.Method+" "+r.URL.Path+"\n"), body...))
			c := &keyedCall{d: d, key: key, fingerprint: hex.EncodeToString(sum[:]), r: r, next: next}
			if required {
				c.serveInUnitOfWork(w)
				return
			}
			c.servePlain(w)
		})
	}
}

// keyedCall is one request that carries an Idempotency-Key.
type keyedCall struct {
	d           IdempotencyDeps
	key         string
	fingerprint string
	r           *http.Request
	next        http.Handler
}

// The outcomes that end the unit of work early.
var (
	errReplayStored  = errors.New("idempotency: the key already has an answer")
	errServerFailure = errors.New("idempotency: the handler answered 5xx")
	errClientFailure = errors.New("idempotency: the handler refused the request")
)

// lockName is the advisory-lock name of a key.
func lockName(key string) string { return "idempotency-key/" + key }

// serveInUnitOfWork runs the handler and stores its answer atomically.
func (c *keyedCall) serveInUnitOfWork(w http.ResponseWriter) {
	ctx := c.r.Context()
	if rec, ok, err := c.d.Store.Lookup(ctx, c.key); err != nil {
		writeError(w, c.r, err)
		return
	} else if ok {
		c.replay(w, rec)
		return
	}
	out := newCapture()
	var prior views.IdempotencyRecord
	err := c.d.UoW.Do(ctx, func(ctx context.Context) error {
		found, err := c.claim(ctx, &prior)
		if err != nil || found {
			return orStored(err, found)
		}
		c.next.ServeHTTP(out, c.r.WithContext(ctx))
		switch {
		case out.status() >= http.StatusInternalServerError:
			return errServerFailure
		case out.status() >= http.StatusBadRequest:
			return errClientFailure
		}
		return c.d.Store.Save(ctx, c.record(out))
	})
	switch {
	case err == nil, errors.Is(err, errServerFailure):
		out.flush(w)
	case errors.Is(err, errReplayStored):
		c.replay(w, prior)
	case errors.Is(err, errClientFailure):
		c.storeRefusal(w, out)
	default:
		writeError(w, c.r, err)
	}
}

// claim takes the key's lock and reports whether an answer is already stored
// (it is then left in prior).
func (c *keyedCall) claim(ctx context.Context, prior *views.IdempotencyRecord) (bool, error) {
	if err := c.d.Locks.Lock(ctx, lockName(c.key)); err != nil {
		return false, err
	}
	rec, ok, err := c.d.Store.Lookup(ctx, c.key)
	if err != nil || !ok {
		return false, err
	}
	*prior = rec
	return true, nil
}

func orStored(err error, found bool) error {
	if err != nil {
		return err
	}
	if found {
		return errReplayStored
	}
	return nil
}

// storeRefusal keeps the refusal under the key after the handler's writes
// were rolled back; a concurrent request that stored an answer first wins.
func (c *keyedCall) storeRefusal(w http.ResponseWriter, out *capture) {
	var prior views.IdempotencyRecord
	err := c.d.UoW.Do(c.r.Context(), func(ctx context.Context) error {
		found, err := c.claim(ctx, &prior)
		if err != nil || found {
			return orStored(err, found)
		}
		return c.d.Store.Save(ctx, c.record(out))
	})
	switch {
	case err == nil:
		out.flush(w)
	case errors.Is(err, errReplayStored):
		c.replay(w, prior)
	default:
		writeError(w, c.r, err)
	}
}

// servePlain is the sweeps' path: look the key up, run the handler, store the
// answer (never a 5xx).
func (c *keyedCall) servePlain(w http.ResponseWriter) {
	ctx := c.r.Context()
	rec, ok, err := c.d.Store.Lookup(ctx, c.key)
	if err != nil {
		writeError(w, c.r, err)
		return
	}
	if ok {
		c.replay(w, rec)
		return
	}
	out := newCapture()
	c.next.ServeHTTP(out, c.r)
	if out.status() < http.StatusInternalServerError {
		if err := c.d.Store.Save(ctx, c.record(out)); err != nil {
			slog.ErrorContext(ctx, "idempotency: the answer could not be stored", "error", err)
		}
	}
	out.flush(w)
}

func (c *keyedCall) record(out *capture) views.IdempotencyRecord {
	return views.IdempotencyRecord{
		Key: c.key, Fingerprint: c.fingerprint, Status: out.status(), ContentType: out.header.Get("Content-Type"),
		Body: out.body.Bytes(), CreatedAt: c.d.Clock.Now().UTC(),
	}
}

// replay answers a request whose key already has a stored answer: that
// answer when the fingerprint (method + path + body hash) matches, 409
// idempotency-key-reused when it does not.
func (c *keyedCall) replay(w http.ResponseWriter, rec views.IdempotencyRecord) {
	if rec.Fingerprint != c.fingerprint {
		writeProblem(w, c.r, idempotencyKeyReused, "the Idempotency-Key was used with a different request")
		return
	}
	if rec.ContentType != "" {
		w.Header().Set("Content-Type", rec.ContentType)
	}
	replayHeaders(w, c.r, rec)
	w.Header().Set(IdempotencyReplayedHeader, "true")
	w.WriteHeader(rec.Status)
	_, _ = w.Write(rec.Body)
}

// replayHeaders rebuilds the headers the stored body implies: the Location of
// a created visit and the ETag of a replaced policy.
func replayHeaders(w http.ResponseWriter, r *http.Request, rec views.IdempotencyRecord) {
	var probe struct {
		VisitID string `json:"visitId"`
		Visit   *struct {
			VisitID string `json:"visitId"`
		} `json:"visit"`
		Version int64 `json:"version"`
	}
	if json.Unmarshal(rec.Body, &probe) != nil {
		return
	}
	if rec.Status == http.StatusCreated && (r.URL.Path == "/visits" || r.URL.Path == "/visit-rejections") {
		id := probe.VisitID
		if probe.Visit != nil {
			id = probe.Visit.VisitID
		}
		w.Header().Set("Location", visitLocation(id))
	}
	if r.Method == http.MethodPut && rec.Status == http.StatusOK && probe.Version > 0 {
		w.Header().Set("ETag", etagOf(probe.Version))
	}
}

// capture records a handler's response so it can be stored and committed
// BEFORE anything reaches the client.
type capture struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func newCapture() *capture { return &capture{header: http.Header{}} }

func (c *capture) Header() http.Header { return c.header }

func (c *capture) WriteHeader(code int) {
	if c.code == 0 {
		c.code = code
	}
}

func (c *capture) Write(b []byte) (int, error) {
	if c.code == 0 {
		c.code = http.StatusOK
	}
	return c.body.Write(b)
}

func (c *capture) status() int {
	if c.code == 0 {
		return http.StatusOK
	}
	return c.code
}

// flush copies the captured response onto the real client connection.
func (c *capture) flush(w http.ResponseWriter) {
	for k, vs := range c.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(c.status())
	_, _ = w.Write(c.body.Bytes())
}
