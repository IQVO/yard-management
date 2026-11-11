// Package memory holds the in-memory test doubles of the application ports:
// one Store implements every repository, local-copy and driven port with the
// constraints of the Postgres adapter (unique indexes, version guards) and a
// snapshot-rollback UnitOfWork. They exist for unit and BDD tests only: the
// shipped binaries run on real Postgres and Kafka (fitness test
// TestRuntimeBinaryHasNoInMemoryAdapters).
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/inspection"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardcheck"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

// state is everything the store holds; a UnitOfWork snapshots it with clone.
type state struct {
	visits      map[string]yardvisit.Snapshot
	trailers    map[string]trailer.Snapshot
	moves       map[string]yardmove.Snapshot
	checks      map[string]yardcheck.Snapshot
	policies    map[string]yardpolicy.Snapshot
	inspections map[string]inspection.Snapshot
	templates   map[string]inspection.TemplateSnapshot // "<id>#<version>" -> custom template
	zones       map[string]string
	locations   map[string]views.LocationInfo
	appts       map[string]views.Appointment
	closures    map[string]views.Closure
	asns        map[string]bool // asn number -> cancelled
	receipts    map[string]views.Receipt
	processed   map[string]bool
	idem        map[string]views.IdempotencyRecord
	events      []shared.Event
}

func newState() state {
	return state{
		visits: map[string]yardvisit.Snapshot{}, trailers: map[string]trailer.Snapshot{}, moves: map[string]yardmove.Snapshot{},
		checks: map[string]yardcheck.Snapshot{}, policies: map[string]yardpolicy.Snapshot{},
		inspections: map[string]inspection.Snapshot{}, templates: map[string]inspection.TemplateSnapshot{}, zones: map[string]string{},
		locations: map[string]views.LocationInfo{}, appts: map[string]views.Appointment{}, closures: map[string]views.Closure{},
		asns: map[string]bool{}, receipts: map[string]views.Receipt{}, processed: map[string]bool{}, idem: map[string]views.IdempotencyRecord{},
	}
}

func copyMap[V any](m map[string]V) map[string]V {
	out := make(map[string]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (s state) clone() state {
	return state{
		visits: copyMap(s.visits), trailers: copyMap(s.trailers), moves: copyMap(s.moves), checks: copyMap(s.checks), policies: copyMap(s.policies),
		inspections: copyMap(s.inspections), templates: copyMap(s.templates),
		zones: copyMap(s.zones), locations: copyMap(s.locations), appts: copyMap(s.appts), closures: copyMap(s.closures), asns: copyMap(s.asns),
		receipts: copyMap(s.receipts), processed: copyMap(s.processed), idem: copyMap(s.idem), events: append([]shared.Event(nil), s.events...),
	}
}

// Store is the in-memory implementation of every application port.
type Store struct {
	// run serialises the outermost unit of work, as a database serialises two
	// transactions that need the same row: the in-memory store has no row locks,
	// so without it two concurrent writers would snapshot the same state and
	// the loser's rollback would erase the winner's commit.
	run sync.Mutex
	mu  sync.Mutex
	st  state
	now time.Time
	seq map[string]int
}

// NewStore builds an empty store whose clock reads at.
func NewStore(at time.Time) *Store {
	return &Store{st: newState(), now: at, seq: map[string]int{}}
}

type ctxKey struct{}

// Do implements ports.UnitOfWork: fn runs on the live state; when it fails the
// state is rolled back to the snapshot taken when the outermost Do began.
func (s *Store) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if ctx.Value(ctxKey{}) != nil {
		return fn(ctx)
	}
	s.run.Lock()
	defer s.run.Unlock()
	s.mu.Lock()
	before := s.st.clone()
	s.mu.Unlock()
	if err := fn(context.WithValue(ctx, ctxKey{}, true)); err != nil {
		s.mu.Lock()
		s.st = before
		s.mu.Unlock()
		return err
	}
	return nil
}

// Lock implements ports.LocationLock; the in-memory unit of work is serial.
func (s *Store) Lock(context.Context, ...string) error { return nil }

// Now implements ports.Clock.
func (s *Store) Now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// SetNow moves the clock.
func (s *Store) SetNow(t time.Time) {
	s.mu.Lock()
	s.now = t
	s.mu.Unlock()
}

// Advance moves the clock forward.
func (s *Store) Advance(d time.Duration) { s.SetNow(s.Now().Add(d)) }

// New implements ports.IDGen: prefix-000001, prefix-000002, ...
func (s *Store) New(prefix string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq[prefix]++
	return prefix + "-" + pad(s.seq[prefix])
}

func pad(n int) string {
	d := []byte("000000")
	for i := len(d) - 1; i >= 0 && n > 0; i-- {
		d[i] = byte('0' + n%10)
		n /= 10
	}
	return string(d)
}

// Publish implements ports.EventPublisher: events are kept for assertions.
func (s *Store) Publish(_ context.Context, events ...shared.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.events = append(s.st.events, events...)
	return nil
}

// Events returns every published event, in order.
func (s *Store) Events() []shared.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]shared.Event(nil), s.st.events...)
}

// EventNames returns the names of the published events, in order.
func (s *Store) EventNames() []string {
	var out []string
	for _, e := range s.Events() {
		out = append(out, e.EventName())
	}
	return out
}

// Claim implements ports.ProcessedEvents.
func (s *Store) Claim(_ context.Context, eventID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.processed[eventID] {
		return false, nil
	}
	s.st.processed[eventID] = true
	return true, nil
}

// Lookup implements ports.IdempotencyStore.
func (s *Store) Lookup(_ context.Context, key string) (views.IdempotencyRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.st.idem[key]
	return r, ok, nil
}

// SaveRecord implements ports.IdempotencyStore (the first writer of a key wins).
func (s *Store) Save(_ context.Context, rec views.IdempotencyRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.st.idem[rec.Key]; !ok {
		s.st.idem[rec.Key] = rec
	}
	return nil
}
