package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
)

// maxIdempotencyKeyLen is the longest accepted idempotencyKey (the REST
// Idempotency-Key bound).
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

// IdempotencyDeps are the ports of the idempotency guard of the write tools.
// It is the REST Idempotency-Key middleware over the same store: the key is
// REQUIRED, the tool runs in the same unit of work as the stored answer, so
// the tool's writes, the outbox rows and the key commit or roll back together.
type IdempotencyDeps struct {
	UoW   ports.UnitOfWork
	Store ports.IdempotencyStore
	// Locks serialises two calls that carry the same key: the second waits for
	// the first to commit and then replays its answer.
	Locks ports.LocationLock
	Clock ports.Clock
}

// Stored answers: a success is status 200 with the JSON of the answer; a
// refusal is the status of its REST problem with {"slug", "detail"}.
const storedOK = 200

type storedRefusal struct {
	Slug   string `json:"slug"`
	Detail string `json:"detail"`
}

// The outcomes that end the unit of work early.
var (
	errReplayStored = errors.New("idempotency: the key already has an answer")
	errRefused      = errors.New("idempotency: the tool refused the call")
)

// lockName is the advisory-lock name of a key (the REST adapter's).
func lockName(key string) string { return "idempotency-key/" + key }

// fingerprint identifies a call: the tool and its business arguments, without
// the idempotency key and the attribution (the same logical request retried
// by another agent id still replays).
func fingerprint(tool string, in any) (string, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", err
	}
	for _, k := range callArgKeys {
		delete(args, k)
	}
	canon, err := json.Marshal(args) // map keys are sorted: a canonical form
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("MCP "+tool+"\n"), canon...))
	return hex.EncodeToString(sum[:]), nil
}

// keyed runs fn exactly once per key:
//
//   - a success commits with its key and a replay returns it (replayed true);
//   - a refusal (an error of the catalogue) rolls the tool's partial writes
//     back and is stored on its own, so the same key and arguments replay the
//     same refusal;
//   - any other error rolls everything back and is never stored: the same key
//     can retry the real work;
//   - the same key with other arguments is idempotency-key-reused.
func keyed[Out any](ctx context.Context, d IdempotencyDeps, key, fp string, fn func(context.Context) (Out, error)) (out Out, replayed bool, err error) {
	if !validIdempotencyKey(key) {
		return out, false, errInvalidKey
	}
	c := &keyedCall[Out]{d: d, key: key, fp: fp}
	if rec, ok, err := d.Store.Lookup(ctx, key); err != nil {
		return out, false, err
	} else if ok {
		out, err = c.replay(rec)
		return out, true, err
	}
	var prior views.IdempotencyRecord
	var refusal error
	err = d.UoW.Do(ctx, func(ctx context.Context) error {
		found, err := c.claim(ctx, &prior)
		if err != nil || found {
			return orStored(err, found)
		}
		res, err := fn(ctx)
		if err != nil {
			if _, typed := problemFor(err); typed {
				refusal = err
				return errRefused
			}
			return err
		}
		out = res
		return c.save(ctx, storedOK, res)
	})
	switch {
	case err == nil:
		return out, false, nil
	case errors.Is(err, errReplayStored):
		out, err = c.replay(prior)
		return out, true, err
	case errors.Is(err, errRefused):
		return out, false, c.storeRefusal(ctx, refusal)
	}
	return out, false, err
}

type keyedCall[Out any] struct {
	d   IdempotencyDeps
	key string
	fp  string
}

// claim takes the key's lock and reports whether an answer is already stored
// (it is then left in prior).
func (c *keyedCall[Out]) claim(ctx context.Context, prior *views.IdempotencyRecord) (bool, error) {
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

func (c *keyedCall[Out]) save(ctx context.Context, status int, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("store the answer of key %q: %w", c.key, err)
	}
	return c.d.Store.Save(ctx, views.IdempotencyRecord{
		Key: c.key, Fingerprint: c.fp, Status: status, ContentType: "application/json", Body: raw, CreatedAt: c.d.Clock.Now().UTC(),
	})
}

// storeRefusal keeps the refusal under the key after the tool's writes were
// rolled back; a concurrent call that stored an answer first wins. It returns
// the error the model sees.
func (c *keyedCall[Out]) storeRefusal(ctx context.Context, refusal error) error {
	p, _ := problemFor(refusal)
	var prior views.IdempotencyRecord
	err := c.d.UoW.Do(ctx, func(ctx context.Context) error {
		found, err := c.claim(ctx, &prior)
		if err != nil || found {
			return orStored(err, found)
		}
		return c.save(ctx, p.status, storedRefusal{Slug: p.slug, Detail: refusal.Error()})
	})
	switch {
	case err == nil:
		return refusal
	case errors.Is(err, errReplayStored):
		_, err = c.replay(prior)
		return err
	}
	return err
}

// replay answers a call whose key already has a stored answer: that answer
// when the fingerprint matches, idempotency-key-reused when it does not.
func (c *keyedCall[Out]) replay(rec views.IdempotencyRecord) (out Out, err error) {
	if rec.Fingerprint != c.fp {
		return out, errKeyReused
	}
	if rec.Status != storedOK {
		var r storedRefusal
		if err := json.Unmarshal(rec.Body, &r); err != nil {
			return out, fmt.Errorf("replay the refusal stored under key %q: %w", c.key, err)
		}
		return out, toolError(r.Slug, r.Detail)
	}
	if err := json.Unmarshal(rec.Body, &out); err != nil {
		return out, fmt.Errorf("replay the answer stored under key %q: %w", c.key, err)
	}
	return out, nil
}
